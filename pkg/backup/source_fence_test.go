package backup_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	backup "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestSourceFenceReceiptAuthenticatesExactRestoreBinding(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey(): %v", err)
	}
	signer := ed25519ManifestSigner{privateKey: privateKey}
	record := backup.SourceFenceRecord{
		Format:                  backup.SourceFenceReceiptFormat,
		Version:                 backup.SourceFenceReceiptVersion,
		ID:                      "source-fence-1",
		SourceClusterID:         "source-cluster",
		SourceGeneration:        "source-generation",
		RestorePlanID:           "restore-plan-1",
		CheckpointID:            "checkpoint-1",
		CheckpointSHA256:        strings.Repeat("a", 64),
		TargetClusterID:         "target-cluster",
		TargetGeneration:        "target-generation",
		FenceControllerRevision: 42,
		RequestedAtUnixMillis:   1710000000000,
		ConvergedAtUnixMillis:   1710000001000,
	}
	receipt, err := backup.SignSourceFenceReceipt(
		context.Background(), record, signer, "source-signing-key",
	)
	if err != nil {
		t.Fatalf("SignSourceFenceReceipt(): %v", err)
	}
	body, err := backup.MarshalSourceFenceReceipt(receipt)
	if err != nil {
		t.Fatalf("MarshalSourceFenceReceipt(): %v", err)
	}
	loaded, err := backup.LoadSourceFenceReceipt(
		context.Background(), body, signer,
	)
	if err != nil {
		t.Fatalf("LoadSourceFenceReceipt(): %v", err)
	}
	if loaded.RestorePlanID != record.RestorePlanID ||
		loaded.TargetGeneration != record.TargetGeneration {
		t.Fatalf("loaded receipt = %+v", loaded)
	}
	firstDigest, err := backup.SourceFenceReceiptDigest(receipt)
	if err != nil {
		t.Fatalf("SourceFenceReceiptDigest(): %v", err)
	}
	secondDigest, err := backup.SourceFenceReceiptDigest(loaded)
	if err != nil || secondDigest != firstDigest {
		t.Fatalf("receipt digest = %q, %v; want %q", secondDigest, err, firstDigest)
	}

	tampered := receipt
	tampered.TargetGeneration = "other-generation"
	if err := backup.VerifySourceFenceReceipt(
		context.Background(), tampered, signer,
	); !errors.Is(err, backup.ErrInvalidSignature) {
		t.Fatalf("VerifySourceFenceReceipt(tampered) = %v, want invalid signature", err)
	}
}

func TestSourceFenceReceiptRejectsPendingAndUnknownJSON(t *testing.T) {
	record := backup.SourceFenceRecord{
		Format:                  backup.SourceFenceReceiptFormat,
		Version:                 backup.SourceFenceReceiptVersion,
		ID:                      "source-fence-1",
		SourceClusterID:         "source-cluster",
		SourceGeneration:        "source-generation",
		RestorePlanID:           "restore-plan-1",
		CheckpointID:            "checkpoint-1",
		CheckpointSHA256:        strings.Repeat("a", 64),
		TargetClusterID:         "target-cluster",
		TargetGeneration:        "target-generation",
		FenceControllerRevision: 42,
		RequestedAtUnixMillis:   1710000000000,
	}
	if err := backup.ValidateSourceFenceRecord(record, true); err == nil {
		t.Fatal("ValidateSourceFenceRecord() accepted a pending fence as converged")
	}

	body, err := json.Marshal(map[string]any{
		"format":                      backup.SourceFenceReceiptFormat,
		"version":                     backup.SourceFenceReceiptVersion,
		"id":                          record.ID,
		"source_cluster_id":           record.SourceClusterID,
		"source_generation":           record.SourceGeneration,
		"restore_plan_id":             record.RestorePlanID,
		"checkpoint_id":               record.CheckpointID,
		"checkpoint_sha256":           record.CheckpointSHA256,
		"target_cluster_id":           record.TargetClusterID,
		"target_generation":           record.TargetGeneration,
		"fence_controller_revision":   record.FenceControllerRevision,
		"requested_at_unix_millis":    record.RequestedAtUnixMillis,
		"converged_at_unix_millis":    record.RequestedAtUnixMillis + 1,
		"signature":                   map[string]any{"algorithm": "ed25519", "key_id": "key", "value": "AA=="},
		"unreviewed_operator_payload": true,
	})
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	_, err = backup.LoadSourceFenceReceipt(
		context.Background(), body, rejectingManifestSigner{},
	)
	if err == nil {
		t.Fatal("LoadSourceFenceReceipt() accepted an unknown field")
	}
}

type rejectingManifestSigner struct{}

func (rejectingManifestSigner) Sign(
	context.Context, string, []byte,
) (backup.ManifestSignature, error) {
	return backup.ManifestSignature{}, errors.New("not supported")
}

func (rejectingManifestSigner) Verify(
	context.Context, backup.ManifestSignature, []byte,
) error {
	return errors.New("not supported")
}
