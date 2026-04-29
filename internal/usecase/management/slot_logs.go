package management

import (
	"context"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

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
type SlotLogEntry struct {
	// Index is the Raft log index.
	Index uint64
	// Term is the Raft term stored on the entry.
	Term uint64
	// Type is the normalized Raft entry type.
	Type string
	// DataSize is the payload size in bytes.
	DataSize int
	// DecodeStatus reports whether the entry payload was decoded for inspection.
	DecodeStatus string
	// DecodedType is the stable command or payload type when decoding succeeds.
	DecodedType string
	// Decoded is a redacted JSON-friendly payload summary for manager inspection.
	Decoded map[string]any
}

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

// ListSlotLogEntries returns one selected node's local Slot Raft log entries.
func (a *App) ListSlotLogEntries(ctx context.Context, req ListSlotLogEntriesRequest) (SlotLogEntriesResponse, error) {
	if a == nil || a.cluster == nil {
		return SlotLogEntriesResponse{}, nil
	}
	page, err := a.cluster.SlotLogEntriesOnNode(ctx, req.NodeID, req.SlotID, raftcluster.SlotLogEntriesOptions{
		Limit:  req.Limit,
		Cursor: req.Cursor,
	})
	if err != nil {
		return SlotLogEntriesResponse{}, err
	}
	return slotLogEntriesResponse(page), nil
}

func slotLogEntriesResponse(page raftcluster.SlotLogEntries) SlotLogEntriesResponse {
	return SlotLogEntriesResponse{
		NodeID:       page.NodeID,
		SlotID:       page.SlotID,
		FirstIndex:   page.FirstIndex,
		LastIndex:    page.LastIndex,
		CommitIndex:  page.CommitIndex,
		AppliedIndex: page.AppliedIndex,
		NextCursor:   page.NextCursor,
		Items:        slotLogEntries(page.Items),
	}
}

func slotLogEntries(entries []raftcluster.SlotLogEntry) []SlotLogEntry {
	out := make([]SlotLogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, SlotLogEntry{
			Index:        entry.Index,
			Term:         entry.Term,
			Type:         entry.Type,
			DataSize:     entry.DataSize,
			DecodeStatus: entry.DecodeStatus,
			DecodedType:  entry.DecodedType,
			Decoded:      entry.Decoded,
		})
	}
	return out
}
