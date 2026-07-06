package store

import (
	"context"
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// MemoryFactory creates in-memory channel stores for tests and benchmarks.
type MemoryFactory struct {
	mu       sync.Mutex
	channels map[ch.ChannelKey]*MemoryChannelStore
}

// NewMemoryFactory returns an empty in-memory store factory.
func NewMemoryFactory() *MemoryFactory {
	return &MemoryFactory{channels: make(map[ch.ChannelKey]*MemoryChannelStore)}
}

// ChannelStore returns the stable in-memory store for one channel.
func (f *MemoryFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (ChannelStore, error) {
	if f == nil {
		return nil, ch.ErrInvalidConfig
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing := f.channels[key]; existing != nil {
		return existing, nil
	}
	cs := &MemoryChannelStore{id: id}
	f.channels[key] = cs
	return cs, nil
}

// MemoryChannelStore is a mutex-protected durable-log test double.
type MemoryChannelStore struct {
	mu         sync.Mutex
	id         ch.ChannelID
	records    []ch.Record
	checkpoint ch.Checkpoint
	retention  RetentionState
}

// Load returns the current in-memory offsets.
func (s *MemoryChannelStore) Load(ctx context.Context) (InitialState, error) {
	if err := ctx.Err(); err != nil {
		return InitialState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	leo := s.leoLocked()
	return InitialState{LEO: leo, HW: minUint64(s.checkpoint.HW, leo), CheckpointHW: minUint64(s.checkpoint.HW, leo)}, nil
}

// AppendLeader appends records as the local leader and assigns continuous indexes.
func (s *MemoryChannelStore) AppendLeader(ctx context.Context, req AppendLeaderRequest) (AppendLeaderResult, error) {
	if err := ctx.Err(); err != nil {
		return AppendLeaderResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(req.Records) == 0 {
		leo := s.leoLocked()
		return AppendLeaderResult{BaseOffset: leo + 1, LastOffset: leo}, nil
	}
	base := s.leoLocked() + 1
	for i, record := range req.Records {
		record.Index = base + uint64(i)
		record.Payload = cloneBytes(record.Payload)
		if record.SizeBytes == 0 {
			record.SizeBytes = len(record.Payload)
		}
		s.records = append(s.records, record)
	}
	return AppendLeaderResult{BaseOffset: base, LastOffset: s.leoLocked()}, nil
}

// ApplyFollower applies leader records, skipping any duplicate prefix already stored.
func (s *MemoryChannelStore) ApplyFollower(ctx context.Context, req ApplyFollowerRequest) (ApplyFollowerResult, error) {
	if err := ctx.Err(); err != nil {
		return ApplyFollowerResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	leo := s.leoLocked()
	for _, record := range req.Records {
		if record.Index == 0 {
			return ApplyFollowerResult{}, ch.ErrInvalidConfig
		}
		if record.Index <= leo {
			existing, ok := s.recordBySeqLocked(record.Index)
			if !ok && record.Index <= s.retention.LocalRetentionThroughSeq {
				continue
			}
			if !ok || existing.ID != record.ID || string(existing.Payload) != string(record.Payload) {
				return ApplyFollowerResult{}, ch.ErrStaleMeta
			}
			continue
		}
		if record.Index != leo+1 {
			return ApplyFollowerResult{}, ch.ErrStaleMeta
		}
		next := record
		next.Payload = cloneBytes(record.Payload)
		if next.SizeBytes == 0 {
			next.SizeBytes = len(next.Payload)
		}
		s.records = append(s.records, next)
		leo++
	}
	return ApplyFollowerResult{LEO: leo}, nil
}

// ReadCommitted returns message rows up to MaxSeq.
func (s *MemoryChannelStore) ReadCommitted(ctx context.Context, req ReadCommittedRequest) (ReadCommittedResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadCommittedResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	leo := s.leoLocked()
	from := req.FromSeq
	if from == 0 {
		from = 1
	}
	if req.MinSeq > 0 && from < req.MinSeq {
		from = req.MinSeq
	}
	limit := req.Limit
	if limit <= 0 {
		limit = len(s.records)
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = int(^uint(0) >> 1)
	}
	if req.Reverse {
		return s.readCommittedReverseLocked(req, limit, maxBytes), nil
	}
	maxSeq := req.MaxSeq
	if maxSeq == 0 || maxSeq > leo {
		maxSeq = leo
	}
	if req.MinSeq > 0 && maxSeq < req.MinSeq {
		return ReadCommittedResult{NextSeq: from}, nil
	}
	messages := make([]ch.Message, 0, limit)
	used := 0
	next := from
	for seq := from; seq <= maxSeq; seq++ {
		record, ok := s.recordBySeqLocked(seq)
		if !ok {
			next = seq + 1
			continue
		}
		if len(messages) >= limit || used+record.SizeBytes > maxBytes && len(messages) > 0 {
			break
		}
		used += record.SizeBytes
		messages = append(messages, messageFromRecord(s.id, record))
		next = seq + 1
	}
	return ReadCommittedResult{Messages: messages, NextSeq: next}, nil
}

func (s *MemoryChannelStore) readCommittedReverseLocked(req ReadCommittedRequest, limit int, maxBytes int) ReadCommittedResult {
	leo := s.leoLocked()
	from := req.FromSeq
	if from == 0 || from > leo {
		from = leo
	}
	if req.MaxSeq > 0 && from > req.MaxSeq {
		from = req.MaxSeq
	}
	minSeq := req.MinSeq
	if minSeq == 0 {
		minSeq = 1
	}
	if from < minSeq {
		return ReadCommittedResult{NextSeq: from}
	}
	messages := make([]ch.Message, 0, limit)
	used := 0
	next := from
	for seq := from; seq >= minSeq && seq <= leo; seq-- {
		record, ok := s.recordBySeqLocked(seq)
		if !ok {
			if seq == minSeq {
				if minSeq == 1 {
					next = 0
				} else {
					next = minSeq - 1
				}
				break
			}
			if seq == 1 {
				next = 0
				break
			}
			next = seq - 1
			continue
		}
		if len(messages) >= limit || used+record.SizeBytes > maxBytes && len(messages) > 0 {
			break
		}
		used += record.SizeBytes
		messages = append(messages, messageFromRecord(s.id, record))
		if seq == minSeq {
			if minSeq == 1 {
				next = 0
			} else {
				next = minSeq - 1
			}
			break
		}
		if seq == 1 {
			next = 0
			break
		}
		next = seq - 1
	}
	return ReadCommittedResult{Messages: messages, NextSeq: next}
}

// LookupMessageByID returns one durable in-memory message by message id.
func (s *MemoryChannelStore) LookupMessageByID(ctx context.Context, messageID uint64) (ch.Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return ch.Message{}, false, err
	}
	if messageID == 0 {
		return ch.Message{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if record.ID == messageID {
			return messageFromRecord(s.id, record), true, nil
		}
	}
	return ch.Message{}, false, nil
}

// ReadLog returns raw records for replication.
func (s *MemoryChannelStore) ReadLog(ctx context.Context, req ReadLogRequest) (ReadLogResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadLogResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	leo := s.leoLocked()
	from := req.FromOffset
	if from == 0 {
		from = 1
	}
	max := req.MaxOffset
	if max == 0 || max > leo {
		max = leo
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = int(^uint(0) >> 1)
	}
	out := make([]ch.Record, 0)
	used := 0
	for seq := from; seq <= max; seq++ {
		record, ok := s.recordBySeqLocked(seq)
		if !ok {
			continue
		}
		if used+record.SizeBytes > maxBytes && len(out) > 0 {
			break
		}
		used += record.SizeBytes
		out = append(out, cloneRecord(record))
	}
	return ReadLogResult{Records: out}, nil
}

// LoadRetentionState returns the in-memory local retention progress.
func (s *MemoryChannelStore) LoadRetentionState(ctx context.Context) (RetentionState, error) {
	if err := ctx.Err(); err != nil {
		return RetentionState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retention, nil
}

// AdoptRetentionBoundary records a monotonic local retention boundary.
func (s *MemoryChannelStore) AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if throughSeq == 0 {
		return 0, ch.ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	leo := s.leoLocked()
	if throughSeq > s.retention.LocalRetentionThroughSeq {
		s.retention.LocalRetentionThroughSeq = throughSeq
	}
	s.retention.RetainedMaxSeq = maxUint64(s.retention.RetainedMaxSeq, maxUint64(leo, throughSeq))
	return s.retention.RetainedMaxSeq, nil
}

// TrimMessagesThrough removes a bounded in-memory prefix through an adopted boundary.
func (s *MemoryChannelStore) TrimMessagesThrough(ctx context.Context, throughSeq uint64, opts RetentionTrimOptions) (RetentionTrimResult, error) {
	if err := ctx.Err(); err != nil {
		return RetentionTrimResult{}, err
	}
	if throughSeq == 0 {
		return RetentionTrimResult{}, ch.ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if throughSeq > s.retention.LocalRetentionThroughSeq {
		return RetentionTrimResult{}, ch.ErrInvalidConfig
	}
	leo := s.leoLocked()
	s.retention.RetainedMaxSeq = maxUint64(s.retention.RetainedMaxSeq, leo)
	result := RetentionTrimResult{}
	deleteCount := 0
	used := 0
	for deleteCount < len(s.records) {
		record := s.records[deleteCount]
		if record.Index > throughSeq {
			break
		}
		if opts.MaxMessages > 0 && result.Deleted >= opts.MaxMessages {
			result.More = true
			break
		}
		if opts.MaxBytes > 0 && result.Deleted > 0 && used+record.SizeBytes > opts.MaxBytes {
			result.More = true
			break
		}
		used += record.SizeBytes
		result.DeletedThroughSeq = record.Index
		result.Deleted++
		deleteCount++
	}
	if deleteCount < len(s.records) && s.records[deleteCount].Index <= throughSeq {
		result.More = true
	}
	if deleteCount > 0 {
		s.records = append([]ch.Record(nil), s.records[deleteCount:]...)
		if result.DeletedThroughSeq > s.retention.PhysicalRetentionThroughSeq {
			s.retention.PhysicalRetentionThroughSeq = result.DeletedThroughSeq
		}
	}
	return result, nil
}

// StoreCheckpoint stores the committed frontier for future loads, ignoring regressive HW.
func (s *MemoryChannelStore) StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if checkpoint.HW > s.checkpoint.HW {
		s.checkpoint = checkpoint
	}
	return nil
}

// Close releases the reactor's in-memory store handle while keeping channel data reloadable.
func (s *MemoryChannelStore) Close() error {
	return nil
}

func messageFromRecord(id ch.ChannelID, record ch.Record) ch.Message {
	return ch.Message{
		MessageID:         record.ID,
		MessageSeq:        record.Index,
		ChannelID:         id.ID,
		ChannelType:       id.Type,
		Setting:           record.Setting,
		FromUID:           record.FromUID,
		ClientMsgNo:       record.ClientMsgNo,
		Payload:           cloneBytes(record.Payload),
		ServerTimestampMS: record.ServerTimestampMS,
		SyncOnce:          record.SyncOnce,
	}
}

func cloneRecord(record ch.Record) ch.Record {
	record.Payload = cloneBytes(record.Payload)
	return record
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func (s *MemoryChannelStore) leoLocked() uint64 {
	if len(s.records) == 0 {
		return s.retention.RetainedMaxSeq
	}
	return maxUint64(s.records[len(s.records)-1].Index, s.retention.RetainedMaxSeq)
}

func (s *MemoryChannelStore) recordBySeqLocked(seq uint64) (ch.Record, bool) {
	if len(s.records) == 0 {
		return ch.Record{}, false
	}
	first := s.records[0].Index
	if seq < first {
		return ch.Record{}, false
	}
	index := seq - first
	if index >= uint64(len(s.records)) {
		return ch.Record{}, false
	}
	record := s.records[index]
	if record.Index != seq {
		return ch.Record{}, false
	}
	return record, true
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}
