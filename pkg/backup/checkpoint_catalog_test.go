package backup_test

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"

	backup "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestCheckpointRoundTripRequiresCompleteSortedVectorCut(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer := ed25519ManifestSigner{privateKey: privateKey}
	checkpoint := validCheckpointArtifact(2)
	signed, err := backup.SignCheckpoint(context.Background(), checkpoint, signer, "signing-key")
	if err != nil {
		t.Fatalf("SignCheckpoint() error = %v", err)
	}
	body, err := backup.MarshalCheckpoint(signed)
	if err != nil {
		t.Fatalf("MarshalCheckpoint() error = %v", err)
	}
	loaded, err := backup.LoadCheckpoint(context.Background(), body, signer)
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	if loaded.ID != checkpoint.ID || len(loaded.Slots) != 2 ||
		loaded.EffectiveAtUnixMillis != checkpoint.EffectiveAtUnixMillis {
		t.Fatalf("checkpoint round trip = %#v", loaded)
	}

	missing := checkpoint
	missing.Slots = missing.Slots[:1]
	if _, err := backup.SignCheckpoint(context.Background(), missing, signer, "signing-key"); err == nil {
		t.Fatal("SignCheckpoint(missing Slot) error = nil")
	}
	duplicate := checkpoint
	duplicate.Slots[1].HashSlot = 0
	if _, err := backup.SignCheckpoint(context.Background(), duplicate, signer, "signing-key"); err == nil {
		t.Fatal("SignCheckpoint(duplicate Slot) error = nil")
	}
}

func TestCheckpointAcceptsFullyReconciledEmptySlotStreams(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	checkpoint := validCheckpointArtifact(1)
	checkpoint.Slots[0].Metadata = backup.CheckpointStream{
		SourceHighWatermark: 7, WatermarkAtUnixMillis: checkpoint.Slots[0].WatermarkAtUnixMillis,
	}
	checkpoint.Slots[0].Messages = backup.CheckpointStream{
		WatermarkAtUnixMillis: checkpoint.Slots[0].WatermarkAtUnixMillis,
	}

	if _, err := backup.SignCheckpoint(
		context.Background(), checkpoint,
		ed25519ManifestSigner{privateKey: privateKey}, "signing-key",
	); err != nil {
		t.Fatalf("SignCheckpoint(empty streams) error = %v", err)
	}
}

func TestCatalogPageRoundTripAuthenticatesHashLink(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer := ed25519ManifestSigner{privateKey: privateKey}
	entry := backup.CatalogCheckpointReference{
		ID: "checkpoint-2", Key: backup.CheckpointObjectKey("checkpoint-2"),
		SHA256: strings.Repeat("a", 64), Bytes: 1234,
		CreatedAtUnixMillis:   1_753_400_200_000,
		EffectiveAtUnixMillis: 1_753_400_100_000,
	}
	page, err := backup.SignCatalogPage(context.Background(), backup.CatalogPage{
		Format: backup.CatalogPageFormat, Version: backup.CatalogPageVersion,
		Sequence: 2, CreatedAtUnixMillis: 1_753_400_200_000,
		Previous: &backup.CatalogPageReference{
			Sequence: 1, Key: backup.CatalogPageObjectKey(1, "checkpoint-1"),
			SHA256: strings.Repeat("b", 64), Bytes: 1200,
			LatestCheckpointID: "checkpoint-1",
		},
		Entries: []backup.CatalogCheckpointReference{entry},
	}, signer, "signing-key")
	if err != nil {
		t.Fatalf("SignCatalogPage() error = %v", err)
	}
	body, err := backup.MarshalCatalogPage(page)
	if err != nil {
		t.Fatalf("MarshalCatalogPage() error = %v", err)
	}
	loaded, err := backup.LoadCatalogPage(context.Background(), body, signer)
	if err != nil {
		t.Fatalf("LoadCatalogPage() error = %v", err)
	}
	if loaded.Sequence != 2 || loaded.Previous == nil ||
		loaded.Previous.SHA256 != strings.Repeat("b", 64) ||
		loaded.Entries[0] != entry {
		t.Fatalf("catalog round trip = %#v", loaded)
	}

	page.Previous.Sequence = 2
	if _, err := backup.MarshalCatalogPage(page); err == nil {
		t.Fatal("MarshalCatalogPage(broken link) error = nil")
	}
}

func validCheckpointArtifact(hashSlotCount uint16) backup.Checkpoint {
	checkpoint := backup.Checkpoint{
		Format: backup.CheckpointFormat, Version: backup.CheckpointVersion,
		ID: "checkpoint-1", RepositoryID: "repository-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", HashSlotCount: hashSlotCount,
		CreatedAtUnixMillis: 1_753_400_200_000,
		Slots:               make([]backup.CheckpointSlot, hashSlotCount),
	}
	for hashSlot := uint16(0); hashSlot < hashSlotCount; hashSlot++ {
		watermark := int64(1_753_400_100_000) + int64(hashSlot)
		checkpoint.Slots[hashSlot] = backup.CheckpointSlot{
			HashSlot: hashSlot, Generation: "slot-generation-1",
			Metadata: backup.CheckpointStream{
				Sequence: 1, Head: checkpointSegmentReference("a"),
				SourceHighWatermark: 10, WatermarkAtUnixMillis: watermark,
			},
			Messages: backup.CheckpointStream{
				Sequence: 1, Head: checkpointSegmentReference("b"),
				CursorHead:          checkpointSegmentReference("c"),
				SourceHighWatermark: 20, WatermarkAtUnixMillis: watermark,
			},
			WatermarkAtUnixMillis: watermark,
		}
	}
	checkpoint.EffectiveAtUnixMillis = checkpoint.Slots[0].WatermarkAtUnixMillis
	return checkpoint
}

func checkpointSegmentReference(char string) *backup.SegmentReference {
	return &backup.SegmentReference{
		SegmentID:      strings.Repeat(char, 64),
		CommitKey:      "segments/" + strings.Repeat(char, 64) + "/commit.json",
		CommitSHA256:   strings.Repeat("d", 64),
		PlaintextBytes: 1,
	}
}
