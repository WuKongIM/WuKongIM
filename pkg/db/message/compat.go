package message

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	defaultCommitCoordinatorFlushWindow = 200 * time.Microsecond
	defaultCommitCoordinatorQueueSize   = 1024
)

// CommitCoordinatorConfig keeps the legacy channel-store tuning surface.
type CommitCoordinatorConfig struct {
	// FlushWindow is the maximum time spent collecting adjacent commit requests.
	FlushWindow time.Duration
	// QueueSize bounds waiting commit requests before callers apply backpressure.
	QueueSize int
	// MaxRequests caps logical requests per physical commit when positive.
	MaxRequests int
	// MaxRecords caps logical records per physical commit when positive.
	MaxRecords int
	// MaxBytes caps approximate payload bytes per physical commit when positive.
	MaxBytes int
}

// Engine is the compatibility entry point used by existing channel callers.
type Engine struct {
	mu        sync.Mutex
	db        *MessageDB
	engine    *engine.DB
	stores    map[channel.ChannelKey]*ChannelStore
	commitCfg CommitCoordinatorConfig
}

// ChannelStore adapts the new typed ChannelLog to the legacy channel store API.
type ChannelStore struct {
	engine *Engine
	log    *ChannelLog
	key    channel.ChannelKey
	id     channel.ChannelID
}

// LogRecord is an offset-addressed compatibility log record.
type LogRecord struct {
	Offset  uint64
	Payload []byte
}

// RetentionScanResult describes the continuous expired prefix found by a scan.
type RetentionScanResult struct {
	// FromSeq is the normalized sequence where the scan started.
	FromSeq uint64
	// ThroughSeq is the highest continuous expired sequence found.
	ThroughSeq uint64
	// Count is the number of expired rows included in the continuous prefix.
	Count int
}

// Open opens a message DB at path.
func Open(path string) (*Engine, error) {
	eng, err := engine.Open(path, engine.Options{})
	if err != nil {
		return nil, err
	}
	return &Engine{
		db:     NewDB(eng),
		engine: eng,
		stores: make(map[channel.ChannelKey]*ChannelStore),
	}, nil
}

// ConfigureCommitCoordinator stores cross-channel commit tuning for legacy callers.
func (e *Engine) ConfigureCommitCoordinator(cfg CommitCoordinatorConfig) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.commitCfg = effectiveCommitCoordinatorConfig(cfg)
}

// CommitCoordinatorConfig returns the effective legacy commit coordinator settings.
func (e *Engine) CommitCoordinatorConfig() CommitCoordinatorConfig {
	if e == nil {
		return effectiveCommitCoordinatorConfig(CommitCoordinatorConfig{})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return effectiveCommitCoordinatorConfig(e.commitCfg)
}

// Close closes the compatibility engine.
func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	eng := e.engine
	e.engine = nil
	e.db = nil
	e.stores = nil
	e.mu.Unlock()
	if eng == nil {
		return nil
	}
	return eng.Close()
}

// ForChannel returns the channel-scoped compatibility store.
func (e *Engine) ForChannel(key channel.ChannelKey, id channel.ChannelID) *ChannelStore {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.db == nil {
		return &ChannelStore{}
	}
	if e.stores == nil {
		e.stores = make(map[channel.ChannelKey]*ChannelStore)
	}
	if st := e.stores[key]; st != nil {
		if st.id != id {
			panic("message: inconsistent channel key and channel id")
		}
		return st
	}
	st := &ChannelStore{
		engine: e,
		log:    e.db.Channel(ChannelKey(key), ChannelID{ID: id.ID, Type: id.Type}),
		key:    key,
		id:     id,
	}
	e.stores[key] = st
	return st
}

// ListChannelKeys returns persisted channels with message or system state.
func (e *Engine) ListChannelKeys() ([]channel.ChannelKey, error) {
	if e == nil || e.db == nil {
		return nil, channel.ErrInvalidArgument
	}
	entries, err := e.db.ListChannels(context.Background())
	if err != nil {
		return nil, toChannelError(err)
	}
	keys := make([]channel.ChannelKey, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, channel.ChannelKey(entry.Key))
	}
	return keys, nil
}

// Read returns offset-addressed records for channelKey in ascending order.
func (e *Engine) Read(channelKey channel.ChannelKey, fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if e == nil || e.db == nil || channelKey == "" {
		return nil, channel.ErrInvalidArgument
	}
	return readOffsetRecords(e.db.Channel(ChannelKey(channelKey), ChannelID{}), fromOffset, limit, maxBytes, false)
}

// ReadReverse returns offset-addressed records for channelKey in descending order.
func (e *Engine) ReadReverse(channelKey channel.ChannelKey, fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if e == nil || e.db == nil || channelKey == "" {
		return nil, channel.ErrInvalidArgument
	}
	return readOffsetRecords(e.db.Channel(ChannelKey(channelKey), ChannelID{}), fromOffset, limit, maxBytes, true)
}

func effectiveCommitCoordinatorConfig(cfg CommitCoordinatorConfig) CommitCoordinatorConfig {
	if cfg.FlushWindow == 0 {
		cfg.FlushWindow = defaultCommitCoordinatorFlushWindow
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultCommitCoordinatorQueueSize
	}
	return cfg
}

func (s *ChannelStore) validate() error {
	if s == nil || s.engine == nil || s.engine.db == nil || s.log == nil || s.key == "" {
		return channel.ErrInvalidArgument
	}
	return nil
}

// Append appends compatibility records and returns the previous zero-based log end offset.
func (s *ChannelStore) Append(records []channel.Record) (uint64, error) {
	return s.appendRecords(context.Background(), records, AppendStrict)
}

// AppendTrusted appends caller-validated contiguous records.
func (s *ChannelStore) AppendTrusted(records []channel.Record) (uint64, error) {
	return s.appendRecords(context.Background(), records, AppendTrustedContiguous)
}

func (s *ChannelStore) appendRecords(ctx context.Context, records []channel.Record, mode AppendMode) (uint64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()

	base, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return 0, toChannelError(err)
	}
	if len(records) == 0 {
		return base, nil
	}
	rows, err := compatibilityRowsFromRecords(base+1, records)
	if err != nil {
		return 0, err
	}
	if err := s.validateRowsForAppend(ctx, rows, mode); err != nil {
		return 0, err
	}
	if err := s.commitRowsLocked(ctx, rows, nil, nil, base+uint64(len(rows))); err != nil {
		return 0, err
	}
	return base, nil
}

// Read returns compatibility records after from offset.
func (s *ChannelStore) Read(from uint64, maxBytes int) ([]channel.Record, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if maxBytes <= 0 || from == math.MaxUint64 {
		return nil, nil
	}
	rows, err := s.log.readRows(context.Background(), from+1, 0, ReadOptions{MaxBytes: maxBytes})
	if err != nil {
		return nil, toChannelError(err)
	}
	return recordsFromRows(rows)
}

// ReadOffsets returns offset-addressed records in ascending order.
func (s *ChannelStore) ReadOffsets(fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return readOffsetRecords(s.log, fromOffset, limit, maxBytes, false)
}

// ReadOffsetsReverse returns offset-addressed records in descending order.
func (s *ChannelStore) ReadOffsetsReverse(fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return readOffsetRecords(s.log, fromOffset, limit, maxBytes, true)
}

func readOffsetRecords(log *ChannelLog, fromOffset uint64, limit int, maxBytes int, reverse bool) ([]LogRecord, error) {
	if limit <= 0 || maxBytes <= 0 || (!reverse && fromOffset == math.MaxUint64) {
		return nil, nil
	}
	var (
		rows []messageRow
		err  error
	)
	if reverse {
		fromSeq := uint64(0)
		if fromOffset < math.MaxUint64 {
			fromSeq = fromOffset + 1
		}
		rows, err = log.readRowsReverse(context.Background(), fromSeq, ReadOptions{Limit: limit, MaxBytes: maxBytes})
	} else {
		rows, err = log.readRows(context.Background(), fromOffset+1, 0, ReadOptions{Limit: limit, MaxBytes: maxBytes})
	}
	if err != nil {
		return nil, toChannelError(err)
	}
	records := make([]LogRecord, 0, len(rows))
	for _, row := range rows {
		record, err := compatibilityRecordFromRow(row)
		if err != nil {
			return nil, err
		}
		records = append(records, LogRecord{Offset: row.MessageSeq - 1, Payload: record.Payload})
	}
	return records, nil
}

// LEO returns the durable log end offset.
func (s *ChannelStore) LEO() uint64 {
	leo, err := s.LEOWithError()
	if err != nil {
		return 0
	}
	return leo
}

// LEOWithError returns the durable log end offset and surfaces corrupt state.
func (s *ChannelStore) LEOWithError() (uint64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	leo, err := s.log.LEO(context.Background())
	if err != nil {
		return 0, toChannelError(err)
	}
	return leo, nil
}

// Truncate removes message rows after to while preserving retention state.
func (s *ChannelStore) Truncate(to uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.truncateLocked(context.Background(), to, false)
}

// Sync preserves the legacy fsync hook. Mutations already commit durably.
func (s *ChannelStore) Sync() error {
	return s.validate()
}

// GetMessageBySeq loads one message by sequence.
func (s *ChannelStore) GetMessageBySeq(seq uint64) (channel.Message, bool, error) {
	if err := s.validate(); err != nil {
		return channel.Message{}, false, err
	}
	row, ok, err := s.log.getRowBySeq(context.Background(), seq)
	if err != nil || !ok {
		return channel.Message{}, ok, toChannelError(err)
	}
	return channelMessageFromRow(row), true, nil
}

// GetMessageByMessageID loads one message through the message_id index.
func (s *ChannelStore) GetMessageByMessageID(messageID uint64) (channel.Message, bool, error) {
	if err := s.validate(); err != nil {
		return channel.Message{}, false, err
	}
	seq, ok, err := s.log.lookupMessageIDSeq(context.Background(), messageID)
	if err != nil || !ok {
		return channel.Message{}, ok, toChannelError(err)
	}
	row, ok, err := s.log.getRowBySeq(context.Background(), seq)
	if err != nil || !ok {
		return channel.Message{}, ok, toChannelError(err)
	}
	if row.MessageID != messageID {
		return channel.Message{}, false, channel.ErrCorruptState
	}
	return channelMessageFromRow(row), true, nil
}

// ListMessagesBySeq scans persisted messages by sequence.
func (s *ChannelStore) ListMessagesBySeq(fromSeq uint64, limit int, maxBytes int, reverse bool) ([]channel.Message, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	var (
		rows []messageRow
		err  error
	)
	if reverse {
		rows, err = s.log.readRowsReverse(context.Background(), fromSeq, ReadOptions{Limit: limit, MaxBytes: maxBytes})
	} else {
		rows, err = s.log.readRows(context.Background(), fromSeq, 0, ReadOptions{Limit: limit, MaxBytes: maxBytes})
	}
	if err != nil {
		return nil, toChannelError(err)
	}
	messages := make([]channel.Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, channelMessageFromRow(row))
	}
	return messages, nil
}

// ListMessagesByClientMsgNo scans one client_msg_no page in descending sequence order.
func (s *ChannelStore) ListMessagesByClientMsgNo(clientMsgNo string, beforeSeq uint64, limit int) ([]channel.Message, uint64, bool, error) {
	if err := s.validate(); err != nil {
		return nil, 0, false, err
	}
	page, err := s.log.ListByClientMsgNo(context.Background(), clientMsgNo, beforeSeq, limit)
	if err != nil {
		return nil, 0, false, toChannelError(err)
	}
	messages := make([]channel.Message, 0, len(page.Messages))
	for _, msg := range page.Messages {
		row, ok, err := s.log.getRowBySeq(context.Background(), msg.MessageSeq)
		if err != nil || !ok {
			return nil, 0, false, toChannelError(err)
		}
		messages = append(messages, channelMessageFromRow(row))
	}
	return messages, page.NextBeforeSeq, page.HasMore, nil
}

// LookupIdempotency loads a durable idempotency hit.
func (s *ChannelStore) LookupIdempotency(key channel.IdempotencyKey) (channel.IdempotencyEntry, uint64, bool, error) {
	if err := s.validateIdempotencyKey(key); err != nil {
		return channel.IdempotencyEntry{}, 0, false, err
	}
	hit, ok, err := s.log.LookupIdempotency(context.Background(), IdempotencyKey{FromUID: key.FromUID, ClientMsgNo: key.ClientMsgNo})
	if err != nil || !ok {
		return channel.IdempotencyEntry{}, 0, ok, toChannelError(err)
	}
	return channel.IdempotencyEntry{MessageID: hit.MessageID, MessageSeq: hit.MessageSeq, Offset: hit.Offset}, hit.PayloadHash, true, nil
}

// PutIdempotency stores a legacy idempotency entry without requiring a message row.
func (s *ChannelStore) PutIdempotency(key channel.IdempotencyKey, entry channel.IdempotencyEntry) error {
	if err := s.validateIdempotencyKey(key); err != nil {
		return err
	}
	value, err := encodeIdempotencyIndexValue(messageRow{
		MessageSeq:  entry.MessageSeq,
		MessageID:   entry.MessageID,
		FromUID:     key.FromUID,
		ClientMsgNo: key.ClientMsgNo,
	})
	if err != nil {
		return toChannelError(err)
	}
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeMessageIdempotencyIndexKey(s.log.key, key.FromUID, key.ClientMsgNo), value); err != nil {
		return toChannelError(err)
	}
	if err := s.log.stageCatalog(batch); err != nil {
		return toChannelError(err)
	}
	return toChannelError(batch.Commit(true))
}

// GetIdempotency loads a legacy idempotency entry without materializing the row.
func (s *ChannelStore) GetIdempotency(key channel.IdempotencyKey) (channel.IdempotencyEntry, bool, error) {
	if err := s.validateIdempotencyKey(key); err != nil {
		return channel.IdempotencyEntry{}, false, err
	}
	value, ok, err := s.log.db.engine.Get(encodeMessageIdempotencyIndexKey(s.log.key, key.FromUID, key.ClientMsgNo))
	if err != nil || !ok {
		return channel.IdempotencyEntry{}, ok, toChannelError(err)
	}
	hit, err := decodeIdempotencyIndexValue(value)
	if err != nil {
		return channel.IdempotencyEntry{}, false, toChannelError(err)
	}
	return channel.IdempotencyEntry{MessageID: hit.MessageID, MessageSeq: hit.MessageSeq, Offset: hit.Offset}, true, nil
}

func (s *ChannelStore) validateIdempotencyKey(key channel.IdempotencyKey) error {
	if err := s.validate(); err != nil {
		return err
	}
	if key.ChannelID != s.id || key.FromUID == "" || key.ClientMsgNo == "" {
		return channel.ErrInvalidArgument
	}
	return nil
}

// StoreApplyFetch stores fetched records and optional checkpoint.
func (s *ChannelStore) StoreApplyFetch(req channel.ApplyFetchStoreRequest) (uint64, error) {
	return s.applyFetchedRecords(context.Background(), req, nil, AppendStrict)
}

// StoreApplyFetchWithEpoch stores fetched records, checkpoint, and epoch history together.
func (s *ChannelStore) StoreApplyFetchWithEpoch(req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (uint64, error) {
	return s.applyFetchedRecords(context.Background(), req, epochPoint, AppendStrict)
}

// StoreApplyFetchTrusted stores fetched records without existing-index reads.
func (s *ChannelStore) StoreApplyFetchTrusted(req channel.ApplyFetchStoreRequest) (uint64, error) {
	return s.applyFetchedRecords(context.Background(), req, nil, AppendTrustedContiguous)
}

// StoreApplyFetchTrustedWithEpoch is the trusted epoch-aware apply variant.
func (s *ChannelStore) StoreApplyFetchTrustedWithEpoch(req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (uint64, error) {
	return s.applyFetchedRecords(context.Background(), req, epochPoint, AppendTrustedContiguous)
}

func (s *ChannelStore) applyFetchedRecords(ctx context.Context, req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint, mode AppendMode) (uint64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()

	base, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return 0, toChannelError(err)
	}
	nextLEO := base + uint64(len(req.Records))
	if req.Checkpoint != nil {
		if err := validateChannelCheckpoint(*req.Checkpoint); err != nil {
			return 0, err
		}
		if req.Checkpoint.HW < req.PreviousCommittedHW || req.Checkpoint.HW > nextLEO {
			return 0, channel.ErrCorruptState
		}
		if err := s.log.validateCheckpointMonotonic(ctx, checkpointFromChannel(*req.Checkpoint), nextLEO, nextLEO); err != nil {
			return 0, toChannelError(err)
		}
	}
	var point *EpochPoint
	if epochPoint != nil {
		if epochPoint.StartOffset != base {
			return 0, channel.ErrCorruptState
		}
		shouldWrite, err := s.shouldAppendEpochPoint(ctx, *epochPoint)
		if err != nil {
			return 0, err
		}
		if shouldWrite {
			converted := epochPointFromChannel(*epochPoint)
			point = &converted
		}
	}
	if len(req.Records) == 0 && req.Checkpoint == nil && point == nil {
		return base, nil
	}
	rows, err := compatibilityRowsFromRecords(base+1, req.Records)
	if err != nil {
		return 0, err
	}
	if err := s.validateRowsForAppend(ctx, rows, mode); err != nil {
		return 0, err
	}
	var checkpoint *Checkpoint
	if req.Checkpoint != nil {
		converted := checkpointFromChannel(*req.Checkpoint)
		checkpoint = &converted
	}
	if err := s.commitRowsLocked(ctx, rows, checkpoint, point, nextLEO); err != nil {
		return 0, err
	}
	return nextLEO, nil
}

// LoadCheckpoint loads the durable checkpoint.
func (s *ChannelStore) LoadCheckpoint() (channel.Checkpoint, error) {
	if err := s.validate(); err != nil {
		return channel.Checkpoint{}, err
	}
	checkpoint, ok, err := s.log.LoadCheckpoint(context.Background())
	if err != nil {
		return channel.Checkpoint{}, toChannelError(err)
	}
	if !ok {
		return channel.Checkpoint{}, channel.ErrEmptyState
	}
	return checkpointToChannel(checkpoint), nil
}

// StoreCheckpoint stores checkpoint without monotonic validation.
func (s *ChannelStore) StoreCheckpoint(checkpoint channel.Checkpoint) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.log.StoreCheckpoint(context.Background(), checkpointFromChannel(checkpoint)); err != nil {
		return toChannelError(err)
	}
	return nil
}

// StoreCheckpointMonotonic stores checkpoint after durable monotonic validation.
func (s *ChannelStore) StoreCheckpointMonotonic(ctx context.Context, checkpoint channel.Checkpoint, visibleHW uint64, leo uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.log.StoreCheckpointMonotonic(ctx, checkpointFromChannel(checkpoint), visibleHW, leo); err != nil {
		return toChannelError(err)
	}
	return nil
}

// LoadHistory loads epoch history points.
func (s *ChannelStore) LoadHistory() ([]channel.EpochPoint, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	points, ok, err := s.log.LoadHistory(context.Background())
	if err != nil {
		return nil, toChannelError(err)
	}
	if !ok {
		return nil, channel.ErrEmptyState
	}
	out := make([]channel.EpochPoint, 0, len(points))
	for _, point := range points {
		out = append(out, epochPointToChannel(point))
	}
	return out, nil
}

// AppendHistory appends an epoch history point.
func (s *ChannelStore) AppendHistory(point channel.EpochPoint) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.log.AppendHistory(context.Background(), epochPointFromChannel(point)); err != nil {
		return toChannelError(err)
	}
	return nil
}

// TruncateHistoryTo removes history points after leo.
func (s *ChannelStore) TruncateHistoryTo(leo uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.log.TruncateHistoryTo(context.Background(), leo); err != nil {
		return toChannelError(err)
	}
	return nil
}

// BeginEpoch durably appends an epoch boundary at expectedLEO.
func (s *ChannelStore) BeginEpoch(ctx context.Context, point channel.EpochPoint, expectedLEO uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	leo, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return toChannelError(err)
	}
	if point.StartOffset != expectedLEO || leo != expectedLEO {
		return fmt.Errorf("%w: epoch start %d expected leo %d actual leo %d", channel.ErrCorruptState, point.StartOffset, expectedLEO, leo)
	}
	shouldWrite, err := s.shouldAppendEpochPoint(ctx, point)
	if err != nil || !shouldWrite {
		return err
	}
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	if err := s.log.writeHistoryPoint(batch, epochPointFromChannel(point)); err != nil {
		return toChannelError(err)
	}
	if err := s.log.stageCatalog(batch); err != nil {
		return toChannelError(err)
	}
	if err := batch.Commit(true); err != nil {
		return toChannelError(err)
	}
	return nil
}

// TruncateLogAndHistory truncates message rows and future epoch history together.
func (s *ChannelStore) TruncateLogAndHistory(ctx context.Context, to uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.truncateLocked(ctx, to, true)
}

func (s *ChannelStore) truncateLocked(ctx context.Context, to uint64, truncateHistory bool) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	leo, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return toChannelError(err)
	}
	if to > leo {
		return fmt.Errorf("%w: truncate target %d > leo %d", channel.ErrCorruptState, to, leo)
	}
	if to == leo && !truncateHistory {
		return nil
	}
	nextRetention, writeRetention, err := s.retentionStateAfterTruncate(ctx, to)
	if err != nil {
		return err
	}
	rows, err := s.log.readRows(ctx, to+1, 0, ReadOptions{})
	if err != nil {
		return toChannelError(err)
	}
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	for _, row := range rows {
		if err := s.log.stageDeleteMessage(batch, messageFromRow(row)); err != nil {
			return toChannelError(err)
		}
	}
	if writeRetention {
		if err := batch.Set(encodeRetentionStateKey(s.log.key), encodeRetentionState(nextRetention)); err != nil {
			return toChannelError(err)
		}
	}
	if truncateHistory {
		prefix := encodeHistoryPrefix(s.log.key)
		span := keycodec.NewPrefixSpan(prefix)
		if err := batch.DeleteRange(engine.Span{Start: encodeHistoryOffsetKey(s.log.key, to+1), End: span.End}); err != nil {
			return toChannelError(err)
		}
	}
	if err := s.log.stageCatalog(batch); err != nil {
		return toChannelError(err)
	}
	if err := batch.Commit(true); err != nil {
		return toChannelError(err)
	}
	if to < leo {
		s.log.leo.Store(to)
		s.log.loaded.Store(true)
	}
	return nil
}

// StoreSnapshotPayload stores snapshot payload bytes.
func (s *ChannelStore) StoreSnapshotPayload(payload []byte) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.log.StoreSnapshotPayload(context.Background(), payload); err != nil {
		return toChannelError(err)
	}
	return nil
}

// LoadSnapshotPayload loads snapshot payload bytes, returning nil when missing.
func (s *ChannelStore) LoadSnapshotPayload() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	payload, _, err := s.log.LoadSnapshotPayload(context.Background())
	if err != nil {
		return nil, toChannelError(err)
	}
	return payload, nil
}

// InstallSnapshotAtomically stores snapshot payload, checkpoint, and history together.
func (s *ChannelStore) InstallSnapshotAtomically(ctx context.Context, snap channel.Snapshot, checkpoint channel.Checkpoint, epochPoint channel.EpochPoint) (uint64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	leo, err := s.log.LEO(ctx)
	if err != nil {
		return 0, toChannelError(err)
	}
	_, err = s.log.InstallSnapshot(ctx, Snapshot{Epoch: snap.Epoch, EndOffset: snap.EndOffset, Payload: snap.Payload}, checkpointFromChannel(checkpoint), epochPointFromChannel(epochPoint))
	if err != nil {
		return 0, toChannelError(err)
	}
	return leo, nil
}

// LoadRetentionState loads durable local retention progress.
func (s *ChannelStore) LoadRetentionState() (channel.RetentionState, error) {
	if err := s.validate(); err != nil {
		return channel.RetentionState{}, err
	}
	state, ok, err := s.log.LoadRetentionState(context.Background())
	if err != nil {
		return channel.RetentionState{}, toChannelError(err)
	}
	if !ok {
		return channel.RetentionState{}, nil
	}
	return retentionStateToChannel(state), nil
}

// ScanExpiredMessagePrefix scans the continuous local message prefix whose timestamps have expired.
func (s *ChannelStore) ScanExpiredMessagePrefix(fromSeq uint64, cutoff time.Time, limit int) (RetentionScanResult, error) {
	if err := s.validate(); err != nil {
		return RetentionScanResult{}, err
	}
	if fromSeq == 0 {
		fromSeq = 1
	}
	result := RetentionScanResult{FromSeq: fromSeq}
	if limit <= 0 {
		return result, nil
	}
	prefix := encodeMessageRowPrefix(s.log.key)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := s.log.db.engine.NewIter(engine.Span{Start: encodeMessageRowKey(s.log.key, fromSeq, messageHeaderFamilyID), End: span.End}, engine.IterOptions{})
	if err != nil {
		return RetentionScanResult{}, toChannelError(err)
	}
	defer iter.Close()
	expectedSeq := fromSeq
	for ok := iter.First(); ok && result.Count < limit; ok = iter.Next() {
		key := iter.Key()
		seq, familyID, ok := decodeMessageRowKey(s.log.key, key)
		if !ok {
			continue
		}
		if seq < expectedSeq {
			continue
		}
		if seq > expectedSeq {
			break
		}
		if familyID != messageHeaderFamilyID {
			return RetentionScanResult{}, channel.ErrCorruptState
		}
		value, err := iter.Value()
		if err != nil {
			return RetentionScanResult{}, toChannelError(err)
		}
		row := messageRow{MessageSeq: seq}
		if err := decodeMessageHeader(key, value, &row); err != nil {
			return RetentionScanResult{}, toChannelError(err)
		}
		if row.Timestamp <= 0 || time.Unix(row.Timestamp, 0).After(cutoff) {
			break
		}
		result.ThroughSeq = seq
		result.Count++
		expectedSeq = seq + 1
	}
	if err := iter.Error(); err != nil {
		return RetentionScanResult{}, toChannelError(err)
	}
	return result, nil
}

// AdoptRetentionBoundary records a local retention boundary and advances replay cursor.
func (s *ChannelStore) AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) error {
	if throughSeq == 0 {
		return channel.ErrInvalidArgument
	}
	if err := s.validateCursorName(cursorName); err != nil {
		return err
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	leo, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return toChannelError(err)
	}
	state, err := s.loadRetentionState(ctx)
	if err != nil {
		return err
	}
	next := state
	next.LocalRetentionThroughSeq = maxUint64(next.LocalRetentionThroughSeq, throughSeq)
	next.RetainedMaxSeq = maxUint64(next.RetainedMaxSeq, maxUint64(leo, throughSeq))

	cursor, ok, err := s.loadCommittedDispatchCursor(cursorName)
	if err != nil {
		return err
	}
	nextCursor := cursor
	if !ok || cursor < next.LocalRetentionThroughSeq {
		nextCursor = next.LocalRetentionThroughSeq
	}
	if next == state && ok && cursor >= next.LocalRetentionThroughSeq {
		return nil
	}
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	if next != state {
		if err := batch.Set(encodeRetentionStateKey(s.log.key), encodeRetentionState(next)); err != nil {
			return toChannelError(err)
		}
	}
	if !ok || cursor < next.LocalRetentionThroughSeq {
		if err := batch.Set(encodeCommittedCursorKey(s.log.key, cursorName), encodeUint64(nextCursor)); err != nil {
			return toChannelError(err)
		}
	}
	if err := s.log.stageCatalog(batch); err != nil {
		return toChannelError(err)
	}
	if err := batch.Commit(true); err != nil {
		return toChannelError(err)
	}
	if next.RetainedMaxSeq > s.log.leo.Load() {
		s.log.leo.Store(next.RetainedMaxSeq)
		s.log.loaded.Store(true)
	}
	return nil
}

// TrimMessagesThrough removes rows through an already-adopted retention boundary.
func (s *ChannelStore) TrimMessagesThrough(ctx context.Context, throughSeq uint64) error {
	if throughSeq == 0 {
		return channel.ErrInvalidArgument
	}
	if err := s.validate(); err != nil {
		return err
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	leo, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return toChannelError(err)
	}
	state, err := s.loadRetentionState(ctx)
	if err != nil {
		return err
	}
	rows, err := s.log.readRows(ctx, 1, throughSeq, ReadOptions{})
	if err != nil {
		return toChannelError(err)
	}
	if len(rows) == 0 {
		return nil
	}
	next := state
	next.PhysicalRetentionThroughSeq = maxUint64(next.PhysicalRetentionThroughSeq, rows[len(rows)-1].MessageSeq)
	if next.PhysicalRetentionThroughSeq > next.LocalRetentionThroughSeq {
		return channel.ErrCorruptState
	}
	next.RetainedMaxSeq = maxUint64(next.RetainedMaxSeq, leo)
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	for _, row := range rows {
		if err := s.log.stageDeleteMessage(batch, messageFromRow(row)); err != nil {
			return toChannelError(err)
		}
	}
	if err := batch.Set(encodeRetentionStateKey(s.log.key), encodeRetentionState(next)); err != nil {
		return toChannelError(err)
	}
	if err := s.log.stageCatalog(batch); err != nil {
		return toChannelError(err)
	}
	if err := batch.Commit(true); err != nil {
		return toChannelError(err)
	}
	s.log.leo.Store(maxUint64(leo, next.RetainedMaxSeq))
	s.log.loaded.Store(true)
	return nil
}

// LoadCommittedDispatchCursor loads the last dispatched sequence for a replay lane.
func (s *ChannelStore) LoadCommittedDispatchCursor(name string) (uint64, bool, error) {
	if err := s.validateCursorName(name); err != nil {
		return 0, false, err
	}
	return s.loadCommittedDispatchCursor(name)
}

// StoreCommittedDispatchCursor persists replay progress for a lane.
func (s *ChannelStore) StoreCommittedDispatchCursor(name string, seq uint64) error {
	if err := s.validateCursorName(name); err != nil {
		return err
	}
	current, ok, err := s.loadCommittedDispatchCursor(name)
	if err != nil {
		return err
	}
	if ok && current >= seq {
		return nil
	}
	return s.storeCommittedDispatchCursor(name, seq, false)
}

// ConfirmCommittedDispatchCursorDurable syncs an existing cursor when it is at least minSeq.
func (s *ChannelStore) ConfirmCommittedDispatchCursorDurable(name string, minSeq uint64) (uint64, error) {
	if err := s.validateCursorName(name); err != nil {
		return 0, err
	}
	seq, ok, err := s.loadCommittedDispatchCursor(name)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, channel.ErrEmptyState
	}
	if seq < minSeq {
		return 0, channel.ErrCorruptState
	}
	if err := s.storeCommittedDispatchCursor(name, seq, true); err != nil {
		return 0, err
	}
	return seq, nil
}

// AdvanceCommittedDispatchCursorDurable durably moves a replay cursor forward.
func (s *ChannelStore) AdvanceCommittedDispatchCursorDurable(name string, seq uint64) error {
	if err := s.validateCursorName(name); err != nil {
		return err
	}
	current, ok, err := s.loadCommittedDispatchCursor(name)
	if err != nil {
		return err
	}
	if ok && current > seq {
		return channel.ErrCorruptState
	}
	return s.storeCommittedDispatchCursor(name, seq, true)
}

func (s *ChannelStore) validateCursorName(name string) error {
	if err := s.validate(); err != nil {
		return err
	}
	if name == "" {
		return channel.ErrInvalidArgument
	}
	return nil
}

func (s *ChannelStore) loadCommittedDispatchCursor(name string) (uint64, bool, error) {
	value, ok, err := s.log.db.engine.Get(encodeCommittedCursorKey(s.log.key, name))
	if err != nil || !ok {
		return 0, ok, toChannelError(err)
	}
	if len(value) != 8 {
		return 0, false, channel.ErrCorruptValue
	}
	return binary.BigEndian.Uint64(value), true, nil
}

func (s *ChannelStore) storeCommittedDispatchCursor(name string, seq uint64, sync bool) error {
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeCommittedCursorKey(s.log.key, name), encodeUint64(seq)); err != nil {
		return toChannelError(err)
	}
	if err := s.log.stageCatalog(batch); err != nil {
		return toChannelError(err)
	}
	if err := batch.Commit(sync); err != nil {
		return toChannelError(err)
	}
	return nil
}

func (s *ChannelStore) validateRowsForAppend(ctx context.Context, rows []messageRow, mode AppendMode) error {
	seenMessageIDs := make(map[uint64]struct{}, len(rows))
	seenIdempotencyKeys := make(map[IdempotencyKey]struct{}, len(rows))
	for _, row := range rows {
		if err := s.log.validateAppendRow(ctx, row, seenMessageIDs, seenIdempotencyKeys, mode); err != nil {
			return toChannelError(err)
		}
	}
	return nil
}

func (s *ChannelStore) commitRowsLocked(ctx context.Context, rows []messageRow, checkpoint *Checkpoint, point *EpochPoint, nextLEO uint64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	if err := s.log.stageMessageRows(batch, rows); err != nil {
		return toChannelError(err)
	}
	if checkpoint != nil {
		if err := batch.Set(encodeCheckpointKey(s.log.key), encodeCheckpoint(*checkpoint)); err != nil {
			return toChannelError(err)
		}
	}
	if point != nil {
		if err := s.log.writeHistoryPoint(batch, *point); err != nil {
			return toChannelError(err)
		}
	}
	if err := s.log.stageCatalog(batch); err != nil {
		return toChannelError(err)
	}
	if err := batch.Commit(true); err != nil {
		return toChannelError(err)
	}
	if len(rows) > 0 {
		s.log.leo.Store(nextLEO)
		s.log.loaded.Store(true)
	}
	return nil
}

func (s *ChannelStore) shouldAppendEpochPoint(ctx context.Context, point channel.EpochPoint) (bool, error) {
	points, ok, err := s.log.LoadHistory(ctx)
	if err != nil {
		return false, toChannelError(err)
	}
	if !ok {
		points = nil
	}
	shouldWrite, err := shouldAppendHistoryPoint(points, epochPointFromChannel(point))
	if err != nil {
		return false, toChannelError(err)
	}
	return shouldWrite, nil
}

func (s *ChannelStore) loadRetentionState(ctx context.Context) (RetentionState, error) {
	state, ok, err := s.log.LoadRetentionState(ctx)
	if err != nil {
		return RetentionState{}, toChannelError(err)
	}
	if !ok {
		return RetentionState{}, nil
	}
	return state, nil
}

func (s *ChannelStore) retentionStateAfterTruncate(ctx context.Context, to uint64) (RetentionState, bool, error) {
	state, ok, err := s.log.LoadRetentionState(ctx)
	if err != nil || !ok {
		return RetentionState{}, false, toChannelError(err)
	}
	if to < state.LocalRetentionThroughSeq {
		return RetentionState{}, false, channel.ErrCorruptState
	}
	next := state
	if next.RetainedMaxSeq > to {
		next.RetainedMaxSeq = to
	}
	if next == state {
		return RetentionState{}, false, nil
	}
	return next, true, nil
}

func (l *ChannelLog) readRows(ctx context.Context, fromSeq uint64, maxSeq uint64, opts ReadOptions) ([]messageRow, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return nil, dberrors.ErrClosed
	}
	if fromSeq == 0 {
		fromSeq = 1
	}
	prefix := encodeMessageRowPrefix(l.key)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := l.db.engine.NewIter(engine.Span{Start: encodeMessageRowKey(l.key, fromSeq, messageHeaderFamilyID), End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	rows := make([]messageRow, 0, boundedCapacity(16, opts.Limit))
	var totalBytes int
	var current messageRow
	var currentSeq uint64
	var haveRow, haveHeader, havePayload bool
	flush := func() (bool, error) {
		if !haveRow {
			return false, nil
		}
		if !haveHeader || !havePayload {
			return false, fmt.Errorf("%w: incomplete message row at seq %d", dberrors.ErrCorruptState, currentSeq)
		}
		if err := validateMaterializedMessageRow(current); err != nil {
			return false, err
		}
		if opts.MaxBytes > 0 && len(rows) > 0 && totalBytes+len(current.Payload) > opts.MaxBytes {
			return true, nil
		}
		rows = append(rows, current)
		totalBytes += len(current.Payload)
		if opts.Limit > 0 && len(rows) >= opts.Limit {
			return true, nil
		}
		haveRow, haveHeader, havePayload = false, false, false
		current = messageRow{}
		currentSeq = 0
		return false, nil
	}
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		key := iter.Key()
		seq, familyID, ok := decodeMessageRowKey(l.key, key)
		if !ok {
			continue
		}
		if maxSeq > 0 && seq > maxSeq {
			break
		}
		if !haveRow || seq != currentSeq {
			stop, err := flush()
			if err != nil || stop {
				return rows, err
			}
			current = messageRow{MessageSeq: seq}
			currentSeq = seq
			haveRow = true
		}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		switch familyID {
		case messageHeaderFamilyID:
			if err := decodeMessageHeader(key, value, &current); err != nil {
				return nil, err
			}
			haveHeader = true
		case messagePayloadFamilyID:
			if err := decodeMessagePayload(key, value, &current); err != nil {
				return nil, err
			}
			havePayload = true
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	_, err = flush()
	return rows, err
}

func (l *ChannelLog) readRowsReverse(ctx context.Context, fromSeq uint64, opts ReadOptions) ([]messageRow, error) {
	if fromSeq == 0 {
		leo, err := l.LEO(ctx)
		if err != nil {
			return nil, err
		}
		fromSeq = leo
	}
	all, err := l.readRows(ctx, 1, fromSeq, ReadOptions{})
	if err != nil {
		return nil, err
	}
	rows := make([]messageRow, 0, boundedCapacity(len(all), opts.Limit))
	var totalBytes int
	for i := len(all) - 1; i >= 0; i-- {
		row := all[i]
		if opts.MaxBytes > 0 && len(rows) > 0 && totalBytes+len(row.Payload) > opts.MaxBytes {
			break
		}
		rows = append(rows, row)
		totalBytes += len(row.Payload)
		if opts.Limit > 0 && len(rows) >= opts.Limit {
			break
		}
	}
	return rows, nil
}

func compatibilityRowsFromRecords(startSeq uint64, records []channel.Record) ([]messageRow, error) {
	if len(records) == 0 {
		return nil, nil
	}
	if startSeq == 0 {
		return nil, channel.ErrInvalidArgument
	}
	rows := make([]messageRow, 0, len(records))
	for i, record := range records {
		expectedSeq := startSeq + uint64(i)
		if record.Index != 0 && record.Index != expectedSeq {
			return nil, channel.ErrCorruptState
		}
		row, err := decodeCompatibilityRecordPayload(record.Payload)
		if err != nil {
			return nil, err
		}
		if record.ID != 0 && record.ID != row.MessageID {
			return nil, channel.ErrCorruptState
		}
		row.MessageSeq = expectedSeq
		if record.SizeBytes > 0 {
			row.PayloadSize = uint64(record.SizeBytes)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func decodeCompatibilityRecordPayload(payload []byte) (messageRow, error) {
	if len(payload) < channel.DurableMessageHeaderSize {
		return messageRow{}, io.ErrUnexpectedEOF
	}
	if payload[0] != channel.DurableMessageCodecVersion {
		return messageRow{}, channel.ErrCorruptValue
	}
	row := messageRow{
		MessageID:   binary.BigEndian.Uint64(payload[1:9]),
		FramerFlags: payload[9],
		Setting:     payload[10],
		StreamFlag:  payload[11],
		ChannelType: payload[12],
		Expire:      uint64(binary.BigEndian.Uint32(payload[13:17])),
		ClientSeq:   binary.BigEndian.Uint64(payload[17:25]),
		StreamID:    binary.BigEndian.Uint64(payload[25:33]),
		Timestamp:   int64(int32(binary.BigEndian.Uint32(payload[33:37]))),
		PayloadHash: binary.BigEndian.Uint64(payload[37:45]),
	}
	if row.MessageID == 0 {
		return messageRow{}, channel.ErrCorruptValue
	}
	pos := channel.DurableMessageHeaderSize
	var err error
	row.MsgKey, pos, err = readCompatibilityString(payload, pos)
	if err != nil {
		return messageRow{}, err
	}
	row.ClientMsgNo, pos, err = readCompatibilityString(payload, pos)
	if err != nil {
		return messageRow{}, err
	}
	row.StreamNo, pos, err = readCompatibilityString(payload, pos)
	if err != nil {
		return messageRow{}, err
	}
	row.ChannelID, pos, err = readCompatibilityString(payload, pos)
	if err != nil {
		return messageRow{}, err
	}
	row.Topic, pos, err = readCompatibilityString(payload, pos)
	if err != nil {
		return messageRow{}, err
	}
	row.FromUID, pos, err = readCompatibilityString(payload, pos)
	if err != nil {
		return messageRow{}, err
	}
	row.Payload, _, err = readCompatibilityBytes(payload, pos)
	if err != nil {
		return messageRow{}, err
	}
	row.Payload = append([]byte(nil), row.Payload...)
	row.PayloadSize = uint64(len(row.Payload))
	if row.PayloadHash == 0 {
		row.PayloadHash = hashPayload(row.Payload)
	}
	return row, nil
}

func compatibilityRecordFromRow(row messageRow) (channel.Record, error) {
	if err := row.validate(); err != nil {
		return channel.Record{}, toChannelError(err)
	}
	payloadHash := row.PayloadHash
	if payloadHash == 0 {
		payloadHash = hashPayload(row.Payload)
	}
	size := channel.DurableMessageHeaderSize
	for _, fieldSize := range []int{len(row.MsgKey), len(row.ClientMsgNo), len(row.StreamNo), len(row.ChannelID), len(row.Topic), len(row.FromUID), len(row.Payload)} {
		if fieldSize > math.MaxUint32 {
			return channel.Record{}, channel.ErrInvalidArgument
		}
		size += 4 + fieldSize
	}
	payload := make([]byte, 0, size)
	payload = append(payload, channel.DurableMessageCodecVersion)
	payload = binary.BigEndian.AppendUint64(payload, row.MessageID)
	payload = append(payload, row.FramerFlags, row.Setting, row.StreamFlag, row.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, uint32(row.Expire))
	payload = binary.BigEndian.AppendUint64(payload, row.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, row.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(row.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, payloadHash)
	payload = appendCompatibilityString(payload, row.MsgKey)
	payload = appendCompatibilityString(payload, row.ClientMsgNo)
	payload = appendCompatibilityString(payload, row.StreamNo)
	payload = appendCompatibilityString(payload, row.ChannelID)
	payload = appendCompatibilityString(payload, row.Topic)
	payload = appendCompatibilityString(payload, row.FromUID)
	payload = appendCompatibilityBytes(payload, row.Payload)
	return channel.Record{ID: row.MessageID, Index: row.MessageSeq, Payload: payload, SizeBytes: len(payload)}, nil
}

func recordsFromRows(rows []messageRow) ([]channel.Record, error) {
	records := make([]channel.Record, 0, len(rows))
	for _, row := range rows {
		record, err := compatibilityRecordFromRow(row)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func readCompatibilityString(payload []byte, pos int) (string, int, error) {
	value, next, err := readCompatibilityBytes(payload, pos)
	if err != nil {
		return "", pos, err
	}
	return string(value), next, nil
}

func readCompatibilityBytes(payload []byte, pos int) ([]byte, int, error) {
	if len(payload)-pos < 4 {
		return nil, pos, io.ErrUnexpectedEOF
	}
	size := int(binary.BigEndian.Uint32(payload[pos : pos+4]))
	pos += 4
	if len(payload)-pos < size {
		return nil, pos, io.ErrUnexpectedEOF
	}
	return payload[pos : pos+size], pos + size, nil
}

func appendCompatibilityString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendCompatibilityBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func channelMessageFromRow(row messageRow) channel.Message {
	return channel.Message{
		MessageID:   row.MessageID,
		MessageSeq:  row.MessageSeq,
		Framer:      decodeMessageRowFramerFlags(row.FramerFlags),
		Setting:     frame.Setting(row.Setting),
		MsgKey:      row.MsgKey,
		Expire:      uint32(row.Expire),
		ClientSeq:   row.ClientSeq,
		ClientMsgNo: row.ClientMsgNo,
		StreamNo:    row.StreamNo,
		StreamID:    row.StreamID,
		StreamFlag:  frame.StreamFlag(row.StreamFlag),
		Timestamp:   int32(row.Timestamp),
		ChannelID:   row.ChannelID,
		ChannelType: row.ChannelType,
		Topic:       row.Topic,
		FromUID:     row.FromUID,
		Payload:     append([]byte(nil), row.Payload...),
	}
}

func decodeMessageRowFramerFlags(flags uint8) frame.Framer {
	return frame.Framer{
		NoPersist:        flags&1 != 0,
		RedDot:           flags&2 != 0,
		SyncOnce:         flags&4 != 0,
		DUP:              flags&8 != 0,
		HasServerVersion: flags&16 != 0,
		End:              flags&32 != 0,
	}
}

func checkpointFromChannel(checkpoint channel.Checkpoint) Checkpoint {
	return Checkpoint{Epoch: checkpoint.Epoch, LogStartOffset: checkpoint.LogStartOffset, HW: checkpoint.HW}
}

func checkpointToChannel(checkpoint Checkpoint) channel.Checkpoint {
	return channel.Checkpoint{Epoch: checkpoint.Epoch, LogStartOffset: checkpoint.LogStartOffset, HW: checkpoint.HW}
}

func epochPointFromChannel(point channel.EpochPoint) EpochPoint {
	return EpochPoint{Epoch: point.Epoch, StartOffset: point.StartOffset}
}

func epochPointToChannel(point EpochPoint) channel.EpochPoint {
	return channel.EpochPoint{Epoch: point.Epoch, StartOffset: point.StartOffset}
}

func retentionStateToChannel(state RetentionState) channel.RetentionState {
	return channel.RetentionState{
		LocalRetentionThroughSeq:    state.LocalRetentionThroughSeq,
		PhysicalRetentionThroughSeq: state.PhysicalRetentionThroughSeq,
		RetainedMaxSeq:              state.RetainedMaxSeq,
	}
}

func validateChannelCheckpoint(checkpoint channel.Checkpoint) error {
	if checkpoint.LogStartOffset > checkpoint.HW {
		return channel.ErrCorruptState
	}
	return nil
}

func encodeUint64(value uint64) []byte {
	return binary.BigEndian.AppendUint64(nil, value)
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func toChannelError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, dberrors.ErrInvalidArgument) || errors.Is(err, dberrors.ErrClosed) {
		return fmt.Errorf("%w: %v", channel.ErrInvalidArgument, err)
	}
	if errors.Is(err, dberrors.ErrCorruptValue) || errors.Is(err, dberrors.ErrChecksumMismatch) {
		return fmt.Errorf("%w: %v", channel.ErrCorruptValue, err)
	}
	if errors.Is(err, dberrors.ErrCorruptState) || errors.Is(err, dberrors.ErrConflict) {
		return fmt.Errorf("%w: %v", channel.ErrCorruptState, err)
	}
	return err
}
