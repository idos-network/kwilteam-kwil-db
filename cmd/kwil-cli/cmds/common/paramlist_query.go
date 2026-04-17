package common

import (
	"context"
	"errors"

	clientType "github.com/trufnetwork/kwil-db/core/client/types"
	jsonrpc "github.com/trufnetwork/kwil-db/core/rpc/json"
	"github.com/trufnetwork/kwil-db/core/types"
)

// QueryForParamList runs the engine introspection query used by GetParamList, choosing between
// user.query and user.authenticated_query based on whether a signer is configured, and reconciling
// private-mode vs open-mode server rules.
func QueryForParamList(ctx context.Context, cl clientType.Client, query string, args map[string]any) (*types.QueryResult, error) {
	skipAuth := cl.Signer() == nil
	res, err := cl.Query(ctx, query, args, skipAuth)
	if err == nil {
		return res, nil
	}
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		return nil, err
	}
	switch rpcErr.Code {
	case jsonrpc.ErrorNoQueryWithPrivateRPC:
		// Only user.query returns -1007 (private RPC). First attempt already used
		// authenticated_query when a signer exists; without a signer there is no
		// alternative RPC to try (retrying with skipAuth=false still calls user.query).
		return nil, err
	case jsonrpc.ErrorAuthenticatedQueryRequiresPrivateRPC:
		if cl.Signer() != nil {
			return cl.Query(ctx, query, args, true)
		}
		return nil, err
	default:
		return nil, err
	}
}
