package node

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	mock "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
	ktypes "github.com/trufnetwork/kwil-db/core/types"
	"github.com/trufnetwork/kwil-db/node/types"
)

// anchorBS is a block store holding a single block, served with the given hash.
type anchorBS struct {
	mockBS
	hash types.Hash
	blk  *ktypes.Block
	ci   *ktypes.CommitInfo
}

func (bs *anchorBS) GetByHeight(height int64) (types.Hash, *ktypes.Block, *ktypes.CommitInfo, error) {
	if bs.blk == nil || bs.blk.Header.Height != height {
		return types.Hash{}, nil, nil, errors.New("not found")
	}
	return bs.hash, bs.blk, bs.ci, nil
}

func TestVerifyAnchorBlock(t *testing.T) {
	const height = 10
	appHash := fakeAppHash(height)

	tests := []struct {
		name    string
		modify  func(hash *types.Hash, blk *ktypes.Block, ci **ktypes.CommitInfo)
		wantErr string
	}{
		{
			name:   "valid",
			modify: func(*types.Hash, *ktypes.Block, **ktypes.CommitInfo) {},
		},
		{
			name: "wrong height",
			modify: func(hash *types.Hash, blk *ktypes.Block, _ **ktypes.CommitInfo) {
				blk.Header.Height = height + 1
				*hash = blk.Hash()
			},
			wantErr: "does not match snapshot height",
		},
		{
			name: "hash does not match block",
			modify: func(hash *types.Hash, _ *ktypes.Block, _ **ktypes.CommitInfo) {
				*hash = types.Hash{1}
			},
			wantErr: "does not match the response hash",
		},
		{
			name: "transactions replaced under a real header",
			modify: func(_ *types.Hash, blk *ktypes.Block, _ **ktypes.CommitInfo) {
				blk.Txns[0] = newTx(99, "mallory", "forged")
			},
			wantErr: "merkle root mismatch",
		},
		{
			name: "transaction dropped",
			modify: func(_ *types.Hash, blk *ktypes.Block, _ **ktypes.CommitInfo) {
				blk.Txns = blk.Txns[:1]
			},
			wantErr: "transaction count mismatch",
		},
		{
			name: "commit info app hash differs from snapshot",
			modify: func(_ *types.Hash, _ *ktypes.Block, ci **ktypes.CommitInfo) {
				(*ci).AppHash = types.Hash{9}
			},
			wantErr: "does not match the snapshot app hash",
		},
		{
			name: "missing commit info",
			modify: func(_ *types.Hash, _ *ktypes.Block, ci **ktypes.CommitInfo) {
				*ci = nil
			},
			wantErr: "missing commit info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blk, _ := createTestBlock(height, 2)
			hash := blk.Hash()
			ci := &ktypes.CommitInfo{AppHash: appHash}
			tt.modify(&hash, blk, &ci)

			err := verifyAnchorBlock(height, appHash, hash, blk, ci)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestFetchAnchorBlock(t *testing.T) {
	ctx := context.Background()
	const height = 10
	appHash := fakeAppHash(height)

	goodBlk, _ := createTestBlock(height, 2)
	good := &anchorBS{hash: goodBlk.Hash(), blk: goodBlk, ci: &ktypes.CommitInfo{AppHash: appHash}}

	forgedBlk, _ := createTestBlock(height, 1)
	forged := &anchorBS{hash: forgedBlk.Hash(), blk: forgedBlk, ci: &ktypes.CommitInfo{AppHash: appHash}}

	setup := func(t *testing.T, trustedBS, untrustedBS blockStore) *StateSyncService {
		mn := mock.New()
		dir := t.TempDir()

		h1, _, _, ss1, _, err := newTestStatesyncer(ctx, t, mn, filepath.Join(dir, "trusted"), testSSConfig(false, nil))
		require.NoError(t, err)
		ss1.blockStore = trustedBS

		_, _, _, ss2, _, err := newTestStatesyncer(ctx, t, mn, filepath.Join(dir, "untrusted"), testSSConfig(false, nil))
		require.NoError(t, err)
		ss2.blockStore = untrustedBS

		h3, _, _, ss3, _, err := newTestStatesyncer(ctx, t, mn, filepath.Join(dir, "syncing"), testSSConfig(false, nil))
		require.NoError(t, err)
		ss3.blockSyncCfg = nil // default timeouts

		require.NoError(t, mn.LinkAll())
		require.NoError(t, mn.ConnectAllButSelf())

		for _, addr := range maddrs(h1) {
			i, err := connectPeer(ctx, addr, h3)
			require.NoError(t, err)
			ss3.trustedProviders = append(ss3.trustedProviders, i)
		}
		return ss3
	}

	t.Run("ignores untrusted peers", func(t *testing.T) {
		ss := setup(t, good, forged)
		for range 5 { // peer order is random in getBlkHeight; fetchAnchorBlock must not use it
			blk, ci, err := ss.fetchAnchorBlock(ctx, height, appHash)
			require.NoError(t, err)
			require.Equal(t, goodBlk.Hash(), blk.Hash())
			require.Equal(t, appHash, ci.AppHash)
		}
	})

	t.Run("block only on untrusted peer", func(t *testing.T) {
		ss := setup(t, &anchorBS{}, good)
		_, _, err := ss.fetchAnchorBlock(ctx, height, appHash)
		require.ErrorContains(t, err, "no trusted provider returned a valid block")
	})

	t.Run("trusted provider block fails verification", func(t *testing.T) {
		mismatched := &anchorBS{hash: goodBlk.Hash(), blk: forgedBlk, ci: &ktypes.CommitInfo{AppHash: appHash}}
		ss := setup(t, mismatched, good)
		_, _, err := ss.fetchAnchorBlock(ctx, height, appHash)
		require.ErrorContains(t, err, "no trusted provider returned a valid block")
	})

	t.Run("wrong app hash", func(t *testing.T) {
		ss := setup(t, good, good)
		_, _, err := ss.fetchAnchorBlock(ctx, height, types.Hash{9})
		require.ErrorContains(t, err, "no trusted provider returned a valid block")
	})

	t.Run("canceled", func(t *testing.T) {
		ss := setup(t, good, good)
		cctx, cancel := context.WithTimeout(ctx, time.Nanosecond)
		defer cancel()
		<-cctx.Done()
		_, _, err := ss.fetchAnchorBlock(cctx, height, appHash)
		require.Error(t, err)
	})
}
