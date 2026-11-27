package metrics

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Spans for requests served by the node. StartOTEL sets the node's tracer
// provider (SetTracerProvider) and the W3C trace-context propagator; without
// them every span here is a no-op. The provider is not the global one, so
// libraries that trace through otel.Tracer (libp2p's DHT) record nothing.
//
// Only the server span of a request may start a trace. The other spans are
// started only under an existing span, so work without a request (block
// execution, background jobs) records nothing. Spans carry names, codes and
// counts, never argument values, SQL text or error messages, which can hold
// user data.

const tracerName = "github.com/trufnetwork/kwil-db/node"

type tracerProviderHolder struct{ trace.TracerProvider }

var nodeTracerProvider atomic.Value // tracerProviderHolder

// SetTracerProvider sets the provider of the node's spans; nil turns them off.
func SetTracerProvider(tp trace.TracerProvider) {
	nodeTracerProvider.Store(tracerProviderHolder{tp})
}

// Tracer returns the node's tracer.
func Tracer() trace.Tracer {
	if h, _ := nodeTracerProvider.Load().(tracerProviderHolder); h.TracerProvider != nil {
		return h.Tracer(tracerName)
	}
	return noop.NewTracerProvider().Tracer(tracerName)
}

// ExtractTraceContext returns ctx with the remote span context from the
// request's traceparent header, if any.
func ExtractTraceContext(ctx context.Context, h http.Header) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(h))
}

// StartRPCSpan starts the server span of a JSON-RPC request, a child of the
// caller's span when the request carried a traceparent. method must be a
// registered method name, not client input.
func StartRPCSpan(ctx context.Context, method string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, method,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("rpc.system", "jsonrpc"),
			attribute.String("rpc.method", method),
		))
}

// EndRPCSpan ends a span from StartRPCSpan. code is the JSON-RPC error code, 0
// on success.
func EndRPCSpan(span trace.Span, code int) {
	if code != 0 {
		span.SetAttributes(attribute.Int("rpc.jsonrpc.error_code", code))
		span.SetStatus(codes.Error, "")
	}
	span.End()
}

// StartActionSpan starts a span for a top-level action call that began at
// start, if ctx already has a span (a request). ok is false otherwise, and the
// returned context and span must not be used.
func StartActionSpan(ctx context.Context, kind, namespace, action string, start time.Time) (_ context.Context, _ trace.Span, ok bool) {
	if ctx == nil || !trace.SpanContextFromContext(ctx).IsValid() {
		return nil, nil, false
	}
	namespace, action = boundedActionName(namespace, action)
	ctx, span := Tracer().Start(ctx, "action "+namespace+"."+action,
		trace.WithTimestamp(start),
		trace.WithAttributes(
			attribute.String("kwil.call.kind", kind),
			attribute.String("kwil.namespace", namespace),
			attribute.String("kwil.action", action),
		))
	return ctx, span, true
}

// EndActionSpan ends a span from StartActionSpan.
func EndActionSpan(span trace.Span, ok bool) {
	span.SetAttributes(attribute.String("kwil.result", result(ok)))
	if !ok {
		span.SetStatus(codes.Error, "")
	}
	span.End()
}
