package cluster

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	defaultLogEntryLimit = 50
	maxLogEntryLimit     = 200
)

// LogEntriesOptions controls a node-local distributed Raft log entry page.
type LogEntriesOptions struct {
	// Limit is the maximum number of entries to return. Zero uses the default.
	Limit int
	// Cursor is the exclusive upper log index bound. Zero starts at the latest entry.
	Cursor uint64
}

// LogEntry is a read-only summary of one distributed Raft log entry.
type LogEntry struct {
	// Index is the Raft log index.
	Index uint64
	// Term is the Raft term stored on the entry.
	Term uint64
	// Type is the normalized Raft entry type.
	Type string
	// CreatedAtMS is the proposer-issued command timestamp in Unix milliseconds when known.
	CreatedAtMS int64
	// DataSize is the payload size in bytes.
	DataSize int
	// DecodeStatus reports whether the entry payload was decoded for inspection.
	DecodeStatus string
	// DecodedType is the stable command or payload type when decoding succeeds.
	DecodedType string
	// Decoded is a redacted JSON-friendly payload summary for manager inspection.
	Decoded map[string]any
}

// ControllerLogEntries is one local page of ControllerV2 Raft log entries.
type ControllerLogEntries struct {
	// NodeID is the node whose local Controller log was read.
	NodeID uint64
	// FirstIndex is the first available local Raft log index.
	FirstIndex uint64
	// LastIndex is the last available local Raft log index.
	LastIndex uint64
	// CommitIndex is the queried node's local committed index watermark.
	CommitIndex uint64
	// AppliedIndex is the queried node's local applied index watermark.
	AppliedIndex uint64
	// NextCursor is the cursor for the next older page. Zero means no more entries.
	NextCursor uint64
	// Items contains entries ordered newest first.
	Items []LogEntry
}

// ControllerRaftStatus is a node-local ControllerV2 Raft status snapshot.
type ControllerRaftStatus = control.ControllerRaftStatus

// ControllerRaftCompactionResult describes one node-local ControllerV2 Raft compaction attempt.
type ControllerRaftCompactionResult = control.ControllerRaftCompactionResult

// SlotLogEntries is one local page of Slot Raft log entries.
type SlotLogEntries struct {
	// NodeID is the node whose local Slot log was read.
	NodeID uint64
	// SlotID is the physical Slot identifier.
	SlotID uint32
	// FirstIndex is the first available local Raft log index.
	FirstIndex uint64
	// LastIndex is the last available local Raft log index.
	LastIndex uint64
	// CommitIndex is the queried node's local committed index watermark.
	CommitIndex uint64
	// AppliedIndex is the queried node's local applied index watermark.
	AppliedIndex uint64
	// NextCursor is the cursor for the next older page. Zero means no more entries.
	NextCursor uint64
	// Items contains entries ordered newest first.
	Items []LogEntry
}

// SlotRaftStatus is one node's local Slot Raft status snapshot.
type SlotRaftStatus struct {
	// NodeID is the node whose local Slot Raft status was read.
	NodeID uint64
	// SlotID is the physical Slot identifier.
	SlotID uint32
	// LeaderID is the best-known Slot Raft leader.
	LeaderID uint64
	// Role is this node's local Raft role for the Slot.
	Role string
	// Term is the local Raft term.
	Term uint64
	// CommitIndex is the queried node's local committed index watermark.
	CommitIndex uint64
	// AppliedIndex is the queried node's local applied index watermark.
	AppliedIndex uint64
	// CurrentVoters is the current Slot Raft voter set observed by the runtime.
	CurrentVoters []uint64
}

// SlotRaftCompactionResult describes one node-local Slot Raft compaction attempt.
type SlotRaftCompactionResult struct {
	// NodeID is the node that handled the local Slot Raft compaction attempt.
	NodeID uint64
	// SlotID is the physical Slot whose local Raft log was compacted.
	SlotID uint32
	// AppliedIndex is the local applied index used as the compaction target.
	AppliedIndex uint64
	// BeforeSnapshotIndex is the persisted snapshot index before the attempt.
	BeforeSnapshotIndex uint64
	// AfterSnapshotIndex is the persisted snapshot index after the attempt.
	AfterSnapshotIndex uint64
	// Compacted reports whether the attempt created a new snapshot and compacted entries.
	Compacted bool
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string
}

type controllerLogReader interface {
	ControllerLogEntries(context.Context, control.ControllerLogEntriesOptions) (control.ControllerLogEntries, error)
}

type controllerRaftOperator interface {
	ControllerRaftStatus(context.Context) (control.ControllerRaftStatus, error)
	CompactControllerRaftLog(context.Context) (control.ControllerRaftCompactionResult, error)
}

// LocalControllerLogEntries returns one page from this node's local ControllerV2 Raft log.
func (n *Node) LocalControllerLogEntries(ctx context.Context, opts LogEntriesOptions) (ControllerLogEntries, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerLogEntries{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ControllerLogEntries{}, err
	}
	reader, ok := n.control.(controllerLogReader)
	if !ok || reader == nil {
		return ControllerLogEntries{}, ErrNotStarted
	}
	page, err := reader.ControllerLogEntries(ctx, control.ControllerLogEntriesOptions{Limit: opts.Limit, Cursor: opts.Cursor})
	if err != nil {
		return ControllerLogEntries{}, err
	}
	return ControllerLogEntries{
		NodeID:       n.NodeID(),
		FirstIndex:   page.FirstIndex,
		LastIndex:    page.LastIndex,
		CommitIndex:  page.CommitIndex,
		AppliedIndex: page.AppliedIndex,
		NextCursor:   page.NextCursor,
		Items:        logEntriesFromController(page.Items),
	}, nil
}

// LocalControllerRaftStatus returns this node's local ControllerV2 Raft status snapshot.
func (n *Node) LocalControllerRaftStatus(ctx context.Context) (ControllerRaftStatus, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftStatus{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ControllerRaftStatus{}, err
	}
	operator, ok := n.control.(controllerRaftOperator)
	if !ok || operator == nil {
		return ControllerRaftStatus{}, ErrNotStarted
	}
	return operator.ControllerRaftStatus(ctx)
}

// LocalCompactControllerRaftLog forces this node's local ControllerV2 Raft log compaction.
func (n *Node) LocalCompactControllerRaftLog(ctx context.Context) (ControllerRaftCompactionResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftCompactionResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ControllerRaftCompactionResult{}, err
	}
	operator, ok := n.control.(controllerRaftOperator)
	if !ok || operator == nil {
		return ControllerRaftCompactionResult{}, ErrNotStarted
	}
	return operator.CompactControllerRaftLog(ctx)
}

// LocalSlotRaftStatus returns this node's local Slot Raft status.
func (n *Node) LocalSlotRaftStatus(ctx context.Context, slotID uint32) (SlotRaftStatus, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotRaftStatus{}, err
	}
	if slotID == 0 {
		return SlotRaftStatus{}, ErrSlotNotFound
	}
	if err := n.ensureForeground(); err != nil {
		return SlotRaftStatus{}, err
	}
	if n.defaultSlotRuntime == nil {
		return SlotRaftStatus{}, ErrNotStarted
	}
	status, err := n.defaultSlotRuntime.Status(multiraft.SlotID(slotID))
	if err != nil {
		return SlotRaftStatus{}, mapSlotLogRuntimeError(err)
	}
	return slotRaftStatusFromRuntime(n.NodeID(), slotID, status), nil
}

// LocalSlotLogEntries returns one page from this node's local Slot Raft log.
func (n *Node) LocalSlotLogEntries(ctx context.Context, slotID uint32, opts LogEntriesOptions) (SlotLogEntries, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotLogEntries{}, err
	}
	if slotID == 0 {
		return SlotLogEntries{}, ErrSlotNotFound
	}
	if err := n.ensureForeground(); err != nil {
		return SlotLogEntries{}, err
	}
	if n.defaultSlotRaftDB == nil || n.defaultSlotRuntime == nil {
		return SlotLogEntries{}, ErrNotStarted
	}
	status, err := n.defaultSlotRuntime.Status(multiraft.SlotID(slotID))
	if err != nil {
		return SlotLogEntries{}, mapSlotLogRuntimeError(err)
	}
	storage := n.defaultSlotRaftDB.ForSlot(uint64(slotID))
	state, err := storage.InitialState(ctx)
	if err != nil {
		return SlotLogEntries{}, err
	}
	page, err := readSlotLogEntriesPage(ctx, storage, normalizeLogEntriesOptions(opts))
	if err != nil {
		return SlotLogEntries{}, err
	}
	page.NodeID = n.NodeID()
	page.SlotID = slotID
	page.CommitIndex = status.CommitIndex
	if page.CommitIndex == 0 {
		page.CommitIndex = state.HardState.Commit
	}
	page.AppliedIndex = status.AppliedIndex
	if page.AppliedIndex == 0 {
		page.AppliedIndex = state.AppliedIndex
	}
	return page, nil
}

// LocalCompactSlotRaftLog forces this node's local Slot Raft log compaction.
func (n *Node) LocalCompactSlotRaftLog(ctx context.Context, slotID uint32) (SlotRaftCompactionResult, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotRaftCompactionResult{}, err
	}
	if slotID == 0 {
		return SlotRaftCompactionResult{}, ErrSlotNotFound
	}
	if err := n.ensureForeground(); err != nil {
		return SlotRaftCompactionResult{}, err
	}
	if n.defaultSlotRuntime == nil {
		return SlotRaftCompactionResult{}, ErrNotStarted
	}
	result, err := n.defaultSlotRuntime.CompactLog(ctx, multiraft.SlotID(slotID))
	if err != nil {
		return SlotRaftCompactionResult{}, mapSlotLogRuntimeError(err)
	}
	out := slotRaftCompactionResultFromRuntime(result)
	if out.NodeID == 0 {
		out.NodeID = n.NodeID()
	}
	if out.SlotID == 0 {
		out.SlotID = slotID
	}
	return out, nil
}

type slotLogStorage interface {
	FirstIndex(context.Context) (uint64, error)
	LastIndex(context.Context) (uint64, error)
	Entries(context.Context, uint64, uint64, uint64) ([]raftpb.Entry, error)
}

func readSlotLogEntriesPage(ctx context.Context, storage slotLogStorage, opts LogEntriesOptions) (SlotLogEntries, error) {
	first, err := storage.FirstIndex(ctx)
	if err != nil {
		return SlotLogEntries{}, err
	}
	last, err := storage.LastIndex(ctx)
	if err != nil {
		return SlotLogEntries{}, err
	}
	page := SlotLogEntries{FirstIndex: first, LastIndex: last}
	if last < first {
		return page, nil
	}
	lo, hi, nextCursor, ok := logEntryWindow(first, last, opts)
	page.NextCursor = nextCursor
	if !ok {
		return page, nil
	}
	entries, err := storage.Entries(ctx, lo, hi, 0)
	if err != nil {
		return SlotLogEntries{}, err
	}
	page.Items = slotLogEntriesFromRaft(entries)
	return page, nil
}

func normalizeLogEntriesOptions(opts LogEntriesOptions) LogEntriesOptions {
	if opts.Limit <= 0 {
		opts.Limit = defaultLogEntryLimit
	}
	if opts.Limit > maxLogEntryLimit {
		opts.Limit = maxLogEntryLimit
	}
	return opts
}

func logEntryWindow(first, last uint64, opts LogEntriesOptions) (uint64, uint64, uint64, bool) {
	if last < first {
		return 0, 0, 0, false
	}
	hi := last + 1
	if opts.Cursor != 0 && opts.Cursor < hi {
		hi = opts.Cursor
	}
	if hi <= first {
		return 0, 0, 0, false
	}
	lo := first
	limit := uint64(opts.Limit)
	if hi > first+limit {
		lo = hi - limit
	}
	var nextCursor uint64
	if lo > first {
		nextCursor = lo
	}
	return lo, hi, nextCursor, true
}

func logEntriesFromController(entries []control.ControllerLogEntry) []LogEntry {
	out := make([]LogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, LogEntry{
			Index:        entry.Index,
			Term:         entry.Term,
			Type:         entry.Type,
			CreatedAtMS:  entry.CreatedAtMS,
			DataSize:     entry.DataSize,
			DecodeStatus: entry.DecodeStatus,
			DecodedType:  entry.DecodedType,
			Decoded:      entry.Decoded,
		})
	}
	return out
}

func slotLogEntriesFromRaft(entries []raftpb.Entry) []LogEntry {
	out := make([]LogEntry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		item := LogEntry{
			Index:    entry.Index,
			Term:     entry.Term,
			Type:     raftEntryTypeName(entry.Type),
			DataSize: len(entry.Data),
		}
		inspectSlotLogEntryPayload(&item, entry)
		out = append(out, item)
	}
	return out
}

func slotRaftCompactionResultFromRuntime(result multiraft.LogCompactionResult) SlotRaftCompactionResult {
	return SlotRaftCompactionResult{
		NodeID:              uint64(result.NodeID),
		SlotID:              uint32(result.SlotID),
		AppliedIndex:        result.AppliedIndex,
		BeforeSnapshotIndex: result.BeforeSnapshotIndex,
		AfterSnapshotIndex:  result.AfterSnapshotIndex,
		Compacted:           result.Compacted,
		SkippedReason:       result.SkippedReason,
	}
}

func slotRaftStatusFromRuntime(nodeID uint64, slotID uint32, status multiraft.Status) SlotRaftStatus {
	if nodeID == 0 {
		nodeID = uint64(status.NodeID)
	}
	if slotID == 0 {
		slotID = uint32(status.SlotID)
	}
	return SlotRaftStatus{
		NodeID:        nodeID,
		SlotID:        slotID,
		LeaderID:      uint64(status.LeaderID),
		Role:          slotRaftRoleName(status.Role),
		Term:          status.Term,
		CommitIndex:   status.CommitIndex,
		AppliedIndex:  status.AppliedIndex,
		CurrentVoters: slotRaftVotersFromRuntime(status.CurrentVoters),
	}
}

func slotRaftVotersFromRuntime(voters []multiraft.NodeID) []uint64 {
	out := make([]uint64, 0, len(voters))
	for _, voter := range voters {
		out = append(out, uint64(voter))
	}
	return out
}

func slotRaftRoleName(role multiraft.Role) string {
	switch role {
	case multiraft.RoleLeader:
		return "leader"
	case multiraft.RoleCandidate:
		return "candidate"
	case multiraft.RoleFollower:
		return "follower"
	default:
		return "unknown"
	}
}

func inspectSlotLogEntryPayload(item *LogEntry, entry raftpb.Entry) {
	if entry.Type != raftpb.EntryNormal {
		return
	}
	if len(entry.Data) == 0 {
		item.DecodeStatus = "empty"
		item.DecodedType = "noop"
		item.Decoded = map[string]any{"command": "noop"}
		return
	}
	if len(entry.Data) < slotProposalEnvelopeSize {
		item.DecodeStatus = "corrupt"
		item.DecodedType = "unknown"
		item.Decoded = map[string]any{"error": fmt.Sprintf("proposal payload too short: %d", len(entry.Data))}
		return
	}
	hashSlot := binary.BigEndian.Uint16(entry.Data[:2])
	item.CreatedAtMS = int64(binary.BigEndian.Uint64(entry.Data[2:slotProposalEnvelopeSize]))
	inspection, err := metafsm.DecodeCommandInspection(entry.Data[slotProposalEnvelopeSize:])
	if err != nil {
		item.DecodeStatus = "corrupt"
		item.DecodedType = "unknown"
		item.Decoded = map[string]any{"error": err.Error()}
		return
	}
	item.DecodeStatus = "ok"
	item.DecodedType = inspection.Type
	item.Decoded = inspection.Payload
	if item.Decoded == nil {
		item.Decoded = map[string]any{}
	}
	item.Decoded["hash_slot"] = uint64(hashSlot)
}

func raftEntryTypeName(entryType raftpb.EntryType) string {
	switch entryType {
	case raftpb.EntryNormal:
		return "normal"
	case raftpb.EntryConfChange:
		return "conf_change"
	case raftpb.EntryConfChangeV2:
		return "conf_change_v2"
	default:
		return entryType.String()
	}
}

func mapSlotLogRuntimeError(err error) error {
	if errors.Is(err, multiraft.ErrSlotNotFound) || errors.Is(err, multiraft.ErrRuntimeClosed) {
		return ErrSlotNotFound
	}
	return err
}
