package metrics

import (
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
)

// idOS fork: the instruments in metrics.go record attributes such as the
// block height, block size, latency and error text. Each new value is a new
// attribute set that the SDK keeps for the life of the process (cumulative
// temporality, no cardinality limit by default) and exports on every
// interval, so with telemetry enabled the node's memory and export size grow
// with every block. boundedView keeps only attributes with a small, fixed set
// of values and drops everything else, including attributes added upstream
// later, before aggregation.

// allowedAttributes lists the attribute keys that are kept. All have a small,
// fixed set of values.
var allowedAttributes = map[attribute.Key]struct{}{
	"method":    {}, // JSON-RPC method, only registered methods are recorded
	"status":    {}, // JSON-RPC error code
	"type":      {}, // SQL command (SELECT, INSERT, ...), see sqlCommand; tx payload type
	"proto":     {}, // p2p protocol id
	"operation": {}, // blacklist add / remove
	"reason":    {}, // blacklist reason, see blacklistReason; epoch waiting reason
	"permanent": {}, // blacklist entry is permanent
	"direction": {}, // inbound / outbound
	// idos.go
	"kind":      {}, // action call kind: tx, call, internal
	"namespace": {}, // namespace of an action that exists
	"action":    {}, // action that exists (unknown names are not recorded)
	"result":    {}, // ok / error
	"bridge":    {}, // ERC-20 bridge instance ID
	"chain":     {}, // EVM chain of a bridge instance
}

// latencyBucketsMS are the histogram bounds for the latency instruments, which
// record milliseconds. The SDK default starts at 5 ms, but most SQL queries
// take less than that.
var latencyBucketsMS = []float64{0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000}

// actionLatencyBucketsMS are fewer bounds for action.duration (idos.go), which
// has one histogram per action: each bound is a series per action.
var actionLatencyBucketsMS = []float64{5, 25, 100, 500, 2500, 10000}

func allowedAttribute(kv attribute.KeyValue) bool {
	_, ok := allowedAttributes[kv.Key]
	return ok
}

// boundedView applies the attribute allowlist to every instrument and the
// millisecond bounds to every histogram.
func boundedView(inst metric.Instrument) (metric.Stream, bool) {
	s := metric.Stream{
		Name:            inst.Name,
		Description:     inst.Description,
		Unit:            inst.Unit,
		AttributeFilter: allowedAttribute,
	}
	if inst.Kind == metric.InstrumentKindHistogram {
		bounds := latencyBucketsMS
		if inst.Name == "action.duration" {
			bounds = actionLatencyBucketsMS
		}
		s.Aggregation = metric.AggregationExplicitBucketHistogram{Boundaries: bounds}
	}
	return s, true
}

// sqlCommand reduces a Postgres command tag such as "INSERT 0 5" or
// "SELECT 12" to its command ("INSERT", "SELECT"), without the row count.
func sqlCommand(tag string) string {
	cmd, _, _ := strings.Cut(strings.TrimSpace(tag), " ")
	if cmd == "" {
		return "UNKNOWN"
	}
	return strings.ToUpper(cmd)
}

// blacklistReason keeps the reasons kwild itself sets and records any other
// reason as "custom". An operator can blacklist a peer with free text through
// the admin service, and the peer gate reports that text again for each
// blocked connection; each distinct text would be a new attribute set. The
// full reason stays in the blacklist and the logs.
func blacklistReason(reason string) string {
	switch reason {
	case "manual", "connection_exhaustion":
		return reason
	default:
		return "custom"
	}
}
