package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestStoredMessageChunkManifestAuthenticatesExactIndex(t *testing.T) {
	ctx := context.Background()
	digest := strings.Repeat("a", 64)
	manifest, err := backup.NewMessageChunkManifest(7, []backup.ChunkReference{{
		Kind:     backup.ChunkKindMessages,
		Sequence: 11,
		Stream:   3,
		Part:     1,
		Final:    true,
		Key:      "slots/007/attempts/attempt-1/messages-000011.zst",
		Descriptor: backup.ChunkDescriptor{
			StoredSHA256:  digest,
			LogicalSHA256: digest,
			LogicalBytes:  128,
			StoredBytes:   64,
			Compression:   backup.CompressionZstd,
		},
		Records:      4,
		MaxMessageID: 42,
	}})
	if err != nil {
		t.Fatalf("NewMessageChunkManifest(): %v", err)
	}
	body, err := backup.MarshalMessageChunkManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalMessageChunkManifest(): %v", err)
	}
	sum := sha256.Sum256(body)
	wantDigest := hex.EncodeToString(sum[:])
	const backupID = "bk-message-index"
	const key = "slots/007/attempts/attempt-1/message-chunks.json"
	store := newMemoryArchiveStore()
	store.add("backups/"+backupID+"/"+key, body)

	loaded, err := backup.LoadStoredMessageChunkManifest(
		ctx, store, backupID, key, wantDigest,
	)
	if err != nil {
		t.Fatalf("LoadStoredMessageChunkManifest(): %v", err)
	}
	if loaded.HashSlot != 7 || loaded.Records != 4 || loaded.MaxMessageID != 42 {
		t.Fatalf("loaded manifest = %#v", loaded)
	}

	if _, err := backup.LoadStoredMessageChunkManifest(
		ctx, store, backupID, key, strings.Repeat("f", 64),
	); !errors.Is(err, backup.ErrObjectCorrupt) {
		t.Fatalf("LoadStoredMessageChunkManifest(digest mismatch) error = %v", err)
	}
	if _, err := backup.LoadStoredMessageChunkManifest(
		ctx, store, backupID, "../message-chunks.json", wantDigest,
	); !errors.Is(err, backup.ErrInvalidObject) {
		t.Fatalf("LoadStoredMessageChunkManifest(unsafe key) error = %v", err)
	}
	if _, err := backup.LoadStoredMessageChunkManifest(
		ctx, store, backupID, key, "not-a-digest",
	); !errors.Is(err, backup.ErrInvalidObject) {
		t.Fatalf("LoadStoredMessageChunkManifest(invalid digest) error = %v", err)
	}

	corruptBody := bytes.Replace(body, []byte(`"records":4`), []byte(`"records":5`), 1)
	corruptSum := sha256.Sum256(corruptBody)
	store.add("backups/"+backupID+"/"+key, corruptBody)
	if _, err := backup.LoadStoredMessageChunkManifest(
		ctx, store, backupID, key, hex.EncodeToString(corruptSum[:]),
	); !errors.Is(err, backup.ErrInvalidManifest) {
		t.Fatalf("LoadStoredMessageChunkManifest(invalid totals) error = %v", err)
	}
}

func TestArchiveManifestRejectsInvalidIdentityTimelineAndAggregates(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*backup.ArchiveManifest)
		wantErr error
	}{
		{
			name: "unsupported version",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.Version++
			},
			wantErr: backup.ErrUnsupportedVersion,
		},
		{
			name: "unsafe archive identity",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.ID = "../backup"
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "unsafe cluster identity",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.SourceClusterID = ".cluster"
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "missing source application",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.SourceApplication = ""
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "unknown trigger",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.Trigger = "retry"
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "cut ends after completion",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.CutEndedUnixMillis = manifest.CompletedAtUnixMillis + 1
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "unsupported compression",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.Compression = "gzip"
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "duplicate Hash Slot",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.Slots[1] = manifest.Slots[0]
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "wrong Slot manifest path",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.Slots[7].ManifestKey = "slots/008/manifest.json"
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "uppercase digest",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.Slots[7].ManifestSHA256 = strings.ToUpper(manifest.Slots[7].ManifestSHA256)
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "logical byte total",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.LogicalBytes = 1
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "stored byte total",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.StoredBytes = 1
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "record total",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.Records = 1
			},
			wantErr: backup.ErrInvalidManifest,
		},
		{
			name: "message high-water mark",
			mutate: func(manifest *backup.ArchiveManifest) {
				manifest.MaxMessageID = 1
			},
			wantErr: backup.ErrInvalidManifest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validArchiveManifest()
			manifest.Slots = append([]backup.SlotReference(nil), manifest.Slots...)
			test.mutate(&manifest)
			_, err := backup.MarshalArchiveManifest(manifest)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("MarshalArchiveManifest() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
