package backup

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// normalizeFrontier validates and detaches one durable Slot frontier.
func (e *CaptureEngine) normalizeFrontier(hashSlot uint16, snapshot FrontierSnapshot) (backupcontract.SlotFrontier, error) {
	if !snapshot.Found {
		if snapshot.Frontier != (backupcontract.SlotFrontier{}) {
			return backupcontract.SlotFrontier{}, fmt.Errorf("%w: missing frontier has state", ErrInvalidCapture)
		}
		return backupcontract.SlotFrontier{HashSlot: hashSlot, Generation: e.options.InitialGeneration}, nil
	}
	frontier := backupcontract.CloneSlotFrontier(snapshot.Frontier)
	if frontier.HashSlot != hashSlot || !validContinuousIdentity(frontier.Generation, 128) || frontier.Revision == 0 ||
		!validSlotCaptureLease(frontier.Lease, frontier.Generation) ||
		frontier.SourceSlotID == 0 ||
		frontier.GenerationStartedAtUnixMillis <= 0 ||
		frontier.GenerationStartedAtUnixMillis > frontier.UpdatedAtUnixMillis ||
		frontier.SourcePinStartedAtUnixMillis <= 0 ||
		frontier.SourcePinStartedAtUnixMillis > frontier.UpdatedAtUnixMillis ||
		validateSlotBaseline(frontier.Baseline, hashSlot) != nil ||
		validateSlotRebase(frontier.Rebase, frontier.Generation) != nil ||
		validateStreamFrontier(backupartifact.SegmentStreamMetadata, frontier.Metadata) != nil ||
		validateStreamFrontier(backupartifact.SegmentStreamMessages, frontier.Messages) != nil ||
		(frontier.Baseline == nil) != (frontier.Messages.BaselineCursorHead == nil) ||
		frontier.WatermarkAtUnixMillis != olderPositiveTime(frontier.Metadata.WatermarkAtUnixMillis, frontier.Messages.WatermarkAtUnixMillis) {
		return backupcontract.SlotFrontier{}, fmt.Errorf("%w: durable frontier is invalid", ErrInvalidCapture)
	}
	return frontier, nil
}

func validateSlotBaseline(reference *backupcontract.SlotBaselineReference, hashSlot uint16) error {
	if reference == nil {
		return nil
	}
	partition := reference.Partition
	if partition.HashSlot != hashSlot || partition.Key == "" ||
		!validLowerSHA256(partition.SHA256) || partition.Bytes <= 0 ||
		partition.ObjectCount == 0 || partition.CiphertextBytes == 0 ||
		partition.Evidence.Version != backupartifact.PartitionEvidenceVersion ||
		(partition.Evidence.MessageRecords == 0) != (partition.Evidence.MaxMessageID == 0) {
		return ErrInvalidCapture
	}
	return nil
}

func validateSlotRebase(rebase *backupcontract.SlotRebase, generation string) error {
	if rebase == nil {
		return nil
	}
	if !validContinuousIdentity(rebase.TargetGeneration, 128) ||
		rebase.TargetGeneration == generation || rebase.Epoch == 0 ||
		rebase.StartedAtUnixMillis <= 0 {
		return ErrInvalidCapture
	}
	switch rebase.Reason {
	case backupcontract.RebaseReasonPinAge,
		backupcontract.RebaseReasonNodeByteBudget,
		backupcontract.RebaseReasonSourceCompacted,
		backupcontract.RebaseReasonSourceRemapped,
		backupcontract.RebaseReasonGenerationBytes,
		backupcontract.RebaseReasonGenerationSegments,
		backupcontract.RebaseReasonGenerationAge,
		backupcontract.RebaseReasonAuditCorruption:
		return nil
	default:
		return ErrInvalidCapture
	}
}

func validSlotCaptureLease(lease backupcontract.SlotCaptureLease, generation string) bool {
	return lease.SlotID > 0 && lease.LeaderTerm > 0 && lease.ConfigEpoch > 0 &&
		lease.HolderNodeID > 0 && lease.Sequence > 0 && lease.AcquiredAtUnixMillis > 0 &&
		lease.Generation == generation && validContinuousIdentity(lease.Generation, 128)
}

func validateRollingPolicy(policy RollingPolicy) error {
	if policy.TargetSegmentBytes <= 0 || policy.TargetSegmentBytes > policy.MaxSegmentBytes ||
		policy.MaxSegmentBytes <= 0 || policy.MaxSegmentBytes > MaxCaptureSegmentBytes ||
		policy.MaxOpenDuration <= 0 || policy.PageRecords <= 0 || policy.PageRecords > 1<<20 ||
		policy.PagesPerReconcile <= 0 || policy.PagesPerReconcile > 1<<20 {
		return fmt.Errorf("%w: continuous capture rolling policy is invalid", ErrInvalidCapture)
	}
	return nil
}

func validateSourceWatermarks(frontier backupcontract.SlotFrontier, watermarks SourceWatermarks) error {
	if watermarks.Metadata.CommittedAtUnixMillis <= 0 || watermarks.Messages.CommittedAtUnixMillis <= 0 {
		return fmt.Errorf("%w: source watermark time is invalid", ErrInvalidCapture)
	}
	if watermarks.Metadata.CutCursor != "" ||
		len(watermarks.Messages.CutCursor) > 8<<10 ||
		!utf8.ValidString(watermarks.Messages.CutCursor) {
		return fmt.Errorf("%w: source watermark cursor is invalid", ErrInvalidCapture)
	}
	if (watermarks.Metadata.DiscoveryPending && !watermarks.Metadata.ReconcilePending) ||
		(watermarks.Messages.DiscoveryPending && !watermarks.Messages.ReconcilePending) {
		return fmt.Errorf("%w: source discovery continuation is invalid", ErrInvalidCapture)
	}
	if watermarks.Messages.Position > frontier.Messages.SourceHighWatermark &&
		watermarks.Messages.CutCursor == "" {
		return fmt.Errorf("%w: message source watermark has no exact cut cursor", ErrInvalidCapture)
	}
	if watermarks.Metadata.Position < frontier.Metadata.SourceHighWatermark ||
		watermarks.Messages.Position < frontier.Messages.SourceHighWatermark {
		return ErrSourceRegressed
	}
	return nil
}

// validateSourcePage proves cursor progress, stream-specific index shape, and
// the exact pre-encoding bytes charged to the node capture budget.
type sourcePageAccounting struct {
	encodedBytes int64
	memoryBytes  int64
}

func validateSourcePage(stream backupartifact.SegmentStream, previousCursor string, previousPosition, throughPosition uint64, page SourcePage, policy RollingPolicy) (sourcePageAccounting, error) {
	if page.NextCursor == previousCursor || len(page.NextCursor) > 8<<10 || !utf8.ValidString(page.NextCursor) ||
		page.NextPosition <= previousPosition || page.NextPosition > throughPosition ||
		page.Done != (page.NextPosition == throughPosition) ||
		len(page.Records) > policy.PageRecords {
		return sourcePageAccounting{}, fmt.Errorf("%w: source page cursor or count is invalid", ErrInvalidCapture)
	}
	if stream == backupartifact.SegmentStreamMetadata && len(page.MessageCursors) != 0 {
		return sourcePageAccounting{}, fmt.Errorf("%w: metadata source page contains message cursors", ErrInvalidCapture)
	}
	if stream == backupartifact.SegmentStreamMessages &&
		((len(page.Records) > 0 && len(page.MessageCursors) == 0) ||
			len(page.MessageCursors) > len(page.Records)) {
		return sourcePageAccounting{}, fmt.Errorf("%w: message source page cursor index is invalid", ErrInvalidCapture)
	}
	var accounting sourcePageAccounting
	for _, record := range page.Records {
		if len(record) == 0 || int64(len(record)) > policy.MaxSegmentBytes ||
			accounting.encodedBytes > math.MaxInt64-4-int64(len(record)) ||
			accounting.memoryBytes > math.MaxInt64-captureRecordHeapOverheadBytes-int64(len(record)) {
			return sourcePageAccounting{}, fmt.Errorf("%w: source page record is invalid", ErrInvalidCapture)
		}
		accounting.encodedBytes += 4 + int64(len(record))
		accounting.memoryBytes += captureRecordHeapOverheadBytes + int64(len(record))
	}
	seenCursors := make(map[channelCursorIdentity]struct{}, len(page.MessageCursors))
	for _, cursor := range page.MessageCursors {
		identity := channelCursorIdentity{channelType: cursor.ChannelType, channelID: cursor.ChannelID}
		if len(cursor.ChannelID) == 0 || len(cursor.ChannelID) > 4<<10 || cursor.Epoch == 0 ||
			cursor.LogStartOffset > cursor.HW {
			return sourcePageAccounting{}, fmt.Errorf("%w: source page message cursor is invalid", ErrInvalidCapture)
		}
		if _, exists := seenCursors[identity]; exists {
			return sourcePageAccounting{}, fmt.Errorf("%w: source page has duplicate message cursor", ErrInvalidCapture)
		}
		seenCursors[identity] = struct{}{}
		if accounting.encodedBytes > math.MaxInt64-int64(len(cursor.ChannelID))-32 ||
			accounting.memoryBytes > math.MaxInt64-captureCursorHeapOverheadBytes-int64(len(cursor.ChannelID)) {
			return sourcePageAccounting{}, fmt.Errorf("%w: source page cursor bytes overflow", ErrInvalidCapture)
		}
		accounting.encodedBytes += int64(len(cursor.ChannelID)) + 32
		accounting.memoryBytes += captureCursorHeapOverheadBytes + int64(len(cursor.ChannelID))
	}
	if accounting.encodedBytes > policy.MaxSegmentBytes {
		return sourcePageAccounting{}, fmt.Errorf("%w: source page exceeds rolling hard limit", ErrInvalidCapture)
	}
	return accounting, nil
}

func olderPositiveTime(left, right int64) int64 {
	if left <= 0 {
		return right
	}
	if right <= 0 || left < right {
		return left
	}
	return right
}

func slotFrontiersEqual(left, right backupcontract.SlotFrontier) bool {
	return left.Revision == right.Revision && left.HashSlot == right.HashSlot &&
		left.Generation == right.Generation &&
		left.GenerationStartedAtUnixMillis == right.GenerationStartedAtUnixMillis &&
		left.SourceSlotID == right.SourceSlotID &&
		left.SourcePinStartedAtUnixMillis == right.SourcePinStartedAtUnixMillis &&
		backupcontract.SlotCaptureLeasesEqual(left.Lease, right.Lease) &&
		slotBaselinesEqual(left.Baseline, right.Baseline) &&
		slotRebasesEqual(left.Rebase, right.Rebase) &&
		streamFrontiersEqual(left.Metadata, right.Metadata) &&
		streamFrontiersEqual(left.Messages, right.Messages) &&
		left.WatermarkAtUnixMillis == right.WatermarkAtUnixMillis &&
		left.UpdatedAtUnixMillis == right.UpdatedAtUnixMillis
}

func slotBaselinesEqual(left, right *backupcontract.SlotBaselineReference) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func slotRebasesEqual(left, right *backupcontract.SlotRebase) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func streamFrontiersEqual(left, right backupcontract.StreamFrontier) bool {
	if left.Sequence != right.Sequence || left.SourceCursor != right.SourceCursor ||
		left.SourceHighWatermark != right.SourceHighWatermark ||
		left.WatermarkAtUnixMillis != right.WatermarkAtUnixMillis ||
		left.CapturedPlaintextBytes != right.CapturedPlaintextBytes {
		return false
	}
	if left.Head == nil || right.Head == nil {
		if left.Head != nil || right.Head != nil {
			return false
		}
	} else if *left.Head != *right.Head {
		return false
	}
	if left.CursorHead == nil || right.CursorHead == nil {
		if left.CursorHead != nil || right.CursorHead != nil {
			return false
		}
	} else if *left.CursorHead != *right.CursorHead {
		return false
	}
	if left.BaselineCursorHead == nil || right.BaselineCursorHead == nil {
		return left.BaselineCursorHead == nil && right.BaselineCursorHead == nil
	}
	return *left.BaselineCursorHead == *right.BaselineCursorHead
}

func cloneRuntimeSegmentReference(reference *backupartifact.SegmentReference) *backupartifact.SegmentReference {
	if reference == nil {
		return nil
	}
	copy := *reference
	return &copy
}

func validateStreamFrontier(stream backupartifact.SegmentStream, frontier backupcontract.StreamFrontier) error {
	if len(frontier.SourceCursor) > 8<<10 || !utf8.ValidString(frontier.SourceCursor) {
		return ErrInvalidCapture
	}
	if frontier.Sequence == 0 {
		if frontier.Head != nil || frontier.CursorHead != nil {
			return ErrInvalidCapture
		}
	} else {
		if frontier.Head == nil {
			return ErrInvalidCapture
		}
		if err := validateCommittedSegmentReference(*frontier.Head); err != nil {
			return err
		}
		if stream == backupartifact.SegmentStreamMessages {
			if frontier.CursorHead == nil {
				return ErrInvalidCapture
			}
			if err := validateCommittedSegmentReference(*frontier.CursorHead); err != nil {
				return err
			}
		} else if frontier.CursorHead != nil {
			return ErrInvalidCapture
		}
	}
	if stream == backupartifact.SegmentStreamMetadata && frontier.BaselineCursorHead != nil {
		return ErrInvalidCapture
	}
	if frontier.BaselineCursorHead != nil {
		if err := validateCommittedSegmentReference(*frontier.BaselineCursorHead); err != nil {
			return err
		}
	}
	if frontier.WatermarkAtUnixMillis < 0 {
		return ErrInvalidCapture
	}
	return nil
}

func validateCommittedSegmentReference(reference backupartifact.SegmentReference) error {
	if !validLowerSHA256(reference.SegmentID) || !validLowerSHA256(reference.CommitSHA256) ||
		reference.PlaintextBytes <= 0 || reference.PlaintextBytes > MaxCaptureSegmentBytes ||
		reference.CommitKey != "segments/"+reference.SegmentID+"/commit.json" {
		return fmt.Errorf("%w: committed segment reference is invalid", ErrInvalidCapture)
	}
	return nil
}

func validLowerSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validContinuousIdentity(value string, maxBytes int) bool {
	if len(value) == 0 || len(value) > maxBytes {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' ||
			(char == '.' && index > 0) {
			continue
		}
		return false
	}
	return !strings.Contains(value, "..")
}
