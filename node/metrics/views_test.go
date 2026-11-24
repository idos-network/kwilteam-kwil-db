package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestSQLCommand(t *testing.T) {
	for tag, want := range map[string]string{
		"INSERT 0 5":   "INSERT",
		"SELECT 12":    "SELECT",
		"UPDATE 3":     "UPDATE",
		"CREATE TABLE": "CREATE",
		"delete 1":     "DELETE",
		"":             "UNKNOWN",
		"  ":           "UNKNOWN",
	} {
		require.Equal(t, want, sqlCommand(tag), "tag %q", tag)
	}
}

// TestBoundedView records through the package's instruments, which use the
// global meter provider, and checks that per-block and per-error values don't
// create new attribute sets.
func TestBoundedView(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithView(boundedView))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		otel.SetMeterProvider(prev)
		_ = mp.Shutdown(context.Background())
	})

	ctx := context.Background()
	for h := int64(1); h <= 50; h++ {
		Consensus.RecordExecuted(ctx, time.Duration(h)*time.Millisecond, h, h%7)
		Consensus.RecordCommit(ctx, time.Millisecond, h)
		Store.BlockStored(ctx, h, 1000+h)
		Node.ServedBlock(ctx, h, 1000+h)
		DB.RecordQuery(ctx, "INSERT 0 "+string(rune('0'+h%10)), 300*time.Microsecond)
		DB.RecordQueryFailure(ctx, "", errors.New("duplicate key value "+string(rune('a'+h%26))))
	}
	RPC.RecordRequest(ctx, "user.call", 0, time.Millisecond)
	RPC.RecordRequest(ctx, "user.call", -32001, time.Millisecond)
	Node.BlacklistOperation(ctx, "add", "manual", true)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))

	attrSets := map[string][]attribute.Set{}
	var queryBounds []float64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					attrSets[m.Name] = append(attrSets[m.Name], dp.Attributes)
				}
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					attrSets[m.Name] = append(attrSets[m.Name], dp.Attributes)
					if m.Name == "query.latency" {
						queryBounds = dp.Bounds
					}
				}
			}
		}
	}

	// One attribute set each, despite 50 heights, sizes, latencies and errors.
	for _, name := range []string{
		"consensus.exec.latency", "consensus.exec.total",
		"consensus.commit.latency", "consensus.commit.total",
		"blocks.stored.count", "blocks.stored.bytes",
		"node.blocks_served.count", "node.blocks_served.bytes",
	} {
		require.Len(t, attrSets[name], 1, name)
		require.Equal(t, 0, attrSets[name][0].Len(), name)
	}

	require.Len(t, attrSets["query.latency"], 1)
	v, _ := attrSets["query.latency"][0].Value("type")
	require.Equal(t, "INSERT", v.AsString())

	require.Len(t, attrSets["query.errors"], 1)
	_, hasErr := attrSets["query.errors"][0].Value("error")
	require.False(t, hasErr, "error text must not be an attribute")
	v, _ = attrSets["query.errors"][0].Value("type")
	require.Equal(t, "UNKNOWN", v.AsString())

	require.Len(t, attrSets["requests.total"], 2) // status 0 and -32001
	require.Len(t, attrSets["node.blacklist.operations.count"], 1)
	require.Equal(t, 3, attrSets["node.blacklist.operations.count"][0].Len())

	require.Equal(t, latencyBucketsMS, queryBounds)
}
