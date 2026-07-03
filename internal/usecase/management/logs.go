package management

import (
	"context"
	"errors"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrLogReaderUnavailable reports that distributed Raft log inspection is not wired.
var ErrLogReaderUnavailable = errors.New("internal/usecase/management: log reader unavailable")

// LogReader exposes node-local distributed Raft log pages for manager views.
type LogReader interface {
	// ControllerLogEntries returns one node's local Controller Raft log entries.
	ControllerLogEntries(context.Context, ListControllerLogEntriesRequest) (ControllerLogEntriesResponse, error)
	// SlotLogEntries returns one node's local Slot Raft log entries.
	SlotLogEntries(context.Context, ListSlotLogEntriesRequest) (SlotLogEntriesResponse, error)
}

// LogEntry is a manager-facing summary of one distributed Raft log entry.
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

// ListControllerLogEntriesRequest selects one node-local Controller Raft log page.
type ListControllerLogEntriesRequest struct {
	// NodeID is the node whose local Controller Raft log should be read.
	NodeID uint64
	// Limit is the maximum number of entries to return. Zero uses the cluster default.
	Limit int
	// Cursor is the exclusive upper log index bound. Zero starts at the latest entry.
	Cursor uint64
}

// ControllerLogEntry is a manager-facing summary of one Controller Raft log entry.
type ControllerLogEntry = LogEntry

// ControllerLogEntriesResponse is one node-local Controller Raft log page.
type ControllerLogEntriesResponse struct {
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
	Items []ControllerLogEntry
}

// ListSlotLogEntriesRequest selects one node-local Slot Raft log page.
type ListSlotLogEntriesRequest struct {
	// NodeID is the node whose local Slot Raft log should be read.
	NodeID uint64
	// SlotID is the physical Slot identifier.
	SlotID uint32
	// Limit is the maximum number of entries to return. Zero uses the cluster default.
	Limit int
	// Cursor is the exclusive upper log index bound. Zero starts at the latest entry.
	Cursor uint64
}

// SlotLogEntry is a manager-facing summary of one Slot Raft log entry.
type SlotLogEntry = LogEntry

// SlotLogEntriesResponse is one node-local Slot Raft log page.
type SlotLogEntriesResponse struct {
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
	Items []SlotLogEntry
}

// ListControllerLogEntries returns one selected node's local Controller Raft log entries.
func (a *App) ListControllerLogEntries(ctx context.Context, req ListControllerLogEntriesRequest) (ControllerLogEntriesResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerLogEntriesResponse{}, err
	}
	if req.NodeID == 0 || req.Limit < 0 {
		return ControllerLogEntriesResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.logs == nil {
		return ControllerLogEntriesResponse{}, ErrLogReaderUnavailable
	}
	return a.logs.ControllerLogEntries(ctx, req)
}

// ListSlotLogEntries returns one selected node's local Slot Raft log entries.
func (a *App) ListSlotLogEntries(ctx context.Context, req ListSlotLogEntriesRequest) (SlotLogEntriesResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotLogEntriesResponse{}, err
	}
	if req.NodeID == 0 || req.SlotID == 0 || req.Limit < 0 {
		return SlotLogEntriesResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.logs == nil {
		return SlotLogEntriesResponse{}, ErrLogReaderUnavailable
	}
	return a.logs.SlotLogEntries(ctx, req)
}
