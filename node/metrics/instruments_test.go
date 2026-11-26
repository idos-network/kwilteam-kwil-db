package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestInstruments records through the instruments in instruments.go and checks
// their names, attribute sets and values as exported.
func TestInstruments(t *testing.T) {
	reader := boundedViewReader()
	ctx := context.Background()
	// Cumulative across go test -count=N in one process; compare the increase.
	before := actionCallCounts(t, collect(t, reader))

	for i := range 20 {
		Engine.RecordActionCall(ctx, "tx", "main", "add_wallet", i%5 != 0, time.Duration(i)*time.Millisecond)
		Engine.RecordActionCall(ctx, "call", "main", "get_wallets", true, time.Millisecond)
		Engine.RecordTx(ctx, "execute", i%5 != 0)
	}
	Mempool.SetSizeFunc(func() (int64, int64) { return 3, 1500 })

	rm := collect(t, reader)

	sums := map[string][]metricdata.DataPoint[int64]{}
	gaugesInt := map[string][]metricdata.DataPoint[int64]{}
	gaugesFloat := map[string][]metricdata.DataPoint[float64]{}
	hists := map[string][]metricdata.HistogramDataPoint[float64]{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				sums[m.Name] = append(sums[m.Name], d.DataPoints...)
			case metricdata.Gauge[int64]:
				gaugesInt[m.Name] = append(gaugesInt[m.Name], d.DataPoints...)
			case metricdata.Gauge[float64]:
				gaugesFloat[m.Name] = append(gaugesFloat[m.Name], d.DataPoints...)
			case metricdata.Histogram[float64]:
				hists[m.Name] = append(hists[m.Name], d.DataPoints...)
			}
		}
	}

	// add_wallet ok and error, get_wallets ok.
	require.Len(t, sums["action.calls"], 3)
	counts := actionCallCounts(t, rm)
	for k := range counts {
		counts[k] -= before[k]
	}
	require.Equal(t, map[string]int64{
		"tx/main/add_wallet/ok": 16, "tx/main/add_wallet/error": 4, "call/main/get_wallets/ok": 20,
	}, counts)
	require.Len(t, hists["action.duration"], 2) // no result attribute
	require.Equal(t, actionLatencyBucketsMS, hists["action.duration"][0].Bounds)

	require.Len(t, sums["tx.executed"], 2)

	require.Equal(t, int64(3), gaugesInt["mempool.txs"][0].Value)
	require.Equal(t, int64(1500), gaugesInt["mempool.bytes"][0].Value)

	require.Positive(t, gaugesInt["go.goroutines"][0].Value)
	require.Positive(t, gaugesInt["go.memory.mapped_bytes"][0].Value)
	require.Positive(t, gaugesInt["go.memory.heap_objects_bytes"][0].Value)
	require.Positive(t, gaugesInt["go.gc.heap_goal_bytes"][0].Value)
	require.Len(t, sums["go.gc.cycles"], 1)
}

// TestBridgeEpochs checks that the epoch gauges follow the latest snapshot: a
// reason that no longer applies goes back to 0, and an instance that is no
// longer in the snapshot (disabled) is no longer exported.
func TestBridgeEpochs(t *testing.T) {
	reader := boundedViewReader()
	t.Cleanup(func() { Bridge.SetEpochs(nil) })

	Bridge.SetEpochs([]EpochState{
		{BridgeID: "b1", Chain: "arbitrum_one", Age: time.Hour, WaitingReason: EpochWaitingPreviousUnconfirmed},
		{BridgeID: "b2", Chain: "arbitrum_one", Age: time.Minute},
	})
	ages, waiting := epochPoints(collect(t, reader))
	require.Equal(t, map[string]float64{"b1": 3600, "b2": 60}, ages)
	require.Equal(t, map[string]int64{
		"b1/" + EpochWaitingNoRewards: 0, "b1/" + EpochWaitingPreviousUnconfirmed: 1,
		"b2/" + EpochWaitingNoRewards: 0, "b2/" + EpochWaitingPreviousUnconfirmed: 0,
	}, waiting)

	// b2 disabled, b1 now waits for rewards.
	Bridge.SetEpochs([]EpochState{
		{BridgeID: "b1", Chain: "arbitrum_one", Age: 2 * time.Hour, WaitingReason: EpochWaitingNoRewards},
	})
	ages, waiting = epochPoints(collect(t, reader))
	require.Equal(t, map[string]float64{"b1": 7200}, ages)
	require.Equal(t, map[string]int64{
		"b1/" + EpochWaitingNoRewards: 1, "b1/" + EpochWaitingPreviousUnconfirmed: 0,
	}, waiting)

	Bridge.SetEpochs(nil)
	ages, waiting = epochPoints(collect(t, reader))
	require.Empty(t, ages)
	require.Empty(t, waiting)
}

// epochPoints returns the exported epoch age by bridge and waiting value by
// bridge/reason.
func epochPoints(rm metricdata.ResourceMetrics) (map[string]float64, map[string]int64) {
	ages := map[string]float64{}
	waiting := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "erc20.epoch.age_seconds":
				for _, dp := range m.Data.(metricdata.Gauge[float64]).DataPoints {
					ages[attr(dp.Attributes, "bridge")] = dp.Value
				}
			case "erc20.epoch.finalization_waiting":
				for _, dp := range m.Data.(metricdata.Gauge[int64]).DataPoints {
					waiting[attr(dp.Attributes, "bridge")+"/"+attr(dp.Attributes, "reason")] = dp.Value
				}
			}
		}
	}
	return ages, waiting
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	return rm
}

// actionCallCounts returns action.calls by kind/namespace/action/result.
func actionCallCounts(t *testing.T, rm metricdata.ResourceMetrics) map[string]int64 {
	counts := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "action.calls" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				counts[attr(dp.Attributes, "kind")+"/"+attr(dp.Attributes, "namespace")+"/"+
					attr(dp.Attributes, "action")+"/"+attr(dp.Attributes, "result")] = dp.Value
			}
		}
	}
	return counts
}

func attr(s attribute.Set, key attribute.Key) string {
	v, _ := s.Value(key)
	return v.AsString()
}

func TestBoundedActionName(t *testing.T) {
	actionNamesMu.Lock()
	saved := actionNames
	actionNames = map[[2]string]struct{}{}
	actionNamesMu.Unlock()
	t.Cleanup(func() {
		actionNamesMu.Lock()
		actionNames = saved
		actionNamesMu.Unlock()
	})

	for i := range maxActionNames {
		ns, action := boundedActionName("main", fmt.Sprintf("a%d", i))
		require.Equal(t, fmt.Sprintf("a%d", i), action)
		require.Equal(t, "main", ns)
	}
	ns, action := boundedActionName("main", "one_too_many")
	require.Equal(t, [2]string{"other", "other"}, [2]string{ns, action})
	// Names seen before the cap keep their own series.
	ns, action = boundedActionName("main", "a7")
	require.Equal(t, [2]string{"main", "a7"}, [2]string{ns, action})
}
