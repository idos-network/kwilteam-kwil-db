package node

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/trufnetwork/kwil-db/node/snapshotter"
)

func TestCopyLimited(t *testing.T) {
	body := bytes.Repeat([]byte("x"), int(snapshotter.ChunkSize)+100)
	var dst bytes.Buffer
	n, err := copyLimited(&dst, bytes.NewReader(body), snapshotter.ChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	if n != snapshotter.ChunkSize || int64(dst.Len()) != snapshotter.ChunkSize {
		t.Fatalf("wrote %d bytes, want %d", dst.Len(), snapshotter.ChunkSize)
	}

	var short bytes.Buffer
	n, err = copyLimited(&short, strings.NewReader("ok"), snapshotter.ChunkSize)
	if err != nil || n != 2 || short.String() != "ok" {
		t.Fatalf("short copy: n=%d err=%v body=%q", n, err, short.String())
	}

	if _, err := copyLimited(io.Discard, strings.NewReader("x"), -1); err == nil {
		t.Fatal("expected error for negative limit")
	}
}

func TestDownloadChunkResumableRejectsOversizedTempFile(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "chunk-0.sql.gz")
	tempPath := finalPath + ".tmp"
	f, err := os.Create(tempPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(snapshotter.ChunkSize + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// host is nil: the size check must return before NewStream.
	s := &StateSyncService{}
	err = s.downloadChunkResumable(context.Background(), &snapshotMetadata{}, peer.AddrInfo{}, 0, 0, finalPath)
	if err == nil {
		t.Fatal("expected oversized chunk error")
	}
	if _, statErr := os.Stat(tempPath); !os.IsNotExist(statErr) {
		t.Fatalf("oversized temp file still present: %v", statErr)
	}
}
