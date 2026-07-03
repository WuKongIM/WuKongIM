package cluster

import (
	"context"

	controllerraft "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

// ControllerLogEntriesOptions controls a node-local Controller Raft log entry page.
type ControllerLogEntriesOptions = SlotLogEntriesOptions

// ControllerLogEntry is a manager-facing read-only summary of one Controller Raft log entry.
type ControllerLogEntry = SlotLogEntry

// ControllerLogEntries is one node-local page of Controller Raft log entries.
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
	Items []ControllerLogEntry
}

// ControllerLogEntriesOnNode returns one node's local Controller Raft log entries.
func (c *Cluster) ControllerLogEntriesOnNode(ctx context.Context, nodeID uint64, opts ControllerLogEntriesOptions) (ControllerLogEntries, error) {
	if c == nil {
		return ControllerLogEntries{}, ErrNotStarted
	}
	opts = normalizeSlotLogEntriesOptions(opts)
	if c.IsLocal(multiraft.NodeID(nodeID)) {
		return c.localControllerLogEntries(ctx, nodeID, opts)
	}
	return c.remoteControllerLogEntries(ctx, nodeID, opts)
}

func (c *Cluster) localControllerLogEntries(ctx context.Context, nodeID uint64, opts ControllerLogEntriesOptions) (ControllerLogEntries, error) {
	if c == nil || c.controllerHost == nil || c.controllerHost.raftDB == nil {
		return ControllerLogEntries{}, ErrNotStarted
	}
	storage := c.controllerHost.raftDB.ForController()
	state, err := storage.InitialState(ctx)
	if err != nil {
		return ControllerLogEntries{}, err
	}
	page, err := readControllerLogEntriesPage(ctx, storage, opts)
	if err != nil {
		return ControllerLogEntries{}, err
	}
	page.NodeID = nodeID
	page.CommitIndex = state.HardState.Commit
	page.AppliedIndex = state.AppliedIndex
	return page, nil
}

func (c *Cluster) remoteControllerLogEntries(ctx context.Context, nodeID uint64, opts ControllerLogEntriesOptions) (ControllerLogEntries, error) {
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCControllerLogs,
		ControllerLogs: &controllerLogEntriesRequest{
			Limit:  opts.Limit,
			Cursor: opts.Cursor,
		},
	})
	if err != nil {
		return ControllerLogEntries{}, err
	}
	respBody, err := c.controllerRPCService(ctx, multiraft.NodeID(nodeID), body)
	if err != nil {
		return ControllerLogEntries{}, err
	}
	resp, err := decodeControllerResponse(controllerRPCControllerLogs, respBody)
	if err != nil {
		return ControllerLogEntries{}, err
	}
	return ControllerLogEntries{
		NodeID:       nodeID,
		FirstIndex:   resp.FirstIndex,
		LastIndex:    resp.LastIndex,
		CommitIndex:  resp.CommitIndex,
		AppliedIndex: resp.AppliedIndex,
		NextCursor:   resp.NextCursor,
		Items:        controllerLogEntriesFromManaged(resp.LogEntries),
	}, nil
}

func readControllerLogEntriesPage(ctx context.Context, storage multiraft.Storage, opts ControllerLogEntriesOptions) (ControllerLogEntries, error) {
	first, err := storage.FirstIndex(ctx)
	if err != nil {
		return ControllerLogEntries{}, err
	}
	last, err := storage.LastIndex(ctx)
	if err != nil {
		return ControllerLogEntries{}, err
	}
	page := ControllerLogEntries{FirstIndex: first, LastIndex: last}
	if last < first {
		return page, nil
	}

	lo, hi, nextCursor, ok := slotLogEntryWindow(first, last, opts)
	page.NextCursor = nextCursor
	if !ok {
		return page, nil
	}
	entries, err := storage.Entries(ctx, lo, hi, 0)
	if err != nil {
		return ControllerLogEntries{}, err
	}
	page.Items = controllerLogEntriesFromRaft(entries)
	return page, nil
}

func controllerLogEntriesFromRaft(entries []raftpb.Entry) []ControllerLogEntry {
	out := make([]ControllerLogEntry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		item := ControllerLogEntry{
			Index:    entry.Index,
			Term:     entry.Term,
			Type:     raftEntryTypeName(entry.Type),
			DataSize: len(entry.Data),
		}
		inspectControllerLogEntryPayload(&item, entry)
		out = append(out, item)
	}
	return out
}

func inspectControllerLogEntryPayload(item *ControllerLogEntry, entry raftpb.Entry) {
	if entry.Type != raftpb.EntryNormal {
		return
	}
	if len(entry.Data) == 0 {
		item.DecodeStatus = "empty"
		item.DecodedType = "noop"
		item.Decoded = map[string]any{"command": "noop"}
		return
	}
	inspection, err := controllerraft.DecodeCommandInspection(entry.Data)
	if err != nil {
		item.DecodeStatus = "corrupt"
		item.DecodedType = "unknown"
		item.Decoded = map[string]any{"error": err.Error()}
		return
	}
	item.DecodeStatus = "ok"
	item.DecodedType = inspection.Type
	item.Decoded = inspection.Payload
}

func controllerLogEntriesFromManaged(entries []managedSlotLogEntry) []ControllerLogEntry {
	out := make([]ControllerLogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, ControllerLogEntry(entry))
	}
	return out
}

func managedSlotLogEntriesFromController(entries []ControllerLogEntry) []managedSlotLogEntry {
	out := make([]managedSlotLogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, managedSlotLogEntry(entry))
	}
	return out
}
