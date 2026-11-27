package metrics

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recordSpans installs a tracer provider with an in-memory recorder and the
// trace-context propagator, as StartOTEL does, until the test ends.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())))
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return rec
}

func attrKeys(attrs []attribute.KeyValue) []string {
	keys := make([]string, len(attrs))
	for i, a := range attrs {
		keys[i] = string(a.Key)
	}
	return keys
}

const (
	testTraceID    = "4bf92f3577b34da6a3ce929d0e0e4736"
	testParentSpan = "00f067aa0ba902b7"
)

func TestRPCSpanContinuesTraceparent(t *testing.T) {
	rec := recordSpans(t)

	h := http.Header{}
	h.Set("Traceparent", "00-"+testTraceID+"-"+testParentSpan+"-01")
	ctx := ExtractTraceContext(context.Background(), h)
	_, span := StartRPCSpan(ctx, "user.call")
	EndRPCSpan(span, -32001)

	spans := rec.Ended()
	require.Len(t, spans, 1)
	s := spans[0]
	assert.Equal(t, "user.call", s.Name())
	assert.Equal(t, testTraceID, s.SpanContext().TraceID().String())
	assert.Equal(t, testParentSpan, s.Parent().SpanID().String())
	assert.ElementsMatch(t, []string{"rpc.system", "rpc.method", "rpc.jsonrpc.error_code"}, attrKeys(s.Attributes()))
	assert.Equal(t, codes.Error, s.Status().Code)
	assert.Empty(t, s.Status().Description)
}

func TestRPCSpanWithoutTraceparent(t *testing.T) {
	rec := recordSpans(t)

	ctx := ExtractTraceContext(context.Background(), http.Header{})
	_, span := StartRPCSpan(ctx, "user.broadcast")
	EndRPCSpan(span, 0)

	spans := rec.Ended()
	require.Len(t, spans, 1)
	assert.False(t, spans[0].Parent().IsValid())
	assert.Equal(t, codes.Unset, spans[0].Status().Code)
	assert.ElementsMatch(t, []string{"rpc.system", "rpc.method"}, attrKeys(spans[0].Attributes()))
}

func TestRPCSpanFollowsUnsampledCaller(t *testing.T) {
	rec := recordSpans(t)

	h := http.Header{}
	h.Set("Traceparent", "00-"+testTraceID+"-"+testParentSpan+"-00")
	_, span := StartRPCSpan(ExtractTraceContext(context.Background(), h), "user.call")
	EndRPCSpan(span, 0)

	assert.Empty(t, rec.Ended())
}

func TestActionSpan(t *testing.T) {
	rec := recordSpans(t)

	// Without a request span (block execution): nothing.
	_, _, ok := StartActionSpan(context.Background(), "tx", "main", "add_wallet", time.Now())
	assert.False(t, ok)
	assert.Empty(t, rec.Started())

	reqCtx, reqSpan := StartRPCSpan(context.Background(), "user.call")
	start := time.Now().Add(-time.Second)
	actCtx, span, ok := StartActionSpan(reqCtx, "call", "main", "get_wallets", start)
	require.True(t, ok)
	EndActionSpan(span, false)
	reqSpan.End()

	var act sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() == "action main.get_wallets" {
			act = s
		}
	}
	require.NotNil(t, act)
	assert.Equal(t, reqSpan.SpanContext().SpanID(), act.Parent().SpanID())
	assert.Equal(t, act.SpanContext(), trace.SpanContextFromContext(actCtx))
	assert.True(t, act.StartTime().Equal(start))
	assert.ElementsMatch(t, []string{"kwil.call.kind", "kwil.namespace", "kwil.action", "kwil.result"}, attrKeys(act.Attributes()))
	assert.Equal(t, codes.Error, act.Status().Code)
	assert.Empty(t, act.Status().Description)
}
