package store

import (
	"context"
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
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
}

// Load returns the current in-memory offsets.
func (s *MemoryChannelStore) Load(ctx context.Context) (InitialState, error) {
	if err := ctx.Err(); err != nil {
		return InitialState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	leo := uint64(len(s.records))
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
		leo := uint64(len(s.records))
		return AppendLeaderResult{BaseOffset: leo + 1, LastOffset: leo}, nil
	}
	base := uint64(len(s.records)) + 1
	for i, record := range req.Records {
		record.Index = base + uint64(i)
		record.Payload = cloneBytes(record.Payload)
		if record.SizeBytes == 0 {
			record.SizeBytes = len(record.Payload)
		}
		s.records = append(s.records, record)
	}
	return AppendLeaderResult{BaseOffset: base, LastOffset: uint64(len(s.records))}, nil
}

// ApplyFollower applies leader records, skipping any duplicate prefix already stored.
func (s *MemoryChannelStore) ApplyFollower(ctx context.Context, req ApplyFollowerRequest) (ApplyFollowerResult, error) {
	if err := ctx.Err(); err != nil {
		return ApplyFollowerResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	leo := uint64(len(s.records))
	for _, record := range req.Records {
		if record.Index == 0 {
			return ApplyFollowerResult{}, ch.ErrInvalidConfig
		}
		if record.Index <= leo {
			existing := s.records[record.Index-1]
			if existing.ID != record.ID || string(existing.Payload) != string(record.Payload) {
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
	from := req.FromSeq
	if from == 0 {
		from = 1
	}
	limit := req.Limit
	if limit <= 0 {
		limit = len(s.records)
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = int(^uint(0) >> 1)
	}
	messages := make([]ch.Message, 0, limit)
	used := 0
	next := from
	for seq := from; seq <= req.MaxSeq && seq <= uint64(len(s.records)); seq++ {
		record := s.records[seq-1]
		if len(messages) >= limit || used+record.SizeBytes > maxBytes && len(messages) > 0 {
			break
		}
		used += record.SizeBytes
		messages = append(messages, messageFromRecord(s.id, record))
		next = seq + 1
	}
	return ReadCommittedResult{Messages: messages, NextSeq: next}, nil
}

// ReadLog returns raw records for replication.
func (s *MemoryChannelStore) ReadLog(ctx context.Context, req ReadLogRequest) (ReadLogResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadLogResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	from := req.FromOffset
	if from == 0 {
		from = 1
	}
	max := req.MaxOffset
	if max == 0 || max > uint64(len(s.records)) {
		max = uint64(len(s.records))
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = int(^uint(0) >> 1)
	}
	out := make([]ch.Record, 0)
	used := 0
	for seq := from; seq <= max; seq++ {
		record := s.records[seq-1]
		if used+record.SizeBytes > maxBytes && len(out) > 0 {
			break
		}
		used += record.SizeBytes
		out = append(out, cloneRecord(record))
	}
	return ReadLogResult{Records: out}, nil
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
	return ch.Message{MessageID: record.ID, MessageSeq: record.Index, ChannelID: id.ID, ChannelType: id.Type, Payload: cloneBytes(record.Payload)}
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

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}
