package meta

import (
	"context"
	"math"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

const (
	// MaxMessageUpdatePayload bounds a single durable replacement.
	MaxMessageUpdatePayload = 1 << 20
	// MaxMessageUpdatePageBytes bounds retained content in one metadata read.
	MaxMessageUpdatePageBytes = 8 << 20
	// MaxMessageUpdatePage bounds index work independently from channel size.
	MaxMessageUpdatePage = 200
)

// MessageUpdateMutation is one deterministic Slot command. Generation is an
// opaque incarnation allocated by the caller only when initializing a head.
type MessageUpdateMutation struct {
	// AfterUID advances notification work only for ExpectedVersion.
	AfterUID string
	// PreviousUID fences subscriber progress without assuming string sort order.
	PreviousUID string
	Op          string
	ChannelID   string
	ChannelType int64
	Generation  string
	// ReplicaSet is the proxy-verified activation proof for the current Slot replicas.
	ReplicaSet      string
	MessageID       uint64
	MessageSeq      uint64
	ExpectedVersion uint64
	// ExpectedContentEpoch is checked under the serving node restore-admission fence before enqueue.
	ExpectedContentEpoch    uint64
	ExpectedChannelEpoch    uint64
	ExpectedRouteGeneration uint64
	RequestID               string
	Digest                  string
	Payload                 []byte
	UpdatedAtMS             int64
}

// MessageUpdateMutationResult is resolved during batch commit, including
// expected business conflicts which must not fail the Raft state machine.
type MessageUpdateMutationResult struct {
	Status  string
	Head    MessageUpdateHead
	Request MessageUpdateRequest
}

// ValidateMessageUpdateMutation bounds commands before proposal and replay.
func ValidateMessageUpdateMutation(q MessageUpdateMutation) error {
	if err := validateChannelKey(ChannelKey{ChannelID: q.ChannelID, ChannelType: q.ChannelType}); err != nil {
		return err
	}
	if len(q.Generation) == 0 || len(q.Generation) > 128 || len(q.ReplicaSet) > 8192 {
		return ErrInvalidArgument
	}
	switch q.Op {
	case "init":
	case "prune":
		if q.MessageID == 0 {
			return ErrInvalidArgument
		}
	case "ack", "progress":
		if q.MessageID == 0 || q.ExpectedVersion == 0 || len(q.AfterUID) > maxKeyStringLen || len(q.PreviousUID) > maxKeyStringLen {
			return ErrInvalidArgument
		}
	case "update":
		if q.MessageID == 0 || q.MessageSeq == 0 || q.RequestID == "" || len(q.RequestID) > 128 || len(q.Digest) != 64 || len(q.Payload) == 0 || len(q.Payload) > MaxMessageUpdatePayload || q.UpdatedAtMS < 0 {
			return ErrInvalidArgument
		}
	default:
		return ErrInvalidArgument
	}
	return nil
}

func loadUpdateRow[R any](t Table[R], state *batchCommitState, slot HashSlot, pk KeyParts) (R, bool, error) {
	key, err := t.primaryRowKey(slot, pk)
	if err != nil {
		var zero R
		return zero, false, err
	}
	return t.loadBatchRow(state, slot, pk, key)
}

// stageUpdateRow maintains primary and secondary indexes in the same engine
// batch, and publishes an overlay for subsequent commands in the apply batch.
func stageUpdateRow[R any](t Table[R], state *batchCommitState, b *engine.Batch, slot HashSlot, row R) error {
	pk := t.spec.Primary.Key(row)
	key, err := t.primaryRowKey(slot, pk)
	if err != nil {
		return err
	}
	old, exists, err := t.loadBatchRow(state, slot, pk, key)
	if err != nil {
		return err
	}
	if exists {
		if err = t.stageDeleteIndexEntries(b, slot, old, pk); err != nil {
			return err
		}
	}
	value, err := t.encodeValue(key, row)
	if err != nil {
		return err
	}
	if err = b.Set(key, value); err != nil {
		return err
	}
	if err = t.stagePutIndexEntries(b, slot, row, pk, value); err != nil {
		return err
	}
	state.tableRows[string(key)] = tableRowOverlay{value: value, exists: true}
	return nil
}

// ApplyMessageUpdate atomically resolves CAS/idempotency, replaces the latest
// index entry and marks the target for notification. The result is valid only
// after the enclosing WriteBatch commits successfully.
func (b *WriteBatch) ApplyMessageUpdate(slot uint16, q MessageUpdateMutation) (*MessageUpdateMutationResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	if err := ValidateMessageUpdateMutation(q); err != nil {
		return nil, err
	}
	result := &MessageUpdateMutationResult{}
	q.Payload = append([]byte(nil), q.Payload...)
	hs := HashSlot(slot)
	cpk := KeyParts{String(q.ChannelID), Int64Ordered(q.ChannelType)}
	b.batch.addOp(hs, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MessageUpdateMutationResult{Status: "message_not_found"}
		channelKey := encodeChannelRowKey(hs, q.ChannelID, q.ChannelType, channelPrimaryFamilyID)
		channel, exists, err := state.loadChannel(ctx, channelKey, q.ChannelID, q.ChannelType)
		if err != nil {
			return err
		}
		if !exists || channel.Disband != 0 {
			return nil
		}
		head, headExists, err := loadUpdateRow(messageUpdateHeadTable, state, hs, cpk)
		if err != nil {
			return err
		}
		if q.Op == "init" {
			if !headExists {
				head = MessageUpdateHead{ChannelID: q.ChannelID, ChannelType: q.ChannelType, Generation: q.Generation, ReplicaSet: q.ReplicaSet}
				if err = stageUpdateRow(messageUpdateHeadTable, state, batch, hs, head); err != nil {
					return err
				}
			}
			result.Status = "ok"
			result.Head = head
			return nil
		}
		if !headExists || head.Generation != q.Generation {
			result.Status = "reset_required"
			return nil
		}
		if head.ReplicaSet != q.ReplicaSet {
			head.ReplicaSet = q.ReplicaSet
			if err = stageUpdateRow(messageUpdateHeadTable, state, batch, hs, head); err != nil {
				return err
			}
		}
		result.Head = head
		pk := append(append(KeyParts(nil), cpk...), Uint64(q.MessageID))
		old, found, err := loadUpdateRow(messageUpdateTable, state, hs, pk)
		if err != nil {
			return err
		}
		if q.Op == "ack" || q.Op == "progress" {
			pending, exists, e := loadUpdateRow(messageUpdatePendingTable, state, hs, pk)
			if e != nil {
				return e
			}
			result.Status = "ok"
			if exists && pending.Version == q.ExpectedVersion {
				if q.Op == "ack" {
					return deleteUpdateRow(messageUpdatePendingTable, state, batch, hs, pk)
				}
				if q.PreviousUID == pending.PendingAfterUID && q.AfterUID != pending.PendingAfterUID {
					pending.PendingAfterUID = q.AfterUID
					return stageUpdateRow(messageUpdatePendingTable, state, batch, hs, pending)
				}
			}
			return nil
		}
		runtimeKey := encodeChannelRuntimeMetaRowKey(hs, q.ChannelID, q.ChannelType, channelRuntimeMetaPrimaryFamilyID)
		runtime, exists, err := state.loadRuntimeMeta(ctx, hs, runtimeKey, q.ChannelID, q.ChannelType)
		if err != nil {
			return err
		}
		if q.Op == "prune" {
			result.Status = "ok"
			if exists && found && old.MessageSeq <= runtime.RetentionThroughSeq {
				return pruneMessageUpdate(state, batch, hs, pk)
			}
			return nil
		}
		if !exists || q.MessageSeq <= runtime.RetentionThroughSeq {
			return nil
		}
		if runtime.ChannelEpoch != q.ExpectedChannelEpoch || runtime.RouteGeneration != q.ExpectedRouteGeneration {
			result.Status = "stale_meta"
			return nil
		}
		requestPK := append(append(KeyParts(nil), pk...), String(q.RequestID))
		previous, duplicate, err := loadUpdateRow(messageUpdateRequestTable, state, hs, requestPK)
		if err != nil {
			return err
		}
		if duplicate {
			if previous.Digest != q.Digest {
				result.Status = "idempotency_conflict"
				return nil
			}
			result.Status = "ok"
			result.Request = previous
			return nil
		}
		if old.Version != q.ExpectedVersion || (found && old.MessageSeq != q.MessageSeq) {
			result.Status = "version_conflict"
			return nil
		}
		if head.UpdateSeq == math.MaxUint64 || old.Version == math.MaxUint64 {
			result.Status = "resource_exhausted"
			return nil
		}
		head.UpdateSeq++
		next := MessageUpdate{ChannelID: q.ChannelID, ChannelType: q.ChannelType, MessageID: q.MessageID, MessageSeq: q.MessageSeq, Version: old.Version + 1, UpdateSeq: head.UpdateSeq, Payload: q.Payload, UpdatedAtMS: q.UpdatedAtMS}
		applied := MessageUpdateRequest{ChannelID: q.ChannelID, ChannelType: q.ChannelType, MessageID: q.MessageID, RequestID: q.RequestID, Digest: q.Digest, MessageSeq: q.MessageSeq, Version: next.Version, UpdateSeq: head.UpdateSeq, UpdatedAtMS: q.UpdatedAtMS}
		if err = stageUpdateRow(messageUpdateTable, state, batch, hs, next); err != nil {
			return err
		}
		pending := next
		pending.Payload = nil
		pending.Pending = 1
		if err = stageUpdateRow(messageUpdatePendingTable, state, batch, hs, pending); err != nil {
			return err
		}
		if err = stageUpdateRow(messageUpdateHeadTable, state, batch, hs, head); err != nil {
			return err
		}
		if err = stageUpdateRow(messageUpdateRequestTable, state, batch, hs, applied); err != nil {
			return err
		}
		result.Status = "ok"
		result.Head = head
		result.Request = applied
		return nil
	})
	return result, nil
}

// MessageUpdateRead selects either exact IDs or a bounded update sequence page.
// A zero Through value captures the current head; callers preserve it in cursors.
type MessageUpdateRead struct {
	ChannelID   string
	ChannelType int64
	IDs         []uint64 `json:"IDs,omitempty"`
	// IncludePending selects only body-free pending rows for the dispatcher.
	IncludePending bool   `json:"IncludePending,omitempty"`
	After          uint64 `json:"After,omitempty"`
	Through        uint64 `json:"Through,omitempty"`
	Limit          int    `json:"Limit,omitempty"`
}

// MessageUpdatePage uses one pinned view for the head, index and payload rows.
type MessageUpdatePage struct {
	Head    MessageUpdateHead `json:"Head,omitzero"`
	Updates []MessageUpdate   `json:"Updates,omitempty"`
	Next    uint64            `json:"Next,omitempty"`
	Through uint64            `json:"Through,omitempty"`
	More    bool              `json:"More,omitempty"`
}

func snapshotUpdateRow[R any](snap *engine.Snapshot, t Table[R], slot HashSlot, pk KeyParts) (R, bool, error) {
	var zero R
	key, err := t.primaryRowKey(slot, pk)
	if err != nil {
		return zero, false, err
	}
	value, exists, err := snap.Get(key)
	if err != nil || !exists {
		return zero, exists, err
	}
	row, err := t.decodeValue(key, pk, value)
	return row, true, err
}

// ReadMessageUpdates reads only the requested channel prefix or exact IDs. It
// is node-local storage; the distributed caller must establish a read barrier.
func (s *ShardStore) ReadMessageUpdates(ctx context.Context, q MessageUpdateRead) (MessageUpdatePage, error) {
	var out MessageUpdatePage
	if err := s.validate(); err != nil {
		return out, err
	}
	if err := s.shard.check(ctx); err != nil {
		return out, err
	}
	if err := validateMessageUpdateRead(q); err != nil {
		return out, err
	}
	snap, err := s.shard.db.engine.NewSnapshot()
	if err != nil {
		return out, err
	}
	defer snap.Close()
	return readMessageUpdatesSnapshot(ctx, snap, s.shard.hashSlot, q)
}

func validateMessageUpdateRead(q MessageUpdateRead) error {
	if err := validateChannelKey(ChannelKey{ChannelID: q.ChannelID, ChannelType: q.ChannelType}); err != nil {
		return err
	}
	if len(q.IDs) > MaxMessageUpdatePage || q.Limit < 0 || q.Limit > MaxMessageUpdatePage {
		return ErrInvalidArgument
	}
	return nil
}

// ReadMessageUpdatesBatch pins one view for a bounded group of logical shards.
// Hash slots align with reads. Distributed callers must establish fresh authority
// and apply barriers before calling; the view and any empty proof end at return.
func (db *DB) ReadMessageUpdatesBatch(ctx context.Context, hashSlots []uint16, reads []MessageUpdateRead) ([]MessageUpdatePage, error) {
	if db == nil || db.meta == nil || db.engine == nil {
		return nil, dberrors.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(hashSlots) != len(reads) || len(reads) > MaxMessageUpdatePage {
		return nil, ErrInvalidArgument
	}
	targets := 0
	for _, read := range reads {
		if err := validateMessageUpdateRead(read); err != nil {
			return nil, err
		}
		targets += max(1, max(len(read.IDs), read.Limit))
		if targets > MaxMessageUpdatePage {
			return nil, ErrInvalidArgument
		}
	}
	pages := make([]MessageUpdatePage, len(reads))
	if len(reads) == 0 {
		return pages, nil
	}
	snap, err := db.engine.NewSnapshot()
	if err != nil {
		return nil, err
	}
	defer snap.Close()
	total := 0
	for i, read := range reads {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := readMessageUpdatesSnapshot(ctx, snap, HashSlot(hashSlots[i]), read)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Updates {
			total += len(row.Payload) + len(row.ChannelID) + len(row.PendingAfterUID) + 128
		}
		if total > MaxMessageUpdatePageBytes {
			return nil, ErrInvalidArgument
		}
		pages[i] = page
	}
	return pages, nil
}

// readMessageUpdatesSnapshot keeps head, index and replacement rows in the
// caller's pinned view. Both single-shard and grouped reads use this decoder.
func readMessageUpdatesSnapshot(ctx context.Context, snap *engine.Snapshot, hs HashSlot, q MessageUpdateRead) (MessageUpdatePage, error) {
	var out MessageUpdatePage
	var err error
	cpk := KeyParts{String(q.ChannelID), Int64Ordered(q.ChannelType)}
	out.Head, _, err = snapshotUpdateRow(snap, messageUpdateHeadTable, hs, cpk)
	if err != nil {
		return out, err
	}
	out.Through = q.Through
	if out.Through == 0 || out.Through > out.Head.UpdateSeq {
		out.Through = out.Head.UpdateSeq
	}
	out.Next = q.After
	bytes := 0
	appendRow := func(row MessageUpdate) bool {
		size := len(row.Payload) + len(row.ChannelID) + len(row.PendingAfterUID) + 128
		if bytes+size > MaxMessageUpdatePageBytes {
			return false
		}
		bytes += size
		out.Updates = append(out.Updates, row)
		return true
	}
	if len(q.IDs) > 0 {
		// Head and dependent rows share one atomic batch and this pinned snapshot.
		// Before the first edit, neither latest replacements nor pending rows exist;
		// this is a per-read proof, never a reusable negative cache.
		if out.Head.UpdateSeq == 0 {
			return out, ctx.Err()
		}
		for _, id := range q.IDs {
			if err = ctx.Err(); err != nil {
				return MessageUpdatePage{}, err
			}
			table := messageUpdateTable
			if q.IncludePending {
				table = messageUpdatePendingTable
			}
			row, ok, e := snapshotUpdateRow(snap, table, hs, append(append(KeyParts(nil), cpk...), Uint64(id)))
			if e != nil {
				return MessageUpdatePage{}, e
			}
			if ok && !appendRow(row) {
				return MessageUpdatePage{}, ErrInvalidArgument
			}
		}
		return out, nil
	}
	if q.Limit == 0 {
		return out, nil
	}
	if q.After >= out.Through {
		out.Next = out.Through
		return out, nil
	}
	base, err := encodeTableIndexScanPrefix(hs, TableIDMessageUpdate, 2, cpk)
	if err != nil {
		return out, err
	}
	span := keycodec.NewPrefixSpan(base)
	start, err := encodeKeyParts(append([]byte(nil), base...), KeyParts{Uint64(q.After + 1)})
	if err != nil {
		return out, err
	}
	iter, err := snap.NewIter(engine.Span{Start: start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return out, err
	}
	defer iter.Close()
	index := messageUpdateTable.spec.Indexes[0]
	indexBase := encodeIndexPrefix(hs, TableIDMessageUpdate, 2)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err = ctx.Err(); err != nil {
			return MessageUpdatePage{}, err
		}
		parts, pk, valid, e := messageUpdateTable.decodeIndexKey(indexBase, iter.Key(), index)
		if e != nil || !valid {
			return MessageUpdatePage{}, dberrors.ErrCorruptValue
		}
		seq := parts[2].U64
		if seq > out.Through {
			break
		}
		if len(out.Updates) == q.Limit {
			out.More = true
			return out, nil
		}
		row, found, e := snapshotUpdateRow(snap, messageUpdateTable, hs, pk)
		if e != nil {
			return MessageUpdatePage{}, e
		}
		if !found || row.UpdateSeq != seq {
			return MessageUpdatePage{}, dberrors.ErrCorruptValue
		}
		if !appendRow(row) {
			out.More = true
			return out, nil
		}
		out.Next = seq
	}
	if err = iter.Error(); err != nil {
		return MessageUpdatePage{}, err
	}
	out.Next = out.Through
	return out, nil
}

// deleteChannelMessageUpdates removes all channel-owned edit spans. A later
// initialization receives a different generation and fences delayed commands.
func deleteChannelMessageUpdates(state *batchCommitState, b *engine.Batch, slot HashSlot, channelID string, channelType int64) error {
	pk := KeyParts{String(channelID), Int64Ordered(channelType)}
	for _, tableID := range []uint32{TableIDMessageUpdate, TableIDMessageUpdateHead, TableIDMessageUpdateRequest, TableIDMessageUpdatePending} {
		prefix, err := encodeKeyParts(encodeRowPrefix(slot, tableID), pk)
		if err != nil {
			return err
		}
		span := keycodec.NewPrefixSpan(prefix)
		for key := range state.tableRows {
			if strings.HasPrefix(key, string(prefix)) {
				state.tableRows[key] = tableRowOverlay{exists: false}
			}
		}
		if err = b.DeleteRange(engine.Span{Start: span.Start, End: span.End}); err != nil {
			return err
		}
	}
	for _, ix := range [][2]uint32{{TableIDMessageUpdate, 2}, {TableIDMessageUpdate, 4}, {TableIDMessageUpdatePending, 3}} {
		prefix, err := encodeTableIndexScanPrefix(slot, ix[0], uint16(ix[1]), pk)
		if err != nil {
			return err
		}
		span := keycodec.NewPrefixSpan(prefix)
		for key := range state.tableRows {
			if strings.HasPrefix(key, string(prefix)) {
				state.tableRows[key] = tableRowOverlay{exists: false}
			}
		}
		if err = b.DeleteRange(engine.Span{Start: span.Start, End: span.End}); err != nil {
			return err
		}
	}
	key, err := messageUpdateHeadTable.primaryRowKey(slot, pk)
	if err != nil {
		return err
	}
	state.tableRows[string(key)] = tableRowOverlay{exists: false}
	return nil
}

// MessageUpdatePendingCursor resumes pending notification discovery by target.
type MessageUpdatePendingCursor struct {
	ChannelID   string
	ChannelType int64
	MessageID   uint64
}

// ListPendingMessageUpdates scans a bounded pinned pending index page. Payloads
// are omitted from tasks; notification delivery never transports message bodies.
func (s *ShardStore) ListPendingMessageUpdates(ctx context.Context, after MessageUpdatePendingCursor, limit int) ([]MessageUpdate, MessageUpdatePendingCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	if limit < 1 || limit > 64 {
		return nil, after, false, ErrInvalidArgument
	}
	hs := s.shard.hashSlot
	base := encodeIndexPrefix(hs, TableIDMessageUpdatePending, 3)
	span := keycodec.NewPrefixSpan(base)
	start := span.Start
	if after.ChannelID != "" {
		pk := KeyParts{String(after.ChannelID), Int64Ordered(after.ChannelType), Uint64(after.MessageID)}
		key, err := encodeTableIndexKey(hs, TableIDMessageUpdatePending, 3, pk, pk)
		if err != nil {
			return nil, after, false, err
		}
		start = append(key, 0)
	}
	snap, err := s.shard.db.engine.NewSnapshot()
	if err != nil {
		return nil, after, false, err
	}
	defer snap.Close()
	iter, err := snap.NewIter(engine.Span{Start: start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, after, false, err
	}
	defer iter.Close()
	rows := make([]MessageUpdate, 0, limit)
	cursor := after
	for ok := iter.First(); ok; ok = iter.Next() {
		if err = ctx.Err(); err != nil {
			return nil, after, false, err
		}
		if len(rows) == limit {
			return rows, cursor, false, nil
		}
		_, pk, valid, e := messageUpdatePendingTable.decodeIndexKey(base, iter.Key(), messageUpdatePendingTable.spec.Indexes[0])
		if e != nil || !valid {
			return nil, after, false, ErrCorruptValue
		}
		row, found, e := snapshotUpdateRow(snap, messageUpdatePendingTable, hs, pk)
		if e != nil {
			return nil, after, false, e
		}
		if !found || row.Pending == 0 {
			return nil, after, false, ErrCorruptValue
		}
		row.Payload = nil
		rows = append(rows, row)
		cursor = MessageUpdatePendingCursor{ChannelID: row.ChannelID, ChannelType: row.ChannelType, MessageID: row.MessageID}
	}
	return rows, cursor, true, iter.Error()
}

// deleteUpdateRow updates the same-batch overlay as well as durable indexes.
func deleteUpdateRow[R any](t Table[R], state *batchCommitState, b *engine.Batch, slot HashSlot, pk KeyParts) error {
	key, err := t.primaryRowKey(slot, pk)
	if err != nil {
		return err
	}
	old, exists, err := t.loadBatchRow(state, slot, pk, key)
	if err != nil {
		return err
	}
	if exists {
		if err = t.stageDeleteIndexEntries(b, slot, old, pk); err != nil {
			return err
		}
	}
	if err = b.Delete(key); err != nil {
		return err
	}
	state.tableRows[string(key)] = tableRowOverlay{exists: false}
	return nil
}
