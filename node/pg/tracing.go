package pg

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/trufnetwork/kwil-db/node/metrics"
)

// queryTracer records a span per SQL statement run under a request span (see
// node/metrics/tracing.go). Statements without one, such as block execution,
// record nothing. The span has the command and the row count, not the
// statement or its arguments, and on failure the SQLSTATE code, not the
// message: Postgres messages can contain row values.
type queryTracer struct{}

var _ pgx.QueryTracer = queryTracer{}

type querySpanKey struct{}

func (queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}
	name := "pg"
	attrs := []attribute.KeyValue{attribute.String("db.system.name", "postgresql")}
	if cmd := leadingKeyword(data.SQL); cmd != "" {
		name += " " + cmd
		attrs = append(attrs, attribute.String("db.operation.name", cmd))
	}
	ctx, span := metrics.Tracer().Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	return context.WithValue(ctx, querySpanKey{}, span)
}

// leadingKeyword is the statement's first word in upper case if it is a plain
// keyword (letters only, at most 16), such as SELECT or WITH, and "" otherwise.
func leadingKeyword(stmt string) string {
	stmt = strings.TrimLeft(stmt, " \t\r\n(")
	end := 0
	for end < len(stmt) && end <= 16 {
		c := stmt[end]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z') {
			break
		}
		end++
	}
	if end == 0 || end > 16 || (end < len(stmt) && !strings.ContainsRune(" \t\r\n(;", rune(stmt[end]))) {
		return ""
	}
	return strings.ToUpper(stmt[:end])
}

func (queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(querySpanKey{}).(trace.Span)
	if !ok {
		return
	}
	// The command tag names what ran (SELECT for a WITH … SELECT); there is
	// none when the statement failed.
	if cmd, _, _ := strings.Cut(data.CommandTag.String(), " "); cmd != "" {
		span.SetName("pg " + cmd)
		span.SetAttributes(
			attribute.String("db.operation.name", cmd),
			attribute.Int64("db.response.returned_rows", data.CommandTag.RowsAffected()),
		)
	}
	if data.Err != nil {
		var pgErr *pgconn.PgError
		if errors.As(data.Err, &pgErr) {
			span.SetAttributes(attribute.String("db.response.status_code", pgErr.Code))
		}
		span.SetStatus(codes.Error, "")
	}
	span.End()
}
