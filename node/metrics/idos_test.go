package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestIdosMetrics records through the idOS instruments (idos.go) and checks
// their names, attribute sets and values as exported.
func TestIdosMetrics(t *testing.T) {
	reader := boundedViewReader()
	ctx := context.Background()
	// Cumulative across go test -count=N in one process; compare the increase.
	before := actionCallCounts(t, collect(t, reader))

	for i := range 20 {
		Engine.RecordActionCall(ctx, "tx", "main", "add_wallet", i%5 != 0, time.Duration(i)*time.Millisecond)
		Engine.RecordActionCall(ctx, "call", "main", "get_wallets", true, time.Millisecond)
		Engine.RecordTx(ctx, "execute", i%5 != 0)
	}
	Bridge.RecordEpoch(ctx, "b1", "arbitrum_one", time.Hour, EpochWaitingPreviousUnconfirmed)
	Bridge.RecordEpoch(ctx, "b1", "arbitrum_one", 2*time.Hour, EpochWaitingNoRewards)
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

	// The reason that no longer applies is back at 0.
	waiting := map[string]int64{}
	for _, dp := range gaugesInt["erc20.epoch.finalization_waiting"] {
		require.Equal(t, "b1", attr(dp.Attributes, "bridge"))
		require.Equal(t, "arbitrum_one", attr(dp.Attributes, "chain"))
		waiting[attr(dp.Attributes, "reason")] = dp.Value
	}
	require.Equal(t, map[string]int64{EpochWaitingNoRewards: 1, EpochWaitingPreviousUnconfirmed: 0}, waiting)
	require.Len(t, gaugesFloat["erc20.epoch.age_seconds"], 1)
	require.Equal(t, 7200.0, gaugesFloat["erc20.epoch.age_seconds"][0].Value)

	require.Equal(t, int64(3), gaugesInt["mempool.txs"][0].Value)
	require.Equal(t, int64(1500), gaugesInt["mempool.bytes"][0].Value)

	require.Positive(t, gaugesInt["go.goroutines"][0].Value)
	require.Positive(t, gaugesInt["go.memory.mapped_bytes"][0].Value)
	require.Positive(t, gaugesInt["go.memory.heap_objects_bytes"][0].Value)
	require.Positive(t, gaugesInt["go.gc.heap_goal_bytes"][0].Value)
	require.Len(t, sums["go.gc.cycles"], 1)
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
