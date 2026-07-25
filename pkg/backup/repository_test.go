package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestReplicatedPublisherDoesNotExposeManifestWhenSecondaryObjectUploadFails(t *testing.T) {
	t.Parallel()

	primary := newMemoryRepository("primary")
	secondary := newMemoryRepository("secondary")
	secondary.failPut = true
	publisher := backup.NewReplicatedPublisher(primary, secondary)

	objects := []backup.SealedObject{
		testSealedObject("objects/00/meta.wkb", 0, []byte("slot-zero")),
		testSealedObject("objects/01/meta.wkb", 1, []byte("slot-one")),
	}
	manifest := testPublishManifest(objects)
	seed := sha256.Sum256([]byte("repository-publish-test-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}

	_, err := publisher.Publish(context.Background(), manifest, objects, signer, "signing-key")
	if !errors.Is(err, backup.ErrRepositoryIncomplete) {
		t.Fatalf("Publish() error = %v, want %v", err, backup.ErrRepositoryIncomplete)
	}
	manifestKey := "restore-points/rp-publish/manifest.json"
	if primary.has(manifestKey) {
		t.Fatalf("primary repository exposed %q after incomplete replica upload", manifestKey)
	}
	if secondary.has(manifestKey) {
		t.Fatalf("secondary repository exposed %q after incomplete replica upload", manifestKey)
	}
}

func TestLoadRestorePointRejectsSignedManifestWithMissingObject(t *testing.T) {
	t.Parallel()

	primary := newMemoryRepository("primary")
	secondary := newMemoryRepository("secondary")
	publisher := backup.NewReplicatedPublisher(primary, secondary)
	objects := []backup.SealedObject{
		testSealedObject("objects/00/meta.wkb", 0, []byte("slot-zero")),
		testSealedObject("objects/01/meta.wkb", 1, []byte("slot-one")),
	}
	seed := sha256.Sum256([]byte("repository-load-test-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	if _, err := publisher.Publish(context.Background(), testPublishManifest(objects), objects, signer, "signing-key"); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	primary.remove(objects[1].Entry.Key)

	_, err := backup.LoadRestorePoint(context.Background(), primary, "rp-publish", signer)
	if !errors.Is(err, backup.ErrRepositoryIncomplete) {
		t.Fatalf("LoadRestorePoint() error = %v, want %v", err, backup.ErrRepositoryIncomplete)
	}
}

func TestReplicatedPublisherPublishesPreviouslyUploadedReferences(t *testing.T) {
	primary := newMemoryRepository("primary")
	secondary := newMemoryRepository("secondary")
	publisher := backup.NewReplicatedPublisher(primary, secondary)
	objects := []backup.SealedObject{
		testSealedObject("objects/00/meta.wkb", 0, []byte("slot-zero")),
		testSealedObject("objects/01/meta.wkb", 1, []byte("slot-one")),
	}
	for _, object := range objects {
		if err := publisher.ReplicateObject(context.Background(), object); err != nil {
			t.Fatalf("ReplicateObject() error = %v", err)
		}
	}
	seed := sha256.Sum256([]byte("repository-reference-publish-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}

	signed, err := publisher.PublishReferences(context.Background(), testPublishManifest(objects), signer, "signing-key")
	if err != nil {
		t.Fatalf("PublishReferences() error = %v", err)
	}
	if signed.Signature == nil || !primary.has("restore-points/rp-publish/manifest.json") || !secondary.has("restore-points/rp-publish/manifest.json") {
		t.Fatalf("PublishReferences() did not expose a signed manifest in both repositories")
	}
	graph, err := backup.LoadRestorePointGraph(context.Background(), primary, "rp-publish", signer)
	if err != nil {
		t.Fatalf("LoadRestorePointGraph() error = %v", err)
	}
	wantKeys := []string{"objects/00/meta.wkb", "objects/01/meta.wkb", "restore-points/rp-publish/manifest.json", "restore-points/rp-publish/published.json"}
	if !equalStrings(graph.Keys, wantKeys) || graph.Manifest.RestorePointID != "rp-publish" {
		t.Fatalf("LoadRestorePointGraph() = %#v, want keys %v", graph, wantKeys)
	}
}

func TestLoadRestorePointGraphMarksMaterializedBaselineCursorPayload(t *testing.T) {
	primary := newMemoryRepository("primary")
	secondary := newMemoryRepository("secondary")
	seed := sha256.Sum256([]byte("baseline-graph-signing-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	segmentStore, err := backup.NewReplicatedSegmentStore(
		primary,
		secondary,
		backup.NewSegmentCodec(
			&countingSegmentKeyManager{mask: 0xa5},
			bytes.NewReader(bytes.Repeat([]byte{0x41}, 128)),
		),
		signer,
		"signing-key",
	)
	if err != nil {
		t.Fatalf("NewReplicatedSegmentStore() error = %v", err)
	}
	cursorBody, err := backup.MarshalChannelIndex(0, []backup.ChannelBoundary{{
		ChannelID: "room-a", ChannelType: 2, Epoch: 1, HW: 7,
	}})
	if err != nil {
		t.Fatalf("MarshalChannelIndex() error = %v", err)
	}
	cursor, err := segmentStore.Commit(context.Background(), backup.SegmentDescriptor{
		Logical: backup.SegmentLogicalDescriptor{
			RepositoryID: "repo-prod", SourceClusterID: "cluster-source",
			SourceGeneration: "generation-7", Generation: "baseline-job",
			HashSlot: 0, Stream: backup.SegmentStreamMessageBaselineCursor,
			Sequence: 1, RecordCount: 1,
		},
		KMSKeyID: "kms-prod",
	}, cursorBody)
	if err != nil {
		t.Fatalf("Commit(cursor) error = %v", err)
	}
	commit, err := backup.LoadSegmentCommit(
		context.Background(), primary.body(cursor.CommitKey), signer,
	)
	if err != nil {
		t.Fatalf("LoadSegmentCommit() error = %v", err)
	}
	metadata := testSealedObject("objects/baseline-job/00000/meta.wkb", 0, []byte("metadata"))
	publisher := backup.NewReplicatedPublisher(primary, secondary)
	if err := publisher.ReplicateObject(context.Background(), metadata); err != nil {
		t.Fatalf("ReplicateObject(metadata) error = %v", err)
	}
	partition := backup.PartitionManifest{
		Format: backup.PartitionManifestFormat, Version: backup.PartitionManifestVersion,
		JobID: "baseline-job", BackupEpoch: 3,
		Cut: backup.PartitionCut{
			HashSlot: 0, PhysicalSlotID: 10,
			RaftIndex: 11, CommittedAtMillis: 1_753_056_300_000,
		},
		BaselineCursor: &cursor,
		Evidence:       backup.PartitionEvidence{Version: backup.PartitionEvidenceVersion},
		Objects:        []backup.ObjectEntry{metadata.Entry},
	}
	partitionBody, err := backup.MarshalPartitionManifest(partition)
	if err != nil {
		t.Fatalf("MarshalPartitionManifest() error = %v", err)
	}
	partitionHash := sha256.Sum256(partitionBody)
	partitionSHA := fmt.Sprintf("%x", partitionHash)
	partitionKey := "partition-manifests/baseline-job/00000.json"
	for _, repository := range []*memoryRepository{primary, secondary} {
		if err := repository.PutImmutable(
			context.Background(), partitionKey, int64(len(partitionBody)),
			partitionSHA, bytes.NewReader(partitionBody),
		); err != nil {
			t.Fatalf("%s PutImmutable(partition) error = %v", repository.Name(), err)
		}
	}
	manifest := backup.Manifest{
		Format: backup.ManifestFormat, Version: backup.ManifestVersion,
		ApplicationVersion: "3.0.0", RepositoryID: "repo-prod",
		SourceClusterID: "cluster-source", SourceGeneration: "generation-7",
		RestorePointID: "rp-baseline", BackupEpoch: 3,
		Kind: backup.RestorePointMaterializedFull, HashSlotCount: 1,
		CreatedAtUnixMillis: 1_753_056_360_000,
		EffectiveAtMillis:   1_753_056_300_000,
		Cuts: []backup.PartitionCut{{
			HashSlot: 0, RaftIndex: 11, CommittedAtMillis: 1_753_056_300_000,
		}},
		Partitions: []backup.PartitionReference{{
			HashSlot: 0, Key: partitionKey, SHA256: partitionSHA,
			Bytes: int64(len(partitionBody)), ObjectCount: 1,
			CiphertextBytes: uint64(metadata.Entry.CiphertextBytes),
			Evidence:        partition.Evidence,
		}},
	}
	if _, err := publisher.PublishReferences(
		context.Background(), manifest, signer, "signing-key",
	); err != nil {
		t.Fatalf("PublishReferences() error = %v", err)
	}
	graph, err := backup.LoadRestorePointGraph(
		context.Background(), primary, manifest.RestorePointID, signer,
	)
	if err != nil {
		t.Fatalf("LoadRestorePointGraph() error = %v", err)
	}
	if !slices.Contains(graph.Keys, cursor.CommitKey) ||
		!slices.Contains(graph.Keys, commit.Payload.Key) {
		t.Fatalf("baseline graph keys = %v, want commit %q and payload %q", graph.Keys, cursor.CommitKey, commit.Payload.Key)
	}
}

func TestReplicatedPublisherRepairsPartialManifestPublishOnRetry(t *testing.T) {
	primary := newMemoryRepository("primary")
	secondary := newMemoryRepository("secondary")
	publisher := backup.NewReplicatedPublisher(primary, secondary)
	objects := []backup.SealedObject{testSealedObject("objects/00/meta.wkb", 0, []byte("slot-zero")), testSealedObject("objects/01/meta.wkb", 1, []byte("slot-one"))}
	for _, object := range objects {
		if err := publisher.ReplicateObject(context.Background(), object); err != nil {
			t.Fatalf("ReplicateObject(): %v", err)
		}
	}
	seed := sha256.Sum256([]byte("repository-manifest-retry-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	primary.failPut = true
	if _, err := publisher.PublishReferences(context.Background(), testPublishManifest(objects), signer, "signing-key"); !errors.Is(err, backup.ErrRepositoryIncomplete) {
		t.Fatalf("first PublishReferences() error = %v", err)
	}
	key := "restore-points/rp-publish/manifest.json"
	if primary.has(key) || !secondary.has(key) {
		t.Fatalf("partial publish state primary=%v secondary=%v", primary.has(key), secondary.has(key))
	}
	if _, err := backup.LoadRestorePoint(context.Background(), secondary, "rp-publish", signer); !errors.Is(err, backup.ErrObjectNotFound) {
		t.Fatalf("LoadRestorePoint() from staged-only secondary error = %v, want object not found", err)
	}
	primary.failPut = false
	retry := testPublishManifest(objects)
	retry.CreatedAtUnixMillis++
	if _, err := publisher.PublishReferences(context.Background(), retry, signer, "signing-key"); err != nil {
		t.Fatalf("retry PublishReferences(): %v", err)
	}
	if !bytes.Equal(primary.body(key), secondary.body(key)) {
		t.Fatal("repaired manifest copies differ")
	}
}

func TestReplicatedPublisherRepairsPartialPublicationMarkerOnRetry(t *testing.T) {
	primary := newMemoryRepository("primary")
	secondary := newMemoryRepository("secondary")
	publisher := backup.NewReplicatedPublisher(primary, secondary)
	objects := []backup.SealedObject{testSealedObject("objects/00/meta.wkb", 0, []byte("slot-zero")), testSealedObject("objects/01/meta.wkb", 1, []byte("slot-one"))}
	for _, object := range objects {
		if err := publisher.ReplicateObject(context.Background(), object); err != nil {
			t.Fatalf("ReplicateObject(): %v", err)
		}
	}
	seed := sha256.Sum256([]byte("repository-publication-retry-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	publicationKey := "restore-points/rp-publish/published.json"
	primary.failPutKey = publicationKey
	if _, err := publisher.PublishReferences(context.Background(), testPublishManifest(objects), signer, "signing-key"); !errors.Is(err, backup.ErrRepositoryIncomplete) {
		t.Fatalf("first PublishReferences() error = %v", err)
	}
	if primary.has(publicationKey) || !secondary.has(publicationKey) {
		t.Fatalf("partial publication state primary=%v secondary=%v", primary.has(publicationKey), secondary.has(publicationKey))
	}
	if _, err := backup.LoadRestorePoint(context.Background(), secondary, "rp-publish", signer); err != nil {
		t.Fatalf("LoadRestorePoint() from committed secondary: %v", err)
	}
	if _, err := backup.LoadRestorePoint(context.Background(), primary, "rp-publish", signer); !errors.Is(err, backup.ErrObjectNotFound) {
		t.Fatalf("LoadRestorePoint() from staged primary error = %v, want object not found", err)
	}

	primary.failPutKey = ""
	if _, err := publisher.PublishReferences(context.Background(), testPublishManifest(objects), signer, "signing-key"); err != nil {
		t.Fatalf("retry PublishReferences(): %v", err)
	}
	if !bytes.Equal(primary.body(publicationKey), secondary.body(publicationKey)) {
		t.Fatal("repaired publication marker copies differ")
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func testSealedObject(key string, hashSlot uint16, ciphertext []byte) backup.SealedObject {
	plainHash := sha256.Sum256([]byte("plain:" + key))
	cipherHash := sha256.Sum256(ciphertext)
	return backup.SealedObject{
		Entry: backup.ObjectEntry{
			Key:              key,
			Kind:             backup.ObjectKindMetadata,
			HashSlot:         hashSlot,
			PlaintextSHA256:  fmt.Sprintf("%x", plainHash),
			CiphertextSHA256: fmt.Sprintf("%x", cipherHash),
			PlaintextBytes:   10,
			CiphertextBytes:  int64(len(ciphertext)),
			Compression:      backup.CompressionZstd,
			Encryption:       backup.EncryptionAES256GCM,
			KMSKeyID:         "kms-prod",
			WrappedKey:       "d3JhcHBlZA==",
			Nonce:            "bm9uY2Utbm9uY2U=",
		},
		Ciphertext: append([]byte(nil), ciphertext...),
	}
}

func testPublishManifest(objects []backup.SealedObject) backup.Manifest {
	entries := make([]backup.ObjectEntry, len(objects))
	for index := range objects {
		entries[index] = objects[index].Entry
	}
	return backup.Manifest{
		Format:                backup.ManifestFormat,
		Version:               backup.ManifestVersion,
		ApplicationVersion:    "3.0.0-beta.1",
		RepositoryID:          "repo-prod",
		SourceClusterID:       "cluster-source",
		SourceGeneration:      "generation-7",
		RestorePointID:        "rp-publish",
		BackupEpoch:           19,
		Kind:                  backup.RestorePointIncremental,
		HashSlotCount:         2,
		CreatedAtUnixMillis:   1_753_056_360_000,
		EffectiveAtMillis:     1_753_056_300_000,
		Cuts:                  []backup.PartitionCut{{HashSlot: 0, RaftIndex: 4, CommittedAtMillis: 1_753_056_300_000}, {HashSlot: 1, RaftIndex: 5, CommittedAtMillis: 1_753_056_310_000}},
		Objects:               entries,
		ErasureLedgerBoundary: 2,
	}
}

type memoryRepository struct {
	name       string
	mu         sync.Mutex
	objects    map[string][]byte
	failPut    bool
	failPutKey string
}

func newMemoryRepository(name string) *memoryRepository {
	return &memoryRepository{name: name, objects: make(map[string][]byte)}
}

func (r *memoryRepository) Name() string { return r.name }

func (r *memoryRepository) PutImmutable(_ context.Context, key string, size int64, checksum string, body io.Reader) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failPut || key == r.failPutKey {
		return errors.New("repository unavailable")
	}
	if _, ok := r.objects[key]; ok {
		return backup.ErrObjectExists
	}
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(value)) != size {
		return errors.New("size mismatch")
	}
	hash := sha256.Sum256(value)
	if fmt.Sprintf("%x", hash) != checksum {
		return errors.New("checksum mismatch")
	}
	r.objects[key] = value
	return nil
}

func (r *memoryRepository) Open(_ context.Context, key string) (io.ReadCloser, backup.RepositoryObject, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.objects[key]
	if !ok {
		return nil, backup.RepositoryObject{}, backup.ErrObjectNotFound
	}
	hash := sha256.Sum256(value)
	return io.NopCloser(bytes.NewReader(value)), backup.RepositoryObject{Key: key, Size: int64(len(value)), SHA256: fmt.Sprintf("%x", hash)}, nil
}

func (r *memoryRepository) Stat(_ context.Context, key string) (backup.RepositoryObject, error) {
	reader, object, err := r.Open(context.Background(), key)
	if reader != nil {
		_ = reader.Close()
	}
	return object, err
}

func (r *memoryRepository) has(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.objects[key]
	return ok
}

func (r *memoryRepository) body(key string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.objects[key]...)
}

func (r *memoryRepository) remove(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.objects, key)
}
