package usersvc

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/trufnetwork/kwil-db/core/log"
	userjson "github.com/trufnetwork/kwil-db/core/rpc/json/user"
	"github.com/trufnetwork/kwil-db/core/types"
	evmsync "github.com/trufnetwork/kwil-db/node/exts/evm-sync"
	"github.com/trufnetwork/kwil-db/node/exts/evm-sync/chains"
)

// For curl examples and usage of user.listener_sync_status, see README.md in this directory.

func TestListenerSyncStatusEscrowInstanceID(t *testing.T) {
	validUUID := "043a5968-975c-5f49-b0ed-816a74f761a0"
	parsedValid, err := types.ParseUUID(validUUID)
	require.NoError(t, err)

	tests := []struct {
		name     string
		topic    string
		wantID   types.UUID
		wantOK   bool
	}{
		{
			name:   "transfer_listener_valid",
			topic:  erc20TransferListenerPrefix + validUUID,
			wantID: *parsedValid,
			wantOK: true,
		},
		{
			name:   "withdrawal_listener_valid",
			topic:  erc20WithdrawalListenerPrefix + validUUID,
			wantID: *parsedValid,
			wantOK: true,
		},
		{
			name:   "non_erc20_topic",
			topic:  "test_listener_sync_svc_success",
			wantOK: false,
		},
		{
			name:   "transfer_prefix_invalid_uuid",
			topic:  erc20TransferListenerPrefix + "not-a-uuid",
			wantOK: false,
		},
		{
			name:   "empty_suffix",
			topic:  erc20TransferListenerPrefix,
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotOK := listenerSyncStatusEscrowInstanceID(tt.topic)
			require.Equal(t, tt.wantOK, gotOK)
			if tt.wantOK {
				require.Equal(t, tt.wantID, gotID)
			}
		})
	}
}

func TestListenerSyncStatus_nilEventStore(t *testing.T) {
	svc := &Service{
		log:        log.DiscardLogger,
		eventStore: nil,
	}
	ctx := context.Background()

	resp, jsonErr := svc.ListenerSyncStatus(ctx, &userjson.ListenerSyncStatusRequest{})

	require.Nil(t, jsonErr)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Listeners)
	require.Empty(t, resp.Listeners)
}

// mockListenerSyncStore implements listenerSyncEventStore for tests.
type mockListenerSyncStore struct {
	kv ListenerSyncKVReader
}

func (m *mockListenerSyncStore) KV(prefix []byte) ListenerSyncKVReader {
	return m.kv
}

// mockEventKVReader implements ListenerSyncKVReader for tests.
type mockEventKVReader struct {
	getFunc func(ctx context.Context, key []byte) ([]byte, error)
}

func (m *mockEventKVReader) Get(ctx context.Context, key []byte) ([]byte, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, key)
	}
	return nil, nil
}

func TestListenerSyncStatus_withEventStore_success(t *testing.T) {
	ctx := context.Background()
	topic := "test_listener_sync_svc_success"
	err := evmsync.EventSyncer.RegisterNewListener(evmsync.EVMEventListenerConfig{
		UniqueName: topic,
		Chain:      chains.ArbitrumOne,
		GetLogs: func(context.Context, *ethclient.Client, uint64, uint64, log.Logger) ([]*evmsync.EthLog, error) {
			return nil, nil
		},
	})
	require.NoError(t, err)
	defer func() { _ = evmsync.EventSyncer.UnregisterListener(topic) }()

	wantBlock := int64(12345678)
	blockBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(blockBytes, uint64(wantBlock))

	kv := &mockEventKVReader{
		getFunc: func(_ context.Context, key []byte) ([]byte, error) {
			if string(key) == "lh"+topic {
				return blockBytes, nil
			}
			return nil, nil
		},
	}
	svc := &Service{
		log:        log.DiscardLogger,
		eventStore: &mockListenerSyncStore{kv: kv},
	}

	resp, jsonErr := svc.ListenerSyncStatus(ctx, &userjson.ListenerSyncStatusRequest{})

	require.Nil(t, jsonErr)
	require.NotNil(t, resp)
	var found *userjson.ListenerStatusEntry
	for i := range resp.Listeners {
		if resp.Listeners[i].Topic == topic && resp.Listeners[i].Chain == string(chains.ArbitrumOne) {
			found = &resp.Listeners[i]
			break
		}
	}
	require.NotNil(t, found, "expected listener %q on chain %s in response", topic, chains.ArbitrumOne)
	require.Equal(t, wantBlock, found.LastProcessedBlock)
}

func TestListenerSyncStatus_withEventStore_error(t *testing.T) {
	ctx := context.Background()
	topic := "test_listener_sync_svc_error"
	err := evmsync.EventSyncer.RegisterNewListener(evmsync.EVMEventListenerConfig{
		UniqueName: topic,
		Chain:      chains.ArbitrumOne,
		GetLogs: func(context.Context, *ethclient.Client, uint64, uint64, log.Logger) ([]*evmsync.EthLog, error) {
			return nil, nil
		},
	})
	require.NoError(t, err)
	defer func() { _ = evmsync.EventSyncer.UnregisterListener(topic) }()

	kvErr := errors.New("kv get failed")
	kv := &mockEventKVReader{
		getFunc: func(context.Context, []byte) ([]byte, error) {
			return nil, kvErr
		},
	}
	svc := &Service{
		log:        log.DiscardLogger,
		eventStore: &mockListenerSyncStore{kv: kv},
	}

	resp, jsonErr := svc.ListenerSyncStatus(ctx, &userjson.ListenerSyncStatusRequest{})

	require.NotNil(t, jsonErr)
	require.Nil(t, resp)
	require.Equal(t, "internal server error", jsonErr.Message)
}
