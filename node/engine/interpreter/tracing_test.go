//go:build pglive

package interpreter_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/trufnetwork/kwil-db/node/metrics"
)

// A top-level call under a request span gets an action span, and its SQL
// statements become children of it; without a request span (block execution)
// nothing is recorded.
func Test_ActionSpans(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	db := newTestDB(t, nil, nil)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	interp := newTestInterp(t, tx, nil, false)
	err = interp.ExecuteWithoutEngineCtx(ctx, tx, "CREATE TABLE tbl (id int primary key, val text);", nil, nil)
	require.NoError(t, err)
	err = interp.ExecuteWithoutEngineCtx(ctx, tx, `CREATE ACTION put($id int, $val text) public { INSERT INTO tbl (id, val) VALUES ($id, $val); };`, nil, nil)
	require.NoError(t, err)

	// Block execution: no request span, no spans.
	rec.Reset()
	_, err = interp.Call(newEngineCtx(defaultCaller), tx, "", "put", []any{1, "secret-value"}, nil)
	require.NoError(t, err)
	assert.Empty(t, rec.Started())

	// A request.
	reqCtx, reqSpan := metrics.Tracer().Start(ctx, "user.call")
	engCtx := newEngineCtx(defaultCaller)
	engCtx.TxContext.Ctx = reqCtx
	_, err = interp.Call(engCtx, tx, "", "put", []any{2, "secret-value"}, nil)
	require.NoError(t, err)
	assert.Equal(t, reqCtx, engCtx.TxContext.Ctx, "request context restored")

	// The same insert again fails on the primary key.
	res, err := interp.Call(engCtx, tx, "", "put", []any{2, "secret-value"}, nil)
	require.NoError(t, err)
	require.NotNil(t, res.Error)
	reqSpan.End()

	var actions []sdktrace.ReadOnlySpan
	children := map[trace.SpanID][]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		if s.Name() == "action main.put" {
			actions = append(actions, s)
		}
		children[s.Parent().SpanID()] = append(children[s.Parent().SpanID()], s)
		for _, a := range s.Attributes() {
			assert.NotContains(t, a.Value.Emit(), "secret")
		}
		assert.NotContains(t, s.Status().Description, "secret")
	}
	require.Len(t, actions, 2)
	for _, a := range actions {
		assert.Equal(t, reqSpan.SpanContext().SpanID(), a.Parent().SpanID())
		require.NotEmpty(t, children[a.SpanContext().SpanID()], "SQL spans under the action")
		for _, q := range children[a.SpanContext().SpanID()] {
			assert.Contains(t, q.Name(), "pg ")
		}
	}
	assert.Equal(t, codes.Unset, actions[0].Status().Code)
	assert.Equal(t, codes.Error, actions[1].Status().Code)
}
