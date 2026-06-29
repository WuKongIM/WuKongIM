package taskaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Store keeps a bounded JSONL-backed ControllerV2 task audit projection.
type Store struct {
	mu        sync.Mutex
	path      string
	file      *os.File
	opts      Options
	snapshots map[string]Snapshot
	events    map[string][]Event
	eventIDs  map[string]struct{}
	closed    bool
}

// Open loads or creates a task audit JSONL store at path.
func Open(path string, opts Options) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: path is required", ErrUnavailable)
	}
	opts = normalizeOptions(opts)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	store := &Store{
		path:      path,
		opts:      opts,
		snapshots: make(map[string]Snapshot),
		events:    make(map[string][]Event),
		eventIDs:  make(map[string]struct{}),
	}
	if err := store.replay(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	store.file = file
	return store, nil
}

// Append persists event and updates bounded query projections.
func (s *Store) Append(ctx context.Context, event Event) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return ErrUnavailable
	}
	event = s.normalizeEvent(event)
	if event.TaskID == "" {
		return fmt.Errorf("%w: task id is required", ErrUnavailable)
	}
	if _, ok := s.eventIDs[event.EventID]; ok {
		return nil
	}
	line, err := encodeJSONL(event)
	if err != nil {
		return err
	}
	if _, err := s.file.Write(line); err != nil {
		return err
	}
	s.applyEventLocked(event)
	s.enforceRetentionLocked()
	return nil
}

// List returns retained task snapshots sorted by newest applied Raft index.
func (s *Store) List(ctx context.Context, req ListRequest) (ListResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ListResponse{}, err
	}
	if s == nil {
		return ListResponse{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ListResponse{}, ErrUnavailable
	}
	limit := s.normalizeLimit(req.Limit)
	items := make([]Snapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		if snapshotMatches(snapshot, req) {
			items = append(items, snapshot)
		}
	}
	sortSnapshotsDesc(items)
	total := len(items)
	truncated := total > limit
	if truncated {
		items = items[:limit]
	}
	return ListResponse{Total: total, Limit: limit, Truncated: truncated, Items: cloneSnapshots(items)}, nil
}

// Events returns the retained timeline for taskID sorted by applied Raft index.
func (s *Store) Events(ctx context.Context, taskID string) (EventsResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return EventsResponse{}, err
	}
	if s == nil {
		return EventsResponse{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return EventsResponse{}, ErrUnavailable
	}
	snapshot, ok := s.snapshots[taskID]
	if !ok {
		return EventsResponse{}, ErrTaskNotFound
	}
	events := cloneEvents(s.events[taskID])
	return EventsResponse{Task: snapshot, Events: events, Truncated: snapshot.Truncated}, nil
}

// Compact rewrites the JSONL file with only retained events.
func (s *Store) Compact(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return ErrUnavailable
	}
	events := s.retainedEventsLocked()
	tmpPath := s.path + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	for _, event := range events {
		line, err := encodeJSONL(event)
		if err != nil {
			_ = tmp.Close()
			return err
		}
		if _, err := tmp.Write(line); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}
	old := s.file
	next, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		_ = old.Close()
		s.file = nil
		return err
	}
	s.file = next
	return old.Close()
}

// Close closes the JSONL file handle.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func (s *Store) replay() error {
	file, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()
	return replayJSONL(file, func(event Event) {
		event = s.normalizeEvent(event)
		if event.TaskID == "" {
			return
		}
		s.applyEventLocked(event)
		s.enforceRetentionLocked()
	})
}

func (s *Store) normalizeEvent(event Event) Event {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.opts.Now().UTC()
	} else {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	if event.EventID == "" {
		event.EventID = fmt.Sprintf("%s:%d:%s:%d", event.TaskID, event.AppliedRaftIndex, event.Type, event.ParticipantNode)
	}
	event.Summary = truncateUTF8(event.Summary, s.opts.DetailLimitBytes)
	event.Reason = truncateUTF8(event.Reason, s.opts.DetailLimitBytes)
	if len(event.Details) > 0 {
		if data, err := json.Marshal(event.Details); err == nil && len(data) > s.opts.DetailLimitBytes {
			event.Details = map[string]any{"truncated": true, "bytes": len(data)}
		}
	}
	return event
}

func (s *Store) applyEventLocked(event Event) {
	if _, ok := s.eventIDs[event.EventID]; ok {
		return
	}
	s.eventIDs[event.EventID] = struct{}{}
	events := append(s.events[event.TaskID], event)
	sortEventsAsc(events)
	s.events[event.TaskID] = events
	snapshot := s.snapshots[event.TaskID]
	if snapshot.TaskID == "" {
		snapshot.TaskID = event.TaskID
		snapshot.FirstAppliedRaftIndex = event.AppliedRaftIndex
		snapshot.StartedAt = event.OccurredAt
	}
	if snapshot.FirstAppliedRaftIndex == 0 || event.AppliedRaftIndex < snapshot.FirstAppliedRaftIndex {
		snapshot.FirstAppliedRaftIndex = event.AppliedRaftIndex
	}
	if event.AppliedRaftIndex >= snapshot.LastAppliedRaftIndex {
		updateSnapshotFromEvent(&snapshot, event)
	}
	snapshot.EventCount = len(events)
	s.snapshots[event.TaskID] = snapshot
}

func updateSnapshotFromEvent(snapshot *Snapshot, event Event) {
	snapshot.LastAppliedRaftIndex = event.AppliedRaftIndex
	if event.Kind != "" {
		snapshot.Kind = event.Kind
	}
	if event.Status != "" {
		snapshot.Status = event.Status
	}
	if event.SlotID != 0 {
		snapshot.SlotID = event.SlotID
	}
	if event.LeaderID != 0 {
		snapshot.LeaderID = event.LeaderID
	}
	if event.SourceNode != 0 {
		snapshot.SourceNode = event.SourceNode
	}
	if event.TargetNode != 0 {
		snapshot.TargetNode = event.TargetNode
	}
	if event.Summary != "" {
		snapshot.Summary = event.Summary
	}
	if event.Reason != "" {
		snapshot.LastReason = event.Reason
	}
	if event.Type == EventCompleted {
		snapshot.CompletedAt = event.OccurredAt
	}
}

func (s *Store) normalizeLimit(limit int) int {
	if limit <= 0 || limit > s.opts.MaxTasks {
		return s.opts.MaxTasks
	}
	return limit
}

func normalizeOptions(opts Options) Options {
	if opts.MaxTasks <= 0 {
		opts.MaxTasks = DefaultMaxTasks
	}
	if opts.MaxEventsPerTask <= 0 {
		opts.MaxEventsPerTask = DefaultMaxEventsPerTask
	}
	if opts.DetailLimitBytes <= 0 {
		opts.DetailLimitBytes = DefaultDetailLimitBytes
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return opts
}

func snapshotMatches(snapshot Snapshot, req ListRequest) bool {
	if req.Kind != "" && snapshot.Kind != req.Kind {
		return false
	}
	if req.Status != "" && snapshot.Status != req.Status {
		return false
	}
	if req.SlotID != 0 && snapshot.SlotID != req.SlotID {
		return false
	}
	if req.NodeID != 0 && snapshot.LeaderID != req.NodeID && snapshot.SourceNode != req.NodeID && snapshot.TargetNode != req.NodeID {
		return false
	}
	if req.Keyword != "" {
		keyword := strings.ToLower(req.Keyword)
		haystack := strings.ToLower(snapshot.TaskID + " " + snapshot.Summary + " " + snapshot.LastReason)
		if !strings.Contains(haystack, keyword) {
			return false
		}
	}
	return true
}

func cloneSnapshots(items []Snapshot) []Snapshot {
	return append([]Snapshot(nil), items...)
}

func cloneEvents(items []Event) []Event {
	out := make([]Event, len(items))
	for i := range items {
		out[i] = cloneEvent(items[i])
	}
	return out
}

func cloneEvent(event Event) Event {
	if len(event.Details) > 0 {
		details := make(map[string]any, len(event.Details))
		for key, value := range event.Details {
			details[key] = value
		}
		event.Details = details
	}
	return event
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 || len([]byte(s)) <= maxBytes {
		return s
	}
	trimmed := string([]byte(s)[:maxBytes])
	for !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}
