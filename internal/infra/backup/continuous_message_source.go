package backup

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	messageSourceMetaPageSize  = 256
	messageSourceCursorVersion = 2
	messageSourceMaxHints      = 8192
	messageSourceMaxSelected   = 8192
)

// MessageLogNode is the narrow real Slot metadata and Channel committed-log source.
// cluster.Node implements local and remote Channel-leader routing behind this seam.
type MessageLogNode interface {
	ListBackupChannelRuntimeMetaPage(context.Context, uint16, metadb.ChannelRuntimeMetaCursor, int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
	GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
	ObserveBackupMessageChannel(context.Context, clusterpkg.BackupMessageChannelRequest) (clusterpkg.BackupMessageChannelBoundary, error)
	ObserveBackupMessageChannels(context.Context, []clusterpkg.BackupMessageChannelRequest) ([]clusterpkg.BackupMessageChannelBoundary, error)
	ReadBackupMessageLogPage(context.Context, clusterpkg.BackupMessageLogPageRequest) (clusterpkg.BackupMessageLogPage, error)
}

// MessageBoundaryResolver reconstructs the exact durable per-Channel cursor cut.
type MessageBoundaryResolver interface {
	// Resolve returns an immutable sorted boundary view.
	Resolve(context.Context, MessageCursorResolveRequest) ([]backupartifact.ChannelBoundary, error)
}

// MessageLogSource reconciles actual committed Channel logs against the
// immutable cursor artifact referenced by the durable message frontier.
type MessageLogSource struct {
	node    MessageLogNode
	cursors MessageBoundaryResolver
	mu      sync.Mutex
	scans   map[uint16]*messageSourceScan
}

// messageSourceScan is bounded, disposable acceleration state. Correctness
// does not depend on it: restart resumes a paged sweep from the durable cursor.
type messageSourceScan struct {
	mu                  sync.Mutex
	generation          string
	sequence            uint64
	sourceCursor        string
	sourceHighWatermark uint64
	after               metadb.ChannelRuntimeMetaCursor
	start               metadb.ChannelRuntimeMetaCursor
	wrapped             bool
	position            uint64
	pending             string
	selected            map[messageSourceIdentity]struct{}
	hints               []messageSourceIdentity
	hintSet             map[messageSourceIdentity]struct{}
}

// HintChannel records a lossy, bounded in-memory acceleration hint. A dropped
// hint is safe because HighWatermark also advances an authoritative paged scan.
func (s *MessageLogSource) HintChannel(hashSlot uint16, channelID string, channelType uint8) bool {
	if s == nil || channelID == "" || len(channelID) > 4<<10 || !utf8.ValidString(channelID) {
		return false
	}
	scan := s.scan(hashSlot)
	scan.mu.Lock()
	defer scan.mu.Unlock()
	identity := messageSourceIdentity{channelID: channelID, channelType: channelType}
	if _, exists := scan.hintSet[identity]; exists {
		return true
	}
	if len(scan.hints) >= messageSourceMaxHints {
		return false
	}
	if scan.hintSet == nil {
		scan.hintSet = make(map[messageSourceIdentity]struct{})
	}
	scan.hintSet[identity] = struct{}{}
	scan.hints = append(scan.hints, identity)
	return true
}

// NewMessageLogSource creates a restart-safe committed-message source.
func NewMessageLogSource(node MessageLogNode, cursors MessageBoundaryResolver) (*MessageLogSource, error) {
	if node == nil || cursors == nil {
		return nil, fmt.Errorf("backup message log source: node and cursor resolver are required")
	}
	return &MessageLogSource{
		node: node, cursors: cursors, scans: make(map[uint16]*messageSourceScan),
	}, nil
}

// HighWatermark observes at most one metadata page and pins at most one exact
// Channel cut. Repeated calls advance a disposable paged sweep; committed
// partial cuts remain fully recoverable from the durable source cursor.
func (s *MessageLogSource) HighWatermark(ctx context.Context, hashSlot uint16, generation string, frontier backupcontract.StreamFrontier) (runtimebackup.SourceWatermark, error) {
	durableCursor, err := decodeMessageSourceCursor(frontier.SourceCursor)
	if err != nil || durableCursor.position() != frontier.SourceHighWatermark {
		return runtimebackup.SourceWatermark{}, runtimebackup.ErrInvalidCapture
	}
	if durableCursor.active() {
		return runtimebackup.SourceWatermark{
			Position:              durableCursor.TargetPosition,
			CommittedAtUnixMillis: durableCursor.Boundary.ObservedAtUnixMillis,
			CutCursor:             frontier.SourceCursor,
		}, nil
	}
	base, releaseBoundaries, err := s.resolveBoundaries(ctx, hashSlot, generation, frontier)
	if err != nil {
		return runtimebackup.SourceWatermark{}, err
	}
	defer releaseBoundaries()
	scan := s.scan(hashSlot)
	scan.mu.Lock()
	defer scan.mu.Unlock()
	if scan.generation != generation || scan.sequence != frontier.Sequence ||
		scan.sourceCursor != frontier.SourceCursor ||
		scan.sourceHighWatermark != frontier.SourceHighWatermark {
		scan.generation = generation
		scan.sequence = frontier.Sequence
		scan.sourceCursor = frontier.SourceCursor
		scan.sourceHighWatermark = frontier.SourceHighWatermark
		scan.after = durableCursor.After
		scan.start = durableCursor.After
		scan.wrapped = false
		scan.position = frontier.SourceHighWatermark
		scan.pending = ""
		scan.selected = make(map[messageSourceIdentity]struct{})
	}
	if scan.pending != "" {
		cursor, err := decodeMessageSourceCursor(scan.pending)
		if err != nil || cursor.Boundary == nil {
			return runtimebackup.SourceWatermark{}, runtimebackup.ErrInvalidCapture
		}
		return runtimebackup.SourceWatermark{
			Position:              cursor.TargetPosition,
			CommittedAtUnixMillis: cursor.Boundary.ObservedAtUnixMillis,
			CutCursor:             scan.pending,
		}, nil
	}
	if len(scan.selected) >= messageSourceMaxSelected {
		return runtimebackup.SourceWatermark{
			Position: frontier.SourceHighWatermark, CommittedAtUnixMillis: time.Now().UTC().UnixMilli(),
			ReconcilePending: true,
		}, nil
	}
	for len(scan.hints) > 0 {
		identity := scan.hints[0]
		scan.hints = scan.hints[1:]
		delete(scan.hintSet, identity)
		if _, alreadySelected := scan.selected[identity]; alreadySelected {
			continue
		}
		meta, err := s.node.GetChannelRuntimeMeta(ctx, identity.channelID, int64(identity.channelType))
		if err != nil {
			return runtimebackup.SourceWatermark{}, err
		}
		if meta.ChannelID != identity.channelID || meta.ChannelType != int64(identity.channelType) {
			return runtimebackup.SourceWatermark{}, runtimebackup.ErrInvalidCapture
		}
		channel, err := messageChannelRequest(hashSlot, meta)
		if err != nil {
			return runtimebackup.SourceWatermark{}, err
		}
		boundary, err := s.node.ObserveBackupMessageChannel(ctx, channel)
		if err != nil {
			return runtimebackup.SourceWatermark{}, err
		}
		previous := base.lookup(identity)
		watermark, found, err := pinMessageSourceTarget(
			scan, previous, channel, boundary, false,
		)
		if err != nil {
			return runtimebackup.SourceWatermark{}, err
		}
		if found {
			watermark.ReconcilePending = true
			watermark.DiscoveryPending = true
			return watermark, nil
		}
	}

	metas, next, done, err := s.node.ListBackupChannelRuntimeMetaPage(
		ctx, hashSlot, scan.after, messageSourceMetaPageSize,
	)
	if err != nil {
		return runtimebackup.SourceWatermark{}, err
	}
	sweepDone := false
	if scan.wrapped && scan.start != (metadb.ChannelRuntimeMetaCursor{}) {
		keep := 0
		for keep < len(metas) &&
			!messageMetaCursorLess(scan.start, runtimeMetaCursor(metas[keep])) {
			keep++
		}
		sweepDone = keep < len(metas) ||
			(keep > 0 && runtimeMetaCursor(metas[keep-1]) == scan.start)
		metas = metas[:keep]
	}
	if len(metas) == 0 {
		if !done {
			if !sweepDone {
				return runtimebackup.SourceWatermark{}, fmt.Errorf("backup message log source: metadata page made no progress")
			}
		} else if scan.start != (metadb.ChannelRuntimeMetaCursor{}) && !scan.wrapped {
			scan.after = metadb.ChannelRuntimeMetaCursor{}
			scan.wrapped = true
			return runtimebackup.SourceWatermark{
				Position: frontier.SourceHighWatermark, CommittedAtUnixMillis: time.Now().UTC().UnixMilli(),
				ReconcilePending: true, DiscoveryPending: true,
			}, nil
		}
		scan.resetSweep()
		return runtimebackup.SourceWatermark{
			Position: frontier.SourceHighWatermark, CommittedAtUnixMillis: time.Now().UTC().UnixMilli(),
			ReconcilePending: scan.position > frontier.SourceHighWatermark,
		}, nil
	}
	requests := make([]clusterpkg.BackupMessageChannelRequest, len(metas))
	for index, meta := range metas {
		requests[index], err = messageChannelRequest(hashSlot, meta)
		if err != nil {
			return runtimebackup.SourceWatermark{}, err
		}
	}
	boundaries, err := s.node.ObserveBackupMessageChannels(ctx, requests)
	if err != nil {
		return runtimebackup.SourceWatermark{}, err
	}
	if len(boundaries) != len(requests) {
		return runtimebackup.SourceWatermark{}, runtimebackup.ErrInvalidCapture
	}
	for index, request := range requests {
		identity := messageSourceIdentity{channelID: request.ChannelID, channelType: request.ChannelType}
		if _, alreadySelected := scan.selected[identity]; alreadySelected {
			continue
		}
		boundary := boundaries[index]
		previous := base.lookup(identity)
		watermark, found, err := pinMessageSourceTarget(
			scan, previous, request, boundary, true,
		)
		if err != nil {
			return runtimebackup.SourceWatermark{}, err
		}
		if found {
			watermark.ReconcilePending = true
			watermark.DiscoveryPending = true
			return watermark, nil
		}
	}
	if !done && !sweepDone && next == scan.after {
		return runtimebackup.SourceWatermark{}, fmt.Errorf("backup message log source: metadata cursor made no progress")
	}
	if sweepDone || (done && (scan.start == (metadb.ChannelRuntimeMetaCursor{}) || scan.wrapped)) {
		scan.resetSweep()
		return runtimebackup.SourceWatermark{
			Position: frontier.SourceHighWatermark, CommittedAtUnixMillis: time.Now().UTC().UnixMilli(),
			ReconcilePending: scan.position > frontier.SourceHighWatermark,
		}, nil
	}
	if done {
		scan.after = metadb.ChannelRuntimeMetaCursor{}
		scan.wrapped = true
	} else {
		scan.after = next
	}
	return runtimebackup.SourceWatermark{
		Position: frontier.SourceHighWatermark, CommittedAtUnixMillis: time.Now().UTC().UnixMilli(),
		ReconcilePending: true, DiscoveryPending: true,
	}, nil
}

func pinMessageSourceTarget(
	scan *messageSourceScan,
	previous backupartifact.ChannelBoundary,
	request clusterpkg.BackupMessageChannelRequest,
	boundary clusterpkg.BackupMessageChannelBoundary,
	advanceSweep bool,
) (runtimebackup.SourceWatermark, bool, error) {
	work, err := runtimebackup.PendingMessageWork(previous, observedMessageBoundary(boundary))
	if err != nil {
		return runtimebackup.SourceWatermark{}, false, err
	}
	if work == 0 {
		return runtimebackup.SourceWatermark{}, false, nil
	}
	if scan.position > math.MaxUint64-work {
		return runtimebackup.SourceWatermark{}, false, fmt.Errorf("backup message log source: watermark overflow")
	}
	firstSeq, err := runtimebackup.FirstPendingMessageSeq(previous, observedMessageBoundary(boundary))
	if err != nil {
		return runtimebackup.SourceWatermark{}, false, err
	}
	basePosition := scan.position
	scan.position += work
	rotation := metadb.ChannelRuntimeMetaCursor{
		ChannelID: request.ChannelID, ChannelType: int64(request.ChannelType),
	}
	if advanceSweep {
		scan.after = rotation
	}
	identity := messageSourceIdentity{channelID: request.ChannelID, channelType: request.ChannelType}
	scan.selected[identity] = struct{}{}
	cursor := messageSourceCursor{
		Version: messageSourceCursorVersion, BasePosition: basePosition,
		TargetPosition: scan.position, Boundary: &boundary,
		PreviousEpoch: previous.Epoch, PreviousStart: previous.LogStartOffset,
		PreviousHW: previous.HW,
	}
	if firstSeq <= boundary.HW {
		cursor.NextSeq = firstSeq
	}
	scan.pending, err = marshalMessageSourceCursor(cursor)
	if err != nil {
		return runtimebackup.SourceWatermark{}, false, err
	}
	return runtimebackup.SourceWatermark{
		Position: scan.position, CommittedAtUnixMillis: boundary.ObservedAtUnixMillis,
		CutCursor: scan.pending,
	}, true, nil
}

// ReadPage replays only the Channel boundary fixed by ThroughCursor. Newer
// commits in that Channel are left for a later cut and cannot displace another
// Channel that existed when this cut was observed.
func (s *MessageLogSource) ReadPage(ctx context.Context, request runtimebackup.SourcePageRequest) (runtimebackup.SourcePage, error) {
	if request.Stream != backupartifact.SegmentStreamMessages || request.ThroughPosition == 0 ||
		request.ThroughCursor == "" ||
		request.MaxBytes <= 0 || request.MaxRecordBytes < request.MaxBytes ||
		request.MaxRecords <= 0 {
		return runtimebackup.SourcePage{}, runtimebackup.ErrInvalidCapture
	}
	cursor, err := parseMessageSourceCursor(
		request.AfterCursor, request.ThroughCursor, request.ThroughPosition,
	)
	if err != nil {
		return runtimebackup.SourcePage{}, err
	}
	if cursor.Boundary == nil ||
		cursor.TargetPosition != request.ThroughPosition {
		return runtimebackup.SourcePage{}, runtimebackup.ErrInvalidCapture
	}
	previous := backupartifact.ChannelBoundary{
		ChannelID: cursor.Boundary.ChannelID, ChannelType: cursor.Boundary.ChannelType,
		Epoch: cursor.PreviousEpoch, LogStartOffset: cursor.PreviousStart, HW: cursor.PreviousHW,
	}
	if cursor.PreviousEpoch == 0 {
		previous = backupartifact.ChannelBoundary{}
	}
	target := *cursor.Boundary
	required, err := runtimebackup.PendingMessageWork(
		previous, observedMessageBoundary(target),
	)
	if err != nil || required != cursor.TargetPosition-cursor.position() {
		return runtimebackup.SourcePage{}, runtimebackup.ErrInvalidCapture
	}
	page := runtimebackup.SourcePage{
		Records:        make([][]byte, 0, min(request.MaxRecords, 64)),
		MessageCursors: make([]backupartifact.ChannelBoundary, 0, 1),
	}
	meta, err := s.node.GetChannelRuntimeMeta(
		ctx, target.ChannelID, int64(target.ChannelType),
	)
	if err != nil {
		return runtimebackup.SourcePage{}, err
	}
	channel, err := messageChannelRequest(request.HashSlot, meta)
	if err != nil {
		return runtimebackup.SourcePage{}, err
	}
	if target.HashSlot != request.HashSlot ||
		channel.ChannelID != target.ChannelID || channel.ChannelType != target.ChannelType ||
		channel.ChannelEpoch != target.Epoch {
		return runtimebackup.SourcePage{}, runtimebackup.ErrSourceRegressed
	}
	// Retention must remain at or before the observed cut. Leader placement may
	// change, so only routing fields are refreshed.
	channel.RetentionSeq = target.LogStartOffset
	if cursor.NextSeq > 0 {
		logPage, err := s.node.ReadBackupMessageLogPage(ctx, clusterpkg.BackupMessageLogPageRequest{
			Channel: channel, FromSeq: cursor.NextSeq, ThroughSeq: target.HW,
			TargetBytes: request.MaxBytes, MaxBytes: request.MaxRecordBytes,
			MaxRecords: request.MaxRecords,
		})
		if err != nil {
			return runtimebackup.SourcePage{}, err
		}
		added, _, err := appendMessageLogSourcePage(&page, logPage, 0, request.MaxBytes)
		if err != nil {
			return runtimebackup.SourcePage{}, err
		}
		if added == 0 {
			return runtimebackup.SourcePage{}, fmt.Errorf("backup message log source: page made no progress")
		}
		cursor.Consumed += uint64(added)
		cursor.NextSeq = logPage.NextSeq
	} else {
		// No new payload exists, but an epoch/retention cursor change is data.
		record, err := backupartifact.MarshalMessageLogRecord(backupartifact.MessageLogRecord{
			Kind: backupartifact.MessageLogRecordBoundary, HashSlot: request.HashSlot,
			ChannelID: target.ChannelID, ChannelType: target.ChannelType,
			Epoch: target.Epoch, LogStartOffset: target.LogStartOffset, HW: target.HW,
		})
		if err != nil {
			return runtimebackup.SourcePage{}, err
		}
		recordBytes := int64(4 + len(record))
		if recordBytes > request.MaxRecordBytes {
			return runtimebackup.SourcePage{}, runtimebackup.ErrInvalidCapture
		}
		page.Records = append(page.Records, record)
		page.MessageCursors = append(page.MessageCursors, artifactBoundary(target))
		cursor.Consumed++
	}
	if len(page.Records) == 0 {
		return runtimebackup.SourcePage{}, fmt.Errorf("backup message log source: page made no progress")
	}
	page.Done = cursor.position() == cursor.TargetPosition
	page.NextPosition = cursor.position()
	if len(page.MessageCursors) > 0 {
		committed := page.MessageCursors[len(page.MessageCursors)-1]
		cursor.PreviousEpoch = committed.Epoch
		cursor.PreviousStart = committed.LogStartOffset
		cursor.PreviousHW = committed.HW
	}
	if page.Done {
		rotation := metadb.ChannelRuntimeMetaCursor{
			ChannelID: target.ChannelID, ChannelType: int64(target.ChannelType),
		}
		cursor = messageSourceCursor{
			Version: messageSourceCursorVersion, BasePosition: request.ThroughPosition,
			TargetPosition: request.ThroughPosition, After: rotation,
		}
	}
	page.NextCursor, err = marshalMessageSourceCursor(cursor)
	if err != nil {
		return runtimebackup.SourcePage{}, err
	}
	return page, nil
}

func (s *MessageLogSource) scan(hashSlot uint16) *messageSourceScan {
	s.mu.Lock()
	defer s.mu.Unlock()
	scan := s.scans[hashSlot]
	if scan == nil {
		scan = &messageSourceScan{hintSet: make(map[messageSourceIdentity]struct{})}
		s.scans[hashSlot] = scan
	}
	return scan
}

// AcknowledgeSourcePage releases a completed in-memory cut only after runtime
// validation and accumulator admission have succeeded.
func (s *MessageLogSource) AcknowledgeSourcePage(hashSlot uint16, stream backupartifact.SegmentStream, target string) {
	if stream != backupartifact.SegmentStreamMessages || target == "" {
		return
	}
	scan := s.scan(hashSlot)
	scan.mu.Lock()
	if scan.pending == target {
		scan.pending = ""
	}
	scan.mu.Unlock()
}

// InvalidateSourceState drops disposable selection and pending-cut state so a
// failed durable frontier publication is retried from its last committed cut.
func (s *MessageLogSource) InvalidateSourceState(hashSlot uint16) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.scans, hashSlot)
	s.mu.Unlock()
}

func observedMessageBoundary(boundary clusterpkg.BackupMessageChannelBoundary) runtimebackup.ObservedMessageBoundary {
	return runtimebackup.ObservedMessageBoundary{
		ChannelID: boundary.ChannelID, ChannelType: boundary.ChannelType,
		Epoch: boundary.Epoch, LogStartOffset: boundary.LogStartOffset, HW: boundary.HW,
		ObservedAtUnixMillis: boundary.ObservedAtUnixMillis,
	}
}

func (scan *messageSourceScan) resetSweep() {
	scan.after = scan.start
	scan.wrapped = false
}

func runtimeMetaCursor(meta metadb.ChannelRuntimeMeta) metadb.ChannelRuntimeMetaCursor {
	return metadb.ChannelRuntimeMetaCursor{ChannelID: meta.ChannelID, ChannelType: meta.ChannelType}
}

func messageMetaCursorLess(left, right metadb.ChannelRuntimeMetaCursor) bool {
	return left.ChannelID < right.ChannelID ||
		(left.ChannelID == right.ChannelID && left.ChannelType < right.ChannelType)
}

type messageBoundaryView struct {
	baseline []backupartifact.ChannelBoundary
	updates  []backupartifact.ChannelBoundary
}

func (v messageBoundaryView) lookup(identity messageSourceIdentity) backupartifact.ChannelBoundary {
	if boundary, found := findMessageBoundary(v.updates, identity); found {
		return boundary
	}
	boundary, _ := findMessageBoundary(v.baseline, identity)
	return boundary
}

func findMessageBoundary(
	boundaries []backupartifact.ChannelBoundary,
	identity messageSourceIdentity,
) (backupartifact.ChannelBoundary, bool) {
	index := sort.Search(len(boundaries), func(index int) bool {
		candidate := boundaries[index]
		return candidate.ChannelType > identity.channelType ||
			(candidate.ChannelType == identity.channelType &&
				candidate.ChannelID >= identity.channelID)
	})
	if index >= len(boundaries) ||
		boundaries[index].ChannelType != identity.channelType ||
		boundaries[index].ChannelID != identity.channelID {
		return backupartifact.ChannelBoundary{}, false
	}
	return boundaries[index], true
}

func (s *MessageLogSource) resolveBoundaries(ctx context.Context, hashSlot uint16, generation string, frontier backupcontract.StreamFrontier) (messageBoundaryView, func(), error) {
	var baseline []backupartifact.ChannelBoundary
	release := func() {}
	var resolvedBaseline *ResolvedBaseline
	var err error
	if frontier.BaselineCursorHead != nil {
		resolver, ok := s.cursors.(interface {
			ResolveBaseline(context.Context, uint16, backupartifact.SegmentReference) (*ResolvedBaseline, error)
		})
		if !ok {
			return messageBoundaryView{}, release, runtimebackup.ErrInvalidCapture
		}
		resolvedBaseline, err = resolver.ResolveBaseline(ctx, hashSlot, *frontier.BaselineCursorHead)
		if err != nil {
			return messageBoundaryView{}, release, err
		}
		release = resolvedBaseline.Release
		baseline = resolvedBaseline.Boundaries
	}
	if frontier.Sequence == 0 {
		if frontier.CursorHead != nil {
			release()
			return messageBoundaryView{}, func() {}, runtimebackup.ErrInvalidCapture
		}
		return messageBoundaryView{baseline: baseline}, release, nil
	}
	if frontier.CursorHead == nil {
		release()
		return messageBoundaryView{}, func() {}, runtimebackup.ErrInvalidCapture
	}
	updates, err := s.cursors.Resolve(ctx, MessageCursorResolveRequest{
		Head: *frontier.CursorHead, HashSlot: hashSlot,
		Generation: generation, Sequence: frontier.Sequence, SourceCursor: frontier.SourceCursor,
		SourceHighWatermark: frontier.SourceHighWatermark,
	})
	if err != nil {
		release()
		return messageBoundaryView{}, func() {}, err
	}
	return messageBoundaryView{baseline: baseline, updates: updates}, release, nil
}

var (
	_ ContinuousStreamSource               = (*MessageLogSource)(nil)
	_ runtimebackup.SourcePageAcknowledger = (*MessageLogSource)(nil)
	_ MessageLogNode                       = (*clusterpkg.Node)(nil)
)
