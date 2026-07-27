//go:build e2e

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestBackupE2ERemoteLatencyIsBounded(t *testing.T) {
	t.Setenv(backupE2ERemoteLatencyEnv, "250ms")
	got, err := backupE2ERemoteLatency()
	if err != nil {
		t.Fatalf("parse latency: %v", err)
	}
	if got != 250*time.Millisecond {
		t.Fatalf("latency = %s, want 250ms", got)
	}

	t.Setenv(backupE2ERemoteLatencyEnv, "31s")
	if _, err := backupE2ERemoteLatency(); err == nil {
		t.Fatal("latency above the e2e bound was accepted")
	}
}

func TestWaitBackupE2ELatencyHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitBackupE2ELatency(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context.Canceled", err)
	}
}

func TestBackupE2ELatencyTriggerIsFailClosed(t *testing.T) {
	trigger := filepath.Join(t.TempDir(), "enabled")
	if got := backupE2EActiveDelay(time.Second, trigger); got != 0 {
		t.Fatalf("inactive delay = %s, want 0", got)
	}
	if err := os.WriteFile(trigger, []byte("enabled"), 0o600); err != nil {
		t.Fatalf("write trigger: %v", err)
	}
	if got := backupE2EActiveDelay(time.Second, trigger); got != time.Second {
		t.Fatalf("active delay = %s, want 1s", got)
	}
}

func TestBackupE2ECorruptionTriggerIsScopedAndBounded(t *testing.T) {
	root := t.TempDir()
	repository, err := backupinfra.NewFileRepository(
		"primary", filepath.Join(root, "repository"),
	)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	faultDir := filepath.Join(root, "faults")
	if err := os.MkdirAll(faultDir, 0o700); err != nil {
		t.Fatalf("make fault directory: %v", err)
	}
	wrapped := &backupE2EDelayedRepository{
		appBackupRepository: repository,
		corruptionDir:       faultDir,
	}
	trigger := filepath.Join(faultDir, "primary.corrupt")
	if err := os.WriteFile(trigger, []byte("once"), 0o600); err != nil {
		t.Fatalf("write one-shot trigger: %v", err)
	}
	const segmentKey = "segments/id/payloads/digest.bin"
	if wrapped.consumeCorruptionTrigger(
		context.Background(), "catalog/page.json",
	) {
		t.Fatal("corruption trigger applied to a non-segment object")
	}
	if !wrapped.consumeCorruptionTrigger(context.Background(), segmentKey) {
		t.Fatal("one-shot segment corruption did not activate")
	}
	if wrapped.consumeCorruptionTrigger(context.Background(), segmentKey) {
		t.Fatal("one-shot segment corruption activated twice")
	}

	if err := os.WriteFile(trigger, []byte("persistent"), 0o600); err != nil {
		t.Fatalf("write persistent trigger: %v", err)
	}
	if !wrapped.consumeCorruptionTrigger(context.Background(), segmentKey) ||
		!wrapped.consumeCorruptionTrigger(context.Background(), segmentKey) {
		t.Fatal("persistent segment corruption was not retained")
	}

	if err := os.WriteFile(trigger, []byte("sticky"), 0o600); err != nil {
		t.Fatalf("write sticky trigger: %v", err)
	}
	stickyKey := filepath.Join(faultDir, "sticky.key")
	if err := os.Remove(stickyKey); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clear sticky key: %v", err)
	}
	if !wrapped.consumeCorruptionTrigger(context.Background(), segmentKey) {
		t.Fatal("sticky segment corruption did not select its first key")
	}
	if wrapped.consumeCorruptionTrigger(
		context.Background(), "segments/other/payloads/digest.bin",
	) {
		t.Fatal("sticky segment corruption spread to a second key")
	}
	if !wrapped.consumeCorruptionTrigger(context.Background(), segmentKey) {
		t.Fatal("sticky segment corruption did not retain its selected key")
	}
}

func TestBackupE2ECorruptionTriggerSelectsExactSegment(t *testing.T) {
	root := t.TempDir()
	repository, err := backupinfra.NewFileRepository(
		"primary", filepath.Join(root, "repository"),
	)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	faultDir := filepath.Join(root, "faults")
	if err := os.MkdirAll(faultDir, 0o700); err != nil {
		t.Fatalf("make fault directory: %v", err)
	}
	wrapped := &backupE2EDelayedRepository{
		appBackupRepository: repository,
		corruptionDir:       faultDir,
	}
	putCommit := func(
		segmentID string,
		hashSlot uint16,
		stream backupartifact.SegmentStream,
		sourceHighWatermark uint64,
	) string {
		t.Helper()
		body, marshalErr := json.Marshal(backupartifact.SegmentCommit{
			SegmentID: segmentID,
			Header: backupartifact.SegmentHeader{
				Logical: backupartifact.SegmentLogicalDescriptor{
					HashSlot: hashSlot,
					Stream:   stream,
				},
				SourceHighWatermark: sourceHighWatermark,
			},
		})
		if marshalErr != nil {
			t.Fatalf("marshal segment commit: %v", marshalErr)
		}
		digest := sha256.Sum256(body)
		if putErr := repository.PutImmutable(
			context.Background(),
			"segments/"+segmentID+"/commit.json",
			int64(len(body)), hex.EncodeToString(digest[:]),
			bytes.NewReader(body),
		); putErr != nil {
			t.Fatalf("put segment commit: %v", putErr)
		}
		return "segments/" + segmentID + "/payloads/digest.bin"
	}
	targetKey := putCommit(
		strings.Repeat("7", 64), 7,
		backupartifact.SegmentStreamMessages, 11,
	)
	staleSlotKey := putCommit(
		strings.Repeat("6", 64), 7,
		backupartifact.SegmentStreamMessages, 10,
	)
	otherStreamKey := putCommit(
		strings.Repeat("5", 64), 7,
		backupartifact.SegmentStreamMetadata, 11,
	)
	otherSlotKey := putCommit(
		strings.Repeat("8", 64), 8,
		backupartifact.SegmentStreamMessages, 11,
	)
	if err := os.WriteFile(
		filepath.Join(faultDir, "sticky.key"),
		[]byte(otherSlotKey), 0o600,
	); err != nil {
		t.Fatalf("write stale generic sticky selection: %v", err)
	}
	trigger := filepath.Join(faultDir, "primary.corrupt")
	if err := os.WriteFile(
		trigger, []byte("sticky-segment:7:messages:11"), 0o600,
	); err != nil {
		t.Fatalf("write exact-segment trigger: %v", err)
	}
	if wrapped.consumeCorruptionTrigger(
		context.Background(), otherSlotKey,
	) {
		t.Fatal("exact-segment trigger selected a different Hash Slot")
	}
	if wrapped.consumeCorruptionTrigger(
		context.Background(), staleSlotKey,
	) {
		t.Fatal("exact-segment trigger selected a stale segment in the same Hash Slot")
	}
	if wrapped.consumeCorruptionTrigger(
		context.Background(), otherStreamKey,
	) {
		t.Fatal("exact-segment trigger selected a different stream")
	}
	if !wrapped.consumeCorruptionTrigger(
		context.Background(), targetKey,
	) {
		t.Fatal("exact-segment trigger did not select the requested segment")
	}
	target, ok := parseBackupE2EStickySegmentTarget(
		"sticky-segment:7:messages:11",
	)
	if !ok {
		t.Fatal("parse exact-segment target")
	}
	selected, err := os.ReadFile(
		backupE2EStickySegmentKey(faultDir, target),
	)
	if err != nil {
		t.Fatalf("read exact-segment sticky selection: %v", err)
	}
	if string(selected) != targetKey {
		t.Fatalf(
			"exact-segment sticky selection = %q, want %q",
			selected, targetKey,
		)
	}
}

func TestConsumeBackupE2EStickyKeyRecoversEmptySelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sticky.key")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write interrupted sticky selection: %v", err)
	}
	const key = "segments/id/payloads/digest.bin"
	if !consumeBackupE2EStickyKey(path, key) {
		t.Fatal("empty sticky selection was not recovered")
	}
	selected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recovered sticky selection: %v", err)
	}
	if string(selected) != key {
		t.Fatalf("recovered sticky selection = %q, want %q", selected, key)
	}
}

func TestBackupE2ERepairClearsStickyCorruption(t *testing.T) {
	root := t.TempDir()
	repository, err := backupinfra.NewFileRepository(
		"primary", filepath.Join(root, "repository"),
	)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	faultDir := filepath.Join(root, "faults")
	if err := os.MkdirAll(faultDir, 0o700); err != nil {
		t.Fatalf("make fault directory: %v", err)
	}
	trigger := filepath.Join(faultDir, "primary.corrupt")
	sticky := filepath.Join(faultDir, "sticky.key")
	if err := os.WriteFile(trigger, []byte("sticky"), 0o600); err != nil {
		t.Fatalf("write corruption trigger: %v", err)
	}
	if err := os.WriteFile(
		sticky, []byte("segments/id/payloads/digest.bin"), 0o600,
	); err != nil {
		t.Fatalf("write sticky key: %v", err)
	}

	body := []byte("healthy payload")
	digest := sha256.Sum256(body)
	repair := &backupE2ERepairRepository{
		RepairRepository: repository,
		trigger:          trigger,
		sticky:           sticky,
	}
	if err := repair.RepairImmutable(
		context.Background(),
		"segments/id/payloads/digest.bin",
		int64(len(body)),
		hex.EncodeToString(digest[:]),
		bytes.NewReader(body),
	); err != nil {
		t.Fatalf("repair immutable: %v", err)
	}
	for _, path := range []string{trigger, sticky} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("corruption marker %q remains after repair: %v", path, err)
		}
	}
}

func TestBackupE2ERepairRetainsStickySelectionDuringDualCorruption(t *testing.T) {
	root := t.TempDir()
	repository, err := backupinfra.NewFileRepository(
		"primary", filepath.Join(root, "repository"),
	)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	faultDir := filepath.Join(root, "faults")
	if err := os.MkdirAll(faultDir, 0o700); err != nil {
		t.Fatalf("make fault directory: %v", err)
	}
	trigger := filepath.Join(faultDir, "primary.corrupt")
	allTrigger := filepath.Join(faultDir, "all.corrupt")
	sticky := filepath.Join(faultDir, "sticky.key")
	for path, body := range map[string][]byte{
		trigger:    []byte("sticky"),
		allTrigger: []byte("sticky"),
		sticky:     []byte("segments/id/payloads/digest.bin"),
	} {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatalf("write fault marker %q: %v", path, err)
		}
	}

	body := []byte("healthy payload")
	digest := sha256.Sum256(body)
	repair := &backupE2ERepairRepository{
		RepairRepository: repository,
		trigger:          trigger,
		sticky:           sticky,
	}
	if err := repair.RepairImmutable(
		context.Background(),
		"segments/id/payloads/digest.bin",
		int64(len(body)),
		hex.EncodeToString(digest[:]),
		bytes.NewReader(body),
	); err != nil {
		t.Fatalf("repair immutable: %v", err)
	}
	if _, err := os.Stat(trigger); !os.IsNotExist(err) {
		t.Fatalf("repository trigger remains after repair: %v", err)
	}
	for _, path := range []string{allTrigger, sticky} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dual-corruption marker %q was cleared: %v", path, err)
		}
	}
}

func TestBackupE2ERepositoryPreservesErasureLedgerListing(t *testing.T) {
	repository, err := backupinfra.NewFileRepository(
		"primary", filepath.Join(t.TempDir(), "repository"),
	)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	wrapped := &backupE2EDelayedRepository{
		appBackupRepository: repository,
	}
	var _ backupinfra.ErasureLedgerCommitLister = wrapped
	keys, err := wrapped.ListErasureLedgerCommitKeys(
		context.Background(),
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	)
	if err != nil {
		t.Fatalf("list erasure ledger commits: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("commit keys = %v, want empty", keys)
	}
}

func TestBackupE2ESourcePinPressureTargetsOneHashSlot(t *testing.T) {
	trigger := filepath.Join(t.TempDir(), "pin-pressure")
	manager := &backupE2ESourcePinManager{
		SourcePinManager: backupE2EPinManagerStub{
			observation: runtimebackup.SourcePinObservation{
				Age: 5 * time.Minute,
			},
		},
		trigger: trigger,
	}
	if err := os.WriteFile(trigger, []byte("7"), 0o600); err != nil {
		t.Fatalf("write pin trigger: %v", err)
	}
	other, err := manager.Observe(
		context.Background(), 6,
		backupcontract.SlotCaptureLease{},
		backupcontract.SlotFrontier{},
	)
	if err != nil {
		t.Fatalf("observe other Hash Slot: %v", err)
	}
	if other.Age != 5*time.Minute {
		t.Fatalf("other Hash Slot age = %s, want 5m", other.Age)
	}
	target, err := manager.Observe(
		context.Background(), 7,
		backupcontract.SlotCaptureLease{},
		backupcontract.SlotFrontier{},
	)
	if err != nil {
		t.Fatalf("observe target Hash Slot: %v", err)
	}
	if target.Age != 2*time.Hour {
		t.Fatalf("target Hash Slot age = %s, want 2h", target.Age)
	}
}

type backupE2EPinManagerStub struct {
	observation runtimebackup.SourcePinObservation
}

func (s backupE2EPinManagerStub) Observe(
	context.Context,
	uint16,
	backupcontract.SlotCaptureLease,
	backupcontract.SlotFrontier,
) (runtimebackup.SourcePinObservation, error) {
	return s.observation, nil
}

func (s backupE2EPinManagerStub) Release(
	context.Context,
	uint16,
	backupcontract.SlotCaptureLease,
) (runtimebackup.SourcePinObservation, error) {
	return s.observation, nil
}

func (s backupE2EPinManagerStub) AdoptLease(
	context.Context,
	uint16,
	backupcontract.SlotCaptureLease,
) (runtimebackup.SourcePinObservation, error) {
	return s.observation, nil
}

func (s backupE2EPinManagerStub) ReleaseObsolete(
	context.Context,
	uint16,
) (runtimebackup.SourcePinObservation, error) {
	return s.observation, nil
}
