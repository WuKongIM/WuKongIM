package backup

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// MaterializedCheckpointBaselineReplayerOptions configures authenticated
// materialized-root replay.
type MaterializedCheckpointBaselineReplayerOptions struct {
	// Codec decrypts the baseline's immutable object chunks.
	Codec *backupartifact.ObjectCodec
	// Segments authenticates the complete baseline message cursor.
	Segments *backupartifact.ReplicatedSegmentStore
}

// MaterializedCheckpointBaselineReplayer converts legacy portable snapshot
// streams into the same target session and evidence stream as deltas.
type MaterializedCheckpointBaselineReplayer struct {
	codec    *backupartifact.ObjectCodec
	segments *backupartifact.ReplicatedSegmentStore
	budget   *CheckpointSlotInstaller
}

// NewMaterializedCheckpointBaselineReplayer creates a baseline decoder. Its
// resource budget is bound by NewCheckpointSlotInstaller.
func NewMaterializedCheckpointBaselineReplayer(
	options MaterializedCheckpointBaselineReplayerOptions,
) (*MaterializedCheckpointBaselineReplayer, error) {
	if options.Codec == nil || options.Segments == nil {
		return nil, fmt.Errorf("backup checkpoint baseline replayer: invalid options")
	}
	return &MaterializedCheckpointBaselineReplayer{
		codec: options.Codec, segments: options.Segments,
	}, nil
}

func (r *MaterializedCheckpointBaselineReplayer) bindCheckpointRestoreBudget(
	installer *CheckpointSlotInstaller,
) {
	r.budget = installer
}

// ReplayCheckpointBaseline authenticates each repository object once and
// replays bounded rows into the isolated target session.
func (r *MaterializedCheckpointBaselineReplayer) ReplayCheckpointBaseline(
	ctx context.Context,
	repository backupartifact.Repository,
	slot backupartifact.CheckpointSlot,
	sink CheckpointRestoreRecordSink,
) (uint64, error) {
	if r == nil || r.codec == nil || r.segments == nil || r.budget == nil ||
		repository == nil || slot.Baseline == nil || sink == nil {
		return 0, fmt.Errorf("backup checkpoint baseline replayer: unavailable")
	}
	layers, err := loadRestorePartitionLayers(
		ctx, repository, slot.Baseline.Partition,
	)
	if err != nil {
		return 0, err
	}
	if len(layers) != 1 ||
		layers[0].Cut.HashSlot != slot.HashSlot ||
		layers[0].BaselineCursor == nil ||
		*layers[0].BaselineCursor != slot.Baseline.MessageCursor {
		return 0, fmt.Errorf(
			"%w: checkpoint baseline is not one materialized root",
			backupartifact.ErrInvalidManifest,
		)
	}
	layer := layers[0]
	metadataGroups, err := restoreObjectGroups(
		layer.Objects, backupartifact.ObjectKindMetadata,
	)
	if err != nil || len(metadataGroups) != 1 ||
		metadataGroups[0].Name != string(backupartifact.ObjectKindMetadata) {
		return 0, fmt.Errorf(
			"%w: checkpoint baseline metadata stream is invalid",
			backupartifact.ErrInvalidManifest,
		)
	}
	var downloaded uint64
	metadataBytes, err := r.withStagedStream(
		ctx, repository, metadataGroups[0].Objects,
		func(file *os.File, size int64) error {
			slots, _, err := metadb.ReplayBackupHashSlotSnapshot(
				ctx, file, size,
				func(entry metadb.BackupSnapshotEntry) error {
					return sink.MetadataSnapshot(entry.Key, entry.Value)
				},
			)
			if err != nil {
				return err
			}
			if len(slots) != 1 || slots[0] != slot.HashSlot {
				return fmt.Errorf(
					"%w: checkpoint baseline metadata Slot mismatch",
					backupartifact.ErrObjectCorrupt,
				)
			}
			return nil
		},
	)
	if err != nil {
		return 0, err
	}
	downloaded = metadataBytes
	messageGroups, err := restoreObjectGroups(
		layer.Objects, backupartifact.ObjectKindMessages,
	)
	if err != nil {
		return 0, err
	}
	for _, group := range messageGroups {
		messageBytes, err := r.withStagedStream(
			ctx, repository, group.Objects,
			func(file *os.File, size int64) error {
				stats, err := messagedb.ReplayBackupSnapshotReader(
					ctx, file, size,
					func(boundary messagedb.BackupSnapshotBoundary) error {
						return sink.Boundary(backupartifact.ChannelBoundary{
							ChannelID:      boundary.ChannelID,
							ChannelType:    boundary.ChannelType,
							Epoch:          boundary.Epoch,
							LogStartOffset: boundary.LogStartOffset,
							HW:             boundary.HW,
						})
					},
					func(record messagedb.BackupSnapshotRecord) error {
						body, err := backupartifact.MarshalMessageLogRecord(
							backupartifact.MessageLogRecord{
								Kind:              backupartifact.MessageLogRecordMessage,
								HashSlot:          slot.HashSlot,
								ChannelID:         record.Boundary.ChannelID,
								ChannelType:       record.Boundary.ChannelType,
								Epoch:             record.Boundary.Epoch,
								LogStartOffset:    record.Boundary.LogStartOffset,
								HW:                record.Boundary.HW,
								MessageSeq:        record.MessageSeq,
								MessageID:         record.MessageID,
								Setting:           record.Setting,
								FromUID:           record.FromUID,
								ClientMsgNo:       record.ClientMsgNo,
								ServerTimestampMS: record.ServerTimestampMS,
								SyncOnce:          record.SyncOnce,
								Payload:           record.Payload,
							},
						)
						if err != nil {
							return err
						}
						return sink.Message(body)
					},
				)
				if err != nil {
					return err
				}
				if stats.HashSlot != slot.HashSlot {
					return fmt.Errorf(
						"%w: checkpoint baseline message Slot mismatch",
						backupartifact.ErrObjectCorrupt,
					)
				}
				return nil
			},
		)
		if err != nil {
			return 0, err
		}
		if downloaded > math.MaxUint64-messageBytes {
			return 0, backupartifact.ErrInvalidObject
		}
		downloaded += messageBytes
	}
	cursorBytes, err := r.replayBaselineCursor(
		ctx, repository, slot, layer.Cut, sink,
	)
	if err != nil {
		return 0, err
	}
	if downloaded > math.MaxUint64-cursorBytes {
		return 0, backupartifact.ErrInvalidObject
	}
	return downloaded + cursorBytes, nil
}

func (r *MaterializedCheckpointBaselineReplayer) replayBaselineCursor(
	ctx context.Context,
	repository backupartifact.Repository,
	slot backupartifact.CheckpointSlot,
	cut backupartifact.PartitionCut,
	sink CheckpointRestoreRecordSink,
) (uint64, error) {
	reference := slot.Baseline.MessageCursor
	weight, ok := checkpointRestoreMemoryWeight(
		reference.PlaintextBytes, 2,
	)
	if !ok || weight > r.budget.memoryMax {
		return 0, fmt.Errorf(
			"backup checkpoint baseline cursor exceeds memory budget",
		)
	}
	if err := r.budget.memoryBudget.Acquire(ctx, weight); err != nil {
		return 0, err
	}
	defer r.budget.memoryBudget.Release(weight)
	body, header, err := r.segments.LoadCopyWithHeader(
		ctx, repository, reference,
	)
	if err != nil {
		return 0, err
	}
	cursor, err := backupartifact.LoadMessageCursorBatch(body)
	if err != nil {
		return 0, err
	}
	if header.Logical.HashSlot != slot.HashSlot ||
		header.Logical.Generation != slot.Generation ||
		header.Logical.Stream !=
			backupartifact.SegmentStreamMessageBaselineCursor ||
		header.Logical.Sequence != 1 ||
		header.Logical.RecordCount != checkpointRestoreCursorRecordCount(
			cursor.Boundaries,
		) ||
		cursor.HashSlot != slot.HashSlot ||
		cursor.Generation != slot.Generation ||
		cursor.Sequence != 1 || !cursor.Checkpoint ||
		cursor.Previous != nil ||
		cursor.SourceHighWatermark != cut.RaftIndex ||
		cursor.WatermarkAtUnixMillis != cut.CommittedAtMillis {
		return 0, backupartifact.ErrObjectCorrupt
	}
	for _, boundary := range cursor.Boundaries {
		if err := sink.Boundary(boundary); err != nil {
			return 0, err
		}
	}
	return uint64(len(body)), nil
}

func (r *MaterializedCheckpointBaselineReplayer) withStagedStream(
	ctx context.Context,
	repository backupartifact.Repository,
	objects []backupartifact.ObjectEntry,
	replay func(*os.File, int64) error,
) (total uint64, returnErr error) {
	if len(objects) == 0 || replay == nil {
		return 0, backupartifact.ErrInvalidManifest
	}
	for _, object := range objects {
		if object.PlaintextBytes <= 0 || object.CiphertextBytes <= 0 ||
			uint64(object.PlaintextBytes) >
				r.budget.options.StagingMaxBytes-total {
			return 0, fmt.Errorf(
				"backup checkpoint baseline exceeds staging budget",
			)
		}
		total += uint64(object.PlaintextBytes)
	}
	file, err := os.CreateTemp(
		r.budget.options.StagingDir, "checkpoint-baseline-*.stage",
	)
	if err != nil {
		return 0, err
	}
	path := file.Name()
	if err := r.budget.stagingQuota.reserveClaim(
		path, path, total,
	); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return 0, err
	}
	defer func() {
		_ = os.Remove(path)
		_ = r.budget.stagingQuota.settleClaim(path)
	}()
	defer file.Close()
	for _, object := range objects {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		weight, ok := baselineObjectMemoryWeight(object)
		if !ok || weight > r.budget.memoryMax {
			return 0, fmt.Errorf(
				"backup checkpoint baseline object exceeds memory budget",
			)
		}
		if err := r.budget.memoryBudget.Acquire(ctx, weight); err != nil {
			return 0, err
		}
		ciphertext, readErr := readRepositoryObject(
			ctx, repository, object.Key,
			object.CiphertextBytes, object.CiphertextSHA256,
		)
		var plaintext []byte
		if readErr == nil {
			plaintext, readErr = r.codec.Open(ctx, object, ciphertext)
		}
		if readErr == nil {
			_, readErr = file.Write(plaintext)
		}
		r.budget.memoryBudget.Release(weight)
		if readErr != nil {
			return 0, readErr
		}
	}
	if total > math.MaxInt64 {
		return 0, backupartifact.ErrInvalidObject
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if err := replay(file, int64(total)); err != nil {
		return 0, err
	}
	return total, nil
}

func baselineObjectMemoryWeight(
	object backupartifact.ObjectEntry,
) (int64, bool) {
	if object.PlaintextBytes <= 0 || object.CiphertextBytes <= 0 ||
		object.PlaintextBytes > math.MaxInt64-object.CiphertextBytes {
		return 0, false
	}
	return object.PlaintextBytes + object.CiphertextBytes, true
}

var _ CheckpointBaselineReplayer = (*MaterializedCheckpointBaselineReplayer)(nil)
