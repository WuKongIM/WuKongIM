package raft

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	defaultLogEntryLimit = 50
	maxLogEntryLimit     = 200
)

// LogEntriesOptions controls a node-local Controller Raft log entry page.
type LogEntriesOptions struct {
	// Limit is the maximum number of entries to return. Zero uses the default.
	Limit int
	// Cursor is the exclusive upper log index bound. Zero starts at the latest entry.
	Cursor uint64
}

// LogEntry is a read-only summary of one Controller Raft log entry.
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
	// Decoded is a JSON-friendly payload summary for manager inspection.
	Decoded map[string]any
}

// LogEntries is one node-local page of Controller Raft log entries.
type LogEntries struct {
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

// LogEntries returns one page from the local Controller Raft log.
func (s *Service) LogEntries(ctx context.Context, opts LogEntriesOptions) (LogEntries, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return LogEntries{}, err
	}
	s.mu.Lock()
	if !s.started {
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return LogEntries{}, ErrStopped
		}
		return LogEntries{}, ErrNotStarted
	}
	store := s.store
	s.mu.Unlock()
	if store == nil {
		return LogEntries{}, ErrNotStarted
	}
	opts = normalizeLogEntriesOptions(opts)
	hs, _, err := store.InitialState()
	if err != nil {
		return LogEntries{}, err
	}
	page, err := readLogEntriesPage(ctx, logStorageAdapter{store: store}, opts)
	if err != nil {
		return LogEntries{}, err
	}
	status := s.Status()
	page.CommitIndex = status.CommitIndex
	if page.CommitIndex == 0 {
		page.CommitIndex = hs.Commit
	}
	page.AppliedIndex = status.AppliedIndex
	if page.AppliedIndex == 0 {
		page.AppliedIndex = store.AppliedIndex()
	}
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

type logStorage interface {
	FirstIndex() (uint64, error)
	LastIndex() (uint64, error)
	Entries(lo, hi, maxSize uint64) ([]raftpb.Entry, error)
}

type logStorageAdapter struct {
	store logStorage
}

func (a logStorageAdapter) FirstIndex(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return a.store.FirstIndex()
}

func (a logStorageAdapter) LastIndex(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return a.store.LastIndex()
}

func (a logStorageAdapter) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return a.store.Entries(lo, hi, maxSize)
}

type logPageStorage interface {
	FirstIndex(context.Context) (uint64, error)
	LastIndex(context.Context) (uint64, error)
	Entries(context.Context, uint64, uint64, uint64) ([]raftpb.Entry, error)
}

func readLogEntriesPage(ctx context.Context, storage logPageStorage, opts LogEntriesOptions) (LogEntries, error) {
	first, err := storage.FirstIndex(ctx)
	if err != nil {
		return LogEntries{}, err
	}
	last, err := storage.LastIndex(ctx)
	if err != nil {
		return LogEntries{}, err
	}
	page := LogEntries{FirstIndex: first, LastIndex: last}
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
		return LogEntries{}, err
	}
	page.Items = logEntriesFromRaft(entries)
	return page, nil
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

func logEntriesFromRaft(entries []raftpb.Entry) []LogEntry {
	out := make([]LogEntry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		item := LogEntry{
			Index:    entry.Index,
			Term:     entry.Term,
			Type:     logEntryTypeName(entry.Type),
			DataSize: len(entry.Data),
		}
		inspectControllerLogEntryPayload(&item, entry)
		out = append(out, item)
	}
	return out
}

func inspectControllerLogEntryPayload(item *LogEntry, entry raftpb.Entry) {
	if entry.Type != raftpb.EntryNormal {
		return
	}
	if len(entry.Data) == 0 {
		item.DecodeStatus = "empty"
		item.DecodedType = "noop"
		item.Decoded = map[string]any{"command": "noop"}
		return
	}
	cmd, err := command.Decode(entry.Data)
	if err != nil {
		item.DecodeStatus = "corrupt"
		item.DecodedType = "unknown"
		item.Decoded = map[string]any{"error": err.Error()}
		return
	}
	if !cmd.IssuedAt.IsZero() {
		item.CreatedAtMS = cmd.IssuedAt.UTC().UnixMilli()
	}
	payload, err := controllerCommandPayload(cmd)
	if err != nil {
		item.DecodeStatus = "corrupt"
		item.DecodedType = "unknown"
		item.Decoded = map[string]any{"error": err.Error()}
		return
	}
	item.DecodeStatus = "ok"
	item.DecodedType = string(cmd.Kind)
	item.Decoded = payload
}

func controllerCommandPayload(cmd command.Command) (map[string]any, error) {
	if cmd.Kind == "" {
		return nil, errors.New("controller/raft: empty command kind")
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	payload["command"] = string(cmd.Kind)
	return payload, nil
}

func logEntryTypeName(entryType raftpb.EntryType) string {
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
