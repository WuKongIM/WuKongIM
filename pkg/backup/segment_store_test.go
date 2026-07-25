package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestReplicatedSegmentStoreReusesAndRepairsCommittedSegment(t *testing.T) {
	primary := newMemoryRepository("primary")
	secondary := newMemoryRepository("secondary")
	keys := &countingSegmentKeyManager{mask: 0xa5}
	codec := backup.NewSegmentCodec(keys, bytes.NewReader(bytes.Repeat([]byte{0x41}, 128)))
	seed := sha256.Sum256([]byte("replicated-segment-store-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	store, err := backup.NewReplicatedSegmentStore(primary, secondary, codec, signer, "signing-key")
	if err != nil {
		t.Fatalf("NewReplicatedSegmentStore() error = %v", err)
	}
	descriptor := testSegmentDescriptor()
	plaintext := []byte("channel-a:41\nchannel-a:42\n")

	first, err := store.Commit(context.Background(), descriptor, plaintext)
	if err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	if keys.generates != 1 {
		t.Fatalf("first Commit() generated %d data keys, want 1", keys.generates)
	}
	if first.PlaintextBytes != int64(len(plaintext)) {
		t.Fatalf("reference plaintext bytes = %d, want %d", first.PlaintextBytes, len(plaintext))
	}
	if _, err := store.VerifyCommit(context.Background(), first); err != nil {
		t.Fatalf("VerifyCommit() error = %v", err)
	}
	second, err := store.Commit(context.Background(), descriptor, plaintext)
	if err != nil {
		t.Fatalf("retry Commit() error = %v", err)
	}
	if second != first {
		t.Fatalf("retry Commit() reference = %#v, want %#v", second, first)
	}
	if keys.generates != 1 {
		t.Fatalf("committed retry generated %d data keys, want 1", keys.generates)
	}
	restored, err := store.Load(context.Background(), first)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !bytes.Equal(restored, plaintext) {
		t.Fatalf("Load() payload = %q, want %q", restored, plaintext)
	}
	wrongSize := first
	wrongSize.PlaintextBytes++
	if _, err := store.Load(context.Background(), wrongSize); !errors.Is(err, backup.ErrObjectCorrupt) {
		t.Fatalf("Load(wrong reference size) error = %v, want %v", err, backup.ErrObjectCorrupt)
	}

	commit, err := backup.LoadSegmentCommit(context.Background(), primary.body(first.CommitKey), signer)
	if err != nil {
		t.Fatalf("LoadSegmentCommit() error = %v", err)
	}
	secondary.remove(commit.Payload.Key)
	secondary.remove(first.CommitKey)
	if _, err := store.VerifyCommit(context.Background(), first); !errors.Is(err, backup.ErrRepositoryIncomplete) {
		t.Fatalf("VerifyCommit(missing secondary) error = %v, want %v", err, backup.ErrRepositoryIncomplete)
	}

	repaired, err := store.Commit(context.Background(), descriptor, plaintext)
	if err != nil {
		t.Fatalf("repair Commit() error = %v", err)
	}
	if repaired != first {
		t.Fatalf("repair Commit() reference = %#v, want %#v", repaired, first)
	}
	if keys.generates != 1 {
		t.Fatalf("repair generated %d data keys, want 1", keys.generates)
	}
	if !bytes.Equal(primary.body(first.CommitKey), secondary.body(first.CommitKey)) {
		t.Fatal("repaired commit copies differ")
	}
	if !bytes.Equal(primary.body(commit.Payload.Key), secondary.body(commit.Payload.Key)) {
		t.Fatal("repaired payload copies differ")
	}
}

func TestReplicatedSegmentStoreReturnsStableReferenceWhenRepairFails(t *testing.T) {
	primary := newMemoryRepository("primary")
	secondary := newMemoryRepository("secondary")
	keys := &countingSegmentKeyManager{mask: 0xa5}
	codec := backup.NewSegmentCodec(keys, bytes.NewReader(bytes.Repeat([]byte{0x43}, 128)))
	seed := sha256.Sum256([]byte("replicated-segment-repair-failure-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	store, err := backup.NewReplicatedSegmentStore(primary, secondary, codec, signer, "signing-key")
	if err != nil {
		t.Fatalf("NewReplicatedSegmentStore() error = %v", err)
	}
	plaintext := []byte("channel-a:41\nchannel-a:42\n")
	committed, err := store.Commit(context.Background(), testSegmentDescriptor(), plaintext)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	commit, err := backup.LoadSegmentCommit(context.Background(), primary.body(committed.CommitKey), signer)
	if err != nil {
		t.Fatalf("LoadSegmentCommit() error = %v", err)
	}
	secondary.remove(commit.Payload.Key)
	secondary.remove(committed.CommitKey)

	failingSecondary := &failPutRepository{Repository: secondary, failCall: 1}
	failingStore, err := backup.NewReplicatedSegmentStore(primary, failingSecondary, codec, signer, "signing-key")
	if err != nil {
		t.Fatalf("NewReplicatedSegmentStore(failing) error = %v", err)
	}
	reference, err := failingStore.Commit(context.Background(), testSegmentDescriptor(), plaintext)
	if !errors.Is(err, backup.ErrRepositoryIncomplete) {
		t.Fatalf("repair Commit() error = %v, want %v", err, backup.ErrRepositoryIncomplete)
	}
	if reference != committed {
		t.Fatalf("repair failure reference = %#v, want %#v", reference, committed)
	}
}

func TestReplicatedSegmentStoreFailureMatrixNeverLoadsPartialCommit(t *testing.T) {
	testCases := []struct {
		name                string
		repository          string
		putCall             int
		failAfterWrite      bool
		fullyCommittedAfter bool
	}{
		{name: "before primary payload", repository: "primary", putCall: 1},
		{name: "after primary payload", repository: "primary", putCall: 1, failAfterWrite: true},
		{name: "before secondary payload", repository: "secondary", putCall: 1},
		{name: "after secondary payload", repository: "secondary", putCall: 1, failAfterWrite: true},
		{name: "before secondary commit", repository: "secondary", putCall: 2},
		{name: "after secondary commit", repository: "secondary", putCall: 2, failAfterWrite: true},
		{name: "before primary commit", repository: "primary", putCall: 2},
		{name: "after primary commit", repository: "primary", putCall: 2, failAfterWrite: true, fullyCommittedAfter: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			primaryBase := newMemoryRepository("primary")
			secondaryBase := newMemoryRepository("secondary")
			var primary backup.Repository = primaryBase
			var secondary backup.Repository = secondaryBase
			if testCase.repository == "primary" {
				primary = &failPutRepository{Repository: primaryBase, failCall: testCase.putCall, failAfterWrite: testCase.failAfterWrite}
			} else {
				secondary = &failPutRepository{Repository: secondaryBase, failCall: testCase.putCall, failAfterWrite: testCase.failAfterWrite}
			}
			keys := &countingSegmentKeyManager{mask: 0xa5}
			random := append(bytes.Repeat([]byte{0x51}, 16), bytes.Repeat([]byte{0x61}, 64)...)
			codec := backup.NewSegmentCodec(keys, bytes.NewReader(random))
			seed := sha256.Sum256([]byte("segment-failure-" + testCase.name))
			signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}
			store, err := backup.NewReplicatedSegmentStore(primary, secondary, codec, signer, "signing-key")
			if err != nil {
				t.Fatalf("NewReplicatedSegmentStore() error = %v", err)
			}
			plaintext := []byte("channel-a:41\nchannel-a:42\n")

			failedReference, err := store.Commit(context.Background(), testSegmentDescriptor(), plaintext)
			if !errors.Is(err, backup.ErrRepositoryIncomplete) {
				t.Fatalf("first Commit() error = %v, want %v", err, backup.ErrRepositoryIncomplete)
			}
			if failedReference.SegmentID == "" || failedReference.CommitKey == "" || failedReference.CommitSHA256 == "" {
				t.Fatalf("failed Commit() reference = %#v, want stable attempt reference", failedReference)
			}
			_, loadErr := store.Load(context.Background(), failedReference)
			if testCase.fullyCommittedAfter {
				if loadErr != nil {
					t.Fatalf("Load() after fully persisted final commit error = %v", loadErr)
				}
			} else if !errors.Is(loadErr, backup.ErrRepositoryIncomplete) {
				t.Fatalf("Load() partial error = %v, want %v", loadErr, backup.ErrRepositoryIncomplete)
			}

			retriedReference, err := store.Commit(context.Background(), testSegmentDescriptor(), plaintext)
			if err != nil {
				t.Fatalf("retry Commit() error = %v", err)
			}
			if retriedReference.SegmentID != failedReference.SegmentID {
				t.Fatalf("retry SegmentID = %q, want %q", retriedReference.SegmentID, failedReference.SegmentID)
			}
			restored, err := store.Load(context.Background(), retriedReference)
			if err != nil {
				t.Fatalf("Load() after retry error = %v", err)
			}
			if !bytes.Equal(restored, plaintext) {
				t.Fatalf("Load() payload = %q, want %q", restored, plaintext)
			}
			if got := countRepositorySuffix(primaryBase, "/commit.json"); got != 1 {
				t.Fatalf("primary commit count = %d, want 1", got)
			}
			if got := countRepositorySuffix(secondaryBase, "/commit.json"); got != 1 {
				t.Fatalf("secondary commit count = %d, want 1", got)
			}
		})
	}
}

func testSegmentDescriptor() backup.SegmentDescriptor {
	return backup.SegmentDescriptor{
		Logical: backup.SegmentLogicalDescriptor{
			RepositoryID:     "repo-prod",
			SourceClusterID:  "cluster-source",
			SourceGeneration: "source-generation-7",
			Generation:       "slot-17-generation-3",
			HashSlot:         17,
			Stream:           backup.SegmentStreamMessages,
			Sequence:         9,
			RecordCount:      2,
		},
		KMSKeyID: "kms-prod",
	}
}

type countingSegmentKeyManager struct {
	mask      byte
	generates int
}

func (m *countingSegmentKeyManager) GenerateDataKey(ctx context.Context, keyID string) (backup.DataKey, error) {
	m.generates++
	return wrappingKeyManager{wrappingByte: m.mask}.GenerateDataKey(ctx, keyID)
}

func (m *countingSegmentKeyManager) UnwrapDataKey(ctx context.Context, keyID string, wrapped []byte) ([]byte, error) {
	return wrappingKeyManager{wrappingByte: m.mask}.UnwrapDataKey(ctx, keyID, wrapped)
}

type failPutRepository struct {
	backup.Repository
	failCall       int
	failAfterWrite bool
	putCalls       int
	failed         bool
}

func (r *failPutRepository) PutImmutable(ctx context.Context, key string, size int64, checksum string, body io.Reader) error {
	r.putCalls++
	shouldFail := !r.failed && r.putCalls == r.failCall
	if shouldFail && !r.failAfterWrite {
		r.failed = true
		return errors.New("injected repository failure before write")
	}
	err := r.Repository.PutImmutable(ctx, key, size, checksum, body)
	if shouldFail && r.failAfterWrite {
		r.failed = true
		return errors.New("injected repository failure after write")
	}
	return err
}

func countRepositorySuffix(repository *memoryRepository, suffix string) int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	count := 0
	for key := range repository.objects {
		if strings.HasSuffix(key, suffix) {
			count++
		}
	}
	return count
}
