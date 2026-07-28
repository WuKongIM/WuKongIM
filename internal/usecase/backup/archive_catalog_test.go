package backup_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestArchiveCatalogShowsOnlyCompleteArchivesAndSupportsHold(t *testing.T) {
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	writeCatalogArchive(t, store, "backup-complete", true, 1_800_000_001_000)
	writeCatalogArchive(t, store, "backup-incomplete", false, 1_800_000_002_000)

	archives, err := backupusecase.ListArchives(context.Background(), store)
	if err != nil {
		t.Fatalf("ListArchives(): %v", err)
	}
	if len(archives) != 1 || archives[0].ID != "backup-complete" || archives[0].Held {
		t.Fatalf("archives = %#v", archives)
	}
	held, err := backupusecase.SetArchiveHold(
		context.Background(), store, "backup-complete", true,
		"quarterly baseline", time.UnixMilli(1_800_000_003_000),
	)
	if err != nil {
		t.Fatalf("SetArchiveHold(): %v", err)
	}
	if !held.Held || held.HoldNote != "quarterly baseline" {
		t.Fatalf("held = %#v", held)
	}
	if err := backupusecase.DeleteArchive(
		context.Background(), store, "backup-complete",
	); err == nil {
		t.Fatal("DeleteArchive(held) error = nil")
	}
}

func writeCatalogArchive(
	t *testing.T,
	store backupartifact.ArchiveStore,
	id string,
	complete bool,
	completedAt int64,
) {
	t.Helper()
	slots := make([]backupartifact.SlotReference, backupartifact.DefaultHashSlotCount)
	for hashSlot := range slots {
		sum := sha256.Sum256([]byte(fmt.Sprintf("slot-%d", hashSlot)))
		slots[hashSlot] = backupartifact.SlotReference{
			HashSlot:       uint16(hashSlot),
			ManifestKey:    fmt.Sprintf("slots/%03d/manifest.json", hashSlot),
			ManifestSHA256: hex.EncodeToString(sum[:]),
		}
	}
	manifest := backupartifact.ArchiveManifest{
		Format: backupartifact.ArchiveFormat, Version: backupartifact.ArchiveVersion,
		ID: id, Trigger: backupartifact.TriggerScheduled,
		SourceClusterID: "cluster-a", SourceApplication: "wukongim-test",
		HashSlotCount:         backupartifact.DefaultHashSlotCount,
		StartedAtUnixMillis:   completedAt - 1000,
		CompletedAtUnixMillis: completedAt,
		CutStartedUnixMillis:  completedAt - 900,
		CutEndedUnixMillis:    completedAt - 100,
		Compression:           backupartifact.CompressionZstd,
		Checksum:              backupartifact.ChecksumSHA256,
		Slots:                 slots,
	}
	body, err := backupartifact.MarshalArchiveManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalArchiveManifest(): %v", err)
	}
	putCatalogObject(t, store, "backups/"+id+"/manifest.json", body)
	if !complete {
		return
	}
	marker, err := backupartifact.NewCompleteMarker(body)
	if err != nil {
		t.Fatalf("NewCompleteMarker(): %v", err)
	}
	markerBody, err := backupartifact.MarshalCompleteMarker(marker)
	if err != nil {
		t.Fatalf("MarshalCompleteMarker(): %v", err)
	}
	putCatalogObject(t, store, "backups/"+id+"/COMPLETE", markerBody)
	putCatalogObject(t, store, "catalog/"+id, markerBody)
}

func putCatalogObject(
	t *testing.T,
	store backupartifact.ArchiveStore,
	key string,
	body []byte,
) {
	t.Helper()
	if err := store.Put(context.Background(), backupartifact.PutObject{
		Key: key, Body: strings.NewReader(string(body)),
		ExpectedBytes: uint64(len(body)),
	}); err != nil {
		t.Fatalf("Put(%s): %v", key, err)
	}
}
