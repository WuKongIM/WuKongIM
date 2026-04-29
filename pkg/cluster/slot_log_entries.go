package cluster

import (
	"context"

	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	defaultSlotLogEntryLimit = 50
	maxSlotLogEntryLimit     = 200
)

// SlotLogEntriesOnNode returns one node's local Raft log entries for a managed Slot.
func (c *Cluster) SlotLogEntriesOnNode(ctx context.Context, nodeID uint64, slotID uint32, opts SlotLogEntriesOptions) (SlotLogEntries, error) {
	if c == nil {
		return SlotLogEntries{}, ErrNotStarted
	}
	opts = normalizeSlotLogEntriesOptions(opts)
	if c.IsLocal(multiraft.NodeID(nodeID)) {
		return c.localSlotLogEntries(ctx, nodeID, slotID, opts)
	}
	return c.remoteSlotLogEntries(ctx, nodeID, slotID, opts)
}

func normalizeSlotLogEntriesOptions(opts SlotLogEntriesOptions) SlotLogEntriesOptions {
	if opts.Limit <= 0 {
		opts.Limit = defaultSlotLogEntryLimit
	}
	if opts.Limit > maxSlotLogEntryLimit {
		opts.Limit = maxSlotLogEntryLimit
	}
	return opts
}

func (c *Cluster) localSlotLogEntries(ctx context.Context, nodeID uint64, slotID uint32, opts SlotLogEntriesOptions) (SlotLogEntries, error) {
	status, err := c.managedSlots().statusOnNode(ctx, multiraft.NodeID(nodeID), multiraft.SlotID(slotID))
	if err != nil {
		return SlotLogEntries{}, err
	}
	if c.cfg.NewStorage == nil {
		return SlotLogEntries{}, ErrNotStarted
	}
	storage, err := c.cfg.NewStorage(multiraft.SlotID(slotID))
	if err != nil {
		return SlotLogEntries{}, err
	}
	page, err := readSlotLogEntriesPage(ctx, storage, opts)
	if err != nil {
		return SlotLogEntries{}, err
	}
	page.NodeID = nodeID
	page.SlotID = slotID
	page.CommitIndex = status.CommitIndex
	page.AppliedIndex = status.AppliedIndex
	return page, nil
}

func (c *Cluster) remoteSlotLogEntries(ctx context.Context, nodeID uint64, slotID uint32, opts SlotLogEntriesOptions) (SlotLogEntries, error) {
	body, err := encodeManagedSlotRequest(managedSlotRPCRequest{
		Kind:   managedSlotRPCLogs,
		SlotID: slotID,
		Limit:  uint64(opts.Limit),
		Cursor: opts.Cursor,
	})
	if err != nil {
		return SlotLogEntries{}, err
	}
	respBody, err := c.RPCService(ctx, multiraft.NodeID(nodeID), multiraft.SlotID(slotID), rpcServiceManagedSlot, body)
	if err != nil {
		return SlotLogEntries{}, err
	}
	resp, err := decodeManagedSlotResponse(respBody)
	if err != nil {
		return SlotLogEntries{}, err
	}
	return SlotLogEntries{
		NodeID:       nodeID,
		SlotID:       slotID,
		FirstIndex:   resp.FirstIndex,
		LastIndex:    resp.LastIndex,
		CommitIndex:  resp.CommitIndex,
		AppliedIndex: resp.AppliedIndex,
		NextCursor:   resp.NextCursor,
		Items:        slotLogEntriesFromManaged(resp.LogEntries),
	}, nil
}

func readSlotLogEntriesPage(ctx context.Context, storage multiraft.Storage, opts SlotLogEntriesOptions) (SlotLogEntries, error) {
	first, err := storage.FirstIndex(ctx)
	if err != nil {
		return SlotLogEntries{}, err
	}
	last, err := storage.LastIndex(ctx)
	if err != nil {
		return SlotLogEntries{}, err
	}
	page := SlotLogEntries{
		FirstIndex: first,
		LastIndex:  last,
	}
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
		return SlotLogEntries{}, err
	}
	page.Items = slotLogEntriesFromRaft(entries)
	return page, nil
}

func slotLogEntryWindow(first, last uint64, opts SlotLogEntriesOptions) (uint64, uint64, uint64, bool) {
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

func slotLogEntriesFromRaft(entries []raftpb.Entry) []SlotLogEntry {
	out := make([]SlotLogEntry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		item := SlotLogEntry{
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

func inspectSlotLogEntryPayload(item *SlotLogEntry, entry raftpb.Entry) {
	if entry.Type != raftpb.EntryNormal {
		return
	}
	if len(entry.Data) == 0 {
		item.DecodeStatus = "empty"
		item.DecodedType = "noop"
		item.Decoded = map[string]any{"command": "noop"}
		return
	}
	inspection, err := metafsm.DecodeCommandInspection(entry.Data)
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

func slotLogEntriesFromManaged(entries []managedSlotLogEntry) []SlotLogEntry {
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

func managedSlotLogEntriesFromSlot(entries []SlotLogEntry) []managedSlotLogEntry {
	out := make([]managedSlotLogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, managedSlotLogEntry{
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
