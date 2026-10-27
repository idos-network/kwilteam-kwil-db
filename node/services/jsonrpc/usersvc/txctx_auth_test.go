package usersvc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/core/types"
	adminTypes "github.com/trufnetwork/kwil-db/core/types/admin"
)

type canonChainClient struct{}

func (canonChainClient) Status(context.Context) (*adminTypes.Status, error) {
	return &adminTypes.Status{Sync: &adminTypes.SyncInfo{}}, nil
}

func (canonChainClient) Peers(context.Context) ([]*adminTypes.PeerInfo, error) {
	return nil, nil
}

func (canonChainClient) ConsensusParams() *types.NetworkParameters {
	return &types.NetworkParameters{}
}

func (canonChainClient) BroadcastTx(context.Context, *types.Transaction, uint8) (types.Hash, *types.TxResult, error) {
	return types.Hash{}, nil, nil
}

func (canonChainClient) TxQuery(context.Context, types.Hash, bool) (*types.TxQueryResponse, error) {
	return nil, nil
}

func TestTxCtx_canonicalizesAuthenticator(t *testing.T) {
	svc := &Service{chainClient: canonChainClient{}}

	txCtx, rpcErr := svc.txCtx(context.Background(), nil, "ED25519")
	require.Nil(t, rpcErr)
	require.Equal(t, "ed25519", txCtx.Authenticator)

	_, rpcErr = svc.txCtx(context.Background(), nil, "not-an-auth")
	require.NotNil(t, rpcErr)
}
