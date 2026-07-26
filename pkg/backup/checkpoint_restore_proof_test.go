package backup_test

import (
	"strings"
	"testing"

	backup "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestValidateCheckpointCatalogProofRejectsPageOutsidePinnedHead(t *testing.T) {
	checkpointID := "checkpoint-1"
	vectorID := strings.Repeat("a", 64)
	checkpoint := backup.CatalogCheckpointReference{
		ID: checkpointID, Key: backup.CheckpointObjectKey(checkpointID),
		SHA256: strings.Repeat("b", 64), Bytes: 10,
		CreatedAtUnixMillis: 1710000001000, EffectiveAtUnixMillis: 1710000000000,
		GenerationVector: backup.GenerationVectorReference{
			ID: vectorID, Key: backup.GenerationVectorObjectKey(vectorID),
			SHA256: strings.Repeat("c", 64), Bytes: 10, HashSlotCount: 1,
		},
	}
	proof := backup.CheckpointCatalogProof{
		Head: backup.CatalogPageReference{
			Sequence: 3, Key: backup.CatalogPageObjectKey(3, "checkpoint-3"),
			SHA256: strings.Repeat("d", 64), Bytes: 10, LatestCheckpointID: "checkpoint-3",
		},
		EntryPage: backup.CatalogPageReference{
			Sequence: 2, Key: backup.CatalogPageObjectKey(2, checkpointID),
			SHA256: strings.Repeat("e", 64), Bytes: 10, LatestCheckpointID: checkpointID,
		},
		Checkpoint: checkpoint,
	}
	if err := backup.ValidateCheckpointCatalogProof(proof); err != nil {
		t.Fatal(err)
	}
	proof.EntryPage.Sequence = 4
	proof.EntryPage.Key = backup.CatalogPageObjectKey(4, checkpointID)
	if err := backup.ValidateCheckpointCatalogProof(proof); err == nil {
		t.Fatal("ValidateCheckpointCatalogProof(page after head) error = nil")
	}
}
