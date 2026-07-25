package backup_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestCaptureEngineAtomicallyAdvancesMetadataAndMessageStreams(t *testing.T) {
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 2, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_090_000, CutCursor: "message-cut-1"},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {{
				Records:    [][]byte{[]byte("meta-1"), []byte("meta-2")},
				NextCursor: "metadata-page-1", Done: true,
			}},
			backupartifact.SegmentStreamMessages: {{
				Records:    [][]byte{[]byte("message-1")},
				NextCursor: "message-page-1", Done: true,
				MessageCursors: []backupartifact.ChannelBoundary{
					{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 1},
				},
			}},
		},
	}
	frontiers := &fakeSlotFrontierStore{}
	segments := &recordingSegmentCommitter{}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1",
		HashSlotCount:     256, Source: source, Frontiers: frontiers, Segments: segments,
		Clock: newAdvancingCaptureClock(),
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 64 << 20, MaxSegmentBytes: 256 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}

	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	if frontiers.commits != 1 {
		t.Fatalf("frontier commits = %d, want 1 atomic commit", frontiers.commits)
	}
	if frontier.Metadata.Sequence != 1 || frontier.Metadata.Head == nil ||
		frontier.Messages.Sequence != 1 || frontier.Messages.Head == nil ||
		frontier.Messages.CursorHead == nil {
		t.Fatalf("frontier streams = %#v", frontier)
	}
	if frontier.WatermarkAtUnixMillis != 1_753_400_090_000 {
		t.Fatalf("frontier watermark = %d, want oldest stream watermark", frontier.WatermarkAtUnixMillis)
	}
	if len(segments.batches) != 2 {
		t.Fatalf("committed batches = %d, want 2", len(segments.batches))
	}
	if segments.batches[0].Stream != backupartifact.SegmentStreamMetadata ||
		segments.batches[1].Stream != backupartifact.SegmentStreamMessages {
		t.Fatalf("committed streams = %q/%q", segments.batches[0].Stream, segments.batches[1].Stream)
	}
	if len(segments.batches[1].MessageCursors) != 1 ||
		segments.batches[1].MessageCursors[0].ChannelID != "channel-a" {
		t.Fatalf("message cursor artifact = %#v", segments.batches[1].MessageCursors)
	}
	if len(segments.cursorBatches) != 1 ||
		segments.cursorBatches[0].Boundaries[0].ChannelID != "channel-a" {
		t.Fatalf("message cursor sidecars = %#v", segments.cursorBatches)
	}
}

func TestCaptureEngineRejectsCommitterWithoutCursorLoader(t *testing.T) {
	_, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 1,
		Source: &fakeContinuousSource{}, Frontiers: &fakeSlotFrontierStore{},
		Segments: commitOnlySegmentCommitter{},
	})
	if !errors.Is(err, backupruntime.ErrInvalidCapture) {
		t.Fatalf("NewCaptureEngine() error = %v, want ErrInvalidCapture", err)
	}
}

func TestCaptureEngineRollsSparseStreamAtMaxOpenDuration(t *testing.T) {
	clock := &fakeCaptureClock{now: time.UnixMilli(1_753_400_000_000)}
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{Position: 0, CommittedAtUnixMillis: 1_753_400_100_000},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {{
				Records: [][]byte{[]byte("meta-1")}, NextCursor: "metadata-page-1", Done: true,
			}},
		},
	}
	frontiers := &fakeSlotFrontierStore{}
	segments := &recordingSegmentCommitter{}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 256,
		Source: source, Frontiers: frontiers, Segments: segments, Clock: clock,
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 1 << 20, MaxSegmentBytes: 2 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}

	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	if frontier.Metadata.Sequence != 0 || len(segments.batches) != 0 {
		t.Fatalf("sparse stream sealed before max-open duration: frontier=%#v batches=%d", frontier, len(segments.batches))
	}
	clock.now = clock.now.Add(31 * time.Second)
	frontier, err = engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(after max-open) error = %v", err)
	}
	if frontier.Metadata.Sequence != 1 || frontier.Messages.Head != nil || len(segments.batches) != 1 {
		t.Fatalf("frontier after max-open = %#v batches=%d", frontier, len(segments.batches))
	}
	if source.pageCalls != 1 {
		t.Fatalf("source page calls = %d, want pending data reused without reread", source.pageCalls)
	}
}

func TestCaptureEnginePublishesSparseExactCutWhenNextObservationTrailsCommittedAccumulator(t *testing.T) {
	clock := &fakeCaptureClock{now: time.UnixMilli(1_753_400_000_000)}
	source := &statefulExactMessageSource{}
	frontiers := &fakeSlotFrontierStore{}
	segments := &recordingSegmentCommitter{}
	engine := newTestCaptureEngine(t, source, frontiers, segments, clock)

	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(first sparse cut) error = %v", err)
	}
	if frontier.Messages.Sequence != 0 || len(segments.batches) != 0 {
		t.Fatalf("sparse cut sealed before deadline: frontier=%#v batches=%d", frontier, len(segments.batches))
	}

	clock.now = clock.now.Add(31 * time.Second)
	frontier, err = engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(after sparse deadline) error = %v", err)
	}
	if frontier.Messages.Sequence != 1 || frontier.Messages.SourceHighWatermark != 1 ||
		frontiers.commits != 1 || len(segments.batches) != 1 {
		t.Fatalf("published sparse cut = %#v commits=%d batches=%d", frontier.Messages, frontiers.commits, len(segments.batches))
	}
}

func TestCaptureEngineInvalidatesTransientSourceStateAfterFrontierConflict(t *testing.T) {
	source := &statefulExactMessageSource{}
	frontiers := &fakeSlotFrontierStore{failCommits: 1}
	segments := &recordingSegmentCommitter{}
	engine := newTestCaptureEngine(t, source, frontiers, segments, nil)

	if _, err := engine.ReconcileSlot(context.Background(), 17); !errors.Is(err, backupruntime.ErrFrontierConflict) {
		t.Fatalf("ReconcileSlot(first conflict) error = %v, want ErrFrontierConflict", err)
	}
	if source.invalidations != 1 {
		t.Fatalf("source invalidations = %d, want 1", source.invalidations)
	}
	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(retry) error = %v", err)
	}
	if frontier.Messages.SourceHighWatermark != 1 || frontiers.commits != 1 ||
		source.reads != 2 || len(segments.batches) != 2 {
		t.Fatalf("retry frontier=%#v commits=%d reads=%d batches=%d", frontier.Messages, frontiers.commits, source.reads, len(segments.batches))
	}
}

func TestCaptureEngineImmediatelyContinuesPagedDiscoveryUnderRun(t *testing.T) {
	source := &pagedDiscoverySource{secondCall: make(chan struct{})}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 1,
		Source: source, Frontiers: &fakeSlotFrontierStore{}, Segments: &recordingSegmentCommitter{},
		ReconcileInterval: time.Hour, WorkerCount: 1,
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 1 << 20, MaxSegmentBytes: 2 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	select {
	case <-source.secondCall:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("paged discovery waited for the periodic ticker")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestCaptureEngineAggregatesCompletedExactMessageCutsUntilRollingDeadline(t *testing.T) {
	clock := &fakeCaptureClock{now: time.UnixMilli(1_753_400_000_000)}
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_100_000, CutCursor: "message-cut-1"},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMessages: {
				{
					Records: [][]byte{[]byte("message-1")}, NextCursor: "message-page-1", Done: true,
					MessageCursors: []backupartifact.ChannelBoundary{
						{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 1},
					},
				},
				{
					Records: [][]byte{[]byte("message-2")}, NextCursor: "message-page-2", Done: true,
					MessageCursors: []backupartifact.ChannelBoundary{
						{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 2},
					},
				},
			},
		},
	}
	frontiers := &fakeSlotFrontierStore{}
	segments := &recordingSegmentCommitter{}
	engine := newTestCaptureEngine(t, source, frontiers, segments, clock)

	if _, err := engine.ReconcileSlot(context.Background(), 17); err != nil {
		t.Fatalf("ReconcileSlot(first cut) error = %v", err)
	}
	source.watermarks.Messages = backupruntime.SourceWatermark{
		Position: 2, CommittedAtUnixMillis: 1_753_400_100_100, CutCursor: "message-cut-2",
	}
	if _, err := engine.ReconcileSlot(context.Background(), 17); err != nil {
		t.Fatalf("ReconcileSlot(new target before deadline) error = %v", err)
	}
	if source.pageCalls != 2 {
		t.Fatalf("message source pages before rolling deadline = %d, want 2 exact cuts", source.pageCalls)
	}

	clock.now = clock.now.Add(31 * time.Second)
	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(after old cut deadline) error = %v", err)
	}
	if frontier.Messages.Sequence != 1 || frontier.Messages.SourceHighWatermark != 2 ||
		frontier.Messages.WatermarkAtUnixMillis != 1_753_400_100_000 ||
		len(segments.batches) != 1 || len(segments.batches[0].Records) != 2 ||
		string(segments.batches[0].Records[0]) != "message-1" ||
		string(segments.batches[0].Records[1]) != "message-2" {
		t.Fatalf("sealed frontier=%#v batches=%#v", frontier.Messages, segments.batches)
	}
	if source.pageCalls != 2 {
		t.Fatalf("message source pages after rolling deadline = %d, want pending cuts reused", source.pageCalls)
	}
}

func TestCaptureEnginePreservesConservativeMessageTimeAcrossSegmentRolls(t *testing.T) {
	const (
		firstObservedAt  = int64(1_753_400_100_000)
		secondObservedAt = int64(1_753_400_100_100)
	)
	dataHead := validRuntimeSegmentReference("b")
	cursorHead := validRuntimeSegmentReference("d")
	frontiers := &fakeSlotFrontierStore{found: true, frontier: backupcontract.SlotFrontier{
		Revision: 1, HashSlot: 17, Generation: "slot-generation-1",
		Messages: backupcontract.StreamFrontier{
			Sequence: 1, Head: &dataHead, CursorHead: &cursorHead,
			SourceCursor: "message-old", SourceHighWatermark: 1,
			WatermarkAtUnixMillis: firstObservedAt,
		},
		WatermarkAtUnixMillis: firstObservedAt,
	}}
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{CommittedAtUnixMillis: secondObservedAt},
			Messages: backupruntime.SourceWatermark{
				Position: 2, CommittedAtUnixMillis: secondObservedAt, CutCursor: "message-cut-2",
			},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMessages: {
				{NextCursor: "message-old"},
				{
					Records: [][]byte{[]byte("message-b")}, NextCursor: "message-new",
					NextPosition: 2, Done: true,
					MessageCursors: []backupartifact.ChannelBoundary{
						{ChannelID: "channel-b", ChannelType: 2, Epoch: 1, HW: 1},
					},
				},
			},
		},
	}
	segments := &recordingSegmentCommitter{}
	engine := newTestCaptureEngine(t, source, frontiers, segments, nil)

	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	if frontier.Messages.Sequence != 2 ||
		frontier.Messages.WatermarkAtUnixMillis != firstObservedAt ||
		len(segments.batches) != 1 ||
		segments.batches[0].WatermarkAtUnixMillis != firstObservedAt ||
		len(segments.cursorBatches) != 1 ||
		segments.cursorBatches[0].WatermarkAtUnixMillis != firstObservedAt {
		t.Fatalf("cross-roll frontier=%#v batches=%#v cursor_batches=%#v",
			frontier.Messages, segments.batches, segments.cursorBatches)
	}
}

func TestCaptureEngineSchedulerSealsSparseStreamAtDeadlineBeforePoll(t *testing.T) {
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: time.Now().UnixMilli()},
			Messages: backupruntime.SourceWatermark{CommittedAtUnixMillis: time.Now().UnixMilli()},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {{
				Records: [][]byte{[]byte("sparse")}, NextCursor: "metadata-page-1", Done: true,
			}},
		},
	}
	committed := make(chan struct{})
	frontiers := &fakeSlotFrontierStore{committed: committed}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 1,
		Source: source, Frontiers: frontiers, Segments: &recordingSegmentCommitter{},
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 1 << 20, MaxSegmentBytes: 2 << 20,
			MaxOpenDuration: 20 * time.Millisecond, PageRecords: 1024,
		},
		ReconcileInterval: time.Hour,
		WorkerCount:       1,
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- engine.Run(ctx) }()

	select {
	case <-committed:
	case <-time.After(time.Second):
		t.Fatal("sparse accumulator was not sealed by its deadline")
	}
	cancel()
	if err := <-runResult; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if source.pageCalls != 1 {
		t.Fatalf("source page calls = %d, want one materialization before deadline seal", source.pageCalls)
	}
}

func TestCaptureEngineRollsBeforeNextPageWouldExceedTarget(t *testing.T) {
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 2, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{Position: 0, CommittedAtUnixMillis: 1_753_400_100_000},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {
				{Records: [][]byte{[]byte(strings.Repeat("a", 40))}, NextCursor: "metadata-page-1"},
				{Records: [][]byte{[]byte(strings.Repeat("b", 40))}, NextCursor: "metadata-page-2", Done: true},
			},
		},
	}
	clock := &fakeCaptureClock{now: time.UnixMilli(1_753_400_200_000)}
	segments := &recordingSegmentCommitter{}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 256,
		Source: source, Frontiers: &fakeSlotFrontierStore{}, Segments: segments,
		Clock: clock,
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 64, MaxSegmentBytes: 1 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}

	firstFrontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	if firstFrontier.Metadata.SourceHighWatermark != 1 ||
		len(segments.batches) != 1 || segments.batches[0].SourceHighWatermark != 1 {
		t.Fatalf("first partial cut frontier=%#v batches=%#v", firstFrontier.Metadata, segments.batches)
	}
	clock.now = clock.now.Add(31 * time.Second)
	if _, err := engine.ReconcileSlot(context.Background(), 17); err != nil {
		t.Fatalf("ReconcileSlot(after max-open) error = %v", err)
	}
	if len(segments.batches) != 2 ||
		len(segments.batches[0].Records) != 1 || len(segments.batches[1].Records) != 1 {
		t.Fatalf("target rolling batches = %#v", segments.batches)
	}
}

func TestCaptureEngineAllowsOneRecordAboveTargetBelowHardLimit(t *testing.T) {
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {{
				Records: [][]byte{[]byte(strings.Repeat("a", 100))}, NextCursor: "metadata-page-1", Done: true,
			}},
		},
		validateRead: func(request backupruntime.SourcePageRequest) error {
			if request.MaxBytes != 64 || request.MaxRecordBytes != 1<<20 {
				return fmt.Errorf("source page bounds = %d/%d", request.MaxBytes, request.MaxRecordBytes)
			}
			return nil
		},
	}
	segments := &recordingSegmentCommitter{}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 1,
		Source: source, Frontiers: &fakeSlotFrontierStore{}, Segments: segments,
		Clock: &fakeCaptureClock{now: time.UnixMilli(1_753_400_200_000)},
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 64, MaxSegmentBytes: 1 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}

	frontier, err := engine.ReconcileSlot(context.Background(), 0)
	if err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	if frontier.Metadata.Sequence != 1 || len(segments.batches) != 1 ||
		len(segments.batches[0].Records) != 1 {
		t.Fatalf("oversized target record frontier=%#v batches=%#v", frontier, segments.batches)
	}
}

func TestCaptureEngineStatusReportsPerSlotFrontierWatermarkLagAndState(t *testing.T) {
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 4, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{Position: 9, CommittedAtUnixMillis: 1_753_400_090_000, CutCursor: "message-cut-9"},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {{
				Records: [][]byte{[]byte("meta-4")}, NextCursor: "metadata-page-4", Done: true,
			}},
			backupartifact.SegmentStreamMessages: {{
				Records: [][]byte{[]byte("message-9")}, NextCursor: "message-page-9", Done: true,
				MessageCursors: []backupartifact.ChannelBoundary{
					{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 9},
				},
			}},
		},
	}
	engine := newTestCaptureEngine(t, source, &fakeSlotFrontierStore{}, &recordingSegmentCommitter{}, nil)
	if _, err := engine.ReconcileSlot(context.Background(), 17); err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}

	statuses := engine.Status()
	if len(statuses) != 1 {
		t.Fatalf("Status() count = %d, want 1", len(statuses))
	}
	status := statuses[0]
	if status.HashSlot != 17 || status.State != backupcontract.CaptureStateIdle ||
		status.Frontier.Metadata.SourceHighWatermark != 4 ||
		status.Frontier.Messages.SourceHighWatermark != 9 ||
		status.MetadataSourceWatermark != 4 || status.MessageSourceWatermark != 9 ||
		status.MetadataLag != 0 || status.MessageLag != 0 ||
		status.Frontier.WatermarkAtUnixMillis != 1_753_400_090_000 {
		t.Fatalf("Status() = %#v", status)
	}
}

func TestCaptureEngineStatusRemainsReconcilingWhileSourceSweepHasMorePages(t *testing.T) {
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{
				CommittedAtUnixMillis: 1_753_400_100_000, ReconcilePending: true,
			},
		},
	}
	engine := newTestCaptureEngine(t, source, &fakeSlotFrontierStore{}, &recordingSegmentCommitter{}, nil)
	if _, err := engine.ReconcileSlot(context.Background(), 17); err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	status := engine.Status()[0]
	if status.State != backupcontract.CaptureStateReconciling ||
		status.MessageLag != 0 || status.MessageSourceWatermark != 0 {
		t.Fatalf("Status() = %#v, want reconciling with zero currently known lag", status)
	}
}

func TestCaptureEngineWakeDoesNotTouchDurableBoundaries(t *testing.T) {
	source := &fakeContinuousSource{}
	frontiers := &fakeSlotFrontierStore{}
	segments := &recordingSegmentCommitter{}
	engine := newTestCaptureEngine(t, source, frontiers, segments, nil)

	if accepted := engine.Wake(17); !accepted {
		t.Fatal("Wake() rejected a valid Slot hint")
	}
	if source.watermarkCalls != 0 || source.pageCalls != 0 ||
		frontiers.loads != 0 || frontiers.commits != 0 || len(segments.batches) != 0 {
		t.Fatalf("Wake() performed durable work: source=%d/%d frontier=%d/%d segments=%d",
			source.watermarkCalls, source.pageCalls, frontiers.loads, frontiers.commits, len(segments.batches))
	}
}

func TestCaptureEngineRunReconcilesDurableFrontierWithoutWakeAfterRestart(t *testing.T) {
	segmentID := strings.Repeat("a", 64)
	cursorID := strings.Repeat("d", 64)
	frontiers := &fakeSlotFrontierStore{
		found: true,
		frontier: backupcontract.SlotFrontier{
			Revision: 1, HashSlot: 0, Generation: "slot-generation-1",
			Metadata: backupcontract.StreamFrontier{
				SourceHighWatermark: 0, WatermarkAtUnixMillis: 1_753_400_080_000,
			},
			Messages: backupcontract.StreamFrontier{
				Sequence: 1, SourceCursor: "message-page-1", SourceHighWatermark: 1,
				WatermarkAtUnixMillis: 1_753_400_080_000,
				Head: &backupartifact.SegmentReference{
					SegmentID: segmentID, CommitKey: "segments/" + segmentID + "/commit.json",
					CommitSHA256: strings.Repeat("c", 64), PlaintextBytes: 1,
				},
				CursorHead: &backupartifact.SegmentReference{
					SegmentID: cursorID, CommitKey: "segments/" + cursorID + "/commit.json",
					CommitSHA256: strings.Repeat("c", 64), PlaintextBytes: 1,
				},
			},
			WatermarkAtUnixMillis: 1_753_400_080_000,
			UpdatedAtUnixMillis:   1_753_400_080_000,
		},
		committed: make(chan struct{}),
	}
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 0, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{Position: 2, CommittedAtUnixMillis: 1_753_400_100_000, CutCursor: "message-cut-2"},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMessages: {
				{
					Records: [][]byte{[]byte("old-page")}, NextCursor: "message-page-1", Done: true,
					MessageCursors: []backupartifact.ChannelBoundary{
						{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 1},
					},
				},
				{
					Records: [][]byte{[]byte("new-after-restart")}, NextCursor: "message-page-2", Done: true,
					MessageCursors: []backupartifact.ChannelBoundary{
						{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 2},
					},
				},
			},
		},
		validateRead: func(request backupruntime.SourcePageRequest) error {
			if request.Stream == backupartifact.SegmentStreamMessages &&
				request.AfterCursor == "message-page-1" &&
				(request.CursorHead == nil || request.CursorHead.SegmentID != cursorID) {
				return fmt.Errorf("message cursor head was not restored: %#v", request.CursorHead)
			}
			return nil
		},
	}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 1,
		Source: source, Frontiers: frontiers, Segments: &recordingSegmentCommitter{},
		Clock: newAdvancingCaptureClock(),
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 64 << 20, MaxSegmentBytes: 256 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
		ReconcileInterval: time.Hour,
		WorkerCount:       1,
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	committed := frontiers.committed
	go func() { runResult <- engine.Run(ctx) }()

	<-committed
	cancel()
	if err := <-runResult; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if frontiers.frontier.Messages.Sequence != 2 ||
		frontiers.frontier.Messages.SourceCursor != "message-page-2" ||
		frontiers.frontier.Messages.SourceHighWatermark != 2 {
		t.Fatalf("frontier after restart = %#v", frontiers.frontier)
	}
}

func TestCaptureEngineUsesDefaultRollingPolicyWithoutEmittingEmptySegments(t *testing.T) {
	policy := backupruntime.DefaultRollingPolicy()
	if policy.TargetSegmentBytes != 64<<20 || policy.MaxSegmentBytes != 256<<20 ||
		policy.MaxOpenDuration != 30*time.Second ||
		backupruntime.DefaultCaptureMemoryBudgetBytes != 784<<20 {
		t.Fatalf("DefaultRollingPolicy() = %#v", policy)
	}
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_090_000},
		},
	}
	frontiers := &fakeSlotFrontierStore{}
	segments := &recordingSegmentCommitter{}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 256,
		Source: source, Frontiers: frontiers, Segments: segments,
		Clock: &fakeCaptureClock{now: time.UnixMilli(1_753_400_200_000)},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}

	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	if frontier.Metadata.Head != nil || frontier.Messages.Head != nil || len(segments.batches) != 0 {
		t.Fatalf("empty source emitted segments: frontier=%#v batches=%d", frontier, len(segments.batches))
	}
	if frontier.WatermarkAtUnixMillis != 0 || frontiers.commits != 0 {
		t.Fatalf("idle source rewrote frontier = %#v commits=%d", frontier, frontiers.commits)
	}
}

func TestCaptureEngineDoesNotAdvanceFrontierWhenMessageSegmentFails(t *testing.T) {
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_090_000, CutCursor: "message-cut-1"},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {{
				Records: [][]byte{[]byte("meta-1")}, NextCursor: "metadata-page-1", Done: true,
			}},
			backupartifact.SegmentStreamMessages: {{
				Records: [][]byte{[]byte("message-1")}, NextCursor: "message-page-1", Done: true,
				MessageCursors: []backupartifact.ChannelBoundary{
					{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 1},
				},
			}},
		},
	}
	frontiers := &fakeSlotFrontierStore{}
	segments := &recordingSegmentCommitter{failStream: backupartifact.SegmentStreamMessages}
	engine := newTestCaptureEngine(t, source, frontiers, segments, nil)

	if _, err := engine.ReconcileSlot(context.Background(), 17); err == nil {
		t.Fatal("ReconcileSlot() error = nil, want message segment failure")
	}
	if frontiers.commits != 0 || frontiers.found {
		t.Fatalf("partial frontier became visible: commits=%d frontier=%#v", frontiers.commits, frontiers.frontier)
	}
	statuses := engine.Status()
	if len(statuses) != 1 || statuses[0].State != backupcontract.CaptureStateFailed ||
		statuses[0].FailureCategory != "message_capture" ||
		statuses[0].MetadataLag != 1 || statuses[0].MessageLag != 1 {
		t.Fatalf("failed capture status = %#v", statuses)
	}

	segments.failStream = ""
	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("retry ReconcileSlot() error = %v", err)
	}
	if frontier.Metadata.Sequence != 1 || frontier.Messages.Sequence != 1 || frontiers.commits != 1 {
		t.Fatalf("retry frontier = %#v commits=%d", frontier, frontiers.commits)
	}
}

func TestCaptureEngineDoesNotAdvanceFrontierWhenMessageCursorSidecarFails(t *testing.T) {
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_100_000, CutCursor: "message-cut-1"},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {{
				Records: [][]byte{[]byte("meta-1")}, NextCursor: "metadata-page-1", Done: true,
			}},
			backupartifact.SegmentStreamMessages: {{
				Records: [][]byte{[]byte("message-1")}, NextCursor: "message-page-1", Done: true,
				MessageCursors: []backupartifact.ChannelBoundary{
					{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 1},
				},
			}},
		},
	}
	frontiers := &fakeSlotFrontierStore{}
	segments := &recordingSegmentCommitter{failStream: backupartifact.SegmentStreamMessageCursor}
	engine := newTestCaptureEngine(t, source, frontiers, segments, nil)

	if _, err := engine.ReconcileSlot(context.Background(), 17); err == nil {
		t.Fatal("ReconcileSlot() error = nil, want cursor sidecar failure")
	}
	if frontiers.commits != 0 || frontiers.found || len(segments.cursorBatches) != 1 {
		t.Fatalf("cursor failure became visible: commits=%d frontier=%#v cursor_batches=%d",
			frontiers.commits, frontiers.frontier, len(segments.cursorBatches))
	}

	segments.failStream = ""
	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("retry ReconcileSlot() error = %v", err)
	}
	if frontier.Messages.CursorHead == nil || frontier.Messages.Sequence != 1 || frontiers.commits != 1 {
		t.Fatalf("retry frontier = %#v commits=%d", frontier, frontiers.commits)
	}
}

func TestCaptureEngineWritesFullCursorCheckpointAtBoundedInterval(t *testing.T) {
	dataHead := validRuntimeSegmentReference("b")
	cursorHead := validRuntimeSegmentReference("d")
	cursorBody, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
		HashSlot: 0, Generation: "slot-generation-1", Sequence: 1023,
		Checkpoint: true, NextCursor: "message-old",
		SourceHighWatermark: 5, WatermarkAtUnixMillis: 1_753_400_100_000,
		Boundaries: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-old", ChannelType: 2, Epoch: 1, HW: 5},
		},
	})
	if err != nil {
		t.Fatalf("MarshalMessageCursorBatch() error = %v", err)
	}
	cursorHead.PlaintextBytes = int64(len(cursorBody))
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{Position: 6, CommittedAtUnixMillis: 1_753_400_100_100, CutCursor: "message-cut-6"},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMessages: {
				{NextCursor: "message-old"},
				{
					Records: [][]byte{[]byte("new-message")}, NextCursor: "message-new", Done: true,
					MessageCursors: []backupartifact.ChannelBoundary{
						{ChannelID: "channel-new", ChannelType: 2, Epoch: 1, HW: 1},
					},
				},
			},
		},
	}
	frontiers := &fakeSlotFrontierStore{found: true, frontier: backupcontract.SlotFrontier{
		Revision: 1, HashSlot: 0, Generation: "slot-generation-1",
		Metadata: backupcontract.StreamFrontier{
			SourceCursor: "metadata-old", SourceHighWatermark: 1,
			WatermarkAtUnixMillis: 1_753_400_100_000,
		},
		Messages: backupcontract.StreamFrontier{
			Sequence: 1023, Head: &dataHead, CursorHead: &cursorHead,
			SourceCursor: "message-old", SourceHighWatermark: 5,
			WatermarkAtUnixMillis: 1_753_400_100_000,
		},
		WatermarkAtUnixMillis: 1_753_400_100_000,
		UpdatedAtUnixMillis:   1_753_400_100_000,
	}}
	segments := &recordingSegmentCommitter{}
	budget := &recordingCaptureBudget{}
	cursorLoader := &budgetAssertingCursorLoader{
		body: cursorBody, budget: budget,
	}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 1,
		Source: source, Frontiers: frontiers, Segments: segments,
		CursorLoader: cursorLoader,
		Clock:        newAdvancingCaptureClock(), MemoryBudget: budget,
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 1 << 20, MaxSegmentBytes: 2 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 16,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}
	frontier, err := engine.ReconcileSlot(context.Background(), 0)
	if err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	if frontier.Messages.Sequence != 1024 || len(segments.cursorBatches) != 1 ||
		!segments.cursorBatches[0].Checkpoint || segments.cursorBatches[0].Previous != nil ||
		len(segments.cursorBatches[0].Boundaries) != 2 {
		t.Fatalf("frontier=%#v cursor batches=%#v", frontier, segments.cursorBatches)
	}
	if !cursorLoader.observedReservation || budget.acquires < 3 || budget.held != 0 {
		t.Fatalf("checkpoint reservation observed=%v acquires=%d held=%d, want pre-load bounded reservations released",
			cursorLoader.observedReservation, budget.acquires, budget.held)
	}
}

func TestCaptureEngineAcquiresNodeBudgetBeforeMaterializingSourcePage(t *testing.T) {
	budget := &recordingCaptureBudget{}
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {{
				Records: [][]byte{[]byte("metadata")}, NextCursor: "metadata-page-1", Done: true,
			}},
		},
		beforeRead: func(backupruntime.SourcePageRequest) {
			if budget.held == 0 {
				t.Fatal("ReadPage() materialized data before acquiring node memory budget")
			}
		},
	}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 1,
		Source: source, Frontiers: &fakeSlotFrontierStore{}, Segments: &recordingSegmentCommitter{},
		Clock: newAdvancingCaptureClock(), MemoryBudget: budget,
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 1 << 20, MaxSegmentBytes: 1 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}
	if _, err := engine.ReconcileSlot(context.Background(), 0); err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	if budget.acquires == 0 || budget.held != 0 {
		t.Fatalf("memory budget acquires=%d held=%d", budget.acquires, budget.held)
	}
}

func TestCaptureEngineYieldsHotSlotAfterBoundedPages(t *testing.T) {
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 4, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {
				{Records: [][]byte{[]byte("one")}, NextCursor: "metadata-page-1"},
				{Records: [][]byte{[]byte("two")}, NextCursor: "metadata-page-2"},
				{Records: [][]byte{[]byte("three")}, NextCursor: "metadata-page-3"},
				{Records: [][]byte{[]byte("four")}, NextCursor: "metadata-page-4", Done: true},
			},
		},
	}
	frontiers := &fakeSlotFrontierStore{}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 1,
		Source: source, Frontiers: frontiers, Segments: &recordingSegmentCommitter{},
		Clock: &fakeCaptureClock{now: time.UnixMilli(1_753_400_000_000)},
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 1, MaxSegmentBytes: 1024,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1, PagesPerReconcile: 2,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}
	first, err := engine.ReconcileSlot(context.Background(), 0)
	if err != nil {
		t.Fatalf("ReconcileSlot(first quantum) error = %v", err)
	}
	if source.pageCalls != 2 || first.Metadata.Sequence != 2 ||
		first.Metadata.SourceHighWatermark != 2 {
		t.Fatalf("first quantum calls=%d frontier=%#v, want two pages through position 2", source.pageCalls, first.Metadata)
	}
	if status := engine.Status()[0]; status.State != backupcontract.CaptureStateCapturing || status.MetadataLag != 2 {
		t.Fatalf("first quantum status = %#v, want capturing with lag 2", status)
	}
	second, err := engine.ReconcileSlot(context.Background(), 0)
	if err != nil {
		t.Fatalf("ReconcileSlot(second quantum) error = %v", err)
	}
	if source.pageCalls != 4 || second.Metadata.Sequence != 4 ||
		second.Metadata.SourceHighWatermark != 4 {
		t.Fatalf("second quantum calls=%d frontier=%#v, want four pages through position 4", source.pageCalls, second.Metadata)
	}
	if status := engine.Status()[0]; status.State != backupcontract.CaptureStateIdle || status.MetadataLag != 0 {
		t.Fatalf("second quantum status = %#v, want idle with zero lag", status)
	}
}

func TestCaptureEngineMemoryPressureDoesNotBlockWorkerAndExpiredPendingReleasesBudget(t *testing.T) {
	clock := &fakeCaptureClock{now: time.UnixMilli(1_753_400_000_000)}
	budget := &recordingCaptureBudget{capacity: 1200}
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{Position: 1, CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
		},
		pages: map[backupartifact.SegmentStream][]backupruntime.SourcePage{
			backupartifact.SegmentStreamMetadata: {
				{Records: [][]byte{[]byte(strings.Repeat("a", 20))}, NextCursor: "metadata-page-1", Done: true},
				{Records: [][]byte{[]byte(strings.Repeat("b", 20))}, NextCursor: "metadata-page-2", Done: true},
			},
		},
	}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 1,
		Source: source, Frontiers: &fakeSlotFrontierStore{}, Segments: &recordingSegmentCommitter{},
		Clock: clock, MemoryBudget: budget,
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 64, MaxSegmentBytes: 1024,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}
	if _, err := engine.ReconcileSlot(context.Background(), 0); err != nil {
		t.Fatalf("ReconcileSlot(first sparse page) error = %v", err)
	}
	source.watermarks.Metadata.Position = 2
	pageCalls := source.pageCalls
	if _, err := engine.ReconcileSlot(context.Background(), 0); !errors.Is(err, backupruntime.ErrCaptureMemoryPressure) {
		t.Fatalf("ReconcileSlot(memory pressure) error = %v", err)
	}
	if source.pageCalls != pageCalls {
		t.Fatalf("source materialized page under memory pressure: calls=%d want=%d", source.pageCalls, pageCalls)
	}
	status := engine.Status()[0]
	if status.State != backupcontract.CaptureStateDegraded || status.FailureCategory != "capture_memory" {
		t.Fatalf("memory-pressure status = %#v", status)
	}

	clock.now = clock.now.Add(31 * time.Second)
	frontier, err := engine.ReconcileSlot(context.Background(), 0)
	if err != nil {
		t.Fatalf("ReconcileSlot(after expiry) error = %v", err)
	}
	if frontier.Metadata.Sequence != 1 || budget.held == 0 {
		t.Fatalf("frontier=%#v budget held=%d, want first segment sealed and second sparse page retained", frontier, budget.held)
	}
}

type fakeContinuousSource struct {
	watermarks     backupruntime.SourceWatermarks
	pages          map[backupartifact.SegmentStream][]backupruntime.SourcePage
	beforeRead     func(backupruntime.SourcePageRequest)
	validateRead   func(backupruntime.SourcePageRequest) error
	watermarkCalls int
	pageCalls      int
}

func (s *fakeContinuousSource) HighWatermarks(context.Context, uint16, backupcontract.SlotFrontier) (backupruntime.SourceWatermarks, error) {
	s.watermarkCalls++
	return s.watermarks, nil
}

func (s *fakeContinuousSource) ReadPage(_ context.Context, request backupruntime.SourcePageRequest) (backupruntime.SourcePage, error) {
	s.pageCalls++
	if s.beforeRead != nil {
		s.beforeRead(request)
	}
	if s.validateRead != nil {
		if err := s.validateRead(request); err != nil {
			return backupruntime.SourcePage{}, err
		}
	}
	pages := s.pages[request.Stream]
	after := ""
	for index, page := range pages {
		if request.AfterCursor == after {
			if page.NextPosition == 0 {
				if page.Done {
					page.NextPosition = request.ThroughPosition
				} else {
					page.NextPosition = uint64(index + 1)
				}
			}
			return page, nil
		}
		after = page.NextCursor
	}
	return backupruntime.SourcePage{}, fmt.Errorf("unexpected %s cursor %q", request.Stream, request.AfterCursor)
}

type statefulExactMessageSource struct {
	selected      bool
	invalidations int
	reads         int
}

func (s *statefulExactMessageSource) HighWatermarks(context.Context, uint16, backupcontract.SlotFrontier) (backupruntime.SourceWatermarks, error) {
	messages := backupruntime.SourceWatermark{
		CommittedAtUnixMillis: 1_753_400_100_000,
		ReconcilePending:      s.selected,
	}
	if !s.selected {
		messages.Position = 1
		messages.CutCursor = "message-cut-1"
	}
	return backupruntime.SourceWatermarks{
		Metadata: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
		Messages: messages,
	}, nil
}

func (s *statefulExactMessageSource) ReadPage(_ context.Context, request backupruntime.SourcePageRequest) (backupruntime.SourcePage, error) {
	if request.Stream != backupartifact.SegmentStreamMessages || request.ThroughPosition != 1 ||
		request.ThroughCursor != "message-cut-1" {
		return backupruntime.SourcePage{}, fmt.Errorf("unexpected exact message request: %#v", request)
	}
	s.reads++
	return backupruntime.SourcePage{
		Records: [][]byte{[]byte("message-1")}, NextCursor: "message-page-1",
		NextPosition: 1, Done: true,
		MessageCursors: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 1},
		},
	}, nil
}

func (s *statefulExactMessageSource) AcknowledgeSourcePage(_ uint16, stream backupartifact.SegmentStream, cutCursor string) {
	if stream == backupartifact.SegmentStreamMessages && cutCursor == "message-cut-1" {
		s.selected = true
	}
}

func (s *statefulExactMessageSource) InvalidateSourceState(uint16) {
	s.invalidations++
	s.selected = false
}

type pagedDiscoverySource struct {
	mu         sync.Mutex
	calls      int
	secondCall chan struct{}
}

func (s *pagedDiscoverySource) HighWatermarks(context.Context, uint16, backupcontract.SlotFrontier) (backupruntime.SourceWatermarks, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 2 {
		close(s.secondCall)
	}
	pending := s.calls == 1
	return backupruntime.SourceWatermarks{
		Metadata: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
		Messages: backupruntime.SourceWatermark{
			CommittedAtUnixMillis: 1_753_400_100_000,
			ReconcilePending:      pending, DiscoveryPending: pending,
		},
	}, nil
}

func (*pagedDiscoverySource) ReadPage(context.Context, backupruntime.SourcePageRequest) (backupruntime.SourcePage, error) {
	return backupruntime.SourcePage{}, fmt.Errorf("paged discovery source should not read capture pages")
}

type fakeSlotFrontierStore struct {
	frontier    backupcontract.SlotFrontier
	found       bool
	loads       int
	commits     int
	failCommits int
	committed   chan struct{}
}

func (s *fakeSlotFrontierStore) Load(context.Context, uint16) (backupruntime.FrontierSnapshot, error) {
	s.loads++
	return backupruntime.FrontierSnapshot{Frontier: s.frontier, Found: s.found}, nil
}

func (s *fakeSlotFrontierStore) CompareAndSwap(_ context.Context, expectedRevision uint64, frontier backupcontract.SlotFrontier) error {
	if s.failCommits > 0 {
		s.failCommits--
		return backupruntime.ErrFrontierConflict
	}
	if s.frontier.Revision != expectedRevision {
		return backupruntime.ErrFrontierConflict
	}
	s.frontier = frontier
	s.found = true
	s.commits++
	if s.committed != nil {
		close(s.committed)
		s.committed = nil
	}
	return nil
}

type recordingSegmentCommitter struct {
	batches       []backupartifact.SegmentBatch
	cursorBatches []backupartifact.MessageCursorBatch
	failStream    backupartifact.SegmentStream
	bodies        map[string][]byte
}

type commitOnlySegmentCommitter struct{}

func (commitOnlySegmentCommitter) Commit(context.Context, backupartifact.SegmentDescriptor, []byte) (backupartifact.SegmentReference, error) {
	return backupartifact.SegmentReference{}, nil
}

type staticContinuousCursorLoader struct {
	bodies map[string][]byte
}

func (l *staticContinuousCursorLoader) Load(_ context.Context, reference backupartifact.SegmentReference) ([]byte, error) {
	body, ok := l.bodies[reference.SegmentID]
	if !ok {
		return nil, fmt.Errorf("missing cursor segment %s", reference.SegmentID)
	}
	return append([]byte(nil), body...), nil
}

type budgetAssertingCursorLoader struct {
	body                []byte
	budget              *recordingCaptureBudget
	observedReservation bool
}

func (l *budgetAssertingCursorLoader) Load(_ context.Context, reference backupartifact.SegmentReference) ([]byte, error) {
	required := 3 * reference.PlaintextBytes
	if reference.PlaintextBytes != int64(len(l.body)) || l.budget == nil || l.budget.held < required {
		return nil, fmt.Errorf("cursor body materialized before its working set was reserved")
	}
	l.observedReservation = true
	return append([]byte(nil), l.body...), nil
}

func validRuntimeSegmentReference(letter string) backupartifact.SegmentReference {
	id := strings.Repeat(letter, 64)
	return backupartifact.SegmentReference{
		SegmentID: id, CommitKey: "segments/" + id + "/commit.json",
		CommitSHA256: strings.Repeat("c", 64), PlaintextBytes: 1,
	}
}

func (s *recordingSegmentCommitter) Commit(_ context.Context, descriptor backupartifact.SegmentDescriptor, plaintext []byte) (backupartifact.SegmentReference, error) {
	switch descriptor.Logical.Stream {
	case backupartifact.SegmentStreamMessageCursor:
		batch, err := backupartifact.LoadMessageCursorBatch(plaintext)
		if err != nil {
			return backupartifact.SegmentReference{}, err
		}
		s.cursorBatches = append(s.cursorBatches, batch)
	default:
		batch, err := backupartifact.LoadSegmentBatch(plaintext)
		if err != nil {
			return backupartifact.SegmentReference{}, err
		}
		s.batches = append(s.batches, batch)
	}
	if descriptor.Logical.Stream == s.failStream {
		return backupartifact.SegmentReference{}, fmt.Errorf("injected %s segment failure", s.failStream)
	}
	idByte := "a"
	if descriptor.Logical.Stream == backupartifact.SegmentStreamMessages {
		idByte = "b"
	} else if descriptor.Logical.Stream == backupartifact.SegmentStreamMessageCursor {
		idByte = "d"
	}
	segmentID := strings.Repeat(idByte, 64)
	if s.bodies == nil {
		s.bodies = make(map[string][]byte)
	}
	s.bodies[segmentID] = append([]byte(nil), plaintext...)
	return backupartifact.SegmentReference{
		SegmentID: segmentID, CommitKey: "segments/" + segmentID + "/commit.json",
		CommitSHA256: strings.Repeat("c", 64), PlaintextBytes: int64(len(plaintext)),
	}, nil
}

func (s *recordingSegmentCommitter) Load(_ context.Context, reference backupartifact.SegmentReference) ([]byte, error) {
	body, ok := s.bodies[reference.SegmentID]
	if !ok {
		return nil, fmt.Errorf("missing committed segment %s", reference.SegmentID)
	}
	return append([]byte(nil), body...), nil
}

type fakeCaptureClock struct {
	now time.Time
}

type advancingCaptureClock struct {
	mu  sync.Mutex
	now time.Time
}

func newAdvancingCaptureClock() *advancingCaptureClock {
	return &advancingCaptureClock{now: time.UnixMilli(1_753_400_200_000)}
}

func (c *advancingCaptureClock) Now() time.Time {
	c.mu.Lock()
	now := c.now
	c.now = c.now.Add(31 * time.Second)
	c.mu.Unlock()
	return now
}

type recordingCaptureBudget struct {
	held     int64
	acquires int
	capacity int64
}

func (b *recordingCaptureBudget) TryAcquire(bytes int64) bool {
	if b.capacity > 0 && b.held+bytes > b.capacity {
		return false
	}
	b.held += bytes
	b.acquires++
	return true
}

func (b *recordingCaptureBudget) Release(bytes int64) {
	b.held -= bytes
}

func (c *fakeCaptureClock) Now() time.Time {
	return c.now
}

func newTestCaptureEngine(t *testing.T, source backupruntime.ContinuousSource, frontiers backupruntime.SlotFrontierStore, segments backupruntime.SegmentCommitter, clock backupruntime.CaptureClock) *backupruntime.CaptureEngine {
	t.Helper()
	if clock == nil {
		clock = newAdvancingCaptureClock()
	}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 256,
		Source: source, Frontiers: frontiers, Segments: segments, Clock: clock,
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 64 << 20, MaxSegmentBytes: 256 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}
	return engine
}
