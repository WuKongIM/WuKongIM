package raftlog

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

// OpenStartupSnapshot pins the manifest and verifies chunks using a fixed-size
// buffer. The returned reader keeps one chunk descriptor open at a time.
func (s *pebbleStore) OpenStartupSnapshot(ctx context.Context, report func(multiraft.RecoveryProgress)) (multiraft.StartupSnapshot, error) {
	manifest, ok, release, err := s.loadSnapshotManifestAndRegisterActive(ctx)
	if err != nil {
		return multiraft.StartupSnapshot{}, err
	}
	if !ok {
		return multiraft.StartupSnapshot{}, nil
	}
	fail := func(e error) (multiraft.StartupSnapshot, error) { release(); return multiraft.StartupSnapshot{}, e }
	if err := manifest.Validate(s.scope); err != nil {
		return fail(err)
	}
	root := filepath.Join(s.db.snapshotStore.scopeDir(s.scope), manifest.SnapshotID)
	whole := crc32.New(snapshotCRC32CTable)
	digest := sha256.New()
	buffer := make([]byte, 64<<10)
	var completed int64
	progress := func() {
		if report != nil {
			report(multiraft.RecoveryProgress{SnapshotIndex: manifest.Index, Stage: "snapshot_verify", Bytes: completed, TotalBytes: int64(manifest.TotalSize)})
		}
	}
	progress()
	for i := uint32(0); i < manifest.ChunkCount; i++ {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		f, err := os.Open(filepath.Join(root, chunkFileName(int(i))))
		if err != nil {
			return fail(err)
		}
		stat, err := f.Stat()
		if err != nil {
			f.Close()
			return fail(err)
		}
		expected := int64(expectedChunkSize(manifest, i))
		if stat.Size() != expected {
			f.Close()
			return fail(errors.New("raftstorage: invalid snapshot chunk size"))
		}
		chunk := crc32.New(snapshotCRC32CTable)
		var read int64
		for read < expected {
			if err := ctx.Err(); err != nil {
				f.Close()
				return fail(err)
			}
			n, e := f.Read(buffer)
			if n > 0 {
				whole.Write(buffer[:n])
				digest.Write(buffer[:n])
				chunk.Write(buffer[:n])
				read += int64(n)
				completed += int64(n)
				progress()
			}
			if e != nil {
				if e == io.EOF && read == expected {
					break
				}
				f.Close()
				return fail(e)
			}
		}
		if err := f.Close(); err != nil {
			return fail(err)
		}
		if chunk.Sum32() != binary.BigEndian.Uint32(manifest.ChunkChecksums[i]) {
			return fail(errors.New("raftstorage: invalid snapshot chunk checksum"))
		}
	}
	if whole.Sum32() != binary.BigEndian.Uint32(manifest.WholeChecksum) {
		return fail(errors.New("raftstorage: invalid snapshot whole checksum"))
	}
	reader := &snapshotChunkReader{ctx: ctx, root: root, chunkSize: int64(manifest.ChunkSize), size: int64(manifest.TotalSize), chunkIndex: -1, release: release}
	var sum [32]byte
	copy(sum[:], digest.Sum(nil))
	return multiraft.StartupSnapshot{Metadata: raftpb.SnapshotMetadata{Index: manifest.Index, Term: manifest.Term, ConfState: cloneConfState(manifest.ConfState)}, Size: int64(manifest.TotalSize), Reader: reader, Digest: sum}, nil
}

// snapshotChunkReader relies on the immutable manifest pin for its lifetime.
// It is owned by one startup goroutine, like an ordinary io.ReadSeeker.
type snapshotChunkReader struct {
	ctx                  context.Context
	root                 string
	chunkSize, size, pos int64
	chunkIndex           int64
	file                 *os.File
	release              func()
	closeOnce            sync.Once
	closed               bool
	closeErr             error
}

func (r *snapshotChunkReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, os.ErrClosed
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.pos >= r.size {
		return 0, io.EOF
	}
	index := r.pos / r.chunkSize
	if index != r.chunkIndex || r.file == nil {
		if r.file != nil {
			if err := r.file.Close(); err != nil {
				return 0, err
			}
			r.file = nil
		}
		f, err := os.Open(filepath.Join(r.root, chunkFileName(int(index))))
		if err != nil {
			return 0, err
		}
		r.file = f
		r.chunkIndex = index
	}
	available := r.chunkSize - r.pos%r.chunkSize
	if left := r.size - r.pos; left < available {
		available = left
	}
	if int64(len(p)) > available {
		p = p[:available]
	}
	n, err := r.file.ReadAt(p, r.pos%r.chunkSize)
	r.pos += int64(n)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return n, err
}
func (r *snapshotChunkReader) Seek(offset int64, whence int) (int64, error) {
	if r.closed {
		return 0, os.ErrClosed
	}
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = r.pos + offset
	case io.SeekEnd:
		next = r.size + offset
	default:
		return 0, errors.New("invalid snapshot seek origin")
	}
	if next < 0 || next > r.size {
		return 0, errors.New("snapshot seek out of range")
	}
	r.pos = next
	return next, nil
}
func (r *snapshotChunkReader) Close() error {
	r.closeOnce.Do(func() {
		r.closed = true
		if r.file != nil {
			r.closeErr = r.file.Close()
		}
		r.release()
	})
	return r.closeErr
}
