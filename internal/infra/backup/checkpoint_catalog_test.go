package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestReplicatedCheckpointCatalogPublishesOnlyNewArtifacts(t *testing.T) {
	primaryFile, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	primary := &catalogRecordingRepository{Repository: primaryFile}
	secondary := &catalogRecordingRepository{Repository: secondaryFile}
	signer := newCatalogTestSigner()
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(primary, secondary, signer, "signing-key")
	require.NoError(t, err)

	first, err := catalog.Publish(context.Background(), catalogTestCheckpoint("checkpoint-1", 1_753_400_200_000), nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.Head.Sequence)
	require.Equal(t, 3, primary.puts)
	require.Equal(t, 3, secondary.puts)
	require.Equal(t, 6, primary.opens+secondary.opens)

	second, err := catalog.Publish(context.Background(), catalogTestCheckpoint("checkpoint-2", 1_753_400_300_000), &first.Head)
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.Head.Sequence)
	require.Equal(t, 5, primary.puts)
	require.Equal(t, 5, secondary.puts)
	require.Equal(t, 12, primary.opens+secondary.opens, "publication inspects only the checkpoint, vector, and page fixed keys")
	require.Equal(t, first.Checkpoint.GenerationVector, second.Checkpoint.GenerationVector)
	referenceBody, err := json.Marshal(second.Checkpoint)
	require.NoError(t, err)
	require.Less(t, len(referenceBody), 1024, "catalog index rows must not embed the 256-Slot vector")

	page, err := catalog.LoadPage(context.Background(), second.Head)
	require.NoError(t, err)
	require.Equal(t, first.Head, *page.Previous)
	require.Equal(t, "checkpoint-2", page.Entries[0].ID)
	checkpoint, err := catalog.LoadCheckpoint(context.Background(), second.Checkpoint)
	require.NoError(t, err)
	require.Equal(t, "checkpoint-2", checkpoint.ID)
}

func TestReplicatedCheckpointCatalogRejectsMismatchedGenerationVectorOnHold(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	signer := newCatalogTestSigner()
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(
		primary, secondary, signer, "signing-key",
	)
	require.NoError(t, err)

	first, err := catalog.Publish(
		context.Background(),
		catalogTestCheckpoint("checkpoint-vector-a", 1_753_400_200_000),
		nil,
	)
	require.NoError(t, err)
	secondCheckpoint := catalogTestCheckpoint("checkpoint-vector-b", 1_753_400_300_000)
	secondCheckpoint.Slots[0].Generation = "slot-generation-2"
	second, err := catalog.Publish(context.Background(), secondCheckpoint, &first.Head)
	require.NoError(t, err)

	forged := first.Checkpoint
	forged.GenerationVector = second.Checkpoint.GenerationVector
	_, err = catalog.SetCheckpointHold(
		context.Background(), forged, true, 1_753_400_400_000, &second.Head,
	)
	require.ErrorIs(t, err, backupartifact.ErrObjectCorrupt)
}

func TestReplicatedCheckpointCatalogRetryIsIdempotent(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	signer := &changingCatalogSigner{}
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(primary, secondary, signer, "signing-key")
	require.NoError(t, err)
	checkpoint := catalogTestCheckpoint("checkpoint-retry", 1_753_400_200_000)

	first, err := catalog.Publish(context.Background(), checkpoint, nil)
	require.NoError(t, err)
	second, err := catalog.Publish(context.Background(), checkpoint, nil)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, 3, signer.signCalls, "retry must reuse existing nondeterministic signatures")
}

func TestReplicatedCheckpointCatalogDoesNotReturnHeadAfterPartialReplication(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	secondary := &catalogFailPutRepository{Repository: secondaryFile, failAt: 2}
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(primary, secondary, newCatalogTestSigner(), "signing-key")
	require.NoError(t, err)

	commit, err := catalog.Publish(context.Background(), catalogTestCheckpoint("checkpoint-partial", 1_753_400_200_000), nil)
	require.ErrorIs(t, err, backupartifact.ErrRepositoryIncomplete)
	require.Empty(t, commit.Head.Key)
}

func TestReplicatedCheckpointCatalogRepairsPartialPageWithOriginalSignature(t *testing.T) {
	primaryFile, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	primary := &catalogFailPutRepository{Repository: primaryFile, failAt: 3}
	signer := &changingCatalogSigner{}
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(primary, secondary, signer, "signing-key")
	require.NoError(t, err)
	checkpoint := catalogTestCheckpoint("checkpoint-repair", 1_753_400_200_000)

	_, err = catalog.Publish(context.Background(), checkpoint, nil)
	require.ErrorIs(t, err, backupartifact.ErrRepositoryIncomplete)
	commit, err := catalog.Publish(context.Background(), checkpoint, nil)
	require.NoError(t, err)
	require.Equal(t, 3, signer.signCalls)
	_, err = catalog.LoadPage(context.Background(), commit.Head)
	require.NoError(t, err)
}

type catalogRecordingRepository struct {
	backupartifact.Repository
	puts  int
	opens int
}

func (r *catalogRecordingRepository) PutImmutable(ctx context.Context, key string, size int64, checksum string, body io.Reader) error {
	r.puts++
	return r.Repository.PutImmutable(ctx, key, size, checksum, body)
}

func (r *catalogRecordingRepository) Open(ctx context.Context, key string) (io.ReadCloser, backupartifact.RepositoryObject, error) {
	r.opens++
	return r.Repository.Open(ctx, key)
}

type catalogFailPutRepository struct {
	backupartifact.Repository
	puts   int
	failAt int
}

type changingCatalogSigner struct {
	signCalls int
}

func (s *changingCatalogSigner) Sign(_ context.Context, keyID string, message []byte) (backupartifact.ManifestSignature, error) {
	s.signCalls++
	sum := sha256.Sum256(message)
	value := make([]byte, len(sum)+1)
	copy(value, sum[:])
	value[len(sum)] = byte(s.signCalls)
	return backupartifact.ManifestSignature{Algorithm: "test-changing", KeyID: keyID, Value: value}, nil
}

func (s *changingCatalogSigner) Verify(_ context.Context, signature backupartifact.ManifestSignature, message []byte) error {
	sum := sha256.Sum256(message)
	if signature.Algorithm != "test-changing" || len(signature.Value) != len(sum)+1 ||
		!bytes.Equal(signature.Value[:len(sum)], sum[:]) {
		return errors.New("invalid changing signature")
	}
	return nil
}

func (r *catalogFailPutRepository) PutImmutable(ctx context.Context, key string, size int64, checksum string, body io.Reader) error {
	r.puts++
	if r.puts == r.failAt {
		return io.ErrUnexpectedEOF
	}
	return r.Repository.PutImmutable(ctx, key, size, checksum, body)
}

func newCatalogTestSigner() testEd25519Signer {
	seed := sha256.Sum256([]byte("checkpoint-catalog-test"))
	return testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])}
}

func catalogTestCheckpoint(id string, createdAt int64) backupartifact.Checkpoint {
	checkpoint := backupartifact.Checkpoint{
		Format: backupartifact.CheckpointFormat, Version: backupartifact.CheckpointVersion,
		ID: id, RepositoryID: "repository-prod",
		SourceClusterID: "cluster-source", SourceGeneration: "source-generation-1",
		HashSlotCount: 2, CreatedAtUnixMillis: createdAt,
		Slots: make([]backupartifact.CheckpointSlot, 2),
	}
	for hashSlot := uint16(0); hashSlot < checkpoint.HashSlotCount; hashSlot++ {
		watermark := createdAt - 1_000 + int64(hashSlot)
		checkpoint.Slots[hashSlot] = backupartifact.CheckpointSlot{
			HashSlot: hashSlot, Generation: "slot-generation-1",
			Metadata: backupartifact.CheckpointStream{
				Sequence: 1, Head: catalogSegmentReference("a"),
				SourceHighWatermark: 10, WatermarkAtUnixMillis: watermark,
			},
			Messages: backupartifact.CheckpointStream{
				Sequence: 1, Head: catalogSegmentReference("b"), CursorHead: catalogSegmentReference("c"),
				SourceHighWatermark: 20, WatermarkAtUnixMillis: watermark,
			},
			WatermarkAtUnixMillis: watermark,
		}
	}
	checkpoint.EffectiveAtUnixMillis = checkpoint.Slots[0].WatermarkAtUnixMillis
	return checkpoint
}

func catalogSegmentReference(character string) *backupartifact.SegmentReference {
	return &backupartifact.SegmentReference{
		SegmentID:    strings.Repeat(character, 64),
		CommitKey:    "segments/" + strings.Repeat(character, 64) + "/commit.json",
		CommitSHA256: strings.Repeat("d", 64), PlaintextBytes: 1,
	}
}
