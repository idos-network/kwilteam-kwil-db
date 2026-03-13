package usersvc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/core/log"
	userjson "github.com/trufnetwork/kwil-db/core/rpc/json/user"
)

// For curl examples and usage of user.listener_sync_status, see README.md in this directory.

func TestListenerSyncStatus_nilEventStore(t *testing.T) {
	svc := &Service{
		log:        log.DiscardLogger,
		eventStore: nil,
	}
	ctx := context.Background()

	resp, jsonErr := svc.ListenerSyncStatus(ctx, &userjson.ListenerSyncStatusRequest{})

	require.Nil(t, jsonErr)
	require.NotNil(t, resp)
	require.Nil(t, resp.Listeners)
}
