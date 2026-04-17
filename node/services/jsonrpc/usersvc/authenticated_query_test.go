package usersvc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/core/log"
	jsonrpc "github.com/trufnetwork/kwil-db/core/rpc/json"
	userjson "github.com/trufnetwork/kwil-db/core/rpc/json/user"
	"github.com/trufnetwork/kwil-db/core/types"
)

const authenticatedQueryOpenRPCDeniedMsg = "user.authenticated_query is only available when RPC private mode is enabled"

func TestAuthenticatedQueryDeniedWhenRPCPrivateModeOff(t *testing.T) {
	svc := &Service{
		log:         log.DiscardLogger,
		privateMode: false,
	}

	_, rpcErr := svc.AuthenticatedQuery(context.Background(), (*userjson.AuthenticatedQueryRequest)(nil))

	require.NotNil(t, rpcErr)
	require.Equal(t, jsonrpc.ErrorAuthenticatedQueryRequiresPrivateRPC, rpcErr.Code)
	require.NotEmpty(t, rpcErr.Message)
	require.Equal(t, authenticatedQueryOpenRPCDeniedMsg, rpcErr.Message)
}

// TestAuthenticatedQueryPrivateModeSmokeUnsignedRequest covers the path after
// the open-RPC guard: private mode does not return ErrorAuthenticatedQueryRequiresPrivateRPC.
// Request is well-typed but missing signature/sender (typed-nil body is not used here
// because SigText would panic on a nil *AuthenticatedQuery).
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
