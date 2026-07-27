package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestReplicatedSegmentStoreAuditsAndRepairsPartitionGraphArtifacts(t *testing.T) {
	tests := []struct {
		name        string
		objectIndex int
		category    backup.SegmentCorruptionCategory
		damage      func(*memoryRepository, backup.PartitionReference, backup.ObjectEntry)
	}{
		{
			name: "manifest commit proof", objectIndex: -1,
			category: backup.SegmentCorruptionCommitProof,
			damage: func(repository *memoryRepository, reference backup.PartitionReference, _ backup.ObjectEntry) {
				repository.mutate(reference.Key)
			},
		},
		{
			name: "payload missing", objectIndex: 0,
			category: backup.SegmentCorruptionMissing,
			damage: func(repository *memoryRepository, _ backup.PartitionReference, entry backup.ObjectEntry) {
				repository.remove(entry.Key)
			},
		},
		{
			name: "payload ciphertext", objectIndex: 0,
			category: backup.SegmentCorruptionCiphertext,
			damage: func(repository *memoryRepository, _ backup.PartitionReference, entry backup.ObjectEntry) {
				repository.mutate(entry.Key)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			primary := newMemoryRepository("primary")
			secondary := newMemoryRepository("secondary")
			keys := &countingSegmentKeyManager{mask: 0xa5}
			objectCodec := backup.NewObjectCodec(
				keys, bytes.NewReader(bytes.Repeat([]byte{0x41}, 64)),
			)
			sealed, err := objectCodec.Seal(ctx, backup.ObjectDescriptor{
				Key:      "objects/slot-generation-7/00007/metadata-000000.wkb",
				Kind:     backup.ObjectKindMetadata,
				HashSlot: 7,
			}, []byte("materialized metadata"))
			require.NoError(t, err)
			manifest := backup.PartitionManifest{
				Format:     backup.PartitionManifestFormat,
				Version:    backup.PartitionManifestVersion,
				Generation: "slot-generation-7", RebaseEpoch: 1,
				Cut: backup.PartitionCut{
					HashSlot: 7, PhysicalSlotID: 8,
					RaftIndex: 9, CommittedAtMillis: 10,
				},
				Evidence: backup.PartitionEvidence{
					Version: backup.PartitionEvidenceVersion,
				},
				Objects: []backup.ObjectEntry{sealed.Entry},
			}
			manifestBody, err := backup.MarshalPartitionManifest(manifest)
			require.NoError(t, err)
			manifestHash := sha256.Sum256(manifestBody)
			reference := backup.PartitionReference{
				HashSlot: 7,
				Key:      "partition-manifests/slot-generation-7/00007.json",
				SHA256:   hex.EncodeToString(manifestHash[:]),
				Bytes:    int64(len(manifestBody)), ObjectCount: 1,
				CiphertextBytes: uint64(len(sealed.Ciphertext)),
				Evidence:        manifest.Evidence,
			}
			for _, repository := range []*memoryRepository{primary, secondary} {
				require.NoError(t, repository.PutImmutable(
					ctx, sealed.Entry.Key, int64(len(sealed.Ciphertext)),
					sealed.Entry.CiphertextSHA256,
					bytes.NewReader(sealed.Ciphertext),
				))
				require.NoError(t, repository.PutImmutable(
					ctx, reference.Key, int64(len(manifestBody)),
					reference.SHA256, bytes.NewReader(manifestBody),
				))
			}
			seed := sha256.Sum256([]byte("partition-auditor-signing-key"))
			store, err := backup.NewReplicatedSegmentStoreWithRepair(
				primary, secondary, primary, secondary,
				backup.NewSegmentCodec(keys, nil),
				ed25519ManifestSigner{
					privateKey: ed25519.NewKeyFromSeed(seed[:]),
				})
			require.NoError(t, err)
			test.damage(secondary, reference, sealed.Entry)

			report, err := store.InspectPartitionArtifactCopies(
				ctx, reference, test.objectIndex,
			)
			require.NoError(t, err)
			require.True(t, report.Copies[0].Healthy)
			require.False(t, report.Copies[1].Healthy)
			require.Equal(t, test.category, report.Copies[1].Category)

			repairedBytes, err := store.RepairPartitionArtifactCopy(
				ctx, reference, test.objectIndex, "secondary",
			)
			require.NoError(t, err)
			require.Positive(t, repairedBytes)
			report, err = store.InspectPartitionArtifactCopies(
				ctx, reference, test.objectIndex,
			)
			require.NoError(t, err)
			require.True(t, report.Copies[0].Healthy)
			require.True(t, report.Copies[1].Healthy)
		})
	}
}

func TestReplicatedSegmentStoreCachesAuthenticatedPartitionManifestPerCycle(t *testing.T) {
	ctx := context.Background()
	primary := newMemoryRepository("primary")
	secondary := newMemoryRepository("secondary")
	keys := &countingSegmentKeyManager{mask: 0xa5}
	objectCodec := backup.NewObjectCodec(
		keys, bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)),
	)
	sealed, err := objectCodec.Seal(ctx, backup.ObjectDescriptor{
		Key:      "objects/slot-generation-cache/00007/metadata-000000.wkb",
		Kind:     backup.ObjectKindMetadata,
		HashSlot: 7,
	}, []byte("cached materialized metadata"))
	require.NoError(t, err)
	manifest := backup.PartitionManifest{
		Format:     backup.PartitionManifestFormat,
		Version:    backup.PartitionManifestVersion,
		Generation: "slot-generation-cache", RebaseEpoch: 1,
		Cut: backup.PartitionCut{
			HashSlot: 7, PhysicalSlotID: 8,
			RaftIndex: 9, CommittedAtMillis: 10,
		},
		Evidence: backup.PartitionEvidence{
			Version: backup.PartitionEvidenceVersion,
		},
		Objects: []backup.ObjectEntry{sealed.Entry},
	}
	manifestBody, err := backup.MarshalPartitionManifest(manifest)
	require.NoError(t, err)
	manifestHash := sha256.Sum256(manifestBody)
	reference := backup.PartitionReference{
		HashSlot: 7,
		Key:      "partition-manifests/slot-generation-cache/00007.json",
		SHA256:   hex.EncodeToString(manifestHash[:]),
		Bytes:    int64(len(manifestBody)), ObjectCount: 1,
		CiphertextBytes: uint64(len(sealed.Ciphertext)),
		Evidence:        manifest.Evidence,
	}
	for _, repository := range []*memoryRepository{primary, secondary} {
		require.NoError(t, repository.PutImmutable(
			ctx, sealed.Entry.Key, int64(len(sealed.Ciphertext)),
			sealed.Entry.CiphertextSHA256,
			bytes.NewReader(sealed.Ciphertext),
		))
		require.NoError(t, repository.PutImmutable(
			ctx, reference.Key, int64(len(manifestBody)),
			reference.SHA256, bytes.NewReader(manifestBody),
		))
	}
	seed := sha256.Sum256([]byte("partition-cache-signing-key"))
	store, err := backup.NewReplicatedSegmentStoreWithRepair(
		primary, secondary, primary, secondary,
		backup.NewSegmentCodec(keys, nil),
		ed25519ManifestSigner{
			privateKey: ed25519.NewKeyFromSeed(seed[:]),
		})
	require.NoError(t, err)

	store.BeginPartitionAuditCycle("cycle-1")
	for range 2 {
		report, inspectErr := store.InspectPartitionArtifactCopies(
			ctx, reference, 0,
		)
		require.NoError(t, inspectErr)
		require.True(t, report.Copies[0].Healthy)
		require.True(t, report.Copies[1].Healthy)
	}
	require.Equal(t, 1, primary.openCount(reference.Key))
	require.Equal(t, 1, secondary.openCount(reference.Key))

	store.BeginPartitionAuditCycle("cycle-2")
	_, err = store.InspectPartitionArtifactCopies(ctx, reference, 0)
	require.NoError(t, err)
	require.Equal(t, 2, primary.openCount(reference.Key))
	require.Equal(t, 2, secondary.openCount(reference.Key))
}
