package interpreter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/common"
	"github.com/trufnetwork/kwil-db/node/types/sql"
)

type accessModeDB struct {
	sql.DB
	mode sql.AccessMode
}

func (d accessModeDB) AccessMode() sql.AccessMode { return d.mode }

func TestActionCallKind(t *testing.T) {
	ro := accessModeDB{mode: sql.ReadOnly}
	rw := accessModeDB{mode: sql.ReadWrite}

	require.Equal(t, "call", actionCallKind(&common.EngineContext{}, ro))
	require.Equal(t, "tx", actionCallKind(&common.EngineContext{}, rw))
	require.Equal(t, "internal", actionCallKind(newInvalidEngineCtx(context.Background()), rw))
	require.Equal(t, "internal", actionCallKind(newInvalidEngineCtx(context.Background()), ro))
}
