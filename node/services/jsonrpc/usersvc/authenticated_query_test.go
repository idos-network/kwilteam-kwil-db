package usersvc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/core/log"
	jsonrpc "github.com/trufnetwork/kwil-db/core/rpc/json"
	userjson "github.com/trufnetwork/kwil-db/core/rpc/json/user"
)

func TestAuthenticatedQueryDeniedWhenRPCPrivateModeOff(t *testing.T) {
	svc := &Service{
		log:         log.DiscardLogger,
		privateMode: false,
	}

	_, rpcErr := svc.AuthenticatedQuery(context.Background(), (*userjson.AuthenticatedQueryRequest)(nil))

	require.NotNil(t, rpcErr)
	require.Equal(t, jsonrpc.ErrorAuthenticatedQueryRequiresPrivateRPC, rpcErr.Code)
}
