package backup_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestFenceSourcePersistsBarrierBeforeSigningAndIsIdempotent(t *testing.T) {
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer := restoreTestSigner{privateKey: key}
	store := &memoryStateStore{state: backupusecase.State{
		Revision: 11,
		CatalogHead: &backupartifact.CatalogPageReference{
			Sequence: 1, Key: "catalog/pages/00000000000000000001-checkpoint-1.json",
			SHA256: strings.Repeat("b", 64), Bytes: 100,
			LatestCheckpointID: "checkpoint-1",
		},
		CatalogRetentionRevision: 1,
	}}
	now := time.UnixMilli(1_800_000_000_000).UTC()
	convergence := &recordingSourceFenceConvergence{}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 1, Store: store,
		CatalogBrowser: sourceFenceCatalogBrowser{
			detail: backupusecase.CheckpointDetail{
				CheckpointSummary: backupusecase.CheckpointSummary{ID: "checkpoint-1"},
				CheckpointSHA256:  strings.Repeat("a", 64),
				SourceClusterID:   "source-cluster", SourceGeneration: "source-gen",
				HashSlotCount: 1,
			},
		},
		SourceClusterID: "source-cluster", SourceGeneration: "source-gen",
		SourceFenceConvergence: convergence,
		SourceFenceSigner:      signer,
		NewSourceFenceID:       func() string { return "source-fence-1" },
		Now: func() time.Time {
			current := now
			now = now.Add(time.Second)
			return current
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := backupusecase.SourceFenceRequest{
		RestorePlanID: "restore-plan-1", CheckpointID: "checkpoint-1",
		TargetClusterID: "target-cluster", TargetGeneration: "target-gen",
	}
	receipt, err := app.FenceSource(context.Background(), request)
	if err != nil {
		t.Fatalf("FenceSource(): %v", err)
	}
	if convergence.calls != 1 ||
		convergence.record.FenceControllerRevision != 12 ||
		convergence.record.ConvergedAtUnixMillis != 0 {
		t.Fatalf("convergence = calls:%d record:%+v", convergence.calls, convergence.record)
	}
	if receipt.FenceControllerRevision != 12 ||
		receipt.ConvergedAtUnixMillis == 0 ||
		receipt.Signature == nil {
		t.Fatalf("receipt = %+v", receipt)
	}
	if err := backupartifact.VerifySourceFenceReceipt(
		context.Background(), receipt, signer,
	); err != nil {
		t.Fatalf("VerifySourceFenceReceipt(): %v", err)
	}
	retry, err := app.FenceSource(context.Background(), request)
	if err != nil || retry.ID != receipt.ID || convergence.calls != 1 {
		t.Fatalf("FenceSource(retry) = %+v, %v; calls=%d", retry, err, convergence.calls)
	}
	_, err = app.FenceSource(
		context.Background(),
		backupusecase.SourceFenceRequest{
			RestorePlanID: "restore-plan-other", CheckpointID: "checkpoint-1",
			TargetClusterID: "target-cluster", TargetGeneration: "target-gen",
		},
	)
	if !errors.Is(err, backupusecase.ErrSourceFenceExists) {
		t.Fatalf("FenceSource(conflict) = %v", err)
	}
}

type sourceFenceCatalogBrowser struct {
	detail backupusecase.CheckpointDetail
}

func (b sourceFenceCatalogBrowser) List(
	context.Context,
	backupartifact.CatalogPageReference,
	backupusecase.CheckpointListRequest,
) (backupusecase.CheckpointPage, error) {
	return backupusecase.CheckpointPage{}, nil
}

func (b sourceFenceCatalogBrowser) Get(
	_ context.Context,
	_ backupartifact.CatalogPageReference,
	checkpointID string,
) (backupusecase.CheckpointDetail, error) {
	if checkpointID != b.detail.ID {
		return backupusecase.CheckpointDetail{}, backupusecase.ErrCheckpointNotFound
	}
	return b.detail, nil
}

type recordingSourceFenceConvergence struct {
	calls  int
	record backupartifact.SourceFenceRecord
}

func (c *recordingSourceFenceConvergence) WaitForSourceFence(
	_ context.Context,
	record backupartifact.SourceFenceRecord,
) error {
	c.calls++
	c.record = record
	return nil
}
