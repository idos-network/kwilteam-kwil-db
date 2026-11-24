package metrics

import (
	"context"
	"errors"
	"sync"
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

// testReader is the reader of the meter provider the package's instruments
// record to. The instruments are created in init() through the global otel
// meter provider, which delegates them to the first provider set and never to
// a later one, so the provider is set once per process (go test -count=N runs
// the test N times in one process).
var (
	testReaderOnce sync.Once
	testReader     *sdkmetric.ManualReader
)

func boundedViewReader() *sdkmetric.ManualReader {
	testReaderOnce.Do(func() {
		testReader = sdkmetric.NewManualReader()
		otel.SetMeterProvider(sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(testReader), sdkmetric.WithView(boundedView)))
	})
	return testReader
}

// TestBoundedView records through the package's instruments and checks that
// per-block and per-error values don't create new attribute sets.
func TestBoundedView(t *testing.T) {
	reader := boundedViewReader()

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
	Node.BlacklistOperation(ctx, "add", "incident 1", true)
	Node.BlacklistOperation(ctx, "add", "incident 2", true)
	Node.BlockedConnection(ctx, "inbound", "incident 1")
	Node.BlockedConnection(ctx, "inbound", "connection_exhaustion")

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
	// "manual" and "custom" (free-text reasons from the admin service).
	require.Len(t, attrSets["node.blacklist.operations.count"], 2)
	require.Equal(t, 3, attrSets["node.blacklist.operations.count"][0].Len())
	require.ElementsMatch(t, []string{"manual", "custom"}, reasons(attrSets["node.blacklist.operations.count"]))
	require.ElementsMatch(t, []string{"custom", "connection_exhaustion"}, reasons(attrSets["node.blacklist.connections_blocked.count"]))

	require.Equal(t, latencyBucketsMS, queryBounds)
}

func reasons(sets []attribute.Set) []string {
	var out []string
	for _, s := range sets {
		v, _ := s.Value("reason")
		out = append(out, v.AsString())
	}
	return out
}
