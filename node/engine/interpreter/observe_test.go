package interpreter

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/common"
)

func TestSQLVerbTableUsesDeterministicKeys(t *testing.T) {
	t.Parallel()

	require.Equal(t, "insert:preliminary_credentials", sqlVerbTable(
		"INSERT INTO preliminary_credentials (id, user_id) VALUES ($request_id, $caller_user_id)",
	))
	require.Equal(t, "select:credentials", sqlVerbTable(
		"SELECT 1 FROM credentials WHERE id = $id",
	))
	require.Equal(t, "select:preliminary_credentials", sqlVerbTable(
		"SELECT 1 FROM preliminary_credentials WHERE original_id = $id OR copy_id = $id",
	))
	require.Equal(t, "update:wallets", sqlVerbTable("UPDATE wallets SET address = $a WHERE id = $id"))
	require.Equal(t, "delete:preliminary_credentials", sqlVerbTable(
		"DELETE FROM preliminary_credentials WHERE id = $preliminary_id",
	))
	require.Equal(t, "select", sqlVerbTable("SELECT 1"))
	require.Equal(t, "insert:credentials", sqlVerbTable("-- note\nINSERT INTO main.credentials VALUES (1)"))
	require.Equal(t, "sql", sqlVerbTable("   "))
}

func TestObserveExecutableSkipsBuiltinsAndNilTrace(t *testing.T) {
	t.Parallel()

	exec := &executionContext{
		engineCtx: &common.EngineContext{},
		scope:     newScope("main"),
	}
	called := false
	def := &executable{
		Name: "notice",
		Type: executableTypeFunction,
		Func: func(_ *executionContext, _ []Value, _ resultFunc) error {
			called = true
			return nil
		},
	}
	require.NoError(t, observeExecutable(exec, "main", def, nil, nil))
	require.True(t, called)
}

func TestObserveExecutableRecordsErrorPath(t *testing.T) {
	t.Parallel()

	tr := common.NewEngineTrace()
	exec := &executionContext{
		engineCtx: &common.EngineContext{
			TxContext: &common.TxContext{EngineTrace: tr},
		},
		scope: newScope("main"),
	}
	def := &executable{
		Name: "credential_id_in_use",
		Type: executableTypeAction,
		Func: func(_ *executionContext, _ []Value, _ resultFunc) error {
			return errors.New("boom")
		},
	}

	err := observeExecutable(exec, "main", def, nil, nil)
	require.EqualError(t, err, "boom")

	stages := tr.Stages()
	require.Len(t, stages, 1)
	require.Equal(t, "action", stages[0].Kind)
	require.Equal(t, "main", stages[0].Namespace)
	require.Equal(t, "credential_id_in_use", stages[0].Name)
	require.Equal(t, 1, stages[0].Count)
}
