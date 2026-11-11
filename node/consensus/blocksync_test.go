//go:build pglive

package consensus

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/trufnetwork/kwil-db/core/crypto/auth"
	"github.com/trufnetwork/kwil-db/core/log"
	ktypes "github.com/trufnetwork/kwil-db/core/types"
	blockprocessor "github.com/trufnetwork/kwil-db/node/block_processor"
	"github.com/trufnetwork/kwil-db/node/mempool"
	"github.com/trufnetwork/kwil-db/node/types"

	"github.com/stretchr/testify/require"
)

// Test that RecheckTxs is called when ErrBlkNotFound occurs during block sync
func TestReplayBlockFromNetwork_CallsRecheckTxsOnErrBlkNotFound(t *testing.T) {
	ctx := context.Background()

	// Create a minimal consensus engine with just the required components
	mp := mempool.New(1000000, 100000) // 1MB mempool, 100KB max tx size

	// Track RecheckTxs calls
	recheckCalled := false

	// Create a mock block processor that tracks RecheckTxs calls
	mockBP := &testBlockProcessor{
		recheckTxsFunc: func(ctx context.Context, height int64, timestamp time.Time) error {
			recheckCalled = true
			return nil
		},
	}

	ce := &ConsensusEngine{
		mempool:        mp,
		blockProcessor: mockBP,
		log:            log.DiscardLogger, // Use discard logger for tests
	}

	// Initialize state info for lastBlockInternal
	now := time.Now()
	ce.stateInfo.lastCommit.blk = &ktypes.Block{
		Header: &ktypes.BlockHeader{
			Height:    1,
			Timestamp: now,
		},
	}
	ce.stateInfo.lastCommit.height = 1

	// Initialize state.lc which is needed by lastBlockInternal
	ce.state.lc = &lastCommit{
		blk: &ktypes.Block{
			Header: &ktypes.BlockHeader{
				Height:    1,
				Timestamp: now,
			},
		},
		height: 1,
	}

	// Mock block requester to return ErrBlkNotFound
	ce.blkRequester = func(ctx context.Context, height int64) (types.Hash, []byte, *ktypes.CommitInfo, int64, error) {
		return types.Hash{}, nil, nil, 0, types.ErrBlkNotFound
	}

	// Call replayBlockFromNetwork - this should trigger our fix
	err := ce.replayBlockFromNetwork(ctx)
	require.NoError(t, err)

	// Verify RecheckTxs was called
	require.True(t, recheckCalled, "RecheckTxs should be called when ErrBlkNotFound occurs")
}

// Test that RecheckTxs is also called for ErrNotFound
func TestReplayBlockFromNetwork_CallsRecheckTxsOnErrNotFound(t *testing.T) {
	ctx := context.Background()

	mp := mempool.New(1000000, 100000)

	recheckCalled := false
	mockBP := &testBlockProcessor{
		recheckTxsFunc: func(ctx context.Context, height int64, timestamp time.Time) error {
			recheckCalled = true
			return nil
		},
	}

	ce := &ConsensusEngine{
		mempool:        mp,
		blockProcessor: mockBP,
		log:            log.DiscardLogger, // Use discard logger for tests
	}

	now := time.Now()
	ce.stateInfo.lastCommit.blk = &ktypes.Block{
		Header: &ktypes.BlockHeader{
			Height:    1,
			Timestamp: now,
		},
	}
	ce.stateInfo.lastCommit.height = 1

	// Initialize state.lc which is needed by lastBlockInternal
	ce.state.lc = &lastCommit{
		blk: &ktypes.Block{
			Header: &ktypes.BlockHeader{
				Height:    1,
				Timestamp: now,
			},
		},
		height: 1,
	}

	// Mock block requester to return ErrNotFound
	ce.blkRequester = func(ctx context.Context, height int64) (types.Hash, []byte, *ktypes.CommitInfo, int64, error) {
		return types.Hash{}, nil, nil, 0, types.ErrNotFound
	}

	err := ce.replayBlockFromNetwork(ctx)
	require.NoError(t, err)

	require.True(t, recheckCalled, "RecheckTxs should be called when ErrNotFound occurs")
}

// IDOS-124: the end-of-sync recheck must hold mempoolMtx, like the other
// recheck callers, so it can't run while QueueTx has stored a tx that is
// still unchecked.
func TestReplayBlockFromNetwork_RecheckHoldsMempoolLock(t *testing.T) {
	ctx := context.Background()

	recheckCalled := make(chan struct{})
	ce := &ConsensusEngine{
		mempool: mempool.New(1000000, 100000),
		blockProcessor: &testBlockProcessor{
			recheckTxsFunc: func(ctx context.Context, height int64, timestamp time.Time) error {
				close(recheckCalled)
				return nil
			},
		},
		log: log.DiscardLogger,
	}

	now := time.Now()
	ce.state.lc = &lastCommit{
		blk:    &ktypes.Block{Header: &ktypes.BlockHeader{Height: 1, Timestamp: now}},
		height: 1,
	}
	ce.stateInfo.lastCommit.blk = ce.state.lc.blk
	ce.stateInfo.lastCommit.height = 1
	ce.blkRequester = func(ctx context.Context, height int64) (types.Hash, []byte, *ktypes.CommitInfo, int64, error) {
		return types.Hash{}, nil, nil, 0, types.ErrBlkNotFound
	}

	// Stand in for a QueueTx call between Store and CheckTx.
	ce.mempoolMtx.Lock()

	done := make(chan error, 1)
	go func() { done <- ce.replayBlockFromNetwork(ctx) }()

	// Wait until the replay is queued on mempoolMtx. If the recheck runs
	// first, it didn't take the lock.
	queued := func() bool {
		ce.mempoolMtx.mtx.Lock()
		defer ce.mempoolMtx.mtx.Unlock()
		return len(ce.mempoolMtx.queue) > 0
	}
	deadline := time.After(5 * time.Second)
	for !queued() {
		select {
		case <-recheckCalled:
			t.Fatal("RecheckTxs ran while mempoolMtx was held")
		case <-deadline:
			t.Fatal("replay never waited for mempoolMtx")
		case <-time.After(time.Millisecond):
		}
	}

	ce.mempoolMtx.Unlock()

	select {
	case <-recheckCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("RecheckTxs did not run after mempoolMtx was released")
	}
	require.NoError(t, <-done)

	// The recheck must release the lock again.
	acquired := make(chan struct{})
	go func() {
		ce.mempoolMtx.Lock()
		close(acquired)
		ce.mempoolMtx.Unlock()
	}()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("mempoolMtx was not released after the recheck")
	}
}

// Test that with stale transactions in mempool, they get removed by RecheckTxs
func TestReplayBlockFromNetwork_RemovesStaleTransactions(t *testing.T) {
	ctx := context.Background()

	mp := mempool.New(1000000, 100000)

	// Add a test transaction to mempool
	testTx := createTestTransaction()
	err := mp.Store(testTx)
	require.NoError(t, err)

	// Verify transaction is in mempool
	require.True(t, mp.Have(testTx.Hash()))

	// Mock block processor that simulates RecheckTxs removing invalid transactions
	mockBP := &testBlockProcessor{
		recheckTxsFunc: func(ctx context.Context, height int64, timestamp time.Time) error {
			// Simulate removing the stale transaction
			mp.Remove(testTx.Hash())
			return nil
		},
	}

	ce := &ConsensusEngine{
		mempool:        mp,
		blockProcessor: mockBP,
		log:            log.DiscardLogger, // Use discard logger for tests
	}

	now := time.Now()
	ce.stateInfo.lastCommit.blk = &ktypes.Block{
		Header: &ktypes.BlockHeader{
			Height:    1,
			Timestamp: now,
		},
	}
	ce.stateInfo.lastCommit.height = 1

	// Initialize state.lc which is needed by lastBlockInternal
	ce.state.lc = &lastCommit{
		blk: &ktypes.Block{
			Header: &ktypes.BlockHeader{
				Height:    1,
				Timestamp: now,
			},
		},
		height: 1,
	}

	ce.blkRequester = func(ctx context.Context, height int64) (types.Hash, []byte, *ktypes.CommitInfo, int64, error) {
		return types.Hash{}, nil, nil, 0, types.ErrBlkNotFound
	}

	// Call replayBlockFromNetwork
	err = ce.replayBlockFromNetwork(ctx)
	require.NoError(t, err)

	// Verify transaction was removed from mempool
	require.False(t, mp.Have(testTx.Hash()), "Stale transaction should be removed from mempool")
}

// Helper function to create a test transaction
func createTestTransaction() *types.Tx {
	tx := &ktypes.Transaction{
		Body: &ktypes.TransactionBody{
			PayloadType: "test",
			Payload:     []byte("test payload"),
			Fee:         big.NewInt(1000),
			Nonce:       1,
		},
		Signature: &auth.Signature{
			Data: []byte("fake signature"),
			Type: "secp256k1",
		},
		Sender: []byte("test sender"),
	}

	return types.NewTx(tx)
}

// Minimal mock block processor that only implements RecheckTxs for testing
type testBlockProcessor struct {
	recheckTxsFunc func(ctx context.Context, height int64, timestamp time.Time) error
}

func (bp *testBlockProcessor) RecheckTxs(ctx context.Context, height int64, timestamp time.Time) error {
	if bp.recheckTxsFunc != nil {
		return bp.recheckTxsFunc(ctx, height, timestamp)
	}
	return nil
}

// Implement other required methods with minimal stubs
func (bp *testBlockProcessor) InitChain(ctx context.Context) (int64, []byte, error) {
	return 0, nil, nil
}
func (bp *testBlockProcessor) SetCallbackFns(applyBlockFn blockprocessor.BroadcastTxFn, addPeer, removePeer func(string) error) {
}
func (bp *testBlockProcessor) PrepareProposal(ctx context.Context, txs []*types.Tx, blockTime time.Time) ([]*ktypes.Transaction, []*ktypes.Transaction, error) {
	return nil, nil, nil
}
func (bp *testBlockProcessor) ExecuteBlock(ctx context.Context, req *ktypes.BlockExecRequest, syncing bool) (*ktypes.BlockExecResult, error) {
	return &ktypes.BlockExecResult{}, nil
}
func (bp *testBlockProcessor) Commit(ctx context.Context, req *ktypes.CommitRequest) error {
	return nil
}
func (bp *testBlockProcessor) Rollback(ctx context.Context, height int64, appHash ktypes.Hash) error {
	return nil
}
func (bp *testBlockProcessor) Close() error { return nil }
func (bp *testBlockProcessor) CheckTx(ctx context.Context, tx *types.Tx, height int64, blockTime time.Time, recheck bool) error {
	return nil
}
func (bp *testBlockProcessor) GetValidators() []*ktypes.Validator { return nil }
func (bp *testBlockProcessor) ConsensusParams() *ktypes.NetworkParameters {
	return &ktypes.NetworkParameters{}
}
func (bp *testBlockProcessor) BlockExecutionStatus() *ktypes.BlockExecutionStatus {
	return &ktypes.BlockExecutionStatus{}
}
func (bp *testBlockProcessor) HasEvents() bool { return false }
func (bp *testBlockProcessor) StateHashes() *blockprocessor.StateHashes {
	return &blockprocessor.StateHashes{}
}
