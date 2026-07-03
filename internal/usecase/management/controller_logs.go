package management

import (
	"context"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
)

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
type ControllerLogEntry = SlotLogEntry

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

// ListControllerLogEntries returns one selected node's local Controller Raft log entries.
func (a *App) ListControllerLogEntries(ctx context.Context, req ListControllerLogEntriesRequest) (ControllerLogEntriesResponse, error) {
	if a == nil || a.cluster == nil {
		return ControllerLogEntriesResponse{}, nil
	}
	page, err := a.cluster.ControllerLogEntriesOnNode(ctx, req.NodeID, raftcluster.ControllerLogEntriesOptions{
		Limit:  req.Limit,
		Cursor: req.Cursor,
	})
	if err != nil {
		return ControllerLogEntriesResponse{}, err
	}
	return controllerLogEntriesResponse(page), nil
}

func controllerLogEntriesResponse(page raftcluster.ControllerLogEntries) ControllerLogEntriesResponse {
	return ControllerLogEntriesResponse{
		NodeID:       page.NodeID,
		FirstIndex:   page.FirstIndex,
		LastIndex:    page.LastIndex,
		CommitIndex:  page.CommitIndex,
		AppliedIndex: page.AppliedIndex,
		NextCursor:   page.NextCursor,
		Items:        controllerLogEntries(page.Items),
	}
}

func controllerLogEntries(entries []raftcluster.ControllerLogEntry) []ControllerLogEntry {
	out := make([]ControllerLogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, ControllerLogEntry{
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
