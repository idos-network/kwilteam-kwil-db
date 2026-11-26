package metrics

import (
	"context"
	"runtime/metrics"
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
	epochAgeGauge       metric.Float64Gauge
	epochFinalizeGauge  metric.Int64Gauge
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
	epochAgeGauge, _ = bridgeMeter.Float64Gauge("erc20.epoch.age_seconds",
		metric.WithDescription("Seconds since the current reward epoch started (block time)"), metric.WithUnit("s"))
	epochFinalizeGauge, _ = bridgeMeter.Int64Gauge("erc20.epoch.finalization_waiting",
		metric.WithDescription("1 while a due epoch is not finalized, by reason (no_rewards, previous_unconfirmed)"))

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

func (engineMetrics) RecordActionCall(ctx context.Context, kind, namespace, action string, ok bool, duration time.Duration) {
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

// BridgeMetrics records the epoch state of ERC-20 bridge instances.
type BridgeMetrics interface {
	// RecordEpoch records, for one bridge instance at the end of a block, the
	// age of the current epoch and why a due epoch isn't finalized ("" when it
	// isn't due or was just finalized).
	RecordEpoch(ctx context.Context, bridgeID, chain string, age time.Duration, waitingReason string)
}

// Reasons a due epoch isn't finalized, for BridgeMetrics.RecordEpoch.
const (
	EpochWaitingNoRewards           = "no_rewards"
	EpochWaitingPreviousUnconfirmed = "previous_unconfirmed"
)

type bridgeMetrics struct{}

func (bridgeMetrics) RecordEpoch(ctx context.Context, bridgeID, chain string, age time.Duration, waitingReason string) {
	bridge := []attribute.KeyValue{attribute.String("bridge", bridgeID), attribute.String("chain", chain)}
	epochAgeGauge.Record(ctx, age.Seconds(), metric.WithAttributes(bridge...))
	// Both reasons on every block, so the one that no longer applies goes back to 0.
	for _, reason := range []string{EpochWaitingNoRewards, EpochWaitingPreviousUnconfirmed} {
		var v int64
		if reason == waitingReason {
			v = 1
		}
		epochFinalizeGauge.Record(ctx, v, metric.WithAttributes(append(bridge, attribute.String("reason", reason))...))
	}
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
