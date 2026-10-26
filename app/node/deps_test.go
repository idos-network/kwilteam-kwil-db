package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trufnetwork/kwil-db/config"
	"github.com/trufnetwork/kwil-db/core/log"
)

func Test_getPGVersion(t *testing.T) {
	tests := []struct {
		name           string
		versionOutput  string
		expectedMajor  int
		expectedMinor  int
		expectedErrMsg string
	}{
		{
			name:          "Valid version string",
			versionOutput: "psql (PostgreSQL) 14.5",
			expectedMajor: 14,
			expectedMinor: 5,
		},
		{
			name:          "Valid version string with patch",
			versionOutput: "psql (PostgreSQL) 13.2.1",
			expectedMajor: 13,
			expectedMinor: 2,
		},
		{
			name:           "Invalid version string",
			versionOutput:  "psql (PostgreSQL) invalid",
			expectedErrMsg: "could not find a valid version in output: psql (PostgreSQL) invalid",
		},
		{
			name:           "Empty version string",
			versionOutput:  "",
			expectedErrMsg: "could not find a valid version in output: ",
		},
		{
			name:          "Version with extra information",
			versionOutput: "psql (PostgreSQL) 15.3 (Debian 15.3-1.pgdg110+1)",
			expectedMajor: 15,
			expectedMinor: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			major, minor, err := getPGVersion(tt.versionOutput)

			if tt.expectedErrMsg != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrMsg)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedMajor, major)
				assert.Equal(t, tt.expectedMinor, minor)
			}
		})
	}
}

func TestCheckVersionAtLeast(t *testing.T) {
	tests := []struct {
		version string
		wantErr bool
	}{
		{version: "16.14", wantErr: true},
		{version: "16.15"},
		{version: "16.16"},
		{version: "17.0", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			command := filepath.Join(t.TempDir(), "psql")
			require.NoError(t, os.WriteFile(command, []byte("#!/bin/sh\necho 'psql (PostgreSQL) "+test.version+"'\n"), 0o755))

			err := checkVersionAtLeast(command, 16, 15)
			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestVerifyDependenciesPsqlGate(t *testing.T) {
	dir := t.TempDir()
	pgDump := filepath.Join(dir, "pg_dump")
	require.NoError(t, os.WriteFile(pgDump, []byte("#!/bin/sh\necho 'pg_dump (PostgreSQL) 16.15'\n"), 0o755))

	psqlOnPath := func(t *testing.T, version string) {
		t.Helper()
		bin := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(bin, "psql"), []byte("#!/bin/sh\necho 'psql (PostgreSQL) "+version+"'\n"), 0o755))
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	deps := func(migrations, stateSync bool, psqlPath string) *coreDependencies {
		return &coreDependencies{
			cfg: &config.Config{
				PGDumpPath: pgDump,
				Migrations: config.MigrationConfig{Enable: migrations},
				StateSync:  config.StateSyncConfig{Enable: stateSync, PsqlPath: psqlPath},
			},
			logger: log.DiscardLogger,
		}
	}

	t.Run("migrations reject psql 16.14", func(t *testing.T) {
		psqlOnPath(t, "16.14")
		require.Panics(t, func() { verifyDependencies(deps(true, false, "psql")) })
	})

	t.Run("migrations accept psql 16.15", func(t *testing.T) {
		psqlOnPath(t, "16.15")
		require.NotPanics(t, func() { verifyDependencies(deps(true, false, "psql")) })
	})

	t.Run("neither feature checks psql", func(t *testing.T) {
		psqlOnPath(t, "16.14")
		require.NotPanics(t, func() { verifyDependencies(deps(false, false, "psql")) })
	})
}
