package pg

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsUserQueryError(t *testing.T) {
	for _, tc := range []struct {
		code, schema string
		want         bool
	}{
		{"23505", "main", true},  // unique_violation
		{"23514", "app", true},   // check_violation
		{"22P02", "main", true},  // invalid_text_representation
		{"P0001", "main", true},  // ERROR() in an action
		{"42P01", "ds_x", true},  // any error in a pre-0.10 namespace
		{"42P01", "main", false}, // undefined_table
		{"53300", "", false},     // too_many_connections
		{"57P01", "", false},     // admin_shutdown
	} {
		if got := isUserQueryError(&pgconn.PgError{Code: tc.code, SchemaName: tc.schema}); got != tc.want {
			t.Errorf("isUserQueryError(%s, %q) = %v, want %v", tc.code, tc.schema, got, tc.want)
		}
	}
}
