package message

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	// Keep the compatibility surface aligned with the measured node-store and
	// physical coordinator default.
	defaultCommitCoordinatorFlushWindow = 500 * time.Microsecond
	defaultCommitCoordinatorQueueSize   = 1024
	batchLockRetryMinInterval           = 50 * time.Microsecond
	batchLockRetryMaxInterval           = 2 * time.Millisecond

	commitLaneLeaderAppend      = "leader_append"
	commitLaneReplicaForeground = "replica_foreground"
	commitLaneReplicaTrailing   = "replica_trailing"
	commitLaneFollowerApply     = "follower_apply"
	commitLaneMessageAppend     = "message_append"
)

// AppendBatchClass distinguishes leader-critical, quorum-follower, and
// post-quorum writes without changing their synchronous durability contract.
type AppendBatchClass uint8

const (
	// AppendBatchClassLeaderQuorum is the default class and keeps leader-local
	// durability ahead of redundant follower work.
	AppendBatchClassLeaderQuorum AppendBatchClass = iota
	// AppendBatchClassFollowerQuorum is a synchronous follower vote. It may
	// share a physical commit with leader work but does not overtake it.
	AppendBatchClassFollowerQuorum
	// AppendBatchClassTrailing is post-quorum replica convergence.
	AppendBatchClassTrailing
)

func (c AppendBatchClass) valid() bool {
	return c == AppendBatchClassLeaderQuorum || c == AppendBatchClassFollowerQuorum || c == AppendBatchClassTrailing
}

// CommitCoordinatorConfig keeps the legacy channel-store tuning surface.
type CommitCoordinatorConfig struct {
	// FlushWindow is the maximum time spent collecting adjacent commit requests.
	FlushWindow time.Duration
	// QueueSize bounds waiting commit requests before callers apply backpressure.
	QueueSize int
	// Shards routes commit requests across independent coordinators by channel partition. One keeps serial behavior.
	Shards int
	// MaxRequests caps logical requests per physical commit when positive.
	MaxRequests int
	// MaxRecords caps logical records per physical commit when positive.
	MaxRecords int
	// MaxBytes caps approximate payload bytes per physical commit when positive.
	MaxBytes int
	// Observer receives cross-channel group-commit queue and batch measurements.
	Observer CommitCoordinatorObserver
}

// CommitCoordinatorBatchEvent describes one physical message DB commit attempt.
type CommitCoordinatorBatchEvent struct {
	// Requests is the number of logical channel append requests in the commit.
	Requests int
	// Records is the total logical message record count in the commit.
	Records int
	// Bytes is the approximate payload byte count in the commit.
	Bytes int
	// CollectDuration is the time spent collecting adjacent requests.
	CollectDuration time.Duration
	// BuildDuration is the time spent staging all request mutations into the batch.
	BuildDuration time.Duration
	// CommitDuration is the time spent in the physical storage commit.
	CommitDuration time.Duration
	// PublishDuration is the time spent publishing committed state to callers.
	PublishDuration time.Duration
	// TotalDuration is the sum of collect, build, commit, and publish durations.
	TotalDuration time.Duration
	// Err is set when build, commit, publish, or close failed the batch.
	Err error
}

// CommitCoordinatorRequestEvent describes one caller-visible commit request wait.
type CommitCoordinatorRequestEvent struct {
	// Lane is the low-cardinality logical commit lane name.
	Lane string
	// Records is the logical message record count carried by the request.
	Records int
	// Bytes is the approximate payload byte count carried by the request.
	Bytes int
	// Duration is the caller-visible time spent inside the commit coordinator.
	Duration time.Duration
	// Result is a low-cardinality classification of the Submit result.
	Result string
}

// CommitCoordinatorObserver receives low-cardinality message DB group-commit measurements.
type CommitCoordinatorObserver interface {
	// SetCommitCoordinatorQueueDepth reports the current logical commit queue depth.
	SetCommitCoordinatorQueueDepth(depth int)
	// ObserveCommitCoordinatorBatch reports one grouped physical commit attempt.
	ObserveCommitCoordinatorBatch(event CommitCoordinatorBatchEvent)
}

// CommitCoordinatorQueueObserver receives commit queue depth with its configured capacity.
type CommitCoordinatorQueueObserver interface {
	// SetCommitCoordinatorQueue reports the current logical commit queue depth and capacity.
	SetCommitCoordinatorQueue(depth int, capacity int)
}

// CommitCoordinatorRequestObserver receives optional logical request wait measurements.
type CommitCoordinatorRequestObserver interface {
	// ObserveCommitCoordinatorRequest reports one Submit call without changing commit behavior.
	ObserveCommitCoordinatorRequest(event CommitCoordinatorRequestEvent)
}

// Engine is the compatibility entry point used by existing channel callers.
type Engine struct {
	// mu serializes coordinator replacement with engine shutdown.
	mu sync.Mutex
	// db owns the one canonical channel registry for this physical engine.
	db *MessageDB
	// engine is the physical message store while the compatibility engine is open.
	engine *engine.DB
	// closing prevents coordinator creation after shutdown admission closes.
	closing bool
	// commitCfg is the effective coordinator configuration.
	commitCfg CommitCoordinatorConfig
	// committer owns admitted asynchronous commit requests.
	committer *commit.Coordinator
	// closeOnce makes compatibility shutdown idempotent.
	closeOnce sync.Once
	// closeErr preserves the first shutdown result.
	closeErr error
}

// ChannelStore adapts the new typed ChannelLog to the legacy channel store API.
type ChannelStore struct {
	// engine selects the coordinator and physical commit domain.
	engine *Engine
	// log is this store's distinct lease over a canonical channel entry.
	log *ChannelLog
	// key is the compatibility channel partition key.
	key channel.ChannelKey
	// id is the logical compatibility channel identity.
	id channel.ChannelID
}

// DurableFrontier is one append/checkpoint-consistent exact log snapshot.
// Manifest and TailIdentity are zero only when LEO is zero.
type DurableFrontier struct {
	LEO          uint64
	Committed    uint64
	Manifest     DurableProposalManifest
	TailIdentity quorumlog.EntryIdentity
}

// DurableEntryProbe is one position-aligned exact identity lookup used by
// bounded quorum recovery.
type DurableEntryProbe struct {
	Index    uint64
	Present  bool
	Identity quorumlog.EntryIdentity
}

// DurableRecoveryState is one append/checkpoint-consistent frontier and entry
// identity view.
type DurableRecoveryState struct {
	DurableFrontier
	Entries []DurableEntryProbe
}

// DurableProposal is one complete immutable proposal loaded by command
// identity for exact retry reconciliation.
type DurableProposal struct {
	Manifest DurableProposalManifest
	Records  []channel.Record
}

// AppendBatchItem is one channel append request in a cross-channel batch.
type AppendBatchItem struct {
	// Store is the channel-scoped store that owns Records.
	Store *ChannelStore
	// Records contains messages to append to Store.
	Records []channel.Record
	// Committed is the monotonic HW persisted atomically with an exact append.
	Committed uint64
	// Class controls commit selection only; every class remains synchronous.
	Class AppendBatchClass
	// ServerAllocatedMessageIDs proves globally unique allocator-issued IDs. A
	// fresh exact extension may also omit redundant future-key absence reads;
	// replay, predecessor, and recovery validation remain durable.
	ServerAllocatedMessageIDs bool
	// ExactBaseOffset requires Records to occupy the range immediately after
	// ExpectedBaseOffset. It also permits an exact durable replay of that range.
	ExactBaseOffset bool
	// ExpectedBaseOffset is the zero-based durable frontier preceding an exact append.
	ExpectedBaseOffset uint64
	// Proposal is the immutable durable identity required by exact appends.
	Proposal DurableProposalManifest
}

// AppendBatchResult is the per-item result returned by StoreAppendBatch.
type AppendBatchResult struct {
	// BaseOffset is the previous zero-based log end offset returned by Append.
	BaseOffset uint64
	// LastOffset is the durable last offset after appending this item.
	LastOffset uint64
	// NeedFrom is the exact next offset when an exact append has a gap.
	NeedFrom uint64
	// Err is the item-specific append error.
	Err error
	// Outcome is the closed proof of what this call did to durable state.
	Outcome quorumlog.AppendOutcome
}

// ApplyFetchBatchItem is one channel apply request in a cross-channel batch.
type ApplyFetchBatchItem struct {
	// Store is the channel-scoped store that owns Request.
	Store *ChannelStore
	// Request carries the fetched records and optional system state to apply.
	Request channel.ApplyFetchStoreRequest
}

// ApplyFetchBatchResult is the per-item result returned by StoreApplyFetchTrustedBatch.
type ApplyFetchBatchResult struct {
	// LEO is the store log end offset after applying this item.
	LEO uint64
	// Err is the item-specific apply error.
	Err error
}

// CheckpointHWBatchItem is one monotonic checkpoint high-watermark update.
type CheckpointHWBatchItem struct {
	// Store owns the channel checkpoint updated by HW.
	Store *ChannelStore
	// HW is the durable high watermark to advance monotonically.
	HW uint64
}

// CheckpointHWBatchResult is the per-item checkpoint batch result.
type CheckpointHWBatchResult struct {
	// Err is the item-specific checkpoint update error.
	Err error
}

type batchOwnerGroup struct {
	// owner is the only physical Engine whose locks a group may hold.
	owner *Engine
	// class keeps leader, quorum-follower, and trailing requests in distinct
	// logical commit lanes while preserving one shared physical coordinator.
	class AppendBatchClass
	// indexes preserve the request order for items owned by owner.
	indexes []int
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
	return OpenWithLogger(path, nil)
}

const messageEngineMemTableSize = 64 << 20
const messageEngineCompactionDebtStep = 128 << 20

func messageEngineOptions(logger wklog.Logger) engine.Options {
	return engine.Options{
		// Message appends are the sustained high-write workload. A larger
		// memtable halves its L0 sublevel creation rate without increasing the
		// memory reserved by metadata and other lower-write databases.
		MemTableSize:                   messageEngineMemTableSize,
		CompactionDebtConcurrencyBytes: messageEngineCompactionDebtStep,
		Logger:                         logger,
	}
}

// OpenWithLogger opens a message DB and routes Pebble diagnostics through logger.
func OpenWithLogger(path string, logger wklog.Logger) (*Engine, error) {
	eng, err := engine.Open(path, messageEngineOptions(logger))
	if err != nil {
		return nil, err
	}
	cfg := effectiveCommitCoordinatorConfig(CommitCoordinatorConfig{})
	return &Engine{
		db:        NewDB(eng),
		engine:    eng,
		commitCfg: cfg,
		committer: commit.NewCoordinator(eng, commitCoordinatorConfig(cfg)),
	}, nil
}

// ConfigureCommitCoordinator stores cross-channel commit tuning for legacy callers.
func (e *Engine) ConfigureCommitCoordinator(cfg CommitCoordinatorConfig) {
	if e == nil {
		return
	}
	cfg = effectiveCommitCoordinatorConfig(cfg)
	e.mu.Lock()
	old := e.committer
	e.commitCfg = cfg
	if e.engine != nil && !e.closing {
		e.committer = commit.NewCoordinator(e.engine, commitCoordinatorConfig(cfg))
	} else {
		e.committer = nil
	}
	e.mu.Unlock()
	if old != nil {
		old.Close()
	}
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
	e.closeOnce.Do(func() {
		e.mu.Lock()
		db := e.db
		committer := e.committer
		e.closing = true
		if db != nil && db.registry != nil {
			db.registry.beginClose()
		}
		e.committer = nil
		e.mu.Unlock()

		if committer != nil {
			committer.Close()
		}
		if db == nil {
			e.mu.Lock()
			e.engine = nil
			e.mu.Unlock()
			return
		}
		e.closeErr = db.closeWithBeforeEngineClose(func() {
			e.mu.Lock()
			if e.db == db {
				e.db = nil
				e.engine = nil
			}
			e.mu.Unlock()
		})
	})
	return e.closeErr
}

// ForChannel returns the channel-scoped compatibility store.
func (e *Engine) ForChannel(key channel.ChannelKey, id channel.ChannelID) (*ChannelStore, error) {
	if e == nil || key == "" {
		return nil, channel.ErrInvalidArgument
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return nil, channel.ErrClosed
	}
	log, err := db.Channel(ChannelKey(key), ChannelID{ID: id.ID, Type: id.Type})
	if err != nil {
		return nil, toChannelError(err)
	}
	return &ChannelStore{engine: e, log: log, key: key, id: id}, nil
}

// ListChannelsPage returns one ordered catalog page after the exclusive cursor.
func (e *Engine) ListChannelsPage(ctx context.Context, after ChannelKey, limit int) ([]ChannelCatalogEntry, ChannelKey, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	if e == nil {
		return nil, "", false, channel.ErrClosed
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return nil, "", false, channel.ErrClosed
	}
	entries, cursor, more, err := db.ListChannelsPage(ctx, after, limit)
	return entries, cursor, more, toChannelError(err)
}

// OpenBackupSnapshot pins and streams exact committed channel cuts for cluster backup.
func (e *Engine) OpenBackupSnapshot(ctx context.Context, request BackupSnapshotRequest) (io.ReadCloser, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if e == nil {
		return nil, channel.ErrClosed
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return nil, channel.ErrClosed
	}
	reader, err := db.OpenBackupSnapshot(ctx, request)
	return reader, toChannelError(err)
}

// OpenBackupSnapshotWithStats pins and streams exact committed cuts with
// record-count and allocator-fence evidence from the same database view.
func (e *Engine) OpenBackupSnapshotWithStats(ctx context.Context, request BackupSnapshotRequest) (io.ReadCloser, BackupSnapshotStats, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, BackupSnapshotStats{}, err
	}
	if e == nil {
		return nil, BackupSnapshotStats{}, channel.ErrClosed
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return nil, BackupSnapshotStats{}, channel.ErrClosed
	}
	reader, stats, err := db.OpenBackupSnapshotWithStats(ctx, request)
	return reader, stats, toChannelError(err)
}

// ImportBackupSnapshot verifies and installs a portable message backup snapshot.
func (e *Engine) ImportBackupSnapshot(ctx context.Context, data []byte) (BackupSnapshotStats, error) {
	if err := ctxErr(ctx); err != nil {
		return BackupSnapshotStats{}, err
	}
	if e == nil {
		return BackupSnapshotStats{}, channel.ErrClosed
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return BackupSnapshotStats{}, channel.ErrClosed
	}
	stats, err := db.ImportBackupSnapshot(ctx, data)
	return stats, toChannelError(err)
}

// ImportBackupSnapshotReader validates and installs a seekable portable stream
// with bounded memory.
func (e *Engine) ImportBackupSnapshotReader(ctx context.Context, reader io.ReadSeeker, size int64) (BackupSnapshotStats, error) {
	if err := ctxErr(ctx); err != nil {
		return BackupSnapshotStats{}, err
	}
	if e == nil {
		return BackupSnapshotStats{}, channel.ErrClosed
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return BackupSnapshotStats{}, channel.ErrClosed
	}
	stats, err := db.ImportBackupSnapshotReader(ctx, reader, size)
	return stats, toChannelError(err)
}

// ListLatestMessages returns one node-local newest-first message page.
func (e *Engine) ListLatestMessages(ctx context.Context, beforeMessageID uint64, limit int) (LatestMessagePage, error) {
	if err := ctx.Err(); err != nil {
		return LatestMessagePage{}, err
	}
	if e == nil {
		return LatestMessagePage{}, channel.ErrClosed
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return LatestMessagePage{}, channel.ErrClosed
	}
	page, err := db.ListLatestMessages(ctx, beforeMessageID, limit)
	return page, toChannelError(err)
}

// ListChannelKeys returns persisted channels with message or system state.
func (e *Engine) ListChannelKeys() ([]channel.ChannelKey, error) {
	if e == nil {
		return nil, channel.ErrInvalidArgument
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return nil, channel.ErrClosed
	}
	entries, err := db.ListChannels(context.Background())
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
	if e == nil || channelKey == "" {
		return nil, channel.ErrInvalidArgument
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return nil, channel.ErrClosed
	}
	if err := db.beginUse(); err != nil {
		return nil, toChannelError(err)
	}
	defer db.endUse()
	return readOffsetRecordsRaw(db, ChannelKey(channelKey), fromOffset, limit, maxBytes, false)
}

// ReadReverse returns offset-addressed records for channelKey in descending order.
func (e *Engine) ReadReverse(channelKey channel.ChannelKey, fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if e == nil || channelKey == "" {
		return nil, channel.ErrInvalidArgument
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return nil, channel.ErrClosed
	}
	if err := db.beginUse(); err != nil {
		return nil, toChannelError(err)
	}
	defer db.endUse()
	return readOffsetRecordsRaw(db, ChannelKey(channelKey), fromOffset, limit, maxBytes, true)
}

func effectiveCommitCoordinatorConfig(cfg CommitCoordinatorConfig) CommitCoordinatorConfig {
	if cfg.FlushWindow == 0 {
		cfg.FlushWindow = defaultCommitCoordinatorFlushWindow
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultCommitCoordinatorQueueSize
	}
	if cfg.Shards <= 0 {
		cfg.Shards = 1
	}
	return cfg
}

func commitCoordinatorConfig(cfg CommitCoordinatorConfig) commit.Config {
	cfg = effectiveCommitCoordinatorConfig(cfg)
	return commit.Config{
		FlushWindow: cfg.FlushWindow,
		QueueSize:   cfg.QueueSize,
		Shards:      cfg.Shards,
		MaxRequests: cfg.MaxRequests,
		MaxRecords:  cfg.MaxRecords,
		MaxBytes:    cfg.MaxBytes,
		Observer:    commitCoordinatorObserver(cfg.Observer, cfg.QueueSize*cfg.Shards),
	}
}

func commitCoordinatorObserver(observer CommitCoordinatorObserver, queueSize int) commit.Observer {
	if observer == nil {
		return nil
	}
	return commitObserverAdapter{observer: observer, queueSize: queueSize}
}

type commitObserverAdapter struct {
	observer  CommitCoordinatorObserver
	queueSize int
}

func (a commitObserverAdapter) SetQueueDepth(depth int) {
	if a.observer == nil {
		return
	}
	if observer, ok := a.observer.(CommitCoordinatorQueueObserver); ok {
		observer.SetCommitCoordinatorQueue(depth, a.queueSize)
		return
	}
	a.observer.SetCommitCoordinatorQueueDepth(depth)
}

func (a commitObserverAdapter) ObserveBatch(event commit.BatchEvent) {
	if a.observer == nil {
		return
	}
	a.observer.ObserveCommitCoordinatorBatch(CommitCoordinatorBatchEvent{
		Requests:        event.Requests,
		Records:         event.Records,
		Bytes:           event.Bytes,
		CollectDuration: event.CollectDuration,
		BuildDuration:   event.BuildDuration,
		CommitDuration:  event.CommitDuration,
		PublishDuration: event.PublishDuration,
		TotalDuration:   event.TotalDuration,
		Err:             event.Err,
	})
}

func (a commitObserverAdapter) ObserveRequest(event commit.RequestEvent) {
	if a.observer == nil {
		return
	}
	observer, ok := a.observer.(CommitCoordinatorRequestObserver)
	if !ok {
		return
	}
	observer.ObserveCommitCoordinatorRequest(CommitCoordinatorRequestEvent{
		Lane:     commitCoordinatorLaneName(event.Lane),
		Records:  event.Records,
		Bytes:    event.Bytes,
		Duration: event.Duration,
		Result:   commitCoordinatorRequestResult(event.Err),
	})
}

func commitCoordinatorLaneName(lane commit.Lane) string {
	if lane.Name == "" {
		return "default"
	}
	return lane.Name
}

func commitCoordinatorRequestResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, commit.ErrClosed):
		return "closed"
	case errors.Is(err, dberrors.ErrInvalidArgument):
		return "invalid"
	default:
		return "err"
	}
}

func (s *ChannelStore) validate() error {
	if s == nil || s.engine == nil || s.log == nil || s.key == "" {
		return channel.ErrInvalidArgument
	}
	if err := s.log.validateLease(); err != nil {
		return toChannelError(err)
	}
	return nil
}

func (s *ChannelStore) beginUse() error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.log.beginUse(); err != nil {
		return toChannelError(err)
	}
	return nil
}

func (s *ChannelStore) endUse() {
	if s != nil && s.log != nil {
		s.log.endUse()
	}
}

// Close releases this compatibility lease without closing the shared engine.
func (s *ChannelStore) Close() error {
	if s == nil || s.log == nil {
		return nil
	}
	return s.log.Close()
}

// Append appends compatibility records and returns the previous zero-based log end offset.
func (s *ChannelStore) Append(records []channel.Record) (uint64, error) {
	return s.appendRecords(context.Background(), records, AppendStrict)
}

// AppendTrusted appends caller-validated contiguous records.
func (s *ChannelStore) AppendTrusted(records []channel.Record) (uint64, error) {
	return s.appendRecords(context.Background(), records, AppendTrustedContiguous)
}

// AppendServerAllocated appends records whose message IDs were issued by the server allocator.
// Existing idempotency keys remain strictly validated.
func (s *ChannelStore) AppendServerAllocated(records []channel.Record) (uint64, error) {
	return s.appendRecords(context.Background(), records, AppendServerAllocatedMessageID)
}

func (s *ChannelStore) appendRecords(ctx context.Context, records []channel.Record, mode AppendMode) (uint64, error) {
	if err := s.beginUse(); err != nil {
		return 0, err
	}
	defer s.endUse()
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	s.log.appendMu.Lock()

	prepared, err := s.prepareAppendRecordsLocked(ctx, records, mode)
	if err != nil {
		s.log.appendMu.Unlock()
		return 0, err
	}
	if !prepared.hasWrites() {
		s.log.appendMu.Unlock()
		return prepared.baseOffset, nil
	}
	if err := s.commitPreparedRowsBatch(ctx, []preparedCommitRows{prepared}, commitLaneLeaderAppend); err != nil {
		return 0, err
	}
	return prepared.baseOffset, nil
}

// StoreAppendBatch appends records for multiple channels in one leader_append commit request when possible.
func StoreAppendBatch(ctx context.Context, items []AppendBatchItem) []AppendBatchResult {
	results := make([]AppendBatchResult, len(items))
	if len(items) == 0 {
		return results
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctxErr(ctx); err != nil {
		for i := range results {
			results[i].Err = err
			results[i].Outcome = quorumlog.AppendOutcomeDefinitelyNotWritten
		}
		return results
	}
	activeStores := make(map[*ChannelStore]struct{}, len(items))
	defer func() {
		for store := range activeStores {
			store.log.endUse()
		}
	}()
	indexesByEntry := make(map[*channelEntry][]int, len(items))
	for i, item := range items {
		if item.Store == nil || item.Store.log == nil || item.Store.log.channelEntry == nil {
			results[i].Err = channel.ErrInvalidArgument
			results[i].Outcome = quorumlog.AppendOutcomeDefinitelyNotWritten
			continue
		}
		indexesByEntry[item.Store.log.channelEntry] = append(indexesByEntry[item.Store.log.channelEntry], i)
	}
	for _, indexes := range indexesByEntry {
		if len(indexes) <= 1 {
			continue
		}
		for _, index := range indexes {
			if !items[index].ExactBaseOffset {
				for _, duplicateIndex := range indexes {
					results[duplicateIndex].Err = channel.ErrInvalidArgument
					results[duplicateIndex].Outcome = quorumlog.AppendOutcomeDefinitelyNotWritten
				}
				break
			}
		}
	}
	for i, item := range items {
		if results[i].Err != nil {
			continue
		}
		if err := item.Store.validate(); err != nil {
			results[i].Err = err
			results[i].Outcome = appendOutcomeForPreCommitError(err)
			continue
		}
		if _, ok := activeStores[item.Store]; !ok {
			if err := item.Store.log.beginUse(); err != nil {
				results[i].Err = toChannelError(err)
				results[i].Outcome = appendOutcomeForPreCommitError(results[i].Err)
				continue
			}
			activeStores[item.Store] = struct{}{}
		}
	}
	type batchOwnerClass struct {
		owner *Engine
		class AppendBatchClass
	}
	groups := make([]batchOwnerGroup, 0)
	groupByOwnerClass := make(map[batchOwnerClass]int)
	for index, item := range items {
		if results[index].Err != nil {
			continue
		}
		if !item.Class.valid() {
			results[index].Err = channel.ErrInvalidArgument
			results[index].Outcome = quorumlog.AppendOutcomeDefinitelyNotWritten
			continue
		}
		owner := item.Store.engine
		key := batchOwnerClass{owner: owner, class: item.Class}
		groupIndex, ok := groupByOwnerClass[key]
		if !ok {
			groupIndex = len(groups)
			groupByOwnerClass[key] = groupIndex
			groups = append(groups, batchOwnerGroup{owner: owner, class: item.Class})
		}
		groups[groupIndex].indexes = append(groups[groupIndex].indexes, index)
	}
	if len(groups) == 0 {
		return results
	}
	for _, group := range groups {
		if err := ctxErr(ctx); err != nil {
			for _, index := range group.indexes {
				results[index].Err = err
				results[index].Outcome = quorumlog.AppendOutcomeDefinitelyNotWritten
			}
			continue
		}
		lane := commitLaneForAppendBatchClass(group.class)
		storeAppendBatchOwner(ctx, group.owner, items, group.indexes, results, lane)
	}
	return results
}

func commitLaneForAppendBatchClass(class AppendBatchClass) string {
	switch class {
	case AppendBatchClassFollowerQuorum:
		return commitLaneReplicaForeground
	case AppendBatchClassTrailing:
		return commitLaneReplicaTrailing
	default:
		return commitLaneLeaderAppend
	}
}

func storeAppendBatchOwner(ctx context.Context, owner *Engine, items []AppendBatchItem, indexes []int, results []AppendBatchResult, lane string) {
	if err := ctxErr(ctx); err != nil {
		for _, index := range indexes {
			results[index].Err = err
			results[index].Outcome = quorumlog.AppendOutcomeDefinitelyNotWritten
		}
		return
	}
	indexesByEntry := make(map[*channelEntry][]int, len(indexes))
	checkpointByEntry := make(map[*channelEntry]struct{}, len(indexes))
	for _, index := range indexes {
		entry := items[index].Store.log.channelEntry
		indexesByEntry[entry] = append(indexesByEntry[entry], index)
		if items[index].Committed > 0 {
			checkpointByEntry[entry] = struct{}{}
		}
	}
	entries := make([]*channelEntry, 0, len(indexesByEntry))
	for entry := range indexesByEntry {
		entries = append(entries, entry)
	}
	checkpointEntries := make([]*channelEntry, 0, len(checkpointByEntry))
	for entry := range checkpointByEntry {
		checkpointEntries = append(checkpointEntries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	sort.Slice(checkpointEntries, func(i, j int) bool { return checkpointEntries[i].key < checkpointEntries[j].key })
	for _, entry := range entries {
		entry.appendMu.Lock()
	}
	for _, entry := range checkpointEntries {
		entry.checkpointMu.Lock()
	}
	locked := make(map[*channelEntry]struct{}, len(entries))
	lockedCheckpoints := make(map[*channelEntry]struct{}, len(checkpointEntries))
	for _, entry := range entries {
		locked[entry] = struct{}{}
	}
	for _, entry := range checkpointEntries {
		lockedCheckpoints[entry] = struct{}{}
	}
	defer func() {
		for entry := range lockedCheckpoints {
			entry.checkpointMu.Unlock()
		}
		for entry := range locked {
			entry.appendMu.Unlock()
		}
	}()

	type pendingAppendResult struct {
		index          int
		successOutcome quorumlog.AppendOutcome
	}
	preparedRows := make([]preparedCommitRows, 0, len(entries))
	pendingResults := make([]pendingAppendResult, 0, len(indexes))
	for _, entry := range entries {
		entryIndexes := indexesByEntry[entry]
		if err := ctxErr(ctx); err != nil {
			for _, index := range entryIndexes {
				results[index].Err = err
				results[index].Outcome = quorumlog.AppendOutcomeDefinitelyNotWritten
			}
			if _, ok := lockedCheckpoints[entry]; ok {
				entry.checkpointMu.Unlock()
				delete(lockedCheckpoints, entry)
			}
			entry.appendMu.Unlock()
			delete(locked, entry)
			continue
		}
		physicalLEO, err := items[entryIndexes[0]].Store.log.loadLEOLocked(ctx)
		if err != nil {
			err = toChannelError(err)
			for _, index := range entryIndexes {
				results[index].Err = err
				results[index].Outcome = appendOutcomeForPreCommitError(err)
			}
			if _, ok := lockedCheckpoints[entry]; ok {
				entry.checkpointMu.Unlock()
				delete(lockedCheckpoints, entry)
			}
			entry.appendMu.Unlock()
			delete(locked, entry)
			continue
		}
		virtualLEO := physicalLEO
		totalRecords := 0
		for _, index := range entryIndexes {
			totalRecords += len(items[index].Records)
		}
		seen := newAppendValidationSeen(totalRecords)
		stagedCommands := make(map[quorumlog.CommandID]durableProposalRecord, len(entryIndexes))
		stagedLast := make(map[uint64]durableProposalRecord, len(entryIndexes))
		stagedEntries := make(map[uint64]quorumlog.EntryIdentity, totalRecords)
		var previousStaged quorumlog.EntryIdentity
		var combined preparedCommitRows
		for _, index := range entryIndexes {
			item := items[index]
			mode := AppendStrict
			if item.ServerAllocatedMessageIDs {
				mode = AppendServerAllocatedMessageID
			}
			var prepared preparedCommitRows
			var prepareErr error
			if item.ExactBaseOffset {
				switch {
				case item.ExpectedBaseOffset > virtualLEO:
					prepareErr = &exactAppendGapError{needFrom: virtualLEO + 1}
				case virtualLEO > physicalLEO && item.ExpectedBaseOffset >= physicalLEO && item.ExpectedBaseOffset < virtualLEO:
					prepared, prepareErr = item.Store.prepareStagedExactReplayLocked(
						ctx, item, virtualLEO, stagedCommands, stagedLast, stagedEntries,
					)
				case item.ExpectedBaseOffset > physicalLEO:
					prepared, prepareErr = item.Store.prepareAdjacentExactAppendLocked(
						ctx, item, previousStaged, mode, &seen, stagedCommands, stagedLast, stagedEntries,
					)
				default:
					prepared, prepareErr = item.Store.prepareExactAppendRecordsLocked(
						ctx, item.ExpectedBaseOffset, item.Records, item.Proposal, item.Committed, mode, &seen,
					)
				}
			} else {
				if item.Committed != 0 {
					prepareErr = channel.ErrInvalidArgument
				} else {
					prepared, prepareErr = item.Store.prepareAppendRecordsLocked(ctx, item.Records, mode)
				}
			}
			if prepareErr != nil {
				results[index].Err = prepareErr
				results[index].Outcome = appendOutcomeForPreCommitError(prepareErr)
				var gap *exactAppendGapError
				if errors.As(prepareErr, &gap) {
					results[index].NeedFrom = gap.needFrom
				}
				continue
			}
			prepared.index = index
			results[index].BaseOffset = prepared.baseOffset
			results[index].LastOffset = prepared.nextLEO
			successOutcome := quorumlog.AppendOutcomeDurable
			if prepared.alreadyDurable {
				successOutcome = quorumlog.AppendOutcomeAlreadyDurable
			}
			if !prepared.hasWrites() && !prepared.dependsOnCommit {
				results[index].Outcome = successOutcome
				continue
			}
			if prepared.hasWrites() {
				if err := mergePreparedCommitRows(&combined, prepared); err != nil {
					results[index] = AppendBatchResult{Outcome: quorumlog.AppendOutcomeDefinitelyNotWritten, Err: err}
					continue
				}
			}
			pendingResults = append(pendingResults, pendingAppendResult{index: index, successOutcome: successOutcome})
			if len(prepared.rows) > 0 {
				virtualLEO = prepared.nextLEO
				for _, proposal := range prepared.proposals {
					stagedCommands[proposal.manifest.CommandID] = proposal
					stagedLast[proposal.manifest.LastOffset] = proposal
				}
				for _, stagedEntry := range prepared.entries {
					stagedEntries[stagedEntry.Index] = stagedEntry
				}
				if len(prepared.entries) > 0 {
					previousStaged = prepared.entries[len(prepared.entries)-1]
				}
			}
		}
		if combined.hasWrites() {
			if _, ok := lockedCheckpoints[entry]; ok {
				combined.checkpointLocked = true
			}
			preparedRows = append(preparedRows, combined)
			delete(locked, entry)
			delete(lockedCheckpoints, entry)
			continue
		}
		if _, ok := lockedCheckpoints[entry]; ok {
			entry.checkpointMu.Unlock()
			delete(lockedCheckpoints, entry)
		}
		entry.appendMu.Unlock()
		delete(locked, entry)
	}
	if len(preparedRows) > 0 {
		commitResult := commitPreparedRowsBatchResult(ctx, owner, preparedRows, lane)
		commitOutcome := appendOutcomeForCommitResult(commitResult)
		commitErr := toChannelError(commitResult.Err)
		for _, pending := range pendingResults {
			if commitResult.Outcome == commit.OutcomeCommitted {
				results[pending.index].Outcome = pending.successOutcome
				results[pending.index].Err = nil
				continue
			}
			results[pending.index].BaseOffset = 0
			results[pending.index].LastOffset = 0
			results[pending.index].Outcome = commitOutcome
			results[pending.index].Err = commitErr
		}
	}
}

func mergePreparedCommitRows(target *preparedCommitRows, item preparedCommitRows) error {
	if target == nil || item.store == nil {
		return channel.ErrInvalidArgument
	}
	if target.store == nil {
		target.store = item.store
		target.baseOffset = item.baseOffset
	}
	if target.store.log == nil || item.store.log == nil ||
		target.store.log.channelEntry != item.store.log.channelEntry {
		return channel.ErrInvalidArgument
	}
	target.rows = append(target.rows, item.rows...)
	target.proposals = append(target.proposals, item.proposals...)
	target.entries = append(target.entries, item.entries...)
	target.checkpointLocked = target.checkpointLocked || item.checkpointLocked
	if item.checkpoint != nil && (target.checkpoint == nil || item.checkpoint.HW > target.checkpoint.HW) {
		checkpoint := *item.checkpoint
		target.checkpoint = &checkpoint
	}
	target.nextLEO = max(target.nextLEO, item.nextLEO)
	return nil
}

func appendOutcomeForPreCommitError(err error) quorumlog.AppendOutcome {
	if errors.Is(err, channel.ErrCorruptState) || errors.Is(err, dberrors.ErrCorruptState) || errors.Is(err, dberrors.ErrConflict) {
		return quorumlog.AppendOutcomeConflict
	}
	return quorumlog.AppendOutcomeDefinitelyNotWritten
}

func appendOutcomeForCommitResult(result commit.SubmitResult) quorumlog.AppendOutcome {
	switch result.Outcome {
	case commit.OutcomeCommitted:
		return quorumlog.AppendOutcomeDurable
	case commit.OutcomeDefinitelyNotCommitted:
		return appendOutcomeForPreCommitError(result.Err)
	default:
		return quorumlog.AppendOutcomeUnknown
	}
}

// Read returns compatibility records after from offset.
func (s *ChannelStore) Read(from uint64, maxBytes int) ([]channel.Record, error) {
	if err := s.beginUse(); err != nil {
		return nil, err
	}
	defer s.endUse()
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
	if err := s.beginUse(); err != nil {
		return nil, err
	}
	defer s.endUse()
	return readOffsetRecords(s.log, fromOffset, limit, maxBytes, false)
}

// ReadOffsetsReverse returns offset-addressed records in descending order.
func (s *ChannelStore) ReadOffsetsReverse(fromOffset uint64, limit int, maxBytes int) ([]LogRecord, error) {
	if err := s.beginUse(); err != nil {
		return nil, err
	}
	defer s.endUse()
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

func readOffsetRecordsRaw(db *MessageDB, key ChannelKey, fromOffset uint64, limit int, maxBytes int, reverse bool) ([]LogRecord, error) {
	if limit <= 0 || maxBytes <= 0 || (!reverse && fromOffset == math.MaxUint64) {
		return nil, nil
	}
	var (
		rows []messageRow
		err  error
	)
	if reverse {
		maxSeq := uint64(0)
		if fromOffset < math.MaxUint64 {
			maxSeq = fromOffset + 1
		}
		all, readErr := readRowsRaw(context.Background(), db, key, 1, maxSeq, ReadOptions{})
		if readErr != nil {
			return nil, toChannelError(readErr)
		}
		rows = make([]messageRow, 0, boundedCapacity(len(all), limit))
		totalBytes := 0
		for i := len(all) - 1; i >= 0; i-- {
			row := all[i]
			if len(rows) > 0 && totalBytes+len(row.Payload) > maxBytes {
				break
			}
			rows = append(rows, row)
			totalBytes += len(row.Payload)
			if len(rows) == limit {
				break
			}
		}
	} else {
		rows, err = readRowsRaw(context.Background(), db, key, fromOffset+1, 0, ReadOptions{Limit: limit, MaxBytes: maxBytes})
		if err != nil {
			return nil, toChannelError(err)
		}
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
	if err := s.beginUse(); err != nil {
		return 0, err
	}
	defer s.endUse()
	leo, err := s.log.loadLEO(context.Background())
	if err != nil {
		return 0, toChannelError(err)
	}
	return leo, nil
}

// LoadDurableFrontier reads the exact proposal and entry identity at the
// durable local tail under the canonical append/checkpoint locks.
func (s *ChannelStore) LoadDurableFrontier(ctx context.Context) (DurableFrontier, error) {
	recovery, err := s.LoadDurableRecovery(ctx, nil)
	return recovery.DurableFrontier, err
}

// LoadDurableRecovery reads one exact frontier plus requested entry identities
// under the canonical append/checkpoint locks.
func (s *ChannelStore) LoadDurableRecovery(ctx context.Context, indexes []uint64) (DurableRecoveryState, error) {
	if ctx == nil {
		return DurableRecoveryState{}, channel.ErrInvalidArgument
	}
	if err := s.beginUse(); err != nil {
		return DurableRecoveryState{}, err
	}
	defer s.endUse()
	if err := ctx.Err(); err != nil {
		return DurableRecoveryState{}, err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	s.log.checkpointMu.Lock()
	defer s.log.checkpointMu.Unlock()

	leo, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return DurableRecoveryState{}, toChannelError(err)
	}
	result := DurableRecoveryState{
		DurableFrontier: DurableFrontier{LEO: leo},
		Entries:         make([]DurableEntryProbe, len(indexes)),
	}
	checkpoint, present, err := s.log.loadCheckpoint(ctx)
	if err != nil {
		return DurableRecoveryState{}, toChannelError(err)
	}
	if present {
		if checkpoint.HW > leo {
			return DurableRecoveryState{}, channel.ErrCorruptState
		}
		result.Committed = checkpoint.HW
	}
	if leo > 0 {
		proposal, present, err := loadDurableProposalPairByLast(s.log.db.engine, s.log.key, leo)
		if err != nil {
			return DurableRecoveryState{}, toChannelError(err)
		}
		if !present {
			return DurableRecoveryState{}, channel.ErrCorruptState
		}
		entry, present, err := loadDurableEntryIdentityFrom(s.log.db.engine, s.log.key, leo)
		if err != nil {
			return DurableRecoveryState{}, toChannelError(err)
		}
		manifest := proposal.manifest
		if !present || manifest.LastOffset != leo || manifest.Digest != entry.Digest ||
			manifest.ChannelEpoch != entry.ChannelEpoch || manifest.LeaderTerm != entry.LeaderTerm ||
			manifest.FenceVersion != entry.FenceVersion || manifest.CommandID != entry.CommandID {
			return DurableRecoveryState{}, channel.ErrCorruptState
		}
		result.Manifest = manifest
		result.TailIdentity = entry
	}
	for position, index := range indexes {
		if index == 0 {
			return DurableRecoveryState{}, channel.ErrInvalidArgument
		}
		probe := DurableEntryProbe{Index: index}
		if index <= leo {
			identity, present, err := loadDurableEntryIdentityFrom(s.log.db.engine, s.log.key, index)
			if err != nil {
				return DurableRecoveryState{}, toChannelError(err)
			}
			if !present || identity.Index != index {
				return DurableRecoveryState{}, channel.ErrCorruptState
			}
			probe.Present = true
			probe.Identity = identity
		}
		result.Entries[position] = probe
	}
	return result, nil
}

// Truncate removes message rows after to while preserving retention state.
func (s *ChannelStore) Truncate(to uint64) error {
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	return s.truncateLocked(context.Background(), to, false)
}

// Sync preserves the legacy fsync hook. Mutations already commit durably.
func (s *ChannelStore) Sync() error {
	if err := s.beginUse(); err != nil {
		return err
	}
	s.endUse()
	return nil
}

// GetMessageBySeq loads one message by sequence.
func (s *ChannelStore) GetMessageBySeq(seq uint64) (channel.Message, bool, error) {
	if err := s.beginUse(); err != nil {
		return channel.Message{}, false, err
	}
	defer s.endUse()
	row, ok, err := s.log.getRowBySeq(context.Background(), seq)
	if err != nil || !ok {
		return channel.Message{}, ok, toChannelError(err)
	}
	return channelMessageFromRow(row), true, nil
}

// GetMessageByMessageID loads one message through the message_id index.
func (s *ChannelStore) GetMessageByMessageID(messageID uint64) (channel.Message, bool, error) {
	if err := s.beginUse(); err != nil {
		return channel.Message{}, false, err
	}
	defer s.endUse()
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

// GetLastSenderMessageSeq returns the latest indexed sender sequence through
// the caller's committed high-water boundary.
func (s *ChannelStore) GetLastSenderMessageSeq(ctx context.Context, fromUID string, throughSeq uint64) (uint64, bool, error) {
	if s == nil || s.log == nil {
		return 0, false, channel.ErrInvalidArgument
	}
	seq, ok, err := s.log.GetLastSenderMessageSeq(ctx, fromUID, throughSeq)
	return seq, ok, toChannelError(err)
}

// ListMessagesBySeq scans persisted messages by sequence while preserving caller cancellation.
func (s *ChannelStore) ListMessagesBySeq(ctx context.Context, fromSeq uint64, limit int, maxBytes int, reverse bool) ([]channel.Message, error) {
	if err := s.beginUse(); err != nil {
		return nil, err
	}
	defer s.endUse()
	var (
		rows []messageRow
		err  error
	)
	if reverse {
		rows, err = s.log.readRowsReverse(ctx, fromSeq, ReadOptions{Limit: limit, MaxBytes: maxBytes})
	} else {
		rows, err = s.log.readRows(ctx, fromSeq, 0, ReadOptions{Limit: limit, MaxBytes: maxBytes})
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
	if err := s.beginUse(); err != nil {
		return nil, 0, false, err
	}
	defer s.endUse()
	page, err := s.log.listByClientMsgNo(context.Background(), clientMsgNo, beforeSeq, limit)
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
	if err := s.beginUse(); err != nil {
		return channel.IdempotencyEntry{}, 0, false, err
	}
	defer s.endUse()
	if err := validateCompatIdempotencyKey(s.id, key); err != nil {
		return channel.IdempotencyEntry{}, 0, false, err
	}
	hit, ok, err := s.log.lookupIdempotency(context.Background(), IdempotencyKey{FromUID: key.FromUID, ClientMsgNo: key.ClientMsgNo})
	if err != nil || !ok {
		return channel.IdempotencyEntry{}, 0, ok, toChannelError(err)
	}
	return channel.IdempotencyEntry{MessageID: hit.MessageID, MessageSeq: hit.MessageSeq, Offset: hit.Offset}, hit.PayloadHash, true, nil
}

// LoadDurableProposal returns one exact proposal while holding the canonical
// Channel append lock so its manifest and rows form one stable view.
func (s *ChannelStore) LoadDurableProposal(ctx context.Context, commandID quorumlog.CommandID, maxRecords int, maxBytes int) (DurableProposal, bool, error) {
	if ctx == nil || commandID == (quorumlog.CommandID{}) || maxRecords <= 0 || maxBytes <= 0 {
		return DurableProposal{}, false, channel.ErrInvalidArgument
	}
	if err := s.beginUse(); err != nil {
		return DurableProposal{}, false, err
	}
	defer s.endUse()
	if err := ctx.Err(); err != nil {
		return DurableProposal{}, false, err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	proposal, present, err := s.loadDurableProposal(encodeProposalByCommandKey(s.log.key, commandID))
	if err != nil || !present {
		return DurableProposal{}, present, toChannelError(err)
	}
	count := proposal.manifest.LastOffset - proposal.manifest.BaseOffset
	if count > uint64(maxRecords) {
		return DurableProposal{}, false, channel.ErrBackpressured
	}
	rows := make([]messageRow, 0, count)
	used := 0
	for index := proposal.manifest.BaseOffset + 1; index <= proposal.manifest.LastOffset; index++ {
		row, ok, loadErr := s.log.getRowBySeq(ctx, index)
		if loadErr != nil {
			return DurableProposal{}, false, toChannelError(loadErr)
		}
		if !ok {
			return DurableProposal{}, false, channel.ErrCorruptState
		}
		identity, identityPresent, identityErr := loadDurableEntryIdentityFrom(s.log.db.engine, s.log.key, index)
		if identityErr != nil {
			return DurableProposal{}, false, toChannelError(identityErr)
		}
		if !identityPresent || identity.CommandID != commandID || identity.Index != index {
			return DurableProposal{}, false, channel.ErrCorruptState
		}
		rowBytes := 96 + len(row.FromUID) + len(row.ClientMsgNo) + len(row.Payload)
		if rowBytes > maxBytes-used {
			return DurableProposal{}, false, channel.ErrBackpressured
		}
		used += rowBytes
		rows = append(rows, row)
	}
	records, err := recordsFromRows(rows)
	if err != nil {
		return DurableProposal{}, false, err
	}
	for index := range records {
		records[index].Epoch = proposal.manifest.ChannelEpoch
	}
	return DurableProposal{Manifest: proposal.manifest, Records: records}, true, nil
}

// PutIdempotency stores a legacy idempotency entry without requiring a message row.
func (s *ChannelStore) PutIdempotency(key channel.IdempotencyKey, entry channel.IdempotencyEntry) error {
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	if err := validateCompatIdempotencyKey(s.id, key); err != nil {
		return err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	value, err := encodeIdempotencyIndexValue(messageRow{
		MessageSeq:  entry.MessageSeq,
		MessageID:   entry.MessageID,
		FromUID:     key.FromUID,
		ClientMsgNo: key.ClientMsgNo,
	})
	if err != nil {
		return toChannelError(err)
	}
	storageKey := encodeMessageIdempotencyIndexKey(s.log.key, key.FromUID, key.ClientMsgNo)
	if s.log.idempotencyMembershipLoaded {
		// Adding before commit can only create a false positive if commit fails.
		s.log.idempotencyMembership.add(storageKey)
	}
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(storageKey, value); err != nil {
		return toChannelError(err)
	}
	if err := s.log.stageCatalog(batch); err != nil {
		return toChannelError(err)
	}
	return toChannelError(batch.Commit(true))
}

// GetIdempotency loads a legacy idempotency entry without materializing the row.
func (s *ChannelStore) GetIdempotency(key channel.IdempotencyKey) (channel.IdempotencyEntry, bool, error) {
	if err := s.beginUse(); err != nil {
		return channel.IdempotencyEntry{}, false, err
	}
	defer s.endUse()
	if err := validateCompatIdempotencyKey(s.id, key); err != nil {
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

func validateCompatIdempotencyKey(id channel.ChannelID, key channel.IdempotencyKey) error {
	if key.ChannelID != id || key.FromUID == "" || key.ClientMsgNo == "" {
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
	if err := s.beginUse(); err != nil {
		return 0, err
	}
	defer s.endUse()
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	s.log.appendMu.Lock()
	checkpointLocked := req.Checkpoint != nil || req.CheckpointHW != nil
	if checkpointLocked {
		s.log.checkpointMu.Lock()
	}

	prepared, err := s.prepareApplyFetchedRecordsLocked(ctx, req, epochPoint, mode)
	if err != nil {
		if checkpointLocked {
			s.log.checkpointMu.Unlock()
		}
		s.log.appendMu.Unlock()
		return 0, err
	}
	if !prepared.hasWrites() {
		if checkpointLocked {
			s.log.checkpointMu.Unlock()
		}
		s.log.appendMu.Unlock()
		return prepared.nextLEO, nil
	}
	if err := s.commitPreparedRowsBatch(ctx, []preparedCommitRows{prepared}, commitLaneFollowerApply); err != nil {
		return 0, err
	}
	return prepared.nextLEO, nil
}

// StoreApplyFetchTrustedBatch applies caller-validated follower records for multiple channels in one commit request when possible.
func StoreApplyFetchTrustedBatch(ctx context.Context, items []ApplyFetchBatchItem) []ApplyFetchBatchResult {
	results := make([]ApplyFetchBatchResult, len(items))
	if len(items) == 0 {
		return results
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctxErr(ctx); err != nil {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	activeStores := make(map[*ChannelStore]struct{}, len(items))
	defer func() {
		for store := range activeStores {
			store.log.endUse()
		}
	}()
	indexesByEntry := make(map[*channelEntry][]int, len(items))
	for i, item := range items {
		if item.Store == nil || item.Store.log == nil || item.Store.log.channelEntry == nil {
			results[i].Err = channel.ErrInvalidArgument
			continue
		}
		indexesByEntry[item.Store.log.channelEntry] = append(indexesByEntry[item.Store.log.channelEntry], i)
	}
	for _, indexes := range indexesByEntry {
		if len(indexes) <= 1 {
			continue
		}
		for _, index := range indexes {
			results[index].Err = channel.ErrInvalidArgument
		}
	}
	for i, item := range items {
		if results[i].Err != nil {
			continue
		}
		if err := item.Store.validate(); err != nil {
			results[i].Err = err
			continue
		}
		if _, ok := activeStores[item.Store]; !ok {
			if err := item.Store.log.beginUse(); err != nil {
				results[i].Err = toChannelError(err)
				continue
			}
			activeStores[item.Store] = struct{}{}
		}
	}
	groups := make([]batchOwnerGroup, 0)
	groupByOwner := make(map[*Engine]int)
	for index, item := range items {
		if results[index].Err != nil {
			continue
		}
		owner := item.Store.engine
		groupIndex, ok := groupByOwner[owner]
		if !ok {
			groupIndex = len(groups)
			groupByOwner[owner] = groupIndex
			groups = append(groups, batchOwnerGroup{owner: owner})
		}
		groups[groupIndex].indexes = append(groups[groupIndex].indexes, index)
	}
	if len(groups) == 0 {
		return results
	}
	for _, group := range groups {
		if err := ctxErr(ctx); err != nil {
			for _, index := range group.indexes {
				results[index].Err = err
			}
			continue
		}
		storeApplyFetchBatchOwner(ctx, group.owner, items, group.indexes, results)
	}
	return results
}

// StoreCheckpointHWMonotonicBatch advances checkpoint high watermarks without taking foreground append locks.
func StoreCheckpointHWMonotonicBatch(ctx context.Context, items []CheckpointHWBatchItem) []CheckpointHWBatchResult {
	results := make([]CheckpointHWBatchResult, len(items))
	if len(items) == 0 {
		return results
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctxErr(ctx); err != nil {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	activeStores := make(map[*ChannelStore]struct{}, len(items))
	defer func() {
		for store := range activeStores {
			store.log.endUse()
		}
	}()
	indexesByEntry := make(map[*channelEntry][]int, len(items))
	for i, item := range items {
		if item.Store == nil || item.Store.log == nil || item.Store.log.channelEntry == nil {
			results[i].Err = channel.ErrInvalidArgument
			continue
		}
		indexesByEntry[item.Store.log.channelEntry] = append(indexesByEntry[item.Store.log.channelEntry], i)
	}
	for _, indexes := range indexesByEntry {
		if len(indexes) <= 1 {
			continue
		}
		for _, index := range indexes {
			results[index].Err = channel.ErrInvalidArgument
		}
	}
	for i, item := range items {
		if results[i].Err != nil {
			continue
		}
		if err := item.Store.validate(); err != nil {
			results[i].Err = err
			continue
		}
		if _, ok := activeStores[item.Store]; !ok {
			if err := item.Store.log.beginUse(); err != nil {
				results[i].Err = toChannelError(err)
				continue
			}
			activeStores[item.Store] = struct{}{}
		}
	}
	groups := make([]batchOwnerGroup, 0)
	groupByOwner := make(map[*Engine]int)
	for index, item := range items {
		if results[index].Err != nil {
			continue
		}
		owner := item.Store.engine
		groupIndex, ok := groupByOwner[owner]
		if !ok {
			groupIndex = len(groups)
			groupByOwner[owner] = groupIndex
			groups = append(groups, batchOwnerGroup{owner: owner})
		}
		groups[groupIndex].indexes = append(groups[groupIndex].indexes, index)
	}
	for _, group := range groups {
		if err := ctxErr(ctx); err != nil {
			for _, index := range group.indexes {
				results[index].Err = err
			}
			continue
		}
		storeCheckpointHWBatchOwner(ctx, group.owner, items, group.indexes, results)
	}
	return results
}

type preparedCheckpointHW struct {
	index      int
	store      *ChannelStore
	checkpoint Checkpoint
}

func storeCheckpointHWBatchOwner(ctx context.Context, owner *Engine, items []CheckpointHWBatchItem, indexes []int, results []CheckpointHWBatchResult) {
	entries := make([]*channelEntry, 0, len(indexes))
	indexByEntry := make(map[*channelEntry]int, len(indexes))
	for _, index := range indexes {
		entry := items[index].Store.log.channelEntry
		entries = append(entries, entry)
		indexByEntry[entry] = index
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	if err := lockCommitEntriesWithoutHoldAndWait(ctx, nil, entries); err != nil {
		for _, index := range indexes {
			results[index].Err = err
		}
		return
	}
	locked := make(map[*channelEntry]struct{}, len(entries))
	for _, entry := range entries {
		locked[entry] = struct{}{}
	}
	defer func() {
		for entry := range locked {
			entry.checkpointMu.Unlock()
		}
	}()

	prepared := make([]preparedCheckpointHW, 0, len(entries))
	for _, entry := range entries {
		index := indexByEntry[entry]
		if err := ctxErr(ctx); err != nil {
			results[index].Err = err
			entry.checkpointMu.Unlock()
			delete(locked, entry)
			continue
		}
		current, ok, err := items[index].Store.log.loadCheckpoint(ctx)
		if err != nil {
			results[index].Err = toChannelError(err)
			entry.checkpointMu.Unlock()
			delete(locked, entry)
			continue
		}
		if !ok {
			current = Checkpoint{}
		}
		if ok && items[index].HW <= current.HW {
			entry.checkpointMu.Unlock()
			delete(locked, entry)
			continue
		}
		current.HW = items[index].HW
		prepared = append(prepared, preparedCheckpointHW{index: index, store: items[index].Store, checkpoint: current})
	}
	if len(prepared) == 0 {
		return
	}
	checkpointEntries := make([]*channelEntry, 0, len(prepared))
	for _, item := range prepared {
		entry := item.store.log.channelEntry
		checkpointEntries = append(checkpointEntries, entry)
		delete(locked, entry)
	}
	if err := commitPreparedCheckpointHWBatch(ctx, owner, prepared, checkpointEntries); err != nil {
		err = toChannelError(err)
		for _, item := range prepared {
			results[item.index].Err = err
		}
	}
}

func commitPreparedCheckpointHWBatch(ctx context.Context, owner *Engine, prepared []preparedCheckpointHW, checkpointEntries []*channelEntry) error {
	if len(prepared) == 0 {
		return nil
	}
	if err := ctxErr(ctx); err != nil {
		unlockCheckpointEntries(checkpointEntries)
		return err
	}
	if owner == nil {
		unlockCheckpointEntries(checkpointEntries)
		return channel.ErrInvalidArgument
	}
	owner.mu.Lock()
	physical := owner.engine
	committer := owner.committer
	owner.mu.Unlock()
	if physical == nil {
		unlockCheckpointEntries(checkpointEntries)
		return channel.ErrClosed
	}
	ownership, err := newCheckpointCommitOwnership(checkpointEntries[0].db.registry, checkpointEntries)
	if err != nil {
		return toChannelError(err)
	}
	request := commit.Request{
		Lane:      commit.Lane{Name: commitRowsLaneName(commitLaneFollowerApply), Priority: commit.PriorityNormal},
		Partition: string(prepared[0].store.key) + ":checkpoint_hw",
		Build: func(batch *engine.Batch) error {
			for _, item := range prepared {
				checkpoint := item.checkpoint
				if err := item.store.log.channelEntry.stageCommitRows(batch, nil, &checkpoint, nil, nil, nil); err != nil {
					return err
				}
			}
			return nil
		},
		Finalize: ownership.finalize,
	}
	if committer != nil {
		return toChannelError(committer.Submit(ctx, request))
	}
	defer ownership.finalize()
	batch := physical.NewBatch()
	defer batch.Close()
	if err := request.Build(batch); err != nil {
		return err
	}
	return toChannelError(batch.Commit(true))
}

func unlockCheckpointEntries(entries []*channelEntry) {
	for i := len(entries) - 1; i >= 0; i-- {
		entries[i].checkpointMu.Unlock()
	}
}

func storeApplyFetchBatchOwner(ctx context.Context, owner *Engine, items []ApplyFetchBatchItem, indexes []int, results []ApplyFetchBatchResult) {
	if err := ctxErr(ctx); err != nil {
		for _, index := range indexes {
			results[index].Err = err
		}
		return
	}
	entries := make([]*channelEntry, 0, len(indexes))
	checkpointEntries := make([]*channelEntry, 0, len(indexes))
	checkpointSet := make(map[*channelEntry]struct{}, len(indexes))
	for _, index := range indexes {
		entry := items[index].Store.log.channelEntry
		entries = append(entries, entry)
		if items[index].Request.Checkpoint != nil || items[index].Request.CheckpointHW != nil {
			checkpointEntries = append(checkpointEntries, entry)
			checkpointSet[entry] = struct{}{}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	sort.Slice(checkpointEntries, func(i, j int) bool { return checkpointEntries[i].key < checkpointEntries[j].key })
	if err := lockCommitEntriesWithoutHoldAndWait(ctx, entries, checkpointEntries); err != nil {
		for _, index := range indexes {
			results[index].Err = err
		}
		return
	}
	locked := make(map[*channelEntry]struct{}, len(entries))
	for _, entry := range entries {
		locked[entry] = struct{}{}
	}
	defer func() {
		for entry := range locked {
			if _, ok := checkpointSet[entry]; ok {
				entry.checkpointMu.Unlock()
			}
			entry.appendMu.Unlock()
		}
	}()

	preparedRows := make([]preparedCommitRows, 0, len(indexes))
	for _, index := range indexes {
		item := items[index]
		entry := item.Store.log.channelEntry
		if err := ctxErr(ctx); err != nil {
			results[index].Err = err
			if _, ok := checkpointSet[entry]; ok {
				entry.checkpointMu.Unlock()
			}
			entry.appendMu.Unlock()
			delete(locked, entry)
			continue
		}
		prepared, err := item.Store.prepareApplyFetchedRecordsLocked(ctx, item.Request, nil, AppendTrustedContiguous)
		if err != nil {
			results[index].Err = err
			if _, ok := checkpointSet[entry]; ok {
				entry.checkpointMu.Unlock()
			}
			entry.appendMu.Unlock()
			delete(locked, entry)
			continue
		}
		prepared.index = index
		results[index].LEO = prepared.nextLEO
		if !prepared.hasWrites() {
			if _, ok := checkpointSet[entry]; ok {
				entry.checkpointMu.Unlock()
			}
			entry.appendMu.Unlock()
			delete(locked, entry)
			continue
		}
		preparedRows = append(preparedRows, prepared)
	}
	if len(preparedRows) > 0 {
		for _, item := range preparedRows {
			delete(locked, item.store.log.channelEntry)
		}
		if err := commitPreparedRowsBatch(ctx, owner, preparedRows, commitLaneFollowerApply); err != nil {
			err = toChannelError(err)
			for _, item := range preparedRows {
				results[item.index].Err = err
			}
		}
	}
}

func lockCommitEntriesWithoutHoldAndWait(ctx context.Context, appendEntries, checkpointEntries []*channelEntry) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var retryTimer *time.Timer
	retryDelay := batchLockRetryMinInterval
	jitterState := uint64(time.Now().UnixNano()) ^ uint64(len(appendEntries))<<32 ^ uint64(len(checkpointEntries))
	defer func() {
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}()
	for {
		lockedAppend := 0
		for lockedAppend < len(appendEntries) && appendEntries[lockedAppend].appendMu.TryLock() {
			lockedAppend++
		}
		lockedCheckpoint := 0
		if lockedAppend == len(appendEntries) {
			for lockedCheckpoint < len(checkpointEntries) && checkpointEntries[lockedCheckpoint].checkpointMu.TryLock() {
				lockedCheckpoint++
			}
		}
		if lockedAppend == len(appendEntries) && lockedCheckpoint == len(checkpointEntries) {
			return nil
		}
		for i := lockedCheckpoint - 1; i >= 0; i-- {
			checkpointEntries[i].checkpointMu.Unlock()
		}
		for i := lockedAppend - 1; i >= 0; i-- {
			appendEntries[i].appendMu.Unlock()
		}
		jitterState ^= jitterState << 13
		jitterState ^= jitterState >> 7
		jitterState ^= jitterState << 17
		retryWait := retryDelay
		if jitterWindow := retryDelay / 2; jitterWindow > 0 {
			retryWait += time.Duration(jitterState % uint64(jitterWindow))
		}
		if retryTimer == nil {
			retryTimer = time.NewTimer(retryWait)
		} else {
			retryTimer.Reset(retryWait)
		}
		select {
		case <-ctx.Done():
			if !retryTimer.Stop() {
				select {
				case <-retryTimer.C:
				default:
				}
			}
			return ctx.Err()
		case <-retryTimer.C:
		}
		retryDelay = min(retryDelay*2, batchLockRetryMaxInterval)
	}
}

type preparedCommitRows struct {
	index int
	store *ChannelStore
	rows  []messageRow
	// checkpointLocked transfers the caller's checkpoint mutex even when the checkpoint mutation is a no-op.
	checkpointLocked bool
	checkpoint       *Checkpoint
	point            *EpochPoint
	proposals        []durableProposalRecord
	entries          []quorumlog.EntryIdentity
	baseOffset       uint64
	nextLEO          uint64
	alreadyDurable   bool
	dependsOnCommit  bool
}

type exactAppendGapError struct {
	needFrom uint64
}

func (e *exactAppendGapError) Error() string { return "message: exact append gap" }

func (e *exactAppendGapError) Unwrap() error { return channel.ErrCorruptState }

type preparedCommitMutation struct {
	// entry is the canonical state pinned for asynchronous commit work.
	entry      *channelEntry
	rows       []messageRow
	checkpoint *Checkpoint
	point      *EpochPoint
	proposals  []durableProposalRecord
	entries    []quorumlog.EntryIdentity
	nextLEO    uint64
}

func (p preparedCommitRows) hasWrites() bool {
	return len(p.rows) > 0 || p.checkpoint != nil || p.point != nil || len(p.proposals) > 0 || len(p.entries) > 0
}

func (s *ChannelStore) prepareAppendRecordsLocked(ctx context.Context, records []channel.Record, mode AppendMode) (preparedCommitRows, error) {
	base, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return preparedCommitRows{}, toChannelError(err)
	}
	prepared := preparedCommitRows{store: s, baseOffset: base, nextLEO: base + uint64(len(records))}
	if len(records) == 0 {
		return prepared, nil
	}
	rows, err := compatibilityRowsFromRecords(base+1, records)
	if err != nil {
		return preparedCommitRows{}, err
	}
	defaultMissingServerTimestampMS(rows, time.Now().UnixMilli())
	if err := s.validateRowsForAppend(ctx, rows, mode); err != nil {
		return preparedCommitRows{}, err
	}
	prepared.rows = rows
	return prepared, nil
}

func (s *ChannelStore) prepareExactAppendRecordsLocked(ctx context.Context, expectedBaseOffset uint64, records []channel.Record, manifest DurableProposalManifest, committed uint64, mode AppendMode, seen *appendValidationSeen) (preparedCommitRows, error) {
	if err := validateDurableProposalManifest(manifest, expectedBaseOffset, len(records)); err != nil {
		return preparedCommitRows{}, err
	}
	rows, err := compatibilityRowsFromRecords(expectedBaseOffset+1, records)
	if err != nil {
		return preparedCommitRows{}, err
	}
	entries, ok := deriveDurableProposalEntries(manifest, records, rows)
	if !ok {
		return preparedCommitRows{}, channel.ErrInvalidArgument
	}
	if entries[len(entries)-1].Digest != manifest.Digest {
		return preparedCommitRows{}, channel.ErrCorruptState
	}
	base, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return preparedCommitRows{}, toChannelError(err)
	}
	if expectedBaseOffset > base {
		return preparedCommitRows{}, &exactAppendGapError{needFrom: base + 1}
	}
	proposal := durableProposalRecord{manifest: manifest}
	sequencedFresh := mode == AppendServerAllocatedMessageID && expectedBaseOffset == base
	if err := s.validateDurableProposalPredecessor(manifest, sequencedFresh); err != nil {
		return preparedCommitRows{}, toChannelError(err)
	}
	proposalDisposition := durableProposalFresh
	if sequencedFresh {
		// A current-frontier extension cannot have a durable last-offset or
		// entry identity above LEO. Allocator-issued globally unique message IDs
		// also make the content-derived command identity fresh. The predecessor
		// remains durably verified above, while replay and recovery retain the
		// complete paired-index validation below.
		s.log.db.sequencedExactFreshAppends.Add(1)
	} else {
		proposalDisposition, err = inspectDurableProposal(s.log.db.engine, s.log.key, proposal, entries)
		if err != nil {
			return preparedCommitRows{}, toChannelError(err)
		}
	}
	if len(records) == 0 {
		return preparedCommitRows{}, channel.ErrInvalidArgument
	}
	if uint64(len(records)) > math.MaxUint64-expectedBaseOffset {
		return preparedCommitRows{}, channel.ErrInvalidArgument
	}
	nextLEO := expectedBaseOffset + uint64(len(records))
	prepared := preparedCommitRows{store: s, baseOffset: expectedBaseOffset, nextLEO: nextLEO}
	if err := s.prepareExactCheckpointLocked(ctx, committed, nextLEO, max(base, nextLEO), &prepared); err != nil {
		return preparedCommitRows{}, err
	}
	if proposalDisposition == durableProposalAlreadyPresent {
		if base < nextLEO {
			return preparedCommitRows{}, channel.ErrCorruptState
		}
		prepared.alreadyDurable = true
		return prepared, nil
	}
	if base < expectedBaseOffset || (base > expectedBaseOffset && base < nextLEO) {
		return preparedCommitRows{}, channel.ErrCorruptState
	}
	if base >= nextLEO {
		return preparedCommitRows{}, channel.ErrCorruptState
	}

	if err := s.validateRowsForAppendSeen(ctx, rows, mode, seen); err != nil {
		return preparedCommitRows{}, err
	}
	prepared.rows = rows
	prepared.proposals = append(prepared.proposals, proposal)
	prepared.entries = entries
	return prepared, nil
}

func (s *ChannelStore) prepareExactCheckpointLocked(ctx context.Context, committed, proposalLEO, visibleLEO uint64, prepared *preparedCommitRows) error {
	if prepared == nil || committed > proposalLEO {
		return channel.ErrInvalidArgument
	}
	if committed == 0 {
		return nil
	}
	prepared.checkpointLocked = true
	checkpoint, present, err := s.log.loadCheckpoint(ctx)
	if err != nil {
		return toChannelError(err)
	}
	if !present {
		checkpoint = Checkpoint{}
	}
	if checkpoint.HW > visibleLEO {
		return channel.ErrCorruptState
	}
	if committed > checkpoint.HW {
		checkpoint.HW = committed
		prepared.checkpoint = &checkpoint
	}
	return nil
}

func (s *ChannelStore) prepareStagedExactReplayLocked(
	ctx context.Context,
	item AppendBatchItem,
	visibleLEO uint64,
	stagedCommands map[quorumlog.CommandID]durableProposalRecord,
	stagedLast map[uint64]durableProposalRecord,
	stagedEntries map[uint64]quorumlog.EntryIdentity,
) (preparedCommitRows, error) {
	manifest := item.Proposal
	if err := validateDurableProposalManifest(manifest, item.ExpectedBaseOffset, len(item.Records)); err != nil {
		return preparedCommitRows{}, err
	}
	rows, err := compatibilityRowsFromRecords(item.ExpectedBaseOffset+1, item.Records)
	if err != nil {
		return preparedCommitRows{}, err
	}
	entries, ok := deriveDurableProposalEntries(manifest, item.Records, rows)
	if !ok || entries[len(entries)-1].Digest != manifest.Digest {
		return preparedCommitRows{}, channel.ErrCorruptState
	}
	proposal := durableProposalRecord{manifest: manifest}
	byCommand, commandPresent := stagedCommands[manifest.CommandID]
	byLast, lastPresent := stagedLast[manifest.LastOffset]
	if !commandPresent || !lastPresent || !sameDurableProposal(byCommand, proposal) || !sameDurableProposal(byLast, proposal) {
		return preparedCommitRows{}, channel.ErrCorruptState
	}
	for _, entry := range entries {
		if persisted, present := stagedEntries[entry.Index]; !present || persisted != entry {
			return preparedCommitRows{}, channel.ErrCorruptState
		}
	}
	prepared := preparedCommitRows{
		store: item.Store, baseOffset: item.ExpectedBaseOffset, nextLEO: manifest.LastOffset,
		alreadyDurable: true, dependsOnCommit: true,
	}
	if err := s.prepareExactCheckpointLocked(ctx, item.Committed, manifest.LastOffset, visibleLEO, &prepared); err != nil {
		return preparedCommitRows{}, err
	}
	return prepared, nil
}

func (s *ChannelStore) prepareAdjacentExactAppendLocked(
	ctx context.Context,
	item AppendBatchItem,
	previous quorumlog.EntryIdentity,
	mode AppendMode,
	seen *appendValidationSeen,
	stagedCommands map[quorumlog.CommandID]durableProposalRecord,
	stagedLast map[uint64]durableProposalRecord,
	stagedEntries map[uint64]quorumlog.EntryIdentity,
) (preparedCommitRows, error) {
	manifest := item.Proposal
	if err := validateDurableProposalManifest(manifest, item.ExpectedBaseOffset, len(item.Records)); err != nil {
		return preparedCommitRows{}, err
	}
	if previous.Index != item.ExpectedBaseOffset || manifest.PreviousIndex != previous.Index ||
		manifest.PreviousTerm != previous.LeaderTerm || manifest.PreviousDigest != previous.Digest {
		return preparedCommitRows{}, channel.ErrCorruptState
	}
	rows, err := compatibilityRowsFromRecords(item.ExpectedBaseOffset+1, item.Records)
	if err != nil {
		return preparedCommitRows{}, err
	}
	entries, ok := deriveDurableProposalEntries(manifest, item.Records, rows)
	if !ok || entries[len(entries)-1].Digest != manifest.Digest {
		return preparedCommitRows{}, channel.ErrCorruptState
	}
	if _, exists := stagedCommands[manifest.CommandID]; exists {
		return preparedCommitRows{}, channel.ErrCorruptState
	}
	if _, exists := stagedLast[manifest.LastOffset]; exists {
		return preparedCommitRows{}, channel.ErrCorruptState
	}
	if _, present, err := s.loadDurableProposal(encodeProposalByCommandKey(s.log.key, manifest.CommandID)); err != nil || present {
		if err != nil {
			return preparedCommitRows{}, toChannelError(err)
		}
		return preparedCommitRows{}, channel.ErrCorruptState
	}
	if _, present, err := s.loadDurableProposal(encodeProposalByLastKey(s.log.key, manifest.LastOffset)); err != nil || present {
		if err != nil {
			return preparedCommitRows{}, toChannelError(err)
		}
		return preparedCommitRows{}, channel.ErrCorruptState
	}
	for _, entry := range entries {
		if _, exists := stagedEntries[entry.Index]; exists {
			return preparedCommitRows{}, channel.ErrCorruptState
		}
		if _, present, err := loadDurableEntryIdentityFrom(s.log.db.engine, s.log.key, entry.Index); err != nil || present {
			if err != nil {
				return preparedCommitRows{}, toChannelError(err)
			}
			return preparedCommitRows{}, channel.ErrCorruptState
		}
	}
	if err := s.validateRowsForAppendSeen(ctx, rows, mode, seen); err != nil {
		return preparedCommitRows{}, err
	}
	prepared := preparedCommitRows{
		store: s, baseOffset: item.ExpectedBaseOffset, nextLEO: manifest.LastOffset,
		rows: rows, proposals: []durableProposalRecord{{manifest: manifest}}, entries: entries,
	}
	if err := s.prepareExactCheckpointLocked(ctx, item.Committed, manifest.LastOffset, manifest.LastOffset, &prepared); err != nil {
		return preparedCommitRows{}, err
	}
	proposal := durableProposalRecord{manifest: manifest}
	stagedCommands[manifest.CommandID] = proposal
	stagedLast[manifest.LastOffset] = proposal
	for _, entry := range entries {
		stagedEntries[entry.Index] = entry
	}
	return prepared, nil
}

func (s *ChannelStore) prepareApplyFetchedRecordsLocked(ctx context.Context, req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint, mode AppendMode) (preparedCommitRows, error) {
	base, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return preparedCommitRows{}, toChannelError(err)
	}
	nextLEO := base + uint64(len(req.Records))
	prepared := preparedCommitRows{
		store:            s,
		baseOffset:       base,
		nextLEO:          nextLEO,
		checkpointLocked: req.Checkpoint != nil || req.CheckpointHW != nil,
	}
	if req.Checkpoint != nil && req.CheckpointHW != nil {
		return preparedCommitRows{}, channel.ErrInvalidArgument
	}
	if req.Checkpoint != nil {
		if err := validateChannelCheckpoint(*req.Checkpoint); err != nil {
			return preparedCommitRows{}, err
		}
		if req.Checkpoint.HW < req.PreviousCommittedHW || req.Checkpoint.HW > nextLEO {
			return preparedCommitRows{}, channel.ErrCorruptState
		}
		if err := s.log.validateCheckpointMonotonicLocked(ctx, checkpointFromChannel(*req.Checkpoint), nextLEO, nextLEO); err != nil {
			return preparedCommitRows{}, toChannelError(err)
		}
		converted := checkpointFromChannel(*req.Checkpoint)
		prepared.checkpoint = &converted
	} else if req.CheckpointHW != nil {
		if *req.CheckpointHW > nextLEO {
			return preparedCommitRows{}, channel.ErrCorruptState
		}
		current, ok, err := s.log.loadCheckpoint(ctx)
		if err != nil {
			return preparedCommitRows{}, toChannelError(err)
		}
		if !ok {
			current = Checkpoint{}
		}
		if *req.CheckpointHW > current.HW {
			current.HW = *req.CheckpointHW
			if err := s.log.validateCheckpointMonotonicLocked(ctx, current, nextLEO, nextLEO); err != nil {
				return preparedCommitRows{}, toChannelError(err)
			}
			prepared.checkpoint = &current
		}
	}
	if epochPoint != nil {
		if epochPoint.StartOffset != base {
			return preparedCommitRows{}, channel.ErrCorruptState
		}
		shouldWrite, err := s.shouldAppendEpochPoint(ctx, *epochPoint)
		if err != nil {
			return preparedCommitRows{}, err
		}
		if shouldWrite {
			converted := epochPointFromChannel(*epochPoint)
			prepared.point = &converted
		}
	}
	if len(req.Records) == 0 && req.Checkpoint == nil && req.CheckpointHW == nil && prepared.point == nil {
		return prepared, nil
	}
	rows, err := compatibilityRowsFromRecords(base+1, req.Records)
	if err != nil {
		return preparedCommitRows{}, err
	}
	if err := s.validateRowsForAppend(ctx, rows, mode); err != nil {
		return preparedCommitRows{}, err
	}
	prepared.rows = rows
	return prepared, nil
}

// LoadCheckpoint loads the durable checkpoint.
func (s *ChannelStore) LoadCheckpoint() (channel.Checkpoint, error) {
	if err := s.beginUse(); err != nil {
		return channel.Checkpoint{}, err
	}
	defer s.endUse()
	checkpoint, ok, err := s.log.loadCheckpoint(context.Background())
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
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	s.log.checkpointMu.Lock()
	defer s.log.checkpointMu.Unlock()
	if err := s.log.storeCheckpointLocked(context.Background(), checkpointFromChannel(checkpoint)); err != nil {
		return toChannelError(err)
	}
	return nil
}

// StoreCheckpointMonotonic stores checkpoint after durable monotonic validation.
func (s *ChannelStore) StoreCheckpointMonotonic(ctx context.Context, checkpoint channel.Checkpoint, visibleHW uint64, leo uint64) error {
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	s.log.checkpointMu.Lock()
	defer s.log.checkpointMu.Unlock()
	converted := checkpointFromChannel(checkpoint)
	if err := s.log.validateCheckpointMonotonicLocked(ctx, converted, visibleHW, leo); err != nil {
		return toChannelError(err)
	}
	if err := s.log.storeCheckpointLocked(ctx, converted); err != nil {
		return toChannelError(err)
	}
	return nil
}

// StoreCheckpointHWMonotonic advances only the high watermark under the canonical checkpoint lock.
func (s *ChannelStore) StoreCheckpointHWMonotonic(ctx context.Context, hw uint64) error {
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	s.log.checkpointMu.Lock()
	defer s.log.checkpointMu.Unlock()
	current, ok, err := s.log.loadCheckpoint(ctx)
	if err != nil {
		return toChannelError(err)
	}
	if !ok {
		current = Checkpoint{}
	}
	if ok && hw <= current.HW {
		return nil
	}
	current.HW = hw
	return toChannelError(s.log.storeCheckpointLocked(ctx, current))
}

// LoadHistory loads epoch history points.
func (s *ChannelStore) LoadHistory() ([]channel.EpochPoint, error) {
	if err := s.beginUse(); err != nil {
		return nil, err
	}
	defer s.endUse()
	points, ok, err := s.log.loadHistory(context.Background())
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
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	if err := s.log.appendHistory(context.Background(), epochPointFromChannel(point)); err != nil {
		return toChannelError(err)
	}
	return nil
}

// TruncateHistoryTo removes history points after leo.
func (s *ChannelStore) TruncateHistoryTo(leo uint64) error {
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	if err := s.log.truncateHistoryTo(context.Background(), leo); err != nil {
		return toChannelError(err)
	}
	return nil
}

// BeginEpoch durably appends an epoch boundary at expectedLEO.
func (s *ChannelStore) BeginEpoch(ctx context.Context, point channel.EpochPoint, expectedLEO uint64) error {
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
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
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	return s.truncateLocked(ctx, to, true)
}

// DiscardForRestore removes every row, secondary index, and durable system
// record for this Channel. It is intentionally limited to explicit restore
// failure cleanup before the cluster is activated.
func (s *ChannelStore) DiscardForRestore(ctx context.Context) error {
	const (
		restoreDiscardBatchMessages = 1024
		restoreDiscardBatchBytes    = 8 << 20
	)
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	nextSeq := uint64(1)
	for {
		rows, err := s.log.readRows(ctx, nextSeq, 0, ReadOptions{
			Limit: restoreDiscardBatchMessages, MaxBytes: restoreDiscardBatchBytes,
		})
		if err != nil {
			return toChannelError(err)
		}
		if len(rows) == 0 {
			break
		}
		batch := s.log.db.engine.NewBatch()
		for _, row := range rows {
			if err := s.log.stageDeleteMessage(
				batch, messageFromRow(row),
			); err != nil {
				_ = batch.Close()
				return toChannelError(err)
			}
		}
		commitErr := batch.Commit(true)
		closeErr := batch.Close()
		if commitErr != nil || closeErr != nil {
			return toChannelError(errors.Join(commitErr, closeErr))
		}
		lastSeq := rows[len(rows)-1].MessageSeq
		if lastSeq < nextSeq {
			return toChannelError(dberrors.ErrCorruptState)
		}
		if lastSeq == ^uint64(0) {
			break
		}
		nextSeq = lastSeq + 1
	}
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	prefix := encodeMessageChannelPartitionPrefix(s.log.key)
	span := keycodec.NewPrefixSpan(prefix)
	if err := batch.DeleteRange(engine.Span{
		Start: span.Start, End: span.End,
	}); err != nil {
		return toChannelError(err)
	}
	if err := batch.Delete(encodeCatalogKey(s.log.key)); err != nil {
		return toChannelError(err)
	}
	if err := batch.Commit(true); err != nil {
		return toChannelError(err)
	}
	s.log.leo.Store(0)
	s.log.loaded.Store(false)
	s.log.clearDurableProposalTailLocked()
	return nil
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
	if err := s.log.channelEntry.stageTruncateDurableProposals(ctx, batch, to); err != nil {
		return toChannelError(err)
	}
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
		s.log.clearDurableProposalTailLocked()
	}
	return nil
}

// StoreSnapshotPayload stores snapshot payload bytes.
func (s *ChannelStore) StoreSnapshotPayload(payload []byte) error {
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	if err := s.log.storeSnapshotPayload(context.Background(), payload); err != nil {
		return toChannelError(err)
	}
	return nil
}

// LoadSnapshotPayload loads snapshot payload bytes, returning nil when missing.
func (s *ChannelStore) LoadSnapshotPayload() ([]byte, error) {
	if err := s.beginUse(); err != nil {
		return nil, err
	}
	defer s.endUse()
	payload, _, err := s.log.loadSnapshotPayload(context.Background())
	if err != nil {
		return nil, toChannelError(err)
	}
	return payload, nil
}

// InstallSnapshotAtomically stores snapshot payload, checkpoint, and history together.
func (s *ChannelStore) InstallSnapshotAtomically(ctx context.Context, snap channel.Snapshot, checkpoint channel.Checkpoint, epochPoint channel.EpochPoint) (uint64, error) {
	if err := s.beginUse(); err != nil {
		return 0, err
	}
	defer s.endUse()
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	s.log.checkpointMu.Lock()
	defer s.log.checkpointMu.Unlock()
	leo, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return 0, toChannelError(err)
	}
	_, err = s.log.installSnapshotLocked(ctx, Snapshot{Epoch: snap.Epoch, EndOffset: snap.EndOffset, Payload: snap.Payload}, checkpointFromChannel(checkpoint), epochPointFromChannel(epochPoint), leo)
	if err != nil {
		return 0, toChannelError(err)
	}
	return leo, nil
}

// LoadRetentionState loads durable local retention progress.
func (s *ChannelStore) LoadRetentionState() (channel.RetentionState, error) {
	if err := s.beginUse(); err != nil {
		return channel.RetentionState{}, err
	}
	defer s.endUse()
	state, ok, err := s.log.loadRetentionState(context.Background())
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
	if err := s.beginUse(); err != nil {
		return RetentionScanResult{}, err
	}
	defer s.endUse()
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
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	if throughSeq == 0 {
		return channel.ErrInvalidArgument
	}
	if err := validateCursorName(cursorName); err != nil {
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
		s.log.clearDurableProposalTailLocked()
	}
	return nil
}

// TrimMessagesThrough removes rows through an already-adopted retention boundary.
func (s *ChannelStore) TrimMessagesThrough(ctx context.Context, throughSeq uint64) error {
	_, err := s.TrimMessagesThroughLimit(ctx, throughSeq, RetentionTrimOptions{})
	return err
}

// TrimMessagesThroughLimit removes a bounded prefix through an already-adopted retention boundary.
func (s *ChannelStore) TrimMessagesThroughLimit(ctx context.Context, throughSeq uint64, opts RetentionTrimOptions) (RetentionTrimResult, error) {
	if throughSeq == 0 {
		return RetentionTrimResult{}, channel.ErrInvalidArgument
	}
	if err := s.beginUse(); err != nil {
		return RetentionTrimResult{}, err
	}
	defer s.endUse()
	if err := ctxErr(ctx); err != nil {
		return RetentionTrimResult{}, err
	}
	result, err := s.log.trimPrefixThroughLimit(ctx, throughSeq, opts, false)
	return result, toChannelError(err)
}

// LoadCommittedDispatchCursor loads the last dispatched sequence for a replay lane.
func (s *ChannelStore) LoadCommittedDispatchCursor(name string) (uint64, bool, error) {
	if err := s.beginUse(); err != nil {
		return 0, false, err
	}
	defer s.endUse()
	if err := validateCursorName(name); err != nil {
		return 0, false, err
	}
	return s.loadCommittedDispatchCursor(name)
}

// StoreCommittedDispatchCursor persists replay progress for a lane.
func (s *ChannelStore) StoreCommittedDispatchCursor(name string, seq uint64) error {
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	if err := validateCursorName(name); err != nil {
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
	if err := s.beginUse(); err != nil {
		return 0, err
	}
	defer s.endUse()
	if err := validateCursorName(name); err != nil {
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
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	if err := validateCursorName(name); err != nil {
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

func validateCursorName(name string) error {
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
	seen := newAppendValidationSeen(len(rows))
	return s.validateRowsForAppendSeen(ctx, rows, mode, &seen)
}

func (s *ChannelStore) validateRowsForAppendSeen(ctx context.Context, rows []messageRow, mode AppendMode, seen *appendValidationSeen) error {
	if seen == nil {
		return channel.ErrInvalidArgument
	}
	cache := s.log.appendKeyCache
	scratch := appendValidationScratch{}
	for _, row := range rows {
		if err := s.log.validateAppendRow(ctx, row, seen, mode, cache, &scratch); err != nil {
			return toChannelError(err)
		}
	}
	return nil
}

func (s *ChannelStore) commitPreparedRowsBatch(ctx context.Context, prepared []preparedCommitRows, lane string) error {
	return commitPreparedRowsBatchResult(ctx, s.engine, prepared, lane).Err
}

func commitPreparedRowsBatch(ctx context.Context, owner *Engine, prepared []preparedCommitRows, lane string) error {
	return commitPreparedRowsBatchResult(ctx, owner, prepared, lane).Err
}

func commitPreparedRowsBatchResult(ctx context.Context, owner *Engine, prepared []preparedCommitRows, lane string) commit.SubmitResult {
	if len(prepared) == 0 {
		return commit.SubmitResult{Outcome: commit.OutcomeDefinitelyNotCommitted}
	}
	appendEntries, checkpointEntries, duplicate := preparedCommitEntries(prepared)
	if len(appendEntries) == 0 {
		return commit.SubmitResult{Outcome: commit.OutcomeDefinitelyNotCommitted, Err: channel.ErrInvalidArgument}
	}
	if duplicate {
		unlockCommitEntries(appendEntries, checkpointEntries)
		return commit.SubmitResult{Outcome: commit.OutcomeDefinitelyNotCommitted, Err: channel.ErrInvalidArgument}
	}
	if err := ctxErr(ctx); err != nil {
		unlockCommitEntries(appendEntries, checkpointEntries)
		return commit.SubmitResult{Outcome: commit.OutcomeDefinitelyNotCommitted, Err: err}
	}
	if owner == nil {
		unlockCommitEntries(appendEntries, checkpointEntries)
		return commit.SubmitResult{Outcome: commit.OutcomeDefinitelyNotCommitted, Err: channel.ErrInvalidArgument}
	}
	owner.mu.Lock()
	physical := owner.engine
	committer := owner.committer
	owner.mu.Unlock()
	if physical == nil {
		unlockCommitEntries(appendEntries, checkpointEntries)
		return commit.SubmitResult{Outcome: commit.OutcomeDefinitelyNotCommitted, Err: channel.ErrClosed}
	}
	ownership, err := newCommitOwnership(appendEntries[0].db.registry, appendEntries, checkpointEntries)
	if err != nil {
		return commit.SubmitResult{Outcome: commit.OutcomeDefinitelyNotCommitted, Err: toChannelError(err)}
	}
	mutations := make([]preparedCommitMutation, 0, len(prepared))
	for _, item := range prepared {
		mutations = append(mutations, preparedCommitMutation{
			entry:      item.store.log.channelEntry,
			rows:       item.rows,
			checkpoint: item.checkpoint,
			point:      item.point,
			proposals:  item.proposals,
			entries:    item.entries,
			nextLEO:    item.nextLEO,
		})
	}
	request := commit.Request{
		Lane:      commit.Lane{Name: commitRowsLaneName(lane), Priority: commitRowsPriority(lane)},
		Partition: preparedRowsPartition(prepared, lane),
		Records:   preparedRowsRecordCount(prepared),
		Bytes:     preparedRowsBytes(prepared),
		Build: func(batch *engine.Batch) error {
			for _, mutation := range mutations {
				if err := mutation.entry.stageCommitRows(batch, mutation.rows, mutation.checkpoint, mutation.point, mutation.proposals, mutation.entries); err != nil {
					return err
				}
			}
			return nil
		},
		Publish: func() error {
			for _, mutation := range mutations {
				mutation.entry.publishCommittedRows(mutation.rows, mutation.nextLEO, mutation.proposals, mutation.entries)
			}
			return nil
		},
		Finalize: ownership.finalize,
	}
	if committer != nil {
		result := committer.SubmitWithOutcome(ctx, request)
		result.Err = toChannelError(result.Err)
		return result
	}
	defer ownership.finalize()
	batch := physical.NewBatch()
	defer batch.Close()
	if err := request.Build(batch); err != nil {
		return commit.SubmitResult{Outcome: commit.OutcomeDefinitelyNotCommitted, Err: err}
	}
	if err := batch.Commit(true); err != nil {
		return commit.SubmitResult{Outcome: commit.OutcomeUnknown, Err: toChannelError(err)}
	}
	return commit.SubmitResult{Outcome: commit.OutcomeCommitted, Err: request.Publish()}
}

func preparedRowsRecordCount(prepared []preparedCommitRows) int {
	total := 0
	for _, item := range prepared {
		total += len(item.rows)
	}
	return total
}

func preparedRowsBytes(prepared []preparedCommitRows) int {
	total := 0
	for _, item := range prepared {
		total += messageRowsBytes(item.rows)
		total += len(item.proposals) * 2 * durableProposalRecordSize
		total += len(item.entries) * durableEntryIdentitySize
	}
	return total
}

func preparedRowsPartition(prepared []preparedCommitRows, lane string) string {
	for _, item := range prepared {
		if item.store != nil && item.store.key != "" {
			return string(item.store.key)
		}
	}
	return commitRowsLaneName(lane) + ":batch"
}

func commitRowsLaneName(lane string) string {
	if lane == "" {
		return commitLaneMessageAppend
	}
	return lane
}

func commitRowsPriority(lane string) commit.Priority {
	switch lane {
	case commitLaneFollowerApply, commitLaneReplicaForeground, commitLaneReplicaTrailing:
		return commit.PriorityNormal
	default:
		return commit.PriorityHigh
	}
}

func (e *channelEntry) stageCommitRows(batch *engine.Batch, rows []messageRow, checkpoint *Checkpoint, point *EpochPoint, proposals []durableProposalRecord, entries []quorumlog.EntryIdentity) error {
	if err := e.stageMessageRows(batch, rows); err != nil {
		return toChannelError(err)
	}
	if checkpoint != nil {
		if err := batch.Set(encodeCheckpointKey(e.key), encodeCheckpoint(*checkpoint)); err != nil {
			return toChannelError(err)
		}
	}
	if point != nil {
		if err := e.writeHistoryPoint(batch, *point); err != nil {
			return toChannelError(err)
		}
	}
	for _, proposal := range proposals {
		value := encodeDurableProposalRecord(proposal)
		if err := batch.Set(encodeProposalByLastKey(e.key, proposal.manifest.LastOffset), value); err != nil {
			return toChannelError(err)
		}
		if err := batch.Set(encodeProposalByCommandKey(e.key, proposal.manifest.CommandID), value); err != nil {
			return toChannelError(err)
		}
	}
	if len(entries) > 0 {
		if len(entries) != len(rows) {
			return channel.ErrCorruptState
		}
		for index, entry := range entries {
			if entry.Index != rows[index].MessageSeq {
				return channel.ErrCorruptState
			}
			if err := batch.Set(encodeEntryIdentityKey(e.key, entry.Index), encodeDurableEntryIdentity(entry)); err != nil {
				return toChannelError(err)
			}
		}
	}
	if len(rows) > 0 {
		if err := e.stageCatalogForAppend(batch, rows[0].MessageSeq); err != nil {
			return toChannelError(err)
		}
	} else if err := e.stageCatalog(batch); err != nil {
		return toChannelError(err)
	}
	return nil
}

func (e *channelEntry) publishCommittedRows(rows []messageRow, nextLEO uint64, proposals []durableProposalRecord, entries []quorumlog.EntryIdentity) {
	if len(rows) > 0 {
		e.leo.Store(nextLEO)
		e.loaded.Store(true)
		e.publishDurableProposalTailLocked(proposals, entries, nextLEO)
	}
}

func messageRowsBytes(rows []messageRow) int {
	total := 0
	for _, row := range rows {
		total += len(row.Payload)
	}
	return total
}

func (s *ChannelStore) shouldAppendEpochPoint(ctx context.Context, point channel.EpochPoint) (bool, error) {
	points, ok, err := s.log.loadHistory(ctx)
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
	state, ok, err := s.log.loadRetentionState(ctx)
	if err != nil {
		return RetentionState{}, toChannelError(err)
	}
	if !ok {
		return RetentionState{}, nil
	}
	return state, nil
}

func (s *ChannelStore) retentionStateAfterTruncate(ctx context.Context, to uint64) (RetentionState, bool, error) {
	state, ok, err := s.log.loadRetentionState(ctx)
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
	return readRowsRaw(ctx, l.db, l.key, fromSeq, maxSeq, opts)
}

func readRowsRaw(ctx context.Context, db *MessageDB, channelKey ChannelKey, fromSeq uint64, maxSeq uint64, opts ReadOptions) ([]messageRow, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if db == nil || db.engine == nil {
		return nil, dberrors.ErrClosed
	}
	if fromSeq == 0 {
		fromSeq = 1
	}
	prefix := encodeMessageRowPrefix(channelKey)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := db.engine.NewIter(engine.Span{Start: encodeMessageRowKey(channelKey, fromSeq, messageHeaderFamilyID), End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	rows := make([]messageRow, 0, boundedCapacity(16, opts.Limit))
	var totalBytes int
	var current messageRow
	var currentSeq uint64
	var haveRow, haveHeader bool
	flush := func() (bool, error) {
		if !haveRow {
			return false, nil
		}
		if !haveHeader {
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
		haveRow, haveHeader = false, false
		current = messageRow{}
		currentSeq = 0
		return false, nil
	}
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		key := iter.Key()
		seq, familyID, ok := decodeMessageRowKey(channelKey, key)
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
		l.appendMu.Lock()
		leo, err := l.loadLEOLocked(ctx)
		l.appendMu.Unlock()
		if err != nil {
			return nil, err
		}
		fromSeq = leo
	}
	return readMessageRowsReverseRaw(ctx, l.db, l.key, fromSeq, opts)
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
	row.Payload, pos, err = readCompatibilityBytes(payload, pos)
	if err != nil {
		return messageRow{}, err
	}
	row.Payload = append([]byte(nil), row.Payload...)
	if serverTimestampMS, ok := decodeCompatibilityServerTimestamp(payload, pos); ok {
		row.ServerTimestampMS = serverTimestampMS
	}
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
	if row.ServerTimestampMS != 0 {
		size += compatibilityServerTimestampSize
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
	payload = appendCompatibilityServerTimestamp(payload, row.ServerTimestampMS)
	return channel.Record{ID: row.MessageID, Index: row.MessageSeq, Payload: payload, SizeBytes: len(payload)}, nil
}

func defaultMissingServerTimestampMS(rows []messageRow, serverTimestampMS int64) {
	if serverTimestampMS == 0 {
		serverTimestampMS = time.Now().UnixMilli()
	}
	for i := range rows {
		if rows[i].ServerTimestampMS == 0 {
			rows[i].ServerTimestampMS = serverTimestampMS
		}
	}
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

var compatibilityServerTimestampMagic = [...]byte{'w', 'k', 't', 's'}

const compatibilityServerTimestampSize = 12

func appendCompatibilityServerTimestamp(dst []byte, serverTimestampMS int64) []byte {
	if serverTimestampMS == 0 {
		return dst
	}
	dst = append(dst, compatibilityServerTimestampMagic[:]...)
	return binary.BigEndian.AppendUint64(dst, uint64(serverTimestampMS))
}

func decodeCompatibilityServerTimestamp(payload []byte, pos int) (int64, bool) {
	if len(payload)-pos < compatibilityServerTimestampSize {
		return 0, false
	}
	if !bytes.Equal(payload[pos:pos+len(compatibilityServerTimestampMagic)], compatibilityServerTimestampMagic[:]) {
		return 0, false
	}
	return int64(binary.BigEndian.Uint64(payload[pos+len(compatibilityServerTimestampMagic) : pos+compatibilityServerTimestampSize])), true
}

func channelMessageFromRow(row messageRow) channel.Message {
	return channel.Message{
		MessageID:         row.MessageID,
		MessageSeq:        row.MessageSeq,
		Framer:            decodeMessageRowFramerFlags(row.FramerFlags),
		Setting:           frame.Setting(row.Setting),
		MsgKey:            row.MsgKey,
		Expire:            uint32(row.Expire),
		ClientSeq:         row.ClientSeq,
		ClientMsgNo:       row.ClientMsgNo,
		StreamNo:          row.StreamNo,
		StreamID:          row.StreamID,
		StreamFlag:        frame.StreamFlag(row.StreamFlag),
		Timestamp:         int32(row.Timestamp),
		ChannelID:         row.ChannelID,
		ChannelType:       row.ChannelType,
		Topic:             row.Topic,
		FromUID:           row.FromUID,
		ServerTimestampMS: row.ServerTimestampMS,
		Payload:           append([]byte(nil), row.Payload...),
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
	if errors.Is(err, dberrors.ErrClosed) {
		return fmt.Errorf("%w: %v", channel.ErrClosed, err)
	}
	if errors.Is(err, dberrors.ErrInvalidArgument) {
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
