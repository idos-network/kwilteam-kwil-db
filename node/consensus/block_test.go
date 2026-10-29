package consensus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/trufnetwork/kwil-db/core/crypto"
	ktypes "github.com/trufnetwork/kwil-db/core/types"
	"github.com/trufnetwork/kwil-db/node/types"
)

func TestValidateProposalTimestamp(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	prev := now.Add(-5 * time.Second)

	tests := []struct {
		name    string
		stamp   time.Time
		prev    time.Time
		wantErr bool
		ahead   bool // errProposalAhead: skipped, not Nacked
	}{
		{"now", now, prev, false, false},
		{"1ms after previous", prev.Add(time.Millisecond), prev, false, false},
		{"at drift limit", now.Add(maxProposalClockDrift), prev, false, false},
		{"no previous block", now, time.Time{}, false, false},
		{"equal to previous", prev, prev, true, false},
		{"before previous", prev.Add(-time.Millisecond), prev, true, false},
		{"past drift limit", now.Add(maxProposalClockDrift + time.Millisecond), prev, true, true},
		{"hours ahead", now.Add(6 * time.Hour), prev, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProposalTimestamp(tt.stamp, tt.prev, now)
			if tt.wantErr {
				require.Error(t, err)
				require.Equal(t, tt.ahead, errors.Is(err, errProposalAhead))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestProposalTimestamp(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).Add(123 * time.Microsecond)

	// Local clock past the previous block: use it, truncated to milliseconds.
	stamp := proposalTimestamp(now, now.Add(-time.Second))
	require.Equal(t, now.Truncate(time.Millisecond).UTC(), stamp)

	// No previous block.
	require.Equal(t, now.Truncate(time.Millisecond).UTC(), proposalTimestamp(now, time.Time{}))

	// Local clock behind or equal to the previous block: step 1ms past it so
	// followers still see an increasing timestamp.
	prev := now.Truncate(time.Millisecond).Add(2 * time.Second)
	stamp = proposalTimestamp(now, prev)
	require.Equal(t, prev.Add(time.Millisecond).UTC(), stamp)
	require.NoError(t, validateProposalTimestamp(stamp, prev, now))

	stamp = proposalTimestamp(prev, prev)
	require.Equal(t, prev.Add(time.Millisecond).UTC(), stamp)
}

// proposalTestEngine returns a validator engine whose last commit is at
// height 3 with timestamp prevStamp, the leader key, and the acks it sends.
func proposalTestEngine(t *testing.T, prevStamp time.Time) (*ConsensusEngine, crypto.PrivateKey, chan *types.AckRes) {
	t.Helper()
	ce, _ := testEngineWithValidators(t, 2)
	leaderKey, leaderPub, err := crypto.GenerateSecp256k1Key(nil)
	require.NoError(t, err)
	valKey, _, err := crypto.GenerateSecp256k1Key(nil)
	require.NoError(t, err)

	ce.leader = leaderPub
	ce.privKey = valKey
	ce.role.Store(types.RoleValidator)
	ce.blockProcessor = stubBlockProcessor{}
	acks := make(chan *types.AckRes, 1)
	ce.ackBroadcaster = func(msg *types.AckRes) error {
		acks <- msg
		return nil
	}

	prev := ktypes.NewBlock(3, ktypes.Hash{}, ktypes.Hash{}, ce.validatorSetHash(), ce.ConsensusParams().Hash(), prevStamp, nil)
	ce.state.lc = &lastCommit{height: 3, blkHash: prev.Hash(), blk: prev}
	return ce, leaderKey, acks
}

func signedProposal(t *testing.T, ce *ConsensusEngine, key crypto.PrivateKey, stamp time.Time) *blockProposal {
	t.Helper()
	blk := ktypes.NewBlock(4, ce.state.lc.blkHash, ce.state.lc.appHash, ce.validatorSetHash(), ce.ConsensusParams().Hash(), stamp, nil)
	require.NoError(t, blk.Sign(key))
	return &blockProposal{height: 4, blkHash: blk.Hash(), blk: blk, done: func() {}}
}

// A proposal too far ahead of the local clock may be clock skew. It must not
// be Nacked as invalid, since enough of those halt the leader, and it must not
// be stored, so the leader's rebroadcast is checked again.
func TestProcessBlockProposalSkipsFutureTimestamp(t *testing.T) {
	ce, leaderKey, acks := proposalTestEngine(t, time.Now().Add(-time.Second))
	prop := signedProposal(t, ce, leaderKey, time.Now().Add(maxProposalClockDrift+time.Hour))

	require.NoError(t, ce.processBlockProposal(context.Background(), prop))
	require.Nil(t, ce.state.blkProp)
	require.Never(t, func() bool { return len(acks) > 0 }, 200*time.Millisecond, 10*time.Millisecond)
}

// A timestamp not after the previous block's is never produced by an honest
// leader, so it stays an invalid-block Nack.
func TestProcessBlockProposalNacksTimestampNotAfterPrevious(t *testing.T) {
	prevStamp := time.Now().Add(-time.Second).Truncate(time.Millisecond)
	ce, leaderKey, acks := proposalTestEngine(t, prevStamp)
	prop := signedProposal(t, ce, leaderKey, prevStamp)

	err := ce.processBlockProposal(context.Background(), prop)
	require.ErrorContains(t, err, "not after the previous block timestamp")
	require.Nil(t, ce.state.blkProp)

	select {
	case ack := <-acks:
		require.False(t, ack.ACK)
		require.Equal(t, types.NackStatusInvalidBlock, *ack.NackStatus)
	case <-time.After(2 * time.Second):
		t.Fatal("expected an invalid-block Nack")
	}
}
