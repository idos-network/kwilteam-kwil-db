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
	evmsync "github.com/trufnetwork/kwil-db/node/exts/evm-sync"
	"github.com/trufnetwork/kwil-db/node/exts/evm-sync/chains"
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
		GetLogs:    func(context.Context, *ethclient.Client, uint64, uint64, log.Logger) ([]*evmsync.EthLog, error) { return nil, nil },
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
	require.Len(t, resp.Listeners, 1)
	require.Equal(t, topic, resp.Listeners[0].Topic)
	require.Equal(t, string(chains.ArbitrumOne), resp.Listeners[0].Chain)
	require.Equal(t, wantBlock, resp.Listeners[0].LastProcessedBlock)
}

func TestListenerSyncStatus_withEventStore_error(t *testing.T) {
	ctx := context.Background()
	topic := "test_listener_sync_svc_error"
	err := evmsync.EventSyncer.RegisterNewListener(evmsync.EVMEventListenerConfig{
		UniqueName: topic,
		Chain:      chains.ArbitrumOne,
		GetLogs:    func(context.Context, *ethclient.Client, uint64, uint64, log.Logger) ([]*evmsync.EthLog, error) { return nil, nil },
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
	require.Contains(t, jsonErr.Message, "kv get failed")
}
