package store

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

const logScanInitialCapacity = 16

type LogRecord struct {
	Offset  uint64
	Payload []byte
}

type pendingLogAppend struct {
	base    uint64
	nextLEO uint64
	rows    []messageRow
}

func (s *ChannelStore) validate() error {
	if s == nil || s.engine == nil || s.engine.db == nil || s.key == "" {
		return channel.ErrInvalidArgument
	}
	return nil
}

func (s *ChannelStore) Append(records []channel.Record) (uint64, error) {
	return s.appendRecordsWithCommit(records, pebble.Sync, true)
}

func (s *ChannelStore) appendRecordsNoSync(records []channel.Record) (uint64, error) {
	return s.appendRecordsWithCommit(records, pebble.NoSync, false)
}

func (s *ChannelStore) appendRecordsWithCommit(records []channel.Record, commitOpts *pebble.WriteOptions, recordCommit bool) (uint64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	pending, err := s.prepareCompatibilityAppendLocked(records)
	if err != nil {
		return 0, err
	}
	if len(pending.rows) == 0 {
		return pending.base, nil
	}

	if commitOpts == pebble.Sync && recordCommit {
		if err := s.appendWithCoordinatorLocked(pending); err != nil {
			s.failPendingWrite()
			return 0, err
		}
		return pending.base, nil
	}

	batch := s.engine.db.NewBatch()
	defer batch.Close()

	if err := pending.build(batch, s.messageTable()); err != nil {
		s.failPendingWrite()
		return 0, err
	}
	if err := batch.Commit(commitOpts); err != nil {
		s.failPendingWrite()
		return 0, err
	}
	if recordCommit {
		s.publishDurableWrite(pending.nextLEO)
		return pending.base, nil
	}
	s.publishWrite(pending.nextLEO)
	return pending.base, nil
}

func (s *ChannelStore) prepareCompatibilityAppendLocked(records []channel.Record) (pendingLogAppend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	base, err := s.leoLocked()
	if err != nil {
		return pendingLogAppend{}, err
	}
	if len(records) == 0 {
		return pendingLogAppend{base: base}, nil
	}

	rows, err := structuredRowsFromCompatibilityRecords(base+1, records)
	if err != nil {
		return pendingLogAppend{}, err
	}

	s.writeInProgress.Store(true)
	return pendingLogAppend{
		base:    base,
		nextLEO: base + uint64(len(rows)),
		rows:    rows,
	}, nil
}

func (s *ChannelStore) appendWithCoordinatorLocked(pending pendingLogAppend) error {
	coordinator := s.commitCoordinator()
	if coordinator == nil {
		return channel.ErrInvalidArgument
	}
	return coordinator.submit(commitRequest{
		channelKey: s.key,
		build: func(writeBatch *pebble.Batch) error {
			return pending.build(writeBatch, s.messageTable())
		},
		publish: func() error {
			s.publishDurableWrite(pending.nextLEO)
			return nil
		},
	})
}

func (p pendingLogAppend) build(writeBatch *pebble.Batch, table *messageTable) error {
	return table.append(writeBatch, p.rows)
}

func (s *ChannelStore) Read(from uint64, maxBytes int) ([]channel.Record, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if maxBytes <= 0 || from == math.MaxUint64 {
		return nil, nil
	}

	rows, err := s.messageTable().scanBySeq(from+1, maxLogScanLimit(), maxBytes)
	if err != nil {
		return nil, err
	}
	return compatibilityRecordsFromRows(rows)
}

func (s *ChannelStore) ReadOffsets(fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	return s.readOffsets(fromOffset, limit, maxBytes)
}

// ReadOffsetsReverse returns committed log records in descending offset order.
func (s *ChannelStore) ReadOffsetsReverse(fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	return s.readOffsetsReverse(fromOffset, limit, maxBytes)
}

func (s *ChannelStore) readOffsets(fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.engine.readOffsets(s.key, fromOffset, limit, maxBytes)
}

func (s *ChannelStore) readOffsetsReverse(fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s.engine.readOffsetsReverse(s.key, fromOffset, limit, maxBytes)
}

func (s *ChannelStore) LEO() uint64 {
	if s == nil {
		return 0
	}
	if s.writeInProgress.Load() {
		return s.leo.Load()
	}
	leo, err := s.leoWithError()
	if err != nil {
		return s.leo.Load()
	}
	return leo
}

func (s *ChannelStore) leoWithError() (uint64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leoLocked()
}

func (s *ChannelStore) leoLocked() (uint64, error) {
	if s.loaded.Load() {
		return s.leo.Load(), nil
	}

	maxSeq, err := s.messageTable().maxSeq()
	if err != nil {
		return 0, err
	}
	s.leo.Store(maxSeq)
	s.loaded.Store(true)
	return maxSeq, nil
}

func (s *ChannelStore) Truncate(to uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	leo, err := s.leoLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if to >= leo {
		s.mu.Unlock()
		return nil
	}
	s.writeInProgress.Store(true)
	s.mu.Unlock()
	defer s.writeInProgress.Store(false)

	batch := s.engine.db.NewBatch()
	defer batch.Close()
	if err := s.messageTable().truncateFromSeq(batch, to+1); err != nil {
		return err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}
	s.recordDurableCommit()
	s.mu.Lock()
	s.leo.Store(to)
	s.loaded.Store(true)
	s.mu.Unlock()
	return nil
}

func (s *ChannelStore) Sync() error {
	if err := s.validate(); err != nil {
		return err
	}
	return nil
}

func (e *Engine) Read(channelKey channel.ChannelKey, fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if e == nil || e.db == nil || channelKey == "" {
		return nil, channel.ErrInvalidArgument
	}
	return e.readOffsets(channelKey, fromOffset, limit, maxBytes)
}

// ReadReverse returns log records in descending offset order starting at fromOffset.
func (e *Engine) ReadReverse(channelKey channel.ChannelKey, fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if e == nil || e.db == nil || channelKey == "" {
		return nil, channel.ErrInvalidArgument
	}
	return e.readOffsetsReverse(channelKey, fromOffset, limit, maxBytes)
}

func (e *Engine) readOffsets(channelKey channel.ChannelKey, fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if limit <= 0 || maxBytes <= 0 || fromOffset == math.MaxUint64 {
		return nil, nil
	}

	rows, err := (&messageTable{channelKey: channelKey, db: e.db}).scanBySeq(fromOffset+1, limit, maxBytes)
	if err != nil {
		return nil, err
	}
	return logRecordsFromStructuredRows(rows)
}

func (e *Engine) readOffsetsReverse(channelKey channel.ChannelKey, fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if limit <= 0 || maxBytes <= 0 {
		return nil, nil
	}

	fromSeq := uint64(math.MaxUint64)
	if fromOffset < math.MaxUint64 {
		fromSeq = fromOffset + 1
	}
	rows, err := (&messageTable{channelKey: channelKey, db: e.db}).scanBySeqReverse(fromSeq, limit, maxBytes)
	if err != nil {
		return nil, err
	}
	return logRecordsFromStructuredRows(rows)
}

func maxLogScanLimit() int {
	return math.MaxInt
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
