package metrics

import (
	"context"
	"runtime/metrics"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Per-action engine calls, executed transactions by payload type, mempool
// size, Go runtime memory and the ERC-20 bridge epoch state. Every attribute
// has a bounded set of values (views.go). Gauge names don't end in "total" or
// "count", which Prometheus naming reserves for counters.

var (
	Engine  EngineMetrics  = engineMetrics{}
	Bridge  BridgeMetrics  = bridgeMetrics{}
	Mempool MempoolMetrics = mempoolMetrics{}
)

// RuntimeMeterName is the meter of the Go runtime metrics of the kwild process.
const RuntimeMeterName = "github.com/trufnetwork/kwil-db/node/runtime"

// ERC20BridgeMeterName is the meter of the ERC-20 bridge extension.
const ERC20BridgeMeterName = "github.com/trufnetwork/kwil-db/node/exts/erc20-bridge"

var (
	actionCallsCounter  metric.Int64Counter
	actionDurationHist  metric.Float64Histogram
	txExecutedCounter   metric.Int64Counter
	bridgeEpochs        atomic.Pointer[[]EpochState]
	mempoolSizeFunc     atomic.Pointer[func() (txs, bytes int64)]
	runtimeMetricSample = []metrics.Sample{
		{Name: "/sched/goroutines:goroutines"},
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/gc/heap/goal:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
)

func init() {
	engineMeter := otel.Meter(EngineMeterName)
	actionCallsCounter, _ = engineMeter.Int64Counter("action.calls",
		metric.WithDescription("Top-level action calls by kind (tx, call, internal), namespace, action and result"))
	actionDurationHist, _ = engineMeter.Float64Histogram("action.duration",
		metric.WithDescription("Duration of top-level action calls in milliseconds"), metric.WithUnit("ms"))

	bpMeter := otel.Meter(BlockProcessorMeterName)
	txExecutedCounter, _ = bpMeter.Int64Counter("tx.executed",
		metric.WithDescription("Transactions executed in blocks by payload type and result"))

	bridgeMeter := otel.Meter(ERC20BridgeMeterName)
	_, _ = bridgeMeter.Float64ObservableGauge("erc20.epoch.age_seconds",
		metric.WithDescription("Seconds since the current reward epoch started (block time)"), metric.WithUnit("s"),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			for _, e := range loadEpochs() {
				o.Observe(e.Age.Seconds(), metric.WithAttributes(e.attrs()...))
			}
			return nil
		}))
	_, _ = bridgeMeter.Int64ObservableGauge("erc20.epoch.finalization_waiting",
		metric.WithDescription("1 while a due epoch is not finalized, by reason (no_rewards, previous_unconfirmed)"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			for _, e := range loadEpochs() {
				for _, reason := range []string{EpochWaitingNoRewards, EpochWaitingPreviousUnconfirmed} {
					var v int64
					if reason == e.WaitingReason {
						v = 1
					}
					o.Observe(v, metric.WithAttributes(append(e.attrs(), attribute.String("reason", reason))...))
				}
			}
			return nil
		}))

	mempoolMeter := otel.Meter(MempoolMeterName)
	_, _ = mempoolMeter.Int64ObservableGauge("mempool.txs",
		metric.WithDescription("Transactions in the mempool"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			if fn := mempoolSizeFunc.Load(); fn != nil {
				txs, _ := (*fn)()
				o.Observe(txs)
			}
			return nil
		}))
	_, _ = mempoolMeter.Int64ObservableGauge("mempool.bytes",
		metric.WithDescription("Size of the transactions in the mempool in bytes"), metric.WithUnit("By"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			if fn := mempoolSizeFunc.Load(); fn != nil {
				_, bytes := (*fn)()
				o.Observe(bytes)
			}
			return nil
		}))

	registerRuntimeMetrics(otel.Meter(RuntimeMeterName))
}

// registerRuntimeMetrics reports Go runtime metrics of the process. They are
// read from runtime/metrics, which doesn't stop the world, once per export.
func registerRuntimeMetrics(m metric.Meter) {
	goroutines, _ := m.Int64ObservableGauge("go.goroutines",
		metric.WithDescription("Live goroutines"))
	heapObjects, _ := m.Int64ObservableGauge("go.memory.heap_objects_bytes",
		metric.WithDescription("Bytes of live and not yet swept heap objects"), metric.WithUnit("By"))
	total, _ := m.Int64ObservableGauge("go.memory.mapped_bytes",
		metric.WithDescription("All memory mapped by the Go runtime"), metric.WithUnit("By"))
	heapGoal, _ := m.Int64ObservableGauge("go.gc.heap_goal_bytes",
		metric.WithDescription("Heap size target for the end of the GC cycle"), metric.WithUnit("By"))
	gcCycles, _ := m.Int64ObservableCounter("go.gc.cycles",
		metric.WithDescription("Completed GC cycles"))

	_, _ = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		samples := make([]metrics.Sample, len(runtimeMetricSample))
		copy(samples, runtimeMetricSample)
		metrics.Read(samples)
		for i, inst := range []metric.Int64Observable{goroutines, heapObjects, total, heapGoal, gcCycles} {
			if samples[i].Value.Kind() != metrics.KindUint64 {
				continue // not supported by this Go version
			}
			o.ObserveInt64(inst, int64(samples[i].Value.Uint64()))
		}
		return nil
	}, goroutines, heapObjects, total, heapGoal, gcCycles)
}

// EngineMetrics records top-level action calls.
type EngineMetrics interface {
	// RecordActionCall records a top-level call of an action that exists. kind
	// is "tx" (in a transaction), "call" (read-only, e.g. user.call) or
	// "internal" (an extension without a transaction context).
	RecordActionCall(ctx context.Context, kind, namespace, action string, ok bool, duration time.Duration)
	// RecordTx records a transaction executed in a block.
	RecordTx(ctx context.Context, payloadType string, ok bool)
}

type engineMetrics struct{}

func result(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

// maxActionNames caps the distinct namespace/action pairs recorded. Only actions
// that exist are recorded, but the SDK keeps a series for the life of the
// process, also after an action is dropped, so creating actions over and over
// would otherwise grow the series without limit. Further pairs are recorded as
// namespace and action "other".
const maxActionNames = 1000

var (
	actionNamesMu sync.Mutex
	actionNames   = map[[2]string]struct{}{}
)

func boundedActionName(namespace, action string) (string, string) {
	key := [2]string{namespace, action}
	actionNamesMu.Lock()
	defer actionNamesMu.Unlock()
	if _, ok := actionNames[key]; ok {
		return namespace, action
	}
	if len(actionNames) >= maxActionNames {
		return "other", "other"
	}
	actionNames[key] = struct{}{}
	return namespace, action
}

func (engineMetrics) RecordActionCall(ctx context.Context, kind, namespace, action string, ok bool, duration time.Duration) {
	namespace, action = boundedActionName(namespace, action)
	attrs := []attribute.KeyValue{
		attribute.String("kind", kind),
		attribute.String("namespace", namespace),
		attribute.String("action", action),
	}
	actionCallsCounter.Add(ctx, 1, metric.WithAttributes(append(attrs, attribute.String("result", result(ok)))...))
	actionDurationHist.Record(ctx, 1000*duration.Seconds(), metric.WithAttributes(attrs...))
}

func (engineMetrics) RecordTx(ctx context.Context, payloadType string, ok bool) {
	txExecutedCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("type", payloadType),
		attribute.String("result", result(ok)),
	))
}

// BridgeMetrics reports the epoch state of ERC-20 bridge instances.
type BridgeMetrics interface {
	// SetEpochs replaces the reported state with that of the active instances
	// at the end of a block. Instances not in the list (disabled, removed)
	// are no longer exported.
	SetEpochs(states []EpochState)
}

// EpochState is the epoch state of one bridge instance at the end of a block.
type EpochState struct {
	BridgeID string
	Chain    string
	// Age is the block time since the current epoch started.
	Age time.Duration
	// WaitingReason is why an epoch that is due isn't finalized:
	// EpochWaitingNoRewards, EpochWaitingPreviousUnconfirmed, or "" when it
	// isn't due or was just finalized.
	WaitingReason string
}

func (e EpochState) attrs() []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("bridge", e.BridgeID), attribute.String("chain", e.Chain)}
}

// Reasons a due epoch isn't finalized, for EpochState.
const (
	EpochWaitingNoRewards           = "no_rewards"
	EpochWaitingPreviousUnconfirmed = "previous_unconfirmed"
)

type bridgeMetrics struct{}

func (bridgeMetrics) SetEpochs(states []EpochState) {
	states = slices.Clone(states)
	bridgeEpochs.Store(&states)
}

func loadEpochs() []EpochState {
	if p := bridgeEpochs.Load(); p != nil {
		return *p
	}
	return nil
}

// MempoolMetrics reports the mempool size.
type MempoolMetrics interface {
	// SetSizeFunc sets the function the mempool gauges read on every export.
	SetSizeFunc(fn func() (txs, bytes int64))
}

type mempoolMetrics struct{}

func (mempoolMetrics) SetSizeFunc(fn func() (txs, bytes int64)) {
	mempoolSizeFunc.Store(&fn)
}
