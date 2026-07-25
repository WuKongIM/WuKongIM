package backup

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// ContinuousStreamSource adapts one authoritative committed-log stream.
type ContinuousStreamSource interface {
	// HighWatermark observes the committed position relative to the durable stream frontier.
	HighWatermark(context.Context, uint16, string, backupcontract.StreamFrontier) (runtimebackup.SourceWatermark, error)
	// ReadPage returns one owned bounded page through a pinned position.
	ReadPage(context.Context, runtimebackup.SourcePageRequest) (runtimebackup.SourcePage, error)
}

// ContinuousSource combines independently implemented metadata and message logs.
type ContinuousSource struct {
	metadata ContinuousStreamSource
	messages ContinuousStreamSource
}

// NewContinuousSource creates the runtime adapter for both independent streams.
func NewContinuousSource(metadata, messages ContinuousStreamSource) (*ContinuousSource, error) {
	if metadata == nil || messages == nil {
		return nil, fmt.Errorf("backup continuous source: metadata and messages are required")
	}
	return &ContinuousSource{metadata: metadata, messages: messages}, nil
}

// HighWatermarks observes both committed-log cuts independently.
func (s *ContinuousSource) HighWatermarks(ctx context.Context, hashSlot uint16, frontier backupcontract.SlotFrontier) (runtimebackup.SourceWatermarks, error) {
	metadata, err := s.metadata.HighWatermark(ctx, hashSlot, frontier.Generation, frontier.Metadata)
	if err != nil {
		return runtimebackup.SourceWatermarks{}, err
	}
	// A materialized baseline may end at a physical Slot index later than the
	// latest command affecting this logical Hash Slot. That complete snapshot
	// makes the physical cut a safe resume floor; only a later logical command
	// may advance it.
	if frontier.Baseline != nil && metadata.Position < frontier.Metadata.SourceHighWatermark {
		metadata.Position = frontier.Metadata.SourceHighWatermark
		metadata.CommittedAtUnixMillis = frontier.Metadata.WatermarkAtUnixMillis
	}
	messages, err := s.messages.HighWatermark(ctx, hashSlot, frontier.Generation, frontier.Messages)
	if err != nil {
		return runtimebackup.SourceWatermarks{}, err
	}
	return runtimebackup.SourceWatermarks{Metadata: metadata, Messages: messages}, nil
}

// ReadPage routes a page request to its independent committed-log adapter.
func (s *ContinuousSource) ReadPage(ctx context.Context, request runtimebackup.SourcePageRequest) (runtimebackup.SourcePage, error) {
	switch request.Stream {
	case backupartifact.SegmentStreamMetadata:
		return s.metadata.ReadPage(ctx, request)
	case backupartifact.SegmentStreamMessages:
		return s.messages.ReadPage(ctx, request)
	default:
		return runtimebackup.SourcePage{}, runtimebackup.ErrInvalidCapture
	}
}

// AcknowledgeSourcePage forwards the optional post-admission hint to the
// selected stream adapter. It never performs durable work.
func (s *ContinuousSource) AcknowledgeSourcePage(hashSlot uint16, stream backupartifact.SegmentStream, cutCursor string) {
	var source ContinuousStreamSource
	switch stream {
	case backupartifact.SegmentStreamMetadata:
		source = s.metadata
	case backupartifact.SegmentStreamMessages:
		source = s.messages
	default:
		return
	}
	if acknowledger, ok := source.(runtimebackup.SourcePageAcknowledger); ok {
		acknowledger.AcknowledgeSourcePage(hashSlot, stream, cutCursor)
	}
}

// InvalidateSourceState discards disposable per-Slot acceleration after the
// runtime fails to publish the corresponding durable SlotFrontier.
func (s *ContinuousSource) InvalidateSourceState(hashSlot uint16) {
	for _, source := range []ContinuousStreamSource{s.metadata, s.messages} {
		if invalidator, ok := source.(runtimebackup.SourceStateInvalidator); ok {
			invalidator.InvalidateSourceState(hashSlot)
		}
	}
}

// MetadataLogNode is the narrow real Slot RaftDB source exposed by cluster.Node.
type MetadataLogNode interface {
	// ObserveBackupMetadataHighWatermark returns the local applied Slot cut.
	ObserveBackupMetadataHighWatermark(context.Context, uint16) (clusterpkg.BackupMetadataHighWatermark, error)
	// ReadBackupMetadataLogPage reads forward retained commands through an applied cut.
	ReadBackupMetadataLogPage(context.Context, clusterpkg.BackupMetadataLogPageRequest) (clusterpkg.BackupMetadataLogPage, error)
}

// MetadataLogSource adapts real applied Slot RaftDB entries to capture pages.
type MetadataLogSource struct {
	node MetadataLogNode
}

// NewMetadataLogSource creates a committed metadata-log adapter.
func NewMetadataLogSource(node MetadataLogNode) (*MetadataLogSource, error) {
	if node == nil {
		return nil, fmt.Errorf("backup metadata log source: cluster Node is required")
	}
	return &MetadataLogSource{node: node}, nil
}

// HighWatermark returns the last applied Raft index relevant to this Hash Slot.
func (s *MetadataLogSource) HighWatermark(ctx context.Context, hashSlot uint16, _ string, _ backupcontract.StreamFrontier) (runtimebackup.SourceWatermark, error) {
	watermark, err := s.node.ObserveBackupMetadataHighWatermark(ctx, hashSlot)
	if err != nil {
		if errors.Is(err, clusterpkg.ErrBackupSourceCompacted) {
			return runtimebackup.SourceWatermark{}, runtimebackup.ErrCaptureSourceCompacted
		}
		return runtimebackup.SourceWatermark{}, err
	}
	if watermark.HashSlot != hashSlot || watermark.ObservedAtUnixMillis <= 0 {
		return runtimebackup.SourceWatermark{}, runtimebackup.ErrInvalidCapture
	}
	return runtimebackup.SourceWatermark{
		Position: watermark.RaftIndex, CommittedAtUnixMillis: watermark.ObservedAtUnixMillis,
	}, nil
}

// ReadPage maps the opaque decimal cursor to a forward Slot RaftDB scan.
func (s *MetadataLogSource) ReadPage(ctx context.Context, request runtimebackup.SourcePageRequest) (runtimebackup.SourcePage, error) {
	if request.Stream != backupartifact.SegmentStreamMetadata {
		return runtimebackup.SourcePage{}, runtimebackup.ErrInvalidCapture
	}
	afterIndex, err := parseMetadataLogCursor(request.AfterCursor)
	if err != nil {
		return runtimebackup.SourcePage{}, err
	}
	page, err := s.node.ReadBackupMetadataLogPage(ctx, clusterpkg.BackupMetadataLogPageRequest{
		HashSlot: request.HashSlot, AfterIndex: afterIndex,
		ThroughIndex: request.ThroughPosition,
		TargetBytes:  request.MaxBytes,
		MaxBytes:     request.MaxRecordBytes,
		MaxRecords:   request.MaxRecords,
	})
	if err != nil {
		if errors.Is(err, clusterpkg.ErrBackupSourceCompacted) {
			return runtimebackup.SourcePage{}, runtimebackup.ErrCaptureSourceCompacted
		}
		return runtimebackup.SourcePage{}, err
	}
	if page.NextIndex <= afterIndex || len(page.Records) > request.MaxRecords {
		return runtimebackup.SourcePage{}, runtimebackup.ErrInvalidCapture
	}
	return runtimebackup.SourcePage{
		Records: page.Records, NextCursor: strconv.FormatUint(page.NextIndex, 10),
		NextPosition: page.NextIndex, Done: page.Done,
	}, nil
}

func parseMetadataLogCursor(cursor string) (uint64, error) {
	if cursor == "" {
		return 0, nil
	}
	index, err := strconv.ParseUint(cursor, 10, 64)
	if err != nil || index == 0 || strconv.FormatUint(index, 10) != cursor {
		return 0, runtimebackup.ErrInvalidCapture
	}
	return index, nil
}

var (
	_ runtimebackup.ContinuousSource       = (*ContinuousSource)(nil)
	_ runtimebackup.SourcePageAcknowledger = (*ContinuousSource)(nil)
	_ ContinuousStreamSource               = (*MetadataLogSource)(nil)
	_ MetadataLogNode                      = (*clusterpkg.Node)(nil)
)
