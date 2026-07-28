package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"testing"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestPublishArchiveRequiresAndVerifiesEveryHashSlot(t *testing.T) {
	store := newMemoryArchiveStore()
	slots := writeCompleteSlotSet(t, store, "backup-publish")

	manifest, err := runtimebackup.PublishArchive(
		context.Background(), store,
		runtimebackup.PublishArchiveRequest{
			ID: "backup-publish", Trigger: backupartifact.TriggerManual,
			SourceClusterID: "cluster-a", SourceApplication: "wukongim-test",
			StartedUnixMillis:   1_800_000_000_000,
			CompletedUnixMillis: 1_800_000_001_000,
			Slots:               slots,
		},
	)
	if err != nil {
		t.Fatalf("PublishArchive(): %v", err)
	}
	if len(manifest.Slots) != backupartifact.DefaultHashSlotCount {
		t.Fatalf("slots = %d", len(manifest.Slots))
	}
	verified, err := runtimebackup.VerifyPublishedArchive(
		context.Background(), store, "backup-publish",
	)
	if err != nil {
		t.Fatalf("VerifyPublishedArchive(): %v", err)
	}
	if verified.SourceClusterID != "cluster-a" {
		t.Fatalf("verified = %#v", verified)
	}
	if body := store.body("catalog/backup-publish"); len(body) == 0 {
		t.Fatal("catalog entry was not published")
	}
	firstManifest := store.body("backups/backup-publish/manifest.json")
	second, err := runtimebackup.PublishArchive(
		context.Background(), store,
		runtimebackup.PublishArchiveRequest{
			ID: "backup-publish", Trigger: backupartifact.TriggerManual,
			SourceClusterID: "cluster-a", SourceApplication: "wukongim-test",
			StartedUnixMillis:   1_800_000_000_000,
			CompletedUnixMillis: 1_800_000_001_000,
			Slots:               slots,
		},
	)
	if err != nil {
		t.Fatalf("PublishArchive(retry): %v", err)
	}
	if !reflect.DeepEqual(second, manifest) ||
		!bytes.Equal(firstManifest, store.body("backups/backup-publish/manifest.json")) {
		t.Fatal("publication retry changed an already COMPLETE archive")
	}
}

func TestPublishArchiveDoesNotExposeCorruptSlot(t *testing.T) {
	store := newMemoryArchiveStore()
	slots := writeCompleteSlotSet(t, store, "backup-corrupt")
	store.mu.Lock()
	store.objects["backups/backup-corrupt/slots/007/meta-000001.zst"][0] ^= 0xff
	store.mu.Unlock()

	_, err := runtimebackup.PublishArchive(
		context.Background(), store,
		runtimebackup.PublishArchiveRequest{
			ID: "backup-corrupt", Trigger: backupartifact.TriggerManual,
			SourceClusterID: "cluster-a", SourceApplication: "wukongim-test",
			StartedUnixMillis:   1_800_000_000_000,
			CompletedUnixMillis: 1_800_000_001_000,
			Slots:               slots,
		},
	)
	if err == nil {
		t.Fatal("PublishArchive(corrupt) error = nil")
	}
	if body := store.body("backups/backup-corrupt/COMPLETE"); body != nil {
		t.Fatal("corrupt archive was published")
	}
}

func writeCompleteSlotSet(
	t *testing.T,
	store *memoryArchiveStore,
	backupID string,
) []backupartifact.SlotReference {
	t.Helper()
	var encoded bytes.Buffer
	descriptor, err := backupartifact.EncodeChunk(&encoded, bytes.NewReader([]byte("metadata")))
	if err != nil {
		t.Fatalf("EncodeChunk(): %v", err)
	}
	references := make(
		[]backupartifact.SlotReference,
		backupartifact.DefaultHashSlotCount,
	)
	for hashSlot := 0; hashSlot < backupartifact.DefaultHashSlotCount; hashSlot++ {
		chunkKey := fmt.Sprintf(
			"backups/%s/slots/%03d/meta-000001.zst",
			backupID, hashSlot,
		)
		store.objects[chunkKey] = append([]byte(nil), encoded.Bytes()...)
		slotManifest := backupartifact.SlotManifest{
			Format:   backupartifact.SlotManifestFormat,
			Version:  backupartifact.SlotManifestVersion,
			HashSlot: uint16(hashSlot),
			Cut: backupartifact.SlotCut{
				PhysicalSlotID: 1, LeaderTerm: 2, AppliedTerm: 2,
				ConfigurationVersion: 3,
				AppliedIndex:         4,
				CapturedAtUnixMillis: 1_800_000_000_100 + int64(hashSlot),
			},
			Chunks: []backupartifact.ChunkReference{{
				Kind: backupartifact.ChunkKindMetadata, Sequence: 1,
				Stream: 0, Part: 1, Final: true,
				Key:        fmt.Sprintf("slots/%03d/meta-000001.zst", hashSlot),
				Descriptor: descriptor,
			}},
			LogicalBytes: descriptor.LogicalBytes,
			StoredBytes:  descriptor.StoredBytes,
		}
		body, err := backupartifact.MarshalSlotManifest(slotManifest)
		if err != nil {
			t.Fatalf("MarshalSlotManifest(%d): %v", hashSlot, err)
		}
		manifestKey := fmt.Sprintf("slots/%03d/manifest.json", hashSlot)
		store.objects["backups/"+backupID+"/"+manifestKey] = body
		sum := sha256.Sum256(body)
		references[hashSlot] = backupartifact.SlotReference{
			HashSlot: uint16(hashSlot), ManifestKey: manifestKey,
			ManifestSHA256: hex.EncodeToString(sum[:]),
			LogicalBytes:   descriptor.LogicalBytes,
			StoredBytes:    descriptor.StoredBytes,
		}
	}
	return references
}
