package node

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	mock "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
	"github.com/trufnetwork/kwil-db/core/crypto"
	"github.com/trufnetwork/kwil-db/node/snapshotter"
)

func TestSnapshotMetadataValidate(t *testing.T) {
	h := sha256.Sum256([]byte("chunk"))

	require.NoError(t, (&snapshotMetadata{Chunks: 1, ChunkHashes: [][32]byte{h}}).Validate())
	require.Error(t, (&snapshotMetadata{}).Validate())
	require.Error(t, (&snapshotMetadata{Chunks: 2, ChunkHashes: [][32]byte{h}}).Validate())
	require.Error(t, (&snapshotMetadata{Chunks: 1, ChunkHashes: [][32]byte{h, h}}).Validate())
}

// servePeerJSON makes a peer that answers every stream on proto with payload.
func servePeerJSON(t *testing.T, mn mock.Mocknet, proto protocol.ID, payload any) peer.AddrInfo {
	_, h := newTestHost(t, mn, crypto.KeyTypeSecp256k1)
	bts, err := json.Marshal(payload)
	require.NoError(t, err)
	h.SetStreamHandler(proto, func(s network.Stream) {
		defer s.Close()
		s.Write(bts)
	})
	return peer.AddrInfo{ID: h.ID(), Addrs: h.Addrs()}
}

func TestRequestSnapshotCatalogsDropsInconsistentMetadata(t *testing.T) {
	ctx := context.Background()
	mn := mock.New()

	_, _, _, ss, _, err := newTestStatesyncer(ctx, t, mn, filepath.Join(t.TempDir(), "n"), testSSConfig(false, nil))
	require.NoError(t, err)

	bad := &snapshotMetadata{
		Height:      10,
		Format:      snapshotter.DefaultSnapshotFormat,
		Chunks:      3,
		Hash:        data[:],
		ChunkHashes: [][32]byte{data},
	}
	provider := servePeerJSON(t, mn, snapshotter.ProtocolIDSnapshotCatalog, []*snapshotMetadata{nil, bad, snap1})
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	require.NoError(t, ss.requestSnapshotCatalogs(ctx, provider))

	snaps := ss.snapshotPool.listSnapshots()
	require.Len(t, snaps, 1)
	require.Equal(t, snap1.Height, snaps[0].Height)
}

func TestVerifySnapshotRejectsInconsistentProviderMetadata(t *testing.T) {
	ctx := context.Background()
	mn := mock.New()

	_, _, _, ss, _, err := newTestStatesyncer(ctx, t, mn, filepath.Join(t.TempDir(), "n"), testSSConfig(false, nil))
	require.NoError(t, err)

	other := sha256.Sum256([]byte("other"))
	snap := &snapshotMetadata{
		Height:      7,
		Format:      snapshotter.DefaultSnapshotFormat,
		Chunks:      2,
		Hash:        data[:],
		ChunkHashes: [][32]byte{data, other},
	}
	// Same chunk count as snap, but fewer chunk hashes.
	meta := *snap
	meta.ChunkHashes = [][32]byte{data}

	provider := servePeerJSON(t, mn, snapshotter.ProtocolIDSnapshotMeta, &meta)
	ss.trustedProviders = []*peer.AddrInfo{&provider}
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	res, appHash := ss.VerifySnapshot(ctx, snap)
	require.Equal(t, VerificationInvalid, res)
	require.Nil(t, appHash)
}
