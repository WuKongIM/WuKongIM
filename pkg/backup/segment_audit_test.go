package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"io"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestReplicatedSegmentStoreAuditsClassifiesRepairsAndRevalidates(t *testing.T) {
	tests := []struct {
		name     string
		category backup.SegmentCorruptionCategory
		damage   func(*memoryRepository, backup.SegmentReference, backup.SegmentCommit)
		wrap     func(*memoryRepository) backup.Repository
	}{
		{
			name: "missing", category: backup.SegmentCorruptionMissing,
			damage: func(repository *memoryRepository, _ backup.SegmentReference, commit backup.SegmentCommit) {
				repository.remove(commit.Payload.Key)
			},
		},
		{
			name: "ciphertext", category: backup.SegmentCorruptionCiphertext,
			damage: func(repository *memoryRepository, _ backup.SegmentReference, commit backup.SegmentCommit) {
				repository.mutate(commit.Payload.Key)
			},
		},
		{
			name: "commit proof", category: backup.SegmentCorruptionCommitProof,
			damage: func(repository *memoryRepository, reference backup.SegmentReference, _ backup.SegmentCommit) {
				repository.mutate(reference.CommitKey)
			},
		},
		{
			name: "checksum", category: backup.SegmentCorruptionChecksum,
			damage: func(*memoryRepository, backup.SegmentReference, backup.SegmentCommit) {},
			wrap: func(repository *memoryRepository) backup.Repository {
				return &checksumRepairRepository{memoryRepository: repository}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			primary := newMemoryRepository("primary")
			secondaryBase := newMemoryRepository("secondary")
			var secondary backup.Repository = secondaryBase
			if test.wrap != nil {
				secondary = test.wrap(secondaryBase)
			}
			keys := &countingSegmentKeyManager{mask: 0xa5}
			codec := backup.NewSegmentCodec(
				keys, bytes.NewReader(bytes.Repeat([]byte{0x51}, 128)),
			)
			seed := sha256.Sum256([]byte("segment-auditor-signing-key"))
			signer := ed25519ManifestSigner{
				privateKey: ed25519.NewKeyFromSeed(seed[:]),
			}
			primaryRepair := backup.RepairRepository(primary)
			secondaryRepair, ok := secondary.(backup.RepairRepository)
			require.True(t, ok)
			store, err := backup.NewReplicatedSegmentStoreWithRepair(
				primary, secondary, primaryRepair, secondaryRepair,
				codec, signer, "signing-key",
			)
			require.NoError(t, err)
			reference, err := store.Commit(
				context.Background(), testSegmentDescriptor(),
				[]byte("fully authenticated plaintext"),
			)
			require.NoError(t, err)
			commit, err := backup.LoadSegmentCommit(
				context.Background(), primary.body(reference.CommitKey), signer,
			)
			require.NoError(t, err)
			test.damage(secondaryBase, reference, commit)
			if corruptor, ok := secondary.(*checksumRepairRepository); ok {
				corruptor.corruptKey = commit.Payload.Key
			}

			report, err := store.InspectSegmentCopies(context.Background(), reference)
			require.NoError(t, err)
			require.Len(t, report.Copies, 2)
			require.True(t, report.Copies[0].Healthy)
			require.False(t, report.Copies[1].Healthy)
			require.Equal(t, test.category, report.Copies[1].Category)

			repairedBytes, err := store.RepairSegmentCopy(
				context.Background(), reference, "secondary",
			)
			require.NoError(t, err)
			require.Positive(t, repairedBytes)

			report, err = store.InspectSegmentCopies(context.Background(), reference)
			require.NoError(t, err)
			require.True(t, report.Copies[0].Healthy)
			require.True(t, report.Copies[1].Healthy)
			require.Equal(t, report.Copies[0].StoredBytes, report.Copies[1].StoredBytes)
		})
	}
}

func TestReplicatedSegmentStoreReportsDualRepositoryLossWithoutRepair(t *testing.T) {
	primary := newMemoryRepository("primary")
	secondary := newMemoryRepository("secondary")
	keys := &countingSegmentKeyManager{mask: 0xa5}
	codec := backup.NewSegmentCodec(
		keys, bytes.NewReader(bytes.Repeat([]byte{0x61}, 128)),
	)
	seed := sha256.Sum256([]byte("segment-auditor-dual-loss-key"))
	signer := ed25519ManifestSigner{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	store, err := backup.NewReplicatedSegmentStoreWithRepair(
		primary, secondary, primary, secondary, codec, signer, "signing-key",
	)
	require.NoError(t, err)
	reference, err := store.Commit(
		context.Background(), testSegmentDescriptor(), []byte("payload"),
	)
	require.NoError(t, err)
	primary.remove(reference.CommitKey)
	secondary.remove(reference.CommitKey)

	report, err := store.InspectSegmentCopies(context.Background(), reference)
	require.NoError(t, err)
	require.False(t, report.Copies[0].Healthy)
	require.False(t, report.Copies[1].Healthy)
	_, err = store.RepairSegmentCopy(context.Background(), reference, "secondary")
	require.ErrorIs(t, err, backup.ErrRepositoryIncomplete)
}

// RepairImmutable gives the shared in-memory test repository the production
// repair capability without weakening its create-only PutImmutable behavior.
func (r *memoryRepository) RepairImmutable(
	_ context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
) error {
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(value)
	if int64(len(value)) != size || fmt.Sprintf("%x", hash) != checksum {
		return backup.ErrObjectCorrupt
	}
	r.mu.Lock()
	r.objects[key] = append([]byte(nil), value...)
	r.mu.Unlock()
	return nil
}

func (r *memoryRepository) mutate(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value := append([]byte(nil), r.objects[key]...)
	if len(value) == 0 {
		return
	}
	value[len(value)/2] ^= 0xff
	r.objects[key] = value
}

type checksumRepairRepository struct {
	*memoryRepository
	corruptKey string
}

func (r *checksumRepairRepository) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, backup.RepositoryObject, error) {
	reader, object, err := r.memoryRepository.Open(ctx, key)
	if err == nil && key == r.corruptKey {
		object.SHA256 = fmt.Sprintf("%064x", 1)
	}
	return reader, object, err
}

func (r *checksumRepairRepository) Stat(
	ctx context.Context,
	key string,
) (backup.RepositoryObject, error) {
	reader, object, err := r.Open(ctx, key)
	if reader != nil {
		_ = reader.Close()
	}
	return object, err
}

func (r *checksumRepairRepository) RepairImmutable(
	ctx context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
) error {
	err := r.memoryRepository.RepairImmutable(ctx, key, size, checksum, body)
	if err == nil && key == r.corruptKey {
		r.corruptKey = ""
	}
	return err
}
