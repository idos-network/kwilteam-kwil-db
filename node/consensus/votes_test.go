package consensus

import (
	"bytes"
	"context"
	"encoding/hex"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/core/crypto"
	"github.com/trufnetwork/kwil-db/core/log"
	ktypes "github.com/trufnetwork/kwil-db/core/types"
	blockprocessor "github.com/trufnetwork/kwil-db/node/block_processor"
	"github.com/trufnetwork/kwil-db/node/types"
)

func TestVerifyVotesRejectsRepeatedSigner(t *testing.T) {
	ce, keys := testEngineWithValidators(t, 4)
	blkID := types.Hash{1}
	appHash := types.Hash{2}

	sig, err := ktypes.SignVote(blkID, true, &appHash, keys[0])
	require.NoError(t, err)
	vote := &ktypes.VoteInfo{AckStatus: ktypes.AckAgree, Signature: *sig}

	err = ce.verifyVotes(&ktypes.CommitInfo{
		AppHash: appHash,
		Votes:   []*ktypes.VoteInfo{vote, vote, vote},
	}, blkID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate vote")

	votes := make([]*ktypes.VoteInfo, 3)
	for i := range votes {
		sig, err := ktypes.SignVote(blkID, true, &appHash, keys[i])
		require.NoError(t, err)
		votes[i] = &ktypes.VoteInfo{AckStatus: ktypes.AckAgree, Signature: *sig}
	}
	require.NoError(t, ce.verifyVotes(&ktypes.CommitInfo{AppHash: appHash, Votes: votes}, blkID))
}

func TestAddVoteRejectsRepeatedSigner(t *testing.T) {
	ce, keys := testEngineWithValidators(t, 4)
	blkID := types.Hash{1}
	appHash := types.Hash{2}

	leaderSig, err := ktypes.SignVote(blkID, true, &appHash, keys[0])
	require.NoError(t, err)
	leaderPub := keys[0].Public()
	ce.state.votes[string(leaderPub.Bytes())] = &ktypes.VoteInfo{
		AckStatus: ktypes.AckAgree,
		Signature: *leaderSig,
	}
	ce.state.blkProp = &blockProposal{height: 1, blkHash: blkID}
	ce.state.blockRes = &blockResult{appHash: appHash}
	ce.state.lc = &lastCommit{}

	// Same signature, but the map key is the hex sender rather than the raw pubkey.
	err = ce.addVote(context.Background(), &vote{msg: &types.AckRes{
		Height:    1,
		ACK:       true,
		BlkHash:   blkID,
		AppHash:   &appHash,
		Signature: leaderSig,
	}}, hex.EncodeToString(leaderPub.Bytes()))
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate vote")
	require.Len(t, ce.state.votes, 1)

	otherSig, err := ktypes.SignVote(blkID, true, &appHash, keys[1])
	require.NoError(t, err)
	otherPub := keys[1].Public().Bytes()
	err = ce.addVote(context.Background(), &vote{msg: &types.AckRes{
		Height:    1,
		ACK:       true,
		BlkHash:   blkID,
		AppHash:   &appHash,
		Signature: otherSig,
	}}, hex.EncodeToString(keys[2].Public().Bytes()))
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match sender")

	err = ce.addVote(context.Background(), &vote{msg: &types.AckRes{
		Height:    1,
		ACK:       true,
		BlkHash:   blkID,
		AppHash:   &appHash,
		Signature: otherSig,
	}}, hex.EncodeToString(otherPub))
	require.NoError(t, err)
	require.Len(t, ce.state.votes, 2)

	// Same signer under another map key must not become the third ack.
	ce.state.votes["duplicate"] = ce.state.votes[hex.EncodeToString(otherPub)]

	ce.processVotes(context.Background())
	require.Len(t, ce.state.votes, 3)
	require.Nil(t, ce.state.commitInfo)
}

func TestProcessVotesSkipsSignatureRecheck(t *testing.T) {
	ce, keys := testEngineWithValidators(t, 4)
	ce.haltChan = make(chan string, 1)
	blkID := types.Hash{1}
	appHash := types.Hash{2}
	ce.state.blkProp = &blockProposal{height: 1, blkHash: blkID}
	ce.state.blockRes = &blockResult{appHash: appHash}
	ce.state.lc = &lastCommit{}

	stranger, _, err := crypto.GenerateSecp256k1Key(nil)
	require.NoError(t, err)
	strangerPub := stranger.Public()
	ce.state.votes["stranger"] = &ktypes.VoteInfo{
		AckStatus: ktypes.AckReject,
		Signature: ktypes.Signature{
			PubKey:     strangerPub.Bytes(),
			PubKeyType: strangerPub.Type(),
			Data:       []byte("not-a-signature"),
		},
	}
	pub0 := keys[0].Public()
	ce.state.votes["wrong-key-type"] = &ktypes.VoteInfo{
		AckStatus: ktypes.AckReject,
		Signature: ktypes.Signature{
			PubKey:     pub0.Bytes(),
			PubKeyType: crypto.KeyTypeEd25519,
			Data:       []byte("not-a-signature"),
		},
	}
	ce.state.votes[hex.EncodeToString(pub0.Bytes())] = &ktypes.VoteInfo{
		AckStatus: ktypes.AckReject,
		Signature: ktypes.Signature{
			PubKey:     pub0.Bytes(),
			PubKeyType: pub0.Type(),
			Data:       []byte("not-a-signature"),
		},
	}
	ce.state.votes["duplicate"] = ce.state.votes[hex.EncodeToString(pub0.Bytes())]
	ce.processVotes(context.Background())
	require.Empty(t, ce.haltChan)

	pub1 := keys[1].Public()
	ce.state.votes[hex.EncodeToString(pub1.Bytes())] = &ktypes.VoteInfo{
		AckStatus: ktypes.AckReject,
		Signature: ktypes.Signature{
			PubKey:     pub1.Bytes(),
			PubKeyType: pub1.Type(),
			Data:       []byte("not-a-signature"),
		},
	}

	ce.processVotes(context.Background())
	select {
	case reason := <-ce.haltChan:
		require.Contains(t, reason, "nacks")
	default:
		t.Fatal("expected halt from votes already accepted by addVote")
	}
}

func TestAddVoteIgnoresOutOfSyncProofThatIsNotAhead(t *testing.T) {
	leaderKey, leaderPub, err := crypto.GenerateSecp256k1Key(nil)
	require.NoError(t, err)
	otherKey, _, err := crypto.GenerateSecp256k1Key(nil)
	require.NoError(t, err)

	blkID := types.Hash{1}
	cases := []struct {
		name  string
		proof *types.OutOfSyncProof
	}{
		{name: "current proposal", proof: leaderSignedHeader(t, leaderKey, 4)},
		{name: "older header", proof: leaderSignedHeader(t, leaderKey, 2)},
		{name: "signed by someone else", proof: leaderSignedHeader(t, otherKey, 9)},
		{name: "missing header", proof: &types.OutOfSyncProof{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ce, keys := testEngineWithValidators(t, 2)
			ce.pubKey = leaderPub
			ce.haltChan = make(chan string, 1)
			ce.newRound = make(chan struct{}, 1)
			ce.state.blkProp = &blockProposal{height: 4, blkHash: blkID}
			ce.state.lc = &lastCommit{height: 3}
			ce.state.blockRes = &blockResult{}

			var fetched atomic.Bool
			ce.blkRequester = func(context.Context, int64) (types.Hash, []byte, *ktypes.CommitInfo, int64, error) {
				fetched.Store(true)
				return types.Hash{}, nil, nil, 0, types.ErrBlkNotFound
			}

			require.NoError(t, ce.addVote(context.Background(), outOfSyncVote(t, keys[1], blkID, tc.proof), hex.EncodeToString(keys[1].Public().Bytes())))

			require.Equal(t, blkID, ce.state.blkProp.blkHash)
			require.Empty(t, ce.state.votes)
			require.Empty(t, ce.haltChan)
			require.Never(t, fetched.Load, 200*time.Millisecond, 10*time.Millisecond)
		})
	}
}

func TestAddVoteOutOfSyncDoesNotAbortWhenNextBlockMissing(t *testing.T) {
	ce, keys := testEngineWithValidators(t, 2)
	leaderKey, leaderPub, err := crypto.GenerateSecp256k1Key(nil)
	require.NoError(t, err)
	ce.pubKey = leaderPub
	ce.haltChan = make(chan string, 1)
	ce.newRound = make(chan struct{}, 1)

	blkID := types.Hash{1}
	ce.state.blkProp = &blockProposal{height: 4, blkHash: blkID}
	ce.state.lc = &lastCommit{height: 3}
	ce.state.blockRes = &blockResult{}

	done := make(chan struct{})
	var gotHeight atomic.Int64
	ce.blkRequester = func(_ context.Context, height int64) (types.Hash, []byte, *ktypes.CommitInfo, int64, error) {
		gotHeight.Store(height)
		close(done)
		return types.Hash{}, nil, nil, 0, types.ErrBlkNotFound
	}

	proof := leaderSignedHeader(t, leaderKey, 10)
	require.NoError(t, ce.addVote(context.Background(), outOfSyncVote(t, keys[1], blkID, proof), hex.EncodeToString(keys[1].Public().Bytes())))
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected a fetch of the next block")
	}

	require.Equal(t, int64(4), gotHeight.Load())
	require.Equal(t, blkID, ce.state.blkProp.blkHash)
	require.Empty(t, ce.haltChan)
	require.Empty(t, ce.newRound)
}

func TestCatchUpFromOutOfSyncKeepsRoundWhenNextBlockMissing(t *testing.T) {
	for _, errNotFound := range []error{types.ErrBlkNotFound, types.ErrNotFound} {
		ce, _ := testEngineWithValidators(t, 1)
		ce.haltChan = make(chan string, 1)
		ce.newRound = make(chan struct{}, 1)
		blkID := types.Hash{1}
		ce.state.blkProp = &blockProposal{height: 4, blkHash: blkID}
		ce.blkRequester = func(context.Context, int64) (types.Hash, []byte, *ktypes.CommitInfo, int64, error) {
			return types.Hash{}, nil, nil, 0, errNotFound
		}

		runCatchup(ce, 4, blkID, 4, 10)

		require.Equal(t, blkID, ce.state.blkProp.blkHash)
		require.Empty(t, ce.haltChan)
		require.Empty(t, ce.newRound)
	}
}

func TestCatchUpFromOutOfSyncSkipsFinishedRound(t *testing.T) {
	ce, _ := testEngineWithValidators(t, 1)
	ce.haltChan = make(chan string, 1)
	ce.newRound = make(chan struct{}, 1)
	ce.state.blkProp = &blockProposal{height: 4, blkHash: types.Hash{2}}
	ce.blkRequester = func(context.Context, int64) (types.Hash, []byte, *ktypes.CommitInfo, int64, error) {
		return types.Hash{1}, []byte{1}, &ktypes.CommitInfo{}, 0, nil
	}

	runCatchup(ce, 4, types.Hash{1}, 4, 10)

	require.Equal(t, types.Hash{2}, ce.state.blkProp.blkHash)
	require.Empty(t, ce.haltChan)
	require.Empty(t, ce.newRound)
}

func TestCatchUpFromOutOfSyncDoesNotHaltWhenSyncMissesBlock(t *testing.T) {
	ce, _ := testEngineWithValidators(t, 1)
	ce.haltChan = make(chan string, 1)
	ce.newRound = make(chan struct{}, 1)
	ce.role.Store(types.RoleLeader)
	ce.blockProcessor = &stubBlockProcessor{}
	prop := types.Hash{9}
	ce.state.blkProp = &blockProposal{height: 4, blkHash: prop}
	ce.state.lc = &lastCommit{height: 3}

	var calls atomic.Int32
	ce.blkRequester = func(context.Context, int64) (types.Hash, []byte, *ktypes.CommitInfo, int64, error) {
		if calls.Add(1) == 1 {
			return types.Hash{1}, []byte{1}, &ktypes.CommitInfo{}, 0, nil
		}
		return types.Hash{}, nil, nil, 0, types.ErrBlkNotFound
	}

	runCatchup(ce, 4, prop, 4, 10)

	require.Nil(t, ce.state.blkProp)
	require.Empty(t, ce.haltChan)
	require.Len(t, ce.newRound, 1)
}

func TestOutOfSyncCatchupKeepsGreatestEndHeight(t *testing.T) {
	ce, _ := testEngineWithValidators(t, 1)
	var logs bytes.Buffer
	ce.log = log.New(log.WithWriter(&logs), log.WithLevel(log.LevelInfo))
	ce.haltChan = make(chan string, 1)
	ce.newRound = make(chan struct{}, 1)
	ce.role.Store(types.RoleLeader)
	ce.blockProcessor = &stubBlockProcessor{}

	prop := types.Hash{9}
	ce.state.blkProp = &blockProposal{height: 4, blkHash: prop}
	ce.state.lc = &lastCommit{height: 3}

	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	ce.blkRequester = func(context.Context, int64) (types.Hash, []byte, *ktypes.CommitInfo, int64, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return types.Hash{1}, []byte{1}, &ktypes.CommitInfo{}, 0, nil
		}
		return types.Hash{}, nil, nil, 0, types.ErrBlkNotFound
	}

	ce.state.mtx.Lock()
	ce.beginOutOfSyncCatchup(context.Background(), 4, prop, 4, 10)
	ce.state.mtx.Unlock()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("expected one catch-up fetch")
	}

	ce.state.mtx.Lock()
	ce.beginOutOfSyncCatchup(context.Background(), 4, prop, 4, 30)
	ce.beginOutOfSyncCatchup(context.Background(), 4, prop, 4, 12)
	require.Equal(t, int64(30), ce.state.catchupEnd)
	require.Equal(t, int32(1), calls.Load())
	ce.state.mtx.Unlock()
	close(release)

	select {
	case <-ce.newRound:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the catch-up worker to finish")
	}
	require.Contains(t, logs.String(), "to=30")
	require.NotContains(t, logs.String(), "to=10")
	require.False(t, ce.state.catchupOn)
	require.Empty(t, ce.haltChan)
}

func runCatchup(ce *ConsensusEngine, propHeight int64, propHash types.Hash, start, end int64) {
	ce.state.catchupOn = true
	ce.state.catchupGen = 1
	ce.state.catchupProp = propHeight
	ce.state.catchupHash = propHash
	ce.state.catchupStart = start
	ce.state.catchupEnd = end
	ce.catchUpFromOutOfSync(context.Background(), 1)
}

func leaderSignedHeader(t *testing.T, key crypto.PrivateKey, height int64) *types.OutOfSyncProof {
	t.Helper()
	hdr := &ktypes.BlockHeader{Height: height, Timestamp: time.UnixMilli(1).UTC()}
	sum := hdr.Hash()
	sig, err := key.Sign(sum[:])
	require.NoError(t, err)
	return &types.OutOfSyncProof{Header: hdr, Signature: sig}
}

func outOfSyncVote(t *testing.T, key crypto.PrivateKey, blkID types.Hash, proof *types.OutOfSyncProof) *vote {
	t.Helper()
	status := types.NackStatusOutOfSync
	sig, err := ktypes.SignVote(blkID, false, nil, key)
	require.NoError(t, err)
	return &vote{msg: &types.AckRes{
		ACK:            false,
		NackStatus:     &status,
		Height:         4,
		BlkHash:        blkID,
		OutOfSyncProof: proof,
		Signature:      sig,
	}}
}

type stubBlockProcessor struct{}

func (stubBlockProcessor) InitChain(context.Context) (int64, []byte, error) { return 0, nil, nil }
func (stubBlockProcessor) SetCallbackFns(blockprocessor.BroadcastTxFn, func(string) error, func(string) error) {
}
func (stubBlockProcessor) PrepareProposal(context.Context, []*types.Tx) ([]*ktypes.Transaction, []*ktypes.Transaction, error) {
	return nil, nil, nil
}
func (stubBlockProcessor) ExecuteBlock(context.Context, *ktypes.BlockExecRequest, bool) (*ktypes.BlockExecResult, error) {
	return &ktypes.BlockExecResult{}, nil
}
func (stubBlockProcessor) Commit(context.Context, *ktypes.CommitRequest) error { return nil }
func (stubBlockProcessor) Rollback(context.Context, int64, ktypes.Hash) error  { return nil }
func (stubBlockProcessor) Close() error                                        { return nil }
func (stubBlockProcessor) CheckTx(context.Context, *types.Tx, int64, time.Time, bool) error {
	return nil
}
func (stubBlockProcessor) RecheckTxs(context.Context, int64, time.Time) error { return nil }
func (stubBlockProcessor) GetValidators() []*ktypes.Validator                 { return nil }
func (stubBlockProcessor) ConsensusParams() *ktypes.NetworkParameters {
	return &ktypes.NetworkParameters{}
}
func (stubBlockProcessor) BlockExecutionStatus() *ktypes.BlockExecutionStatus {
	return &ktypes.BlockExecutionStatus{}
}
func (stubBlockProcessor) HasEvents() bool { return false }
func (stubBlockProcessor) StateHashes() *blockprocessor.StateHashes {
	return &blockprocessor.StateHashes{}
}

func TestVerifyVotesStillChecksSignature(t *testing.T) {
	ce, keys := testEngineWithValidators(t, 1)
	pub := keys[0].Public()
	err := ce.verifyVotes(&ktypes.CommitInfo{
		AppHash: types.Hash{2},
		Votes: []*ktypes.VoteInfo{{
			AckStatus: ktypes.AckAgree,
			Signature: ktypes.Signature{
				PubKey:     pub.Bytes(),
				PubKeyType: pub.Type(),
				Data:       []byte("not-a-signature"),
			},
		}},
	}, types.Hash{1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "verifying vote")
}

func testEngineWithValidators(t *testing.T, n int) (*ConsensusEngine, []crypto.PrivateKey) {
	t.Helper()
	keys := make([]crypto.PrivateKey, n)
	ce := &ConsensusEngine{
		log:          log.DiscardLogger,
		validatorSet: make(map[string]ktypes.Validator, n),
		state:        state{votes: make(map[string]*ktypes.VoteInfo)},
	}
	for i := range n {
		priv, pub, err := crypto.GenerateSecp256k1Key(nil)
		require.NoError(t, err)
		keys[i] = priv
		ce.validatorSet[hex.EncodeToString(pub.Bytes())] = ktypes.Validator{
			AccountID: ktypes.AccountID{
				Identifier: pub.Bytes(),
				KeyType:    pub.Type(),
			},
			Power: 1,
		}
	}
	return ce, keys
}
