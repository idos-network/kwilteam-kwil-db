package pg

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/trufnetwork/kwil-db/node/metrics"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	metrics.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { metrics.SetTracerProvider(nil) })
	return rec
}

func TestLeadingKeyword(t *testing.T) {
	for stmt, want := range map[string]string{
		"SELECT 1":                         "SELECT",
		"  with x AS (SELECT 1) SELECT *":  "WITH",
		"(select 1)":                       "SELECT",
		"BEGIN":                            "BEGIN",
		"SAVEPOINT sp_1":                   "SAVEPOINT",
		"commit;":                          "COMMIT",
		"-- comment\nSELECT 1":             "",
		"SELECT$1":                         "",
		"abcdefghijklmnopqrstuvwxyz stuff": "",
		"":                                 "",
	} {
		assert.Equal(t, want, leadingKeyword(stmt), stmt)
	}
}

func TestQueryTracerWithoutRequest(t *testing.T) {
	rec := recordSpans(t)
	var tr queryTracer

	ctx := context.Background()
	got := tr.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	assert.Equal(t, ctx, got)
	tr.TraceQueryEnd(got, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})
	assert.Empty(t, rec.Started())
}

func TestQueryTracer(t *testing.T) {
	rec := recordSpans(t)
	var tr queryTracer

	reqCtx, reqSpan := metrics.Tracer().Start(context.Background(), "user.call")

	const secret = "0x1234567890abcdef1234567890abcdef12345678"
	ctx := tr.TraceQueryStart(reqCtx, nil, pgx.TraceQueryStartData{
		SQL: "SELECT * FROM wallets WHERE address = $1", Args: []any{secret}})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 3")})

	pgErr := &pgconn.PgError{Code: "23505", Message: "duplicate key value",
		Detail: fmt.Sprintf("Key (address)=(%s) already exists.", secret)}
	ctx = tr.TraceQueryStart(reqCtx, nil, pgx.TraceQueryStartData{SQL: "INSERT INTO wallets VALUES ($1)", Args: []any{secret}})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: fmt.Errorf("insert: %w", pgErr)})

	ctx = tr.TraceQueryStart(reqCtx, nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("conn closed")})
	reqSpan.End()

	spans := rec.Ended()
	require.Len(t, spans, 4) // three queries and the request

	sel := spans[0]
	assert.Equal(t, "pg SELECT", sel.Name())
	assert.Equal(t, reqSpan.SpanContext().SpanID(), sel.Parent().SpanID())
	attrs := map[string]any{}
	for _, a := range sel.Attributes() {
		attrs[string(a.Key)] = a.Value.AsInterface()
	}
	assert.Equal(t, map[string]any{
		"db.system.name":            "postgresql",
		"db.operation.name":         "SELECT",
		"db.response.returned_rows": int64(3),
	}, attrs)

	ins := spans[1]
	assert.Equal(t, "pg INSERT", ins.Name()) // from the statement: no command tag on failure
	assert.Equal(t, codes.Error, ins.Status().Code)
	assert.Empty(t, ins.Status().Description)
	attrs = map[string]any{}
	for _, a := range ins.Attributes() {
		attrs[string(a.Key)] = a.Value.AsInterface()
	}
	assert.Equal(t, map[string]any{
		"db.system.name":          "postgresql",
		"db.operation.name":       "INSERT",
		"db.response.status_code": "23505",
	}, attrs)
	assert.Empty(t, ins.Events())

	other := spans[2]
	assert.Equal(t, codes.Error, other.Status().Code)
	assert.Empty(t, other.Status().Description)

	for _, s := range spans {
		for _, a := range s.Attributes() {
			assert.NotContains(t, a.Value.Emit(), secret)
		}
	}
}
