package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestCheckpointRestoreStagingQuotaScavengesOrphansAndReservesNodeBytes(
	t *testing.T,
) {
	root := t.TempDir()
	requireWriteRestoreQuotaFile(t,
		filepath.Join(root, "checkpoint-segment-crash.stage"), 700)
	requireWriteRestoreQuotaFile(t,
		filepath.Join(root, "checkpoint-target", "receipt.json"), 400)

	quota, err := NewCheckpointRestoreStagingQuota(root, 500)
	if err != nil {
		t.Fatalf("NewCheckpointRestoreStagingQuota() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(
		root, "checkpoint-segment-crash.stage",
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan stage stat error = %v, want not exist", err)
	}
	overPath := filepath.Join(quota.root, "checkpoint-segments", "over.stage")
	if err := quota.reserveClaim(overPath, overPath, 101); !errors.Is(
		err, backupartifact.ErrInvalidObject,
	) {
		t.Fatalf("claim over quota error = %v", err)
	}
	activePath := filepath.Join(
		quota.root, "checkpoint-segments", "active.stage",
	)
	if err := quota.reserveClaim(activePath, activePath, 100); err != nil {
		t.Fatalf("claim exact remainder error = %v", err)
	}
	requireWriteRestoreQuotaFile(t, activePath, 100)
	if err := quota.settleClaim(activePath); err != nil {
		t.Fatalf("settle exact remainder claim error = %v", err)
	}
	if err := quota.validate(); err != nil {
		t.Fatalf("validate exact quota error = %v", err)
	}
}

func TestCheckpointRestoreStagingQuotaCoordinatesChildRoots(t *testing.T) {
	root := t.TempDir()
	requireWriteRestoreQuotaFile(t,
		filepath.Join(root, "checkpoint-target", "snapshot"), 600)
	quota, err := NewCheckpointRestoreStagingQuota(root, 1024)
	if err != nil {
		t.Fatalf("NewCheckpointRestoreStagingQuota() error = %v", err)
	}
	firstPath := filepath.Join(quota.root, "slot-00001", "attempt")
	if err := quota.reserveClaim(firstPath, firstPath, 424); err != nil {
		t.Fatalf("claim remaining node bytes error = %v", err)
	}
	secondPath := filepath.Join(quota.root, "slot-00002", "attempt")
	if err := quota.reserveClaim(secondPath, secondPath, 1); !errors.Is(
		err, backupartifact.ErrInvalidObject,
	) {
		t.Fatalf("concurrent claim error = %v", err)
	}
	if err := quota.settleClaim(firstPath); err != nil {
		t.Fatalf("settle remaining node claim error = %v", err)
	}
}

func TestCheckpointRestoreReplicaBeginClaimFailureLeavesNoRestartMarker(
	t *testing.T,
) {
	root := t.TempDir()
	const maxBytes = uint64(5 << 20)
	requireWriteRestoreQuotaFile(
		t, filepath.Join(root, "occupied.bin"), 2<<20,
	)
	quota, err := NewCheckpointRestoreStagingQuota(root, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	fence := CheckpointRestoreInstallFence{
		PlanID: "claim-failure-plan", CheckpointID: "checkpoint",
		CheckpointSHA256: strings.Repeat("a", 64),
		TargetGeneration: "target-generation",
		HashSlot:         0, TargetSlotID: 7, ReplicaCount: 3,
		LeaderNodeID: 2, LeaderTerm: 3, ConfigEpoch: 4, Attempt: 1,
	}
	request := backupcontract.CheckpointReplicaRequest{
		Fence: checkpointRestoreFenceToContract(fence),
		Files: []backupcontract.CheckpointReplicaFile{
			{
				Kind: backupcontract.CheckpointReplicaMetadata,
				Size: 1, SHA256: strings.Repeat("b", 64),
			},
			{
				Kind: backupcontract.CheckpointReplicaErasures,
				Size: 1, SHA256: strings.Repeat("c", 64),
			},
		},
		Evidence: backupartifact.RestoreEvidence{
			Version:             backupartifact.RestoreEvidenceVersion,
			ContentSHA256:       strings.Repeat("d", 64),
			MessageMerkleSHA256: strings.Repeat("e", 64),
		},
		InstalledAtUnixMillis: 1,
	}
	receiver := &CheckpointRestoreReplicaReceiver{
		stagingDir: quota.root, stagingMaxBytes: maxBytes,
		stagingQuota: quota, reservations: make(map[string]uint64),
	}
	_, err = receiver.begin(context.Background(), fence, request)
	if !errors.Is(err, backupartifact.ErrInvalidObject) {
		t.Fatalf("begin over quota error = %v", err)
	}
	attemptDir := checkpointRestoreAttemptDir(quota.root, fence)
	if _, err := os.Stat(attemptDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attempt directory stat error = %v, want not exist", err)
	}

	restartedQuota, err := NewCheckpointRestoreStagingQuota(root, maxBytes)
	if err != nil {
		t.Fatalf("restart quota error = %v", err)
	}
	restarted := &CheckpointRestoreReplicaReceiver{
		stagingDir: restartedQuota.root, stagingMaxBytes: maxBytes,
		stagingQuota: restartedQuota, reservations: make(map[string]uint64),
	}
	if err := restarted.rehydrateReplicaReservations(); err != nil {
		t.Fatalf("rehydrate after rejected begin error = %v", err)
	}
	if len(restarted.reservations) != 0 {
		t.Fatalf(
			"rehydrated reservations = %v, want none",
			restarted.reservations,
		)
	}
}

func TestCheckpointRestoreReplicaReservationRehydratesFullAttemptCapacity(
	t *testing.T,
) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	attemptDir := filepath.Join(resolvedRoot, "slot-00000", "attempt")
	if err := os.MkdirAll(attemptDir, 0o750); err != nil {
		t.Fatal(err)
	}
	files := []backupcontract.CheckpointReplicaFile{
		{
			Kind:   backupcontract.CheckpointReplicaMetadata,
			Size:   100,
			SHA256: strings.Repeat("a", 64),
		},
		{
			Kind:    backupcontract.CheckpointReplicaMessages,
			Ordinal: 0, Size: 100,
			SHA256: strings.Repeat("b", 64),
		},
		{
			Kind:   backupcontract.CheckpointReplicaErasures,
			Size:   100,
			SHA256: strings.Repeat("c", 64),
		},
	}
	transfer := checkpointRestoreReplicaTransfer{
		Files: files,
		Evidence: backupartifact.RestoreEvidence{
			Version:              backupartifact.RestoreEvidenceVersion,
			ChannelBoundaryCount: 2,
		},
	}
	if err := writeCheckpointRestoreReplicaTransfer(
		attemptDir, transfer,
	); err != nil {
		t.Fatal(err)
	}
	requireWriteRestoreQuotaFile(
		t, checkpointRestoreReplicaPartPath(attemptDir, files[0]), 40,
	)
	const maxBytes = uint64(8 << 20)
	quota, err := NewCheckpointRestoreStagingQuota(root, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	receiver := &CheckpointRestoreReplicaReceiver{
		stagingDir: quota.root, stagingMaxBytes: maxBytes,
		stagingQuota: quota, reservations: make(map[string]uint64),
	}
	if err := receiver.rehydrateReplicaReservations(); err != nil {
		t.Fatal(err)
	}
	capacity, err := checkpointRestoreReplicaAttemptCapacity(
		300, transfer.Evidence,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := capacity
	if got := receiver.reservations[attemptDir]; got != want {
		t.Fatalf("rehydrated reservation = %d, want %d", got, want)
	}
	if got := quota.claims[attemptDir].capacity; got != want {
		t.Fatalf("quota claim = %d, want %d", got, want)
	}
}

func TestCheckpointRestoreReplicaReservationSettlesAfterCompletion(
	t *testing.T,
) {
	root := t.TempDir()
	quota, err := NewCheckpointRestoreStagingQuota(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	attemptDir := filepath.Join(quota.root, "attempt")
	if err := quota.reserveClaim(attemptDir, attemptDir, 300); err != nil {
		t.Fatal(err)
	}
	receiver := &CheckpointRestoreReplicaReceiver{
		stagingQuota: quota,
		reservations: map[string]uint64{attemptDir: 300},
	}
	requireWriteRestoreQuotaFile(
		t, filepath.Join(attemptDir, "replica-metadata.snapshot.part"), 100,
	)
	requireWriteRestoreQuotaFile(
		t, filepath.Join(attemptDir, "replica-boundaries", "index"), 50,
	)
	if err := receiver.settleReplicaReservation(attemptDir); err != nil {
		t.Fatal(err)
	}
	if _, found := quota.claims[attemptDir]; found {
		t.Fatal("settled quota claim still exists")
	}
	nextPath := filepath.Join(quota.root, "next-attempt")
	if err := quota.reserveClaim(
		nextPath, nextPath, 1024-150,
	); err != nil {
		t.Fatalf("claim remaining capacity error = %v", err)
	}
}

func TestCheckpointRestoreStagingClaimSurvivesConcurrentRefreshDuringWrite(
	t *testing.T,
) {
	root := t.TempDir()
	quota, err := NewCheckpointRestoreStagingQuota(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(quota.root, "checkpoint-segment-active.stage")
	if err := quota.reserveClaim(path, path, 512); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(
		path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(make([]byte, 256)); err != nil {
		t.Fatal(err)
	}
	if err := quota.validate(); err != nil {
		t.Fatalf("validate during claimed write error = %v", err)
	}
	if _, err := file.Write(make([]byte, 256)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := quota.settleClaim(path); err != nil {
		t.Fatal(err)
	}
	if quota.used != 512 {
		t.Fatalf("settled used bytes = %d, want 512", quota.used)
	}
	nextPath := filepath.Join(quota.root, "checkpoint-segment-next.stage")
	if err := quota.reserveClaim(nextPath, nextPath, 512); err != nil {
		t.Fatalf("claim remaining bytes error = %v", err)
	}
}

func TestCheckpointRestoreClaimAdmissionAvoidsRootRescan(t *testing.T) {
	quota, err := NewCheckpointRestoreStagingQuota(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	var rootScans int
	quota.sizePath = func(path string, exclude string) (uint64, error) {
		if filepath.Clean(path) == quota.root {
			rootScans++
		}
		return checkpointRestoreStagingBytes(path, exclude)
	}
	path := filepath.Join(quota.root, "checkpoint-segment-fast.stage")
	if err := quota.reserveClaim(path, path, 512); err != nil {
		t.Fatal(err)
	}
	requireWriteRestoreQuotaFile(t, path, 256)
	if err := quota.settleClaim(path); err != nil {
		t.Fatal(err)
	}
	if rootScans != 0 {
		t.Fatalf("normal claim root scans = %d, want 0", rootScans)
	}
	if err := quota.validate(); err != nil {
		t.Fatal(err)
	}
	if rootScans != 1 {
		t.Fatalf("explicit validation root scans = %d, want 1", rootScans)
	}
}

func TestCheckpointRestoreCommittedRemovalKeepsCachedUsageExact(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "checkpoint-segment-settled.stage")
	requireWriteRestoreQuotaFile(t, path, 256)
	quota, err := NewCheckpointRestoreStagingQuota(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(quota.root, "checkpoint-segment-settled.stage")
	if err := quota.removeCommittedPath(path, 256); err != nil {
		t.Fatal(err)
	}
	nextPath := filepath.Join(quota.root, "checkpoint-segment-next.stage")
	if err := quota.reserveClaim(nextPath, nextPath, 1024); err != nil {
		t.Fatalf("claim full capacity after committed removal error = %v", err)
	}
}

func TestCheckpointRestoreEvidenceReadOnlyOpenDoesNotGrowStaging(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "evidence")
	index, err := openCheckpointRestoreEvidenceIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.ObserveCursor(backupartifact.ChannelBoundary{
		ChannelID: "room", ChannelType: 1, Epoch: 1, HW: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := checkpointRestoreStagingBytes(path, "")
	if err != nil {
		t.Fatal(err)
	}
	readOnly, err := openCheckpointRestoreEvidenceIndexReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	var visited int
	if err := readOnly.VisitBoundaries(
		func(backupartifact.ChannelBoundary) error {
			visited++
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := checkpointRestoreStagingBytes(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if visited != 1 || after != before {
		t.Fatalf(
			"read-only evidence = (visited %d, bytes %d), want (1, %d)",
			visited, after, before,
		)
	}
}

func TestCheckpointRestoreReceiptClaimCoversSiblingTemporaryFile(
	t *testing.T,
) {
	root := t.TempDir()
	attemptDir := filepath.Join(root, "attempt")
	receiptPath := filepath.Join(attemptDir, "receipt.json")
	requireWriteRestoreQuotaFile(t, receiptPath, 100)
	quota, err := NewCheckpointRestoreStagingQuota(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	attemptDir = filepath.Join(quota.root, "attempt")
	receiptPath = filepath.Join(attemptDir, "receipt.json")
	claim := receiptPath + "#test"
	if err := reserveCheckpointRestoreReceiptClaim(
		quota, attemptDir, claim, 256,
	); err != nil {
		t.Fatal(err)
	}
	requireWriteRestoreQuotaFile(
		t, filepath.Join(attemptDir, ".receipt-test.tmp"), 200,
	)
	if err := quota.validate(); err != nil {
		t.Fatalf("validate receipt temporary file error = %v", err)
	}
	if err := quota.settleClaim(claim); err != nil {
		t.Fatalf("settle receipt claim error = %v", err)
	}
}

func TestCheckpointRestoreQuotaSharesAttemptLockAcrossComponents(
	t *testing.T,
) {
	quota, err := NewCheckpointRestoreStagingQuota(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	attemptDir := filepath.Join(quota.root, "attempt")
	unlockTarget := quota.attemptLocks.lock(attemptDir)
	receiverEntered := make(chan struct{})
	receiverDone := make(chan struct{})
	go func() {
		unlockReceiver := quota.attemptLocks.lock(attemptDir)
		close(receiverEntered)
		unlockReceiver()
		close(receiverDone)
	}()
	select {
	case <-receiverEntered:
		t.Fatal("receiver entered the same attempt while target held its lock")
	case <-time.After(20 * time.Millisecond):
	}
	otherEntered := make(chan struct{})
	go func() {
		unlockOther := quota.attemptLocks.lock(
			filepath.Join(quota.root, "other-attempt"),
		)
		close(otherEntered)
		unlockOther()
	}()
	select {
	case <-otherEntered:
	case <-time.After(time.Second):
		t.Fatal("independent Slot attempt was serialized")
	}
	unlockTarget()
	select {
	case <-receiverDone:
	case <-time.After(time.Second):
		t.Fatal("receiver did not resume after the shared attempt lock released")
	}
}

func TestCheckpointRestoreReceiverRechecksCanceledContextAfterAttemptLock(
	t *testing.T,
) {
	quota, err := NewCheckpointRestoreStagingQuota(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	fence := CheckpointRestoreInstallFence{
		PlanID: "canceled-lock-plan", CheckpointID: "checkpoint",
		CheckpointSHA256: strings.Repeat("a", 64),
		TargetGeneration: "target-generation",
		HashSlot:         0, TargetSlotID: 7, ReplicaCount: 3,
		LeaderNodeID: 2, LeaderTerm: 3, ConfigEpoch: 4, Attempt: 1,
	}
	attemptDir := checkpointRestoreAttemptDir(quota.root, fence)
	unlock := quota.attemptLocks.lock(attemptDir)
	receiver := &CheckpointRestoreReplicaReceiver{
		stagingDir: quota.root, stagingMaxBytes: 1024,
		stagingQuota: quota, reservations: make(map[string]uint64),
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := receiver.HandleCheckpointReplica(
			ctx,
			backupcontract.CheckpointReplicaRequest{
				Action: backupcontract.CheckpointReplicaStatus,
				Fence:  checkpointRestoreFenceToContract(fence),
			},
		)
		result <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	unlock()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("receiver error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("receiver did not leave canceled lock wait")
	}
}

func TestCheckpointRestoreTargetTakesOverPartialFollowerClaim(t *testing.T) {
	root := t.TempDir()
	quota, err := NewCheckpointRestoreStagingQuota(root, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	fence := CheckpointRestoreInstallFence{
		PlanID: "takeover-plan", CheckpointID: "checkpoint",
		CheckpointSHA256: strings.Repeat("d", 64),
		TargetGeneration: "target-generation",
		HashSlot:         0, TargetSlotID: 7, ReplicaCount: 3,
		LeaderNodeID: 2, LeaderTerm: 3, ConfigEpoch: 4, Attempt: 1,
	}
	attemptDir := checkpointRestoreAttemptDir(quota.root, fence)
	if err := quota.reserveClaim(
		attemptDir, attemptDir, 24<<20,
	); err != nil {
		t.Fatal(err)
	}
	requireWriteRestoreQuotaFile(
		t, filepath.Join(attemptDir, "replica-metadata.snapshot.part"), 1024,
	)
	target, err := NewDurableCheckpointRestoreTarget(
		DurableCheckpointRestoreTargetOptions{
			StagingDir: quota.root, StagingMaxBytes: 64 << 20,
			StagingQuota: quota,
			Distributor:  checkpointRestoreClaimTestDistributor{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	promoted := fence
	promoted.LeaderNodeID = 1
	promoted.LeaderTerm++
	promoted.Attempt++
	session, err := target.BeginCheckpointRestore(
		context.Background(), promoted, 16<<20,
	)
	if err != nil {
		t.Fatalf("promoted BeginCheckpointRestore() error = %v", err)
	}
	if err := session.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type checkpointRestoreClaimTestDistributor struct{}

func (checkpointRestoreClaimTestDistributor) DistributeCheckpointRestoreSnapshot(
	context.Context,
	CheckpointRestoreInstallFence,
	CheckpointRestoreSnapshot,
) (CheckpointRestoreReplicaResult, error) {
	return CheckpointRestoreReplicaResult{}, nil
}

func requireWriteRestoreQuotaFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
