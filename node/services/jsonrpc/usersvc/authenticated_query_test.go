package usersvc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/common"
	"github.com/trufnetwork/kwil-db/core/crypto"
	"github.com/trufnetwork/kwil-db/core/crypto/auth"
	"github.com/trufnetwork/kwil-db/core/log"
	jsonrpc "github.com/trufnetwork/kwil-db/core/rpc/json"
	userjson "github.com/trufnetwork/kwil-db/core/rpc/json/user"
	"github.com/trufnetwork/kwil-db/core/types"
	authExt "github.com/trufnetwork/kwil-db/extensions/auth"
	"github.com/trufnetwork/kwil-db/node/types/sql"
)

func TestAuthenticatedQueryNilRequest(t *testing.T) {
	svc := &Service{
		log: log.DiscardLogger,
	}

	_, rpcErr := svc.AuthenticatedQuery(context.Background(), nil)

	require.NotNil(t, rpcErr)
	require.Equal(t, jsonrpc.ErrorInvalidParams, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "missing authenticated query request")
}

func TestAuthenticatedQueryNilBody(t *testing.T) {
	svc := &Service{
		log: log.DiscardLogger,
	}

	_, rpcErr := svc.AuthenticatedQuery(context.Background(), &userjson.AuthenticatedQueryRequest{})

	require.NotNil(t, rpcErr)
	require.Equal(t, jsonrpc.ErrorInvalidParams, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "missing authenticated query body")
}

func TestAuthenticatedQueryOpenModeSmokeUnsignedRequest(t *testing.T) {
	svc := &Service{
		log:           log.DiscardLogger,
		privateMode:   false,
		readTxTimeout: defaultReadTxTimeout,
	}

	req := &userjson.AuthenticatedQueryRequest{
		Body: &types.RawStatement{
			Statement: "SELECT 1",
		},
		// Non-nil request but unsigned / no sender: authenticated_query now
		// requires authentication before reaching DB/engine in every RPC mode.
	}

	_, rpcErr := svc.AuthenticatedQuery(context.Background(), req)

	require.NotNil(t, rpcErr)
	require.Equal(t, jsonrpc.ErrorCallChallengeNotFound, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "signed call message with challenge required")
}

func TestAuthenticatedQueryPrivateModeSmokeUnsignedRequest(t *testing.T) {
	svc := &Service{
		log:           log.DiscardLogger,
		privateMode:   true,
		readTxTimeout: defaultReadTxTimeout,
	}

	req := &userjson.AuthenticatedQueryRequest{
		Body: &types.RawStatement{
			Statement: "SELECT 1",
		},
		// Non-nil request but unsigned / no sender: authenticate rejects before DB/engine.
	}

	_, rpcErr := svc.AuthenticatedQuery(context.Background(), req)

	require.NotNil(t, rpcErr)
	require.NotEqual(t, jsonrpc.ErrorAuthenticatedQueryRequiresPrivateRPC, rpcErr.Code,
		"private RPC must not hit the open-mode-only denial")
	require.Equal(t, jsonrpc.ErrorCallChallengeNotFound, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "signed call message with challenge required")
}

type ownerGateEngine struct {
	owner    string
	executed bool
}

func (e *ownerGateEngine) Call(*common.EngineContext, sql.DB, string, string, []any, func(*common.Row) error) (*common.CallResult, error) {
	return nil, nil
}

func (e *ownerGateEngine) Execute(*common.EngineContext, sql.DB, string, map[string]any, func(*common.Row) error) error {
	e.executed = true
	return nil
}

func (e *ownerGateEngine) IsOwner(user string) bool {
	return user == e.owner
}

// stubReadTx only supports Rollback, which is all AuthenticatedQuery calls
// directly; the engine stub never touches the tx.
type stubReadTx struct {
	sql.OuterReadTx
}

func (stubReadTx) Rollback(context.Context) error { return nil }

type stubReadDB struct{}

func (stubReadDB) BeginReadTx(context.Context) (sql.OuterReadTx, error) { return stubReadTx{}, nil }

func (stubReadDB) BeginDelayedReadTx() sql.OuterReadTx { return stubReadTx{} }

func TestAuthenticatedQueryOwnerOnly(t *testing.T) {
	privKey, _, err := crypto.GenerateSecp256k1Key(nil)
	require.NoError(t, err)
	signer := auth.GetUserSigner(privKey)
	ident, err := authExt.GetIdentifierFromSigner(signer)
	require.NoError(t, err)

	run := func(t *testing.T, owner string) (*ownerGateEngine, *jsonrpc.Error) {
		challenge := [32]byte{4, 5, 6}
		req, err := types.CreateAuthenticatedQuery("SELECT 1", nil, challenge[:], signer)
		require.NoError(t, err)

		engine := &ownerGateEngine{owner: owner}
		svc := &Service{
			log:           log.DiscardLogger,
			engine:        engine,
			db:            stubReadDB{},
			chainClient:   canonChainClient{},
			readTxTimeout: defaultReadTxTimeout,
			challenges: map[[32]byte]time.Time{
				challenge: time.Now().Add(time.Minute),
			},
		}

		_, rpcErr := svc.AuthenticatedQuery(context.Background(), req)
		return engine, rpcErr
	}

	t.Run("non-owner is rejected before the engine runs", func(t *testing.T) {
		engine, rpcErr := run(t, "someone_else")
		require.NotNil(t, rpcErr)
		require.Equal(t, jsonrpc.ErrorAuthenticatedQueryOwnerOnly, rpcErr.Code)
		require.False(t, engine.executed)
	})

	t.Run("owner runs the query", func(t *testing.T) {
		engine, rpcErr := run(t, ident)
		require.Nil(t, rpcErr)
		require.True(t, engine.executed)
	})
}

func TestAuthenticateOpenModeOptionalForCalls(t *testing.T) {
	svc := &Service{
		log:         log.DiscardLogger,
		privateMode: false,
	}

	rpcErr := svc.authenticate(nil, nil, nil, "", "", authExt.VerifyContext{})

	require.Nil(t, rpcErr)
}

func TestAuthenticateRequiredAcceptsSignedChallenge(t *testing.T) {
	privKey, _, err := crypto.GenerateSecp256k1Key(nil)
	require.NoError(t, err)

	signer := auth.GetUserSigner(privKey)
	require.NotNil(t, signer)

	challenge := [32]byte{1, 2, 3}
	req, err := types.CreateAuthenticatedQuery("SELECT 1", nil, challenge[:], signer)
	require.NoError(t, err)

	sigText, err := req.SigText()
	require.NoError(t, err)

	svc := &Service{
		log: log.DiscardLogger,
		challenges: map[[32]byte]time.Time{
			challenge: time.Now().Add(time.Minute),
		},
	}

	rpcErr := svc.authenticateRequired(req.SignatureData, req.Challenge, req.Sender, req.AuthType, sigText, authExt.VerifyContext{})

	require.Nil(t, rpcErr)
	require.Empty(t, svc.challenges, "verified challenges are single-use")
}
