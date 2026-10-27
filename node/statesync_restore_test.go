package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/trufnetwork/kwil-db/config"
	"github.com/trufnetwork/kwil-db/core/log"
)

func TestStageSnapshotDumpRejectsHashMismatchBeforeImport(t *testing.T) {
	dump := []byte("CREATE SCHEMA kwild_voting;\n")
	wrong := sha256.Sum256([]byte("not-the-dump"))

	f, err := stageSnapshotDump(context.Background(), t.TempDir(), bytes.NewReader(dump), wrong[:])
	require.Error(t, err)
	require.Nil(t, f)
	require.Contains(t, err.Error(), "invalid snapshot hash")
}

func TestStageSnapshotDumpReturnsVerifiedDump(t *testing.T) {
	dump := []byte("CREATE SCHEMA kwild_voting;\n")
	sum := sha256.Sum256(dump)

	dir := t.TempDir()
	f, err := stageSnapshotDump(context.Background(), dir, bytes.NewReader(dump), sum[:])
	require.NoError(t, err)
	t.Cleanup(func() {
		f.Close()
		os.Remove(f.Name())
	})
	require.Equal(t, dir, filepath.Dir(f.Name()))

	got, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, dump, got)
}

func TestStageSnapshotDumpHonorsCancel(t *testing.T) {
	dump := []byte("CREATE SCHEMA kwild_voting;\n")
	sum := sha256.Sum256(dump)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	f, err := stageSnapshotDump(ctx, t.TempDir(), bytes.NewReader(dump), sum[:])
	require.Nil(t, f)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRestoreDBHashMismatchDoesNotStartPsql(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	script := filepath.Join(dir, "psql")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\ntouch "+started+"\ncat >/dev/null\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dump := []byte("CREATE SCHEMA kwild_voting;\n")
	staging := t.TempDir()
	err := RestoreDB(context.Background(), bytes.NewReader(dump), config.DBConfig{
		Host:   "127.0.0.1",
		Port:   "5432",
		User:   "kwild",
		DBName: "kwild",
	}, []byte("bad-hash-bytes-not-32-but-checked"), staging, log.DiscardLogger)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid snapshot hash")
	_, statErr := os.Stat(started)
	require.ErrorIs(t, statErr, os.ErrNotExist)
	incomplete, err := RestoreIncomplete(staging)
	require.NoError(t, err)
	require.False(t, incomplete)
}

func TestRestoreDBImportFailureWaitsAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "psql.log")
	script := filepath.Join(dir, "psql")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
echo "$*" >> "`+logPath+`"
for arg in "$@"; do
  if [ "$arg" = "-c" ]; then
    echo CLEANUP >> "`+logPath+`"
    exit 0
  fi
done
cat >/dev/null
echo WAITED >> "`+logPath+`"
exit 1
`), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dump := []byte("CREATE SCHEMA kwild_voting;\n")
	sum := sha256.Sum256(dump)
	staging := t.TempDir()
	err := RestoreDB(context.Background(), bytes.NewReader(dump), config.DBConfig{
		Host:   "127.0.0.1",
		Port:   "5432",
		User:   "kwild",
		DBName: "kwild",
	}, sum[:], staging, log.DiscardLogger)
	require.Error(t, err)
	incomplete, err := RestoreIncomplete(staging)
	require.NoError(t, err)
	require.True(t, incomplete, "failed import must stay marked so restart wipes it")

	logged, err := os.ReadFile(logPath)
	require.NoError(t, err)
	out := string(logged)
	require.Contains(t, out, "WAITED")
	require.Contains(t, out, "CLEANUP")
	require.Less(t, strings.Index(out, "WAITED"), strings.Index(out, "CLEANUP"), "cleanup must run after cmd.Wait")
}

func TestRestoreIncompleteMarkerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	incomplete, err := RestoreIncomplete(dir)
	require.NoError(t, err)
	require.False(t, incomplete)
	require.NoError(t, markRestoreIncomplete(dir))
	incomplete, err = RestoreIncomplete(dir)
	require.NoError(t, err)
	require.True(t, incomplete)
	require.NoError(t, ClearRestoreIncomplete(dir))
	incomplete, err = RestoreIncomplete(dir)
	require.NoError(t, err)
	require.False(t, incomplete)

	file := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	_, err = RestoreIncomplete(file)
	require.Error(t, err)
}

func TestDropRestoreSchemasSQLDropsUserNamespaces(t *testing.T) {
	require.Contains(t, dropRestoreSchemasSQL, "nspname <> 'public'")
	require.Contains(t, dropRestoreSchemasSQL, "information_schema")
	require.Contains(t, dropRestoreSchemasSQL, `NOT LIKE 'pg\_%'`)
	require.NotContains(t, dropRestoreSchemasSQL, "kwild\\_%")
}

func TestDropRestoreSchemasAfterFailure(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "psql.log")
	script := filepath.Join(dir, "psql")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho CLEANUP >> \""+logPath+"\"\n"), 0o755))

	err := dropRestoreSchemasAfterFailure(script, config.DBConfig{
		Host:   "127.0.0.1",
		Port:   "5432",
		User:   "kwild",
		DBName: "kwild",
	})
	require.NoError(t, err)

	logged, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(logged), "CLEANUP")
}
