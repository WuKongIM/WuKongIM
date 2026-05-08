package raftlog

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

var snapshotNoncePattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

var errSnapshotNoOverwriteRenameUnsupported = errors.New("raftstorage: atomic no-overwrite snapshot rename is unsupported on this platform")

// snapshotStore persists snapshot payload bytes in external chunk directories.
type snapshotStore struct {
	// root is the external snapshot directory root.
	root string
	// chunkSize is the maximum number of bytes written to each chunk file.
	chunkSize uint64
	// nonce generates the random snapshot ID suffix and is injectable for tests.
	nonce func() (string, error)
}

// stagedSnapshot is a prepared snapshot filesystem plan plus its manifest.
type stagedSnapshot struct {
	// manifest describes the external chunk files that will be published.
	manifest SnapshotManifest
	// tmpDir is the unpublished directory used while writing chunk files.
	tmpDir string
	// finalDir is the durable snapshot directory after publication.
	finalDir string
}

// newSnapshotStore creates an external chunk store rooted at root.
func newSnapshotStore(root string, chunkSize uint64) *snapshotStore {
	if chunkSize == 0 {
		chunkSize = defaultSnapshotChunkSize
	}
	return &snapshotStore{root: root, chunkSize: chunkSize, nonce: randomSnapshotNonce}
}

// prepare builds the manifest and filesystem paths without creating files.
func (s *snapshotStore) prepare(ctx context.Context, scope Scope, snapshot raftpb.Snapshot) (*stagedSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !isValidScopeKind(scope.Kind) {
		return nil, errors.New("raftstorage: invalid snapshot scope kind")
	}
	if snapshot.Metadata.Index == 0 {
		return nil, errors.New("raftstorage: snapshot index must be non-zero")
	}
	if snapshot.Metadata.Term == 0 {
		return nil, errors.New("raftstorage: snapshot term must be non-zero")
	}

	nonce, err := s.nonce()
	if err != nil {
		return nil, err
	}
	if !snapshotNoncePattern.MatchString(nonce) {
		return nil, errors.New("raftstorage: invalid snapshot nonce")
	}

	snapshotID := fmt.Sprintf("snap-%016x-%016x-%s", snapshot.Metadata.Index, snapshot.Metadata.Term, nonce)
	if err := validateSnapshotID(snapshotID); err != nil {
		return nil, err
	}
	scopeDir := s.scopeDir(scope)
	manifest := SnapshotManifest{
		Version:           snapshotManifestVersion,
		ScopeKind:         uint8(scope.Kind),
		ScopeID:           scope.ID,
		Index:             snapshot.Metadata.Index,
		Term:              snapshot.Metadata.Term,
		ConfState:         cloneConfState(snapshot.Metadata.ConfState),
		SnapshotID:        snapshotID,
		ChunkSize:         s.chunkSize,
		ChecksumType:      snapshotChecksumCRC32C,
		CreatedAtUnixNano: time.Now().UnixNano(),
	}
	return &stagedSnapshot{
		manifest: manifest,
		tmpDir:   filepath.Join(scopeDir, ".tmp-"+snapshotID),
		finalDir: filepath.Join(scopeDir, snapshotID),
	}, nil
}

// write creates the temporary chunk directory and writes all payload chunks.
func (s *snapshotStore) write(ctx context.Context, staged *stagedSnapshot, data []byte) error {
	if staged == nil {
		return errors.New("raftstorage: nil staged snapshot")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(staged.tmpDir), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(staged.tmpDir, 0o700); err != nil {
		return err
	}
	if err := fsyncDir(filepath.Dir(staged.tmpDir)); err != nil {
		return err
	}

	chunkCount, err := manifestChunkCount(uint64(len(data)), staged.manifest.ChunkSize)
	if err != nil {
		return err
	}
	staged.manifest.TotalSize = uint64(len(data))
	staged.manifest.ChunkCount = chunkCount
	staged.manifest.WholeChecksum = snapshotChecksum(data)
	staged.manifest.ChunkChecksums = make([][]byte, 0, chunkCount)

	totalSize := uint64(len(data))
	for i, offset := 0, uint64(0); offset < totalSize; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunkLen := staged.manifest.ChunkSize
		if remaining := totalSize - offset; chunkLen > remaining {
			chunkLen = remaining
		}
		chunk := data[int(offset):int(offset+chunkLen)]
		if err := writeSyncedFile(filepath.Join(staged.tmpDir, chunkFileName(i)), chunk); err != nil {
			return err
		}
		staged.manifest.ChunkChecksums = append(staged.manifest.ChunkChecksums, snapshotChecksum(chunk))
		offset += chunkLen
	}
	if err := fsyncDir(staged.tmpDir); err != nil {
		return err
	}
	return staged.manifest.Validate(Scope{Kind: ScopeKind(staged.manifest.ScopeKind), ID: staged.manifest.ScopeID})
}

// publishFinal atomically publishes the staged directory without overwriting a final directory.
func (s *snapshotStore) publishFinal(staged *stagedSnapshot) error {
	if staged == nil {
		return errors.New("raftstorage: nil staged snapshot")
	}
	if err := renameNoOverwrite(staged.tmpDir, staged.finalDir); err != nil {
		return err
	}
	return fsyncDir(filepath.Dir(staged.finalDir))
}

// stage prepares, writes, and publishes a snapshot, retrying existing final ID collisions.
func (s *snapshotStore) stage(ctx context.Context, scope Scope, snapshot raftpb.Snapshot) (*stagedSnapshot, error) {
	for {
		staged, err := s.prepare(ctx, scope, snapshot)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(staged.finalDir); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := s.write(ctx, staged, snapshot.Data); err != nil {
			return nil, err
		}
		if err := s.publishFinal(staged); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, err
		}
		return staged, nil
	}
}

// read loads and verifies all external chunks described by manifest.
func (s *snapshotStore) read(ctx context.Context, scope Scope, manifest SnapshotManifest) (raftpb.Snapshot, error) {
	if err := manifest.Validate(scope); err != nil {
		return raftpb.Snapshot{}, err
	}
	if manifest.TotalSize > uint64(maxInt()) {
		return raftpb.Snapshot{}, errors.New("raftstorage: snapshot total size exceeds memory limit")
	}
	finalDir := filepath.Join(s.scopeDir(scope), manifest.SnapshotID)
	data := make([]byte, 0, int(manifest.TotalSize))
	for i := uint32(0); i < manifest.ChunkCount; i++ {
		if err := ctx.Err(); err != nil {
			return raftpb.Snapshot{}, err
		}
		chunk, err := os.ReadFile(filepath.Join(finalDir, chunkFileName(int(i))))
		if err != nil {
			return raftpb.Snapshot{}, err
		}
		expectedSize := expectedChunkSize(manifest, i)
		if uint64(len(chunk)) != expectedSize {
			return raftpb.Snapshot{}, errors.New("raftstorage: invalid snapshot chunk size")
		}
		if !checksumEqual(snapshotChecksum(chunk), manifest.ChunkChecksums[i]) {
			return raftpb.Snapshot{}, errors.New("raftstorage: invalid snapshot chunk checksum")
		}
		data = append(data, chunk...)
	}
	if uint64(len(data)) != manifest.TotalSize {
		return raftpb.Snapshot{}, errors.New("raftstorage: invalid snapshot total size")
	}
	if !checksumEqual(snapshotChecksum(data), manifest.WholeChecksum) {
		return raftpb.Snapshot{}, errors.New("raftstorage: invalid snapshot whole checksum")
	}
	return raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			ConfState: cloneConfState(manifest.ConfState),
			Index:     manifest.Index,
			Term:      manifest.Term,
		},
		Data: data,
	}, nil
}

func (s *snapshotStore) scopeDir(scope Scope) string {
	switch scope.Kind {
	case ScopeSlot:
		return filepath.Join(s.root, fmt.Sprintf("slot-%d", scope.ID))
	case ScopeController:
		return filepath.Join(s.root, fmt.Sprintf("controller-%d", scope.ID))
	default:
		return filepath.Join(s.root, "invalid-scope")
	}
}

func randomSnapshotNonce() (string, error) {
	var buf [8]byte
	if _, err := io.ReadFull(rand.Reader, buf[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", binary.BigEndian.Uint64(buf[:])), nil
}

func chunkFileName(index int) string {
	return fmt.Sprintf("chunk-%06d", index)
}

func expectedChunkSize(manifest SnapshotManifest, chunkIndex uint32) uint64 {
	if manifest.ChunkCount == 0 {
		return 0
	}
	if chunkIndex+1 < manifest.ChunkCount {
		return manifest.ChunkSize
	}
	remaining := manifest.TotalSize - uint64(chunkIndex)*manifest.ChunkSize
	return remaining
}

func checksumEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func writeSyncedFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, writeErr := file.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func fsyncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func normalizeNoOverwriteRenameError(err error) error {
	if err == nil {
		return nil
	}
	if os.IsExist(err) {
		return fmt.Errorf("raftstorage: snapshot final directory already exists: %w", os.ErrExist)
	}
	return err
}
