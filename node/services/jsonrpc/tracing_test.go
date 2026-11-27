package rpcserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/trufnetwork/kwil-db/core/log"
	jsonrpc "github.com/trufnetwork/kwil-db/core/rpc/json"
)

func TestServerSpans(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	const (
		traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
		spanID  = "00f067aa0ba902b7"
	)

	newServer := func(t *testing.T, opts ...Opt) (*Server, *trace.SpanContext) {
		srv, err := NewServer("127.0.0.1:", log.DiscardLogger, opts...)
		require.NoError(t, err)
		var handlerSpan trace.SpanContext
		srv.RegisterMethodHandler("user.dummy",
			MakeMethodHandler(func(ctx context.Context, _ *any) (*json.RawMessage, *jsonrpc.Error) {
				handlerSpan = trace.SpanContextFromContext(ctx)
				return nil, jsonrpc.NewError(jsonrpc.ErrorNoQueryWithPrivateRPC, "secret detail", nil)
			}))
		srv.RegisterMethodHandler("user.panic",
			MakeMethodHandler(func(context.Context, *any) (*json.RawMessage, *jsonrpc.Error) {
				panic("secret detail")
			}))
		return srv, &handlerSpan
	}
	post := func(srv *Server, body string) {
		r := httptest.NewRequest(http.MethodPost, pathRPCV1, strings.NewReader(body))
		r.Header.Set("Traceparent", "00-"+traceID+"-"+spanID+"-01")
		srv.srv.Handler.ServeHTTP(httptest.NewRecorder(), r)
	}

	t.Run("registered method", func(t *testing.T) {
		rec.Reset()
		srv, handlerSpan := newServer(t, WithTracing())
		post(srv, `{"jsonrpc":"2.0","id":1,"method":"user.dummy"}`)

		spans := rec.Ended()
		require.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, "user.dummy", s.Name())
		assert.Equal(t, trace.SpanKindServer, s.SpanKind())
		assert.Equal(t, traceID, s.SpanContext().TraceID().String())
		assert.Equal(t, spanID, s.Parent().SpanID().String())
		assert.Equal(t, s.SpanContext().SpanID(), handlerSpan.SpanID(), "handler runs under the span")
		assert.Equal(t, codes.Error, s.Status().Code)
		assert.Empty(t, s.Status().Description)
		for _, a := range s.Attributes() {
			assert.NotContains(t, a.Value.Emit(), "secret")
		}
	})

	t.Run("panicking handler", func(t *testing.T) {
		rec.Reset()
		srv, _ := newServer(t, WithTracing())
		post(srv, `{"jsonrpc":"2.0","id":1,"method":"user.panic"}`)

		spans := rec.Ended()
		require.Len(t, spans, 1)
		assert.Equal(t, codes.Error, spans[0].Status().Code)
		assert.Empty(t, spans[0].Status().Description)
		assert.Empty(t, spans[0].Events(), "panic value not recorded")
	})

	t.Run("unknown method", func(t *testing.T) {
		rec.Reset()
		srv, _ := newServer(t, WithTracing())
		post(srv, `{"jsonrpc":"2.0","id":1,"method":"user.0x1234567890abcdef1234567890abcdef12345678"}`)
		assert.Empty(t, rec.Started())
	})

	t.Run("without WithTracing", func(t *testing.T) {
		rec.Reset()
		srv, handlerSpan := newServer(t)
		post(srv, `{"jsonrpc":"2.0","id":1,"method":"user.dummy"}`)
		assert.Empty(t, rec.Started())
		assert.False(t, handlerSpan.IsValid(), "traceparent not taken over")
	})
}
