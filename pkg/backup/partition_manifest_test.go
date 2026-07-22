package backup_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestPartitionManifestStrictRoundTrip(t *testing.T) {
	manifest := backup.PartitionManifest{
		Format:      backup.PartitionManifestFormat,
		Version:     backup.PartitionManifestVersion,
		JobID:       "backup-7",
		BackupEpoch: 7,
		Cut: backup.PartitionCut{
			HashSlot:          3,
			RaftIndex:         91,
			CommittedAtMillis: 1710000000000,
		},
		Evidence: backup.PartitionEvidence{
			Version:         backup.PartitionEvidenceVersion,
			MetadataRecords: 7,
			MessageRecords:  11,
			MaxMessageID:    42,
		},
		Objects: []backup.ObjectEntry{
			validPartitionObject("objects/backup-7/00003/messages-000000.bin", backup.ObjectKindMessages),
			validPartitionObject("objects/backup-7/00003/metadata-000000.bin", backup.ObjectKindMetadata),
		},
	}

	body, err := backup.MarshalPartitionManifest(manifest)
	require.NoError(t, err)
	loaded, err := backup.LoadPartitionManifest(body)
	require.NoError(t, err)
	require.Equal(t, manifest, loaded)

	unknown := bytes.Replace(body, []byte(`"version":1`), []byte(`"version":1,"unknown":true`), 1)
	_, err = backup.LoadPartitionManifest(unknown)
	require.ErrorIs(t, err, backup.ErrInvalidManifest)
}

func TestPartitionManifestRequiresExplicitRestoreEvidence(t *testing.T) {
	manifest := backup.PartitionManifest{
		Format:      backup.PartitionManifestFormat,
		Version:     backup.PartitionManifestVersion,
		JobID:       "backup-7",
		BackupEpoch: 7,
		Cut: backup.PartitionCut{
			HashSlot:          3,
			RaftIndex:         91,
			CommittedAtMillis: 1710000000000,
		},
		Objects: []backup.ObjectEntry{
			validPartitionObject("objects/backup-7/00003/metadata-000000.bin", backup.ObjectKindMetadata),
		},
	}

	_, err := backup.MarshalPartitionManifest(manifest)
	require.ErrorIs(t, err, backup.ErrInvalidManifest)
}

func TestPartitionManifestRejectsMessageEvidenceWithoutAllocatorFence(t *testing.T) {
	manifest := backup.PartitionManifest{
		Format:      backup.PartitionManifestFormat,
		Version:     backup.PartitionManifestVersion,
		JobID:       "backup-7",
		BackupEpoch: 7,
		Cut: backup.PartitionCut{
			HashSlot:          3,
			RaftIndex:         91,
			CommittedAtMillis: 1710000000000,
		},
		Evidence: backup.PartitionEvidence{
			Version:        backup.PartitionEvidenceVersion,
			MessageRecords: 1,
		},
		Objects: []backup.ObjectEntry{
			validPartitionObject("objects/backup-7/00003/metadata-000000.bin", backup.ObjectKindMetadata),
		},
	}

	_, err := backup.MarshalPartitionManifest(manifest)
	require.ErrorIs(t, err, backup.ErrInvalidManifest)
}

func validPartitionObject(key string, kind backup.ObjectKind) backup.ObjectEntry {
	return backup.ObjectEntry{
		Key:              key,
		Kind:             kind,
		HashSlot:         3,
		PlaintextSHA256:  strings.Repeat("a", 64),
		CiphertextSHA256: strings.Repeat("b", 64),
		PlaintextBytes:   64,
		CiphertextBytes:  80,
		Compression:      backup.CompressionZstd,
		Encryption:       backup.EncryptionAES256GCM,
		KMSKeyID:         "kms-backup",
		WrappedKey:       "d3JhcHBlZA==",
		Nonce:            "MDEyMzQ1Njc4OTAx",
	}
}
