package store

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"io"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// MessageDBFactory adapts the shared message DB engine to the channel runtime.
type MessageDBFactory struct {
	// engine owns the compatibility message database runtime.
	engine *messagedb.Engine
	// closed rejects new adapter and batch acquisitions during shutdown.
	closed atomic.Bool
}

// MessageDBFactoryOptions configures the message DB adapter.
type MessageDBFactoryOptions struct {
	// CommitFlushWindow is the maximum delay for grouping adjacent channel append commits.
	CommitFlushWindow time.Duration
	// CommitMaxRequests caps logical append requests in one grouped physical commit.
	CommitMaxRequests int
	// CommitMaxRecords caps message records in one grouped physical commit.
	CommitMaxRecords int
	// CommitMaxBytes caps approximate payload bytes in one grouped physical commit.
	CommitMaxBytes int
	// CommitShards routes grouped commit requests across independent message DB coordinators. Zero keeps one coordinator.
	CommitShards int
	// CommitObserver receives message DB group-commit measurements.
	CommitObserver messagedb.CommitCoordinatorObserver
}

// NewMessageDBFactory opens a message DB engine behind the v2 adapter.
func NewMessageDBFactory(path string) *MessageDBFactory {
	return NewMessageDBFactoryWithOptions(path, MessageDBFactoryOptions{})
}

// NewMessageDBFactoryWithOptions opens a message DB engine behind the v2 adapter.
func NewMessageDBFactoryWithOptions(path string, opts MessageDBFactoryOptions) *MessageDBFactory {
	engine, err := messagedb.Open(path)
	if err != nil {
		return &MessageDBFactory{}
	}
	engine.ConfigureCommitCoordinator(messagedb.CommitCoordinatorConfig{
		FlushWindow: opts.CommitFlushWindow,
		MaxRequests: opts.CommitMaxRequests,
		MaxRecords:  opts.CommitMaxRecords,
		MaxBytes:    opts.CommitMaxBytes,
		Shards:      opts.CommitShards,
		Observer:    opts.CommitObserver,
	})
	return &MessageDBFactory{engine: engine}
}

// CommitCoordinatorConfig returns the effective message DB commit coordinator settings.
func (f *MessageDBFactory) CommitCoordinatorConfig() messagedb.CommitCoordinatorConfig {
	if f == nil || f.engine == nil {
		return messagedb.CommitCoordinatorConfig{}
	}
	return f.engine.CommitCoordinatorConfig()
}

// ChannelStore returns an adapter for one message DB channel store.
func (f *MessageDBFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (ChannelStore, error) {
	if err := f.availabilityError(); err != nil {
		return nil, err
	}
	dbStore, err := f.engine.ForChannel(channel.ChannelKey(key), channel.ChannelID{ID: id.ID, Type: id.Type})
	if err != nil {
		return nil, f.mapError(err)
	}
	return &messageDBChannelStoreAdapter{store: dbStore, id: id, owner: f}, nil
}

// ListChannelsPage returns one ordered page from the local message channel catalog.
func (f *MessageDBFactory) ListChannelsPage(ctx context.Context, after ch.ChannelKey, limit int) ([]ChannelCatalogEntry, ch.ChannelKey, bool, error) {
	if err := f.availabilityError(); err != nil {
		return nil, "", false, err
	}
	entries, cursor, more, err := f.engine.ListChannelsPage(ctx, messagedb.ChannelKey(after), limit)
	if err != nil {
		return nil, "", false, f.mapError(err)
	}
	out := make([]ChannelCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, ChannelCatalogEntry{
			Key: ch.ChannelKey(entry.Key),
			ID:  ch.ChannelID{ID: entry.ID.ID, Type: entry.ID.Type},
		})
	}
	return out, ch.ChannelKey(cursor), more, nil
}

// ListLatestMessages returns one newest-first page from the node-local shared message database.
func (f *MessageDBFactory) ListLatestMessages(ctx context.Context, beforeMessageID uint64, limit int) ([]ch.Message, bool, uint64, error) {
	if err := f.availabilityError(); err != nil {
		return nil, false, 0, err
	}
	page, err := f.engine.ListLatestMessages(ctx, beforeMessageID, limit)
	if err != nil {
		return nil, false, 0, f.mapError(err)
	}
	out := make([]ch.Message, 0, len(page.Messages))
	for _, msg := range page.Messages {
		out = append(out, ch.Message{
			MessageID:         msg.MessageID,
			MessageSeq:        msg.MessageSeq,
			ChannelID:         msg.ChannelID,
			ChannelType:       msg.ChannelType,
			FromUID:           msg.FromUID,
			ClientMsgNo:       msg.ClientMsgNo,
			Payload:           cloneBytes(msg.Payload),
			ServerTimestampMS: msg.ServerTimestampMS,
		})
	}
	return out, page.HasMore, page.NextBeforeMessageID, nil
}

// DeleteLatestMessageIndexes removes retained rows from the manager-only global projection.
func (f *MessageDBFactory) DeleteLatestMessageIndexes(ctx context.Context, messageIDs []uint64) error {
	if err := f.availabilityError(); err != nil {
		return err
	}
	return f.mapError(f.engine.DeleteLatestMessageIndexes(ctx, messageIDs))
}

// AppendLeaderBatch appends leader records for multiple channels through one message DB batch request when possible.
func (f *MessageDBFactory) AppendLeaderBatch(ctx context.Context, items []AppendLeaderBatchItem) []AppendLeaderBatchResult {
	results := make([]AppendLeaderBatchResult, len(items))
	if len(items) == 0 {
		return results
	}
	if err := f.availabilityError(); err != nil {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	dbItems := make([]messagedb.AppendBatchItem, 0, len(items))
	acquired := make([]batchAcquiredStore, 0, len(items))
	defer func() {
		for _, item := range acquired {
			if err := mapMessageDBAdapterError(item.store.Close()); err != nil && results[item.index].Err == nil {
				results[item.index].Err = err
			}
		}
	}()
	for i, item := range items {
		dbStore, err := f.engine.ForChannel(channel.ChannelKey(item.ChannelKey), channel.ChannelID{ID: item.ChannelID.ID, Type: item.ChannelID.Type})
		if err != nil {
			results[i].Err = f.mapError(err)
			continue
		}
		dbItems = append(dbItems, messagedb.AppendBatchItem{
			Store:   dbStore,
			Records: encodeRecordsForMessageDB(item.ChannelID, item.Request.Records),
		})
		acquired = append(acquired, batchAcquiredStore{index: i, store: dbStore})
	}
	dbResults := messagedb.StoreAppendBatch(ctx, dbItems)
	if len(dbResults) != len(acquired) {
		for _, item := range acquired {
			results[item.index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	for i, acquiredItem := range acquired {
		dbResult := dbResults[i]
		err := f.mapError(dbResult.Err)
		if len(items[acquiredItem.index].Request.Records) == 0 {
			results[acquiredItem.index] = AppendLeaderBatchResult{BaseOffset: dbResult.BaseOffset + 1, LastOffset: dbResult.BaseOffset, Err: err}
			continue
		}
		results[acquiredItem.index] = AppendLeaderBatchResult{BaseOffset: dbResult.BaseOffset + 1, LastOffset: dbResult.LastOffset, Err: err}
	}
	return results
}

// ApplyFollowerBatch applies follower records for multiple channels through one message DB batch request when possible.
func (f *MessageDBFactory) ApplyFollowerBatch(ctx context.Context, items []ApplyFollowerBatchItem) []ApplyFollowerBatchResult {
	results := make([]ApplyFollowerBatchResult, len(items))
	if len(items) == 0 {
		return results
	}
	if err := f.availabilityError(); err != nil {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	dbItems := make([]messagedb.ApplyFetchBatchItem, 0, len(items))
	acquired := make([]batchAcquiredStore, 0, len(items))
	checkpointHWs := make([]uint64, 0, len(items))
	defer func() {
		for _, item := range acquired {
			if err := mapMessageDBAdapterError(item.store.Close()); err != nil && results[item.index].Err == nil {
				results[item.index].Err = err
			}
		}
	}()
	for i, item := range items {
		dbStore, err := f.engine.ForChannel(channel.ChannelKey(item.ChannelKey), channel.ChannelID{ID: item.ChannelID.ID, Type: item.ChannelID.Type})
		if err != nil {
			results[i].Err = f.mapError(err)
			continue
		}
		records := encodeRecordsForMessageDB(item.ChannelID, item.Request.Records)
		checkpointHW := followerApplyCheckpointHW(records, item.Request.LeaderHW)
		dbItems = append(dbItems, messagedb.ApplyFetchBatchItem{
			Store: dbStore,
			Request: channel.ApplyFetchStoreRequest{
				Records:      records,
				CheckpointHW: checkpointHW,
			},
		})
		acquired = append(acquired, batchAcquiredStore{index: i, store: dbStore})
		checkpointHWs = append(checkpointHWs, checkpointHWValue(checkpointHW))
	}
	dbResults := messagedb.StoreApplyFetchTrustedBatch(ctx, dbItems)
	if len(dbResults) != len(acquired) {
		for _, item := range acquired {
			results[item.index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	for i, acquiredItem := range acquired {
		results[acquiredItem.index] = ApplyFollowerBatchResult{
			LEO:          dbResults[i].LEO,
			CheckpointHW: checkpointHWs[i],
			Err:          f.mapError(dbResults[i].Err),
		}
	}
	return results
}

// StoreCheckpointBatch persists monotonic checkpoint HW updates without taking foreground append locks.
func (f *MessageDBFactory) StoreCheckpointBatch(ctx context.Context, items []StoreCheckpointBatchItem) []StoreCheckpointBatchResult {
	results := make([]StoreCheckpointBatchResult, len(items))
	if len(items) == 0 {
		return results
	}
	if err := f.availabilityError(); err != nil {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	dbItems := make([]messagedb.CheckpointHWBatchItem, 0, len(items))
	acquired := make([]batchAcquiredStore, 0, len(items))
	defer func() {
		for _, item := range acquired {
			if err := mapMessageDBAdapterError(item.store.Close()); err != nil && results[item.index].Err == nil {
				results[item.index].Err = err
			}
		}
	}()
	for i, item := range items {
		dbStore, err := f.engine.ForChannel(channel.ChannelKey(item.ChannelKey), channel.ChannelID{ID: item.ChannelID.ID, Type: item.ChannelID.Type})
		if err != nil {
			results[i].Err = f.mapError(err)
			continue
		}
		hw := item.Checkpoint.HW
		dbItems = append(dbItems, messagedb.CheckpointHWBatchItem{Store: dbStore, HW: hw})
		acquired = append(acquired, batchAcquiredStore{index: i, store: dbStore})
	}
	dbResults := messagedb.StoreCheckpointHWMonotonicBatch(ctx, dbItems)
	if len(dbResults) != len(acquired) {
		for _, item := range acquired {
			results[item.index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	for i, acquiredItem := range acquired {
		results[acquiredItem.index].Err = f.mapError(dbResults[i].Err)
	}
	return results
}

// Close closes the wrapped message DB engine.
func (f *MessageDBFactory) Close() error {
	if f == nil || f.engine == nil {
		return nil
	}
	f.closed.Store(true)
	return mapMessageDBAdapterError(f.engine.Close())
}

func (f *MessageDBFactory) availabilityError() error {
	if f == nil || f.engine == nil {
		return ch.ErrInvalidConfig
	}
	if f.closed.Load() {
		return ch.ErrClosed
	}
	return nil
}

func (f *MessageDBFactory) mapError(err error) error {
	mapped := mapMessageDBAdapterError(err)
	if mapped == nil || f == nil || !f.closed.Load() {
		return mapped
	}
	if errors.Is(mapped, context.Canceled) || errors.Is(mapped, context.DeadlineExceeded) {
		return mapped
	}
	return ch.ErrClosed
}

type batchAcquiredStore struct {
	// index preserves alignment with the caller's batch item.
	index int
	// store is the acquired lease that must close before batch return.
	store *messagedb.ChannelStore
}

func mapMessageDBAdapterError(err error) error {
	if errors.Is(err, channel.ErrClosed) {
		return ch.ErrClosed
	}
	return err
}

type messageDBChannelStoreAdapter struct {
	// store is the compatibility lease owned by this adapter.
	store *messagedb.ChannelStore
	// id is the logical channel identity used by record encoding and lookups.
	id ch.ChannelID
	// owner exposes factory shutdown without probing the physical store.
	owner *MessageDBFactory

	// closed provides a lock-free rejection path for post-close operations.
	closed atomic.Bool
	// closeOnce releases store exactly once.
	closeOnce sync.Once
	// closeErr preserves the terminal lease release result.
	closeErr error
}

func (a *messageDBChannelStoreAdapter) ensureOpen() error {
	if a == nil || a.store == nil || a.owner == nil {
		return ch.ErrInvalidConfig
	}
	if a.closed.Load() || a.owner.closed.Load() {
		return ch.ErrClosed
	}
	return nil
}

func (a *messageDBChannelStoreAdapter) mapError(err error) error {
	return mapMessageDBAdapterError(err)
}

func (a *messageDBChannelStoreAdapter) Load(ctx context.Context) (InitialState, error) {
	if err := a.ensureOpen(); err != nil {
		return InitialState{}, err
	}
	if err := ctx.Err(); err != nil {
		return InitialState{}, err
	}
	leo, err := a.store.LEOWithError()
	if err != nil {
		return InitialState{}, a.mapError(err)
	}
	checkpoint, err := a.store.LoadCheckpoint()
	if err != nil && !errors.Is(err, channel.ErrEmptyState) {
		return InitialState{}, a.mapError(err)
	}
	hw := minUint64(checkpoint.HW, leo)
	return InitialState{LEO: leo, HW: hw, CheckpointHW: hw}, nil
}

func (a *messageDBChannelStoreAdapter) AppendLeader(ctx context.Context, req AppendLeaderRequest) (AppendLeaderResult, error) {
	if err := a.ensureOpen(); err != nil {
		return AppendLeaderResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return AppendLeaderResult{}, err
	}
	records := a.encodeRecords(req.Records)
	base, err := a.store.Append(records)
	if err != nil {
		return AppendLeaderResult{}, a.mapError(err)
	}
	if len(records) == 0 {
		return AppendLeaderResult{BaseOffset: base + 1, LastOffset: base}, nil
	}
	return AppendLeaderResult{BaseOffset: base + 1, LastOffset: base + uint64(len(records))}, nil
}

func (a *messageDBChannelStoreAdapter) ApplyFollower(ctx context.Context, req ApplyFollowerRequest) (ApplyFollowerResult, error) {
	if err := a.ensureOpen(); err != nil {
		return ApplyFollowerResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ApplyFollowerResult{}, err
	}
	records := encodeRecordsForMessageDB(a.id, req.Records)
	checkpointHW := followerApplyCheckpointHW(records, req.LeaderHW)
	leo, err := storeApplyFetchRecords(a.store, channel.ApplyFetchStoreRequest{
		Records:      records,
		CheckpointHW: checkpointHW,
	})
	if err != nil {
		return ApplyFollowerResult{}, a.mapError(err)
	}
	return ApplyFollowerResult{LEO: leo, CheckpointHW: checkpointHWValue(checkpointHW)}, nil
}

func followerApplyCheckpointHW(records []channel.Record, leaderHW uint64) *uint64 {
	if leaderHW == 0 || len(records) == 0 {
		return nil
	}
	hw := minUint64(leaderHW, records[len(records)-1].Index)
	if hw == 0 {
		return nil
	}
	return &hw
}

func checkpointHWValue(hw *uint64) uint64 {
	if hw == nil {
		return 0
	}
	return *hw
}

func (a *messageDBChannelStoreAdapter) ReadCommitted(ctx context.Context, req ReadCommittedRequest) (ReadCommittedResult, error) {
	if err := a.ensureOpen(); err != nil {
		return ReadCommittedResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReadCommittedResult{}, err
	}
	readFrom := req.FromSeq
	if !req.Reverse && req.MinSeq > 0 && readFrom < req.MinSeq {
		readFrom = req.MinSeq
	}
	if req.Reverse && req.MinSeq > 0 && readFrom < req.MinSeq {
		return ReadCommittedResult{NextSeq: readFrom}, nil
	}
	if !req.Reverse && req.MaxSeq > 0 && req.MinSeq > 0 && req.MaxSeq < req.MinSeq {
		return ReadCommittedResult{NextSeq: readFrom}, nil
	}
	messages, err := a.store.ListMessagesBySeq(ctx, readFrom, req.Limit, req.MaxBytes, req.Reverse)
	if err != nil {
		return ReadCommittedResult{}, a.mapError(err)
	}
	out := make([]ch.Message, 0, len(messages))
	next := readFrom
	for _, msg := range messages {
		if req.MinSeq > 0 && msg.MessageSeq < req.MinSeq {
			if req.Reverse {
				next = req.MinSeq - 1
				break
			}
			continue
		}
		if req.MaxSeq > 0 && msg.MessageSeq > req.MaxSeq {
			if req.Reverse {
				continue
			}
			break
		}
		out = append(out, fromDBMessage(msg))
		if req.Reverse {
			if msg.MessageSeq == 0 {
				next = 0
			} else {
				next = msg.MessageSeq - 1
			}
		} else {
			next = msg.MessageSeq + 1
		}
	}
	return ReadCommittedResult{Messages: out, NextSeq: next}, nil
}

func (a *messageDBChannelStoreAdapter) LookupMessageByID(ctx context.Context, messageID uint64) (ch.Message, bool, error) {
	if err := a.ensureOpen(); err != nil {
		return ch.Message{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return ch.Message{}, false, err
	}
	if messageID == 0 {
		return ch.Message{}, false, nil
	}
	msg, ok, err := a.store.GetMessageByMessageID(messageID)
	if err != nil || !ok {
		return ch.Message{}, ok, a.mapError(err)
	}
	return fromDBMessage(msg), true, nil
}

func (a *messageDBChannelStoreAdapter) LookupIdempotency(ctx context.Context, fromUID string, clientMsgNo string) (IdempotencyHit, bool, error) {
	if err := a.ensureOpen(); err != nil {
		return IdempotencyHit{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return IdempotencyHit{}, false, err
	}
	if fromUID == "" || clientMsgNo == "" {
		return IdempotencyHit{}, false, nil
	}
	entry, payloadHash, ok, err := a.store.LookupIdempotency(channel.IdempotencyKey{
		ChannelID:   channel.ChannelID{ID: a.id.ID, Type: a.id.Type},
		FromUID:     fromUID,
		ClientMsgNo: clientMsgNo,
	})
	if err != nil || !ok {
		return IdempotencyHit{}, ok, a.mapError(err)
	}
	msg, ok, err := a.store.GetMessageBySeq(entry.MessageSeq)
	if err != nil || !ok {
		return IdempotencyHit{}, ok, a.mapError(err)
	}
	msg.MessageSeq = entry.MessageSeq
	msg.MessageID = entry.MessageID
	return IdempotencyHit{Message: fromDBMessage(msg), PayloadHash: payloadHash}, true, nil
}

func (a *messageDBChannelStoreAdapter) ReadLog(ctx context.Context, req ReadLogRequest) (ReadLogResult, error) {
	if err := a.ensureOpen(); err != nil {
		return ReadLogResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReadLogResult{}, err
	}
	fromZeroBased := uint64(0)
	if req.FromOffset > 0 {
		fromZeroBased = req.FromOffset - 1
	}
	records, err := a.store.Read(fromZeroBased, req.MaxBytes)
	if err != nil {
		return ReadLogResult{}, a.mapError(err)
	}
	out := make([]ch.Record, 0, len(records))
	for _, record := range records {
		if req.MaxOffset > 0 && record.Index > req.MaxOffset {
			break
		}
		out = append(out, fromDBRecord(record))
	}
	return ReadLogResult{Records: out}, nil
}

func (a *messageDBChannelStoreAdapter) LoadRetentionState(ctx context.Context) (RetentionState, error) {
	if err := a.ensureOpen(); err != nil {
		return RetentionState{}, err
	}
	if err := ctx.Err(); err != nil {
		return RetentionState{}, err
	}
	state, err := a.store.LoadRetentionState()
	if err != nil {
		return RetentionState{}, a.mapError(err)
	}
	return RetentionState{
		LocalRetentionThroughSeq:    state.LocalRetentionThroughSeq,
		PhysicalRetentionThroughSeq: state.PhysicalRetentionThroughSeq,
		RetainedMaxSeq:              state.RetainedMaxSeq,
	}, nil
}

func (a *messageDBChannelStoreAdapter) AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) (uint64, error) {
	if err := a.ensureOpen(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := a.store.AdoptRetentionBoundary(ctx, throughSeq, cursorName); err != nil {
		return 0, a.mapError(err)
	}
	state, err := a.LoadRetentionState(ctx)
	if err != nil {
		return 0, err
	}
	return state.RetainedMaxSeq, nil
}

func (a *messageDBChannelStoreAdapter) TrimMessagesThrough(ctx context.Context, throughSeq uint64, opts RetentionTrimOptions) (RetentionTrimResult, error) {
	if err := a.ensureOpen(); err != nil {
		return RetentionTrimResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RetentionTrimResult{}, err
	}
	result, err := a.store.TrimMessagesThroughLimit(ctx, throughSeq, messagedb.RetentionTrimOptions{
		MaxMessages: opts.MaxMessages,
		MaxBytes:    opts.MaxBytes,
	})
	if err != nil {
		return RetentionTrimResult{}, a.mapError(err)
	}
	return RetentionTrimResult{
		DeletedThroughSeq: result.DeletedThroughSeq,
		Deleted:           result.Deleted,
		More:              result.More,
	}, nil
}

func (a *messageDBChannelStoreAdapter) StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error {
	if err := a.ensureOpen(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.mapError(a.store.StoreCheckpointHWMonotonic(ctx, checkpoint.HW))
}

func (a *messageDBChannelStoreAdapter) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		a.closed.Store(true)
		if a.store != nil {
			a.closeErr = mapMessageDBAdapterError(a.store.Close())
		}
	})
	return a.closeErr
}

func (a *messageDBChannelStoreAdapter) encodeRecords(records []ch.Record) []channel.Record {
	return encodeRecordsForMessageDB(a.id, records)
}

func encodeRecordsForMessageDB(id ch.ChannelID, records []ch.Record) []channel.Record {
	out := make([]channel.Record, len(records))
	for i, record := range records {
		msg := channel.Message{
			MessageID:         record.ID,
			MessageSeq:        record.Index,
			Framer:            frame.Framer{SyncOnce: record.SyncOnce},
			Setting:           frame.Setting(record.Setting),
			ChannelID:         id.ID,
			ChannelType:       id.Type,
			FromUID:           record.FromUID,
			ClientMsgNo:       record.ClientMsgNo,
			ServerTimestampMS: record.ServerTimestampMS,
			Payload:           cloneBytes(record.Payload),
		}
		payload, _ := encodeDBCompatibleMessage(msg)
		out[i] = channel.Record{ID: record.ID, Index: record.Index, Epoch: record.Epoch, Payload: payload, SizeBytes: len(payload)}
	}
	return out
}

type applyFetchStore interface {
	StoreApplyFetch(channel.ApplyFetchStoreRequest) (uint64, error)
}

type trustedApplyFetchStore interface {
	StoreApplyFetchTrusted(channel.ApplyFetchStoreRequest) (uint64, error)
}

func storeApplyFetchRecords(store applyFetchStore, req channel.ApplyFetchStoreRequest) (uint64, error) {
	if trusted, ok := store.(trustedApplyFetchStore); ok {
		return trusted.StoreApplyFetchTrusted(req)
	}
	return store.StoreApplyFetch(req)
}

func fromDBRecord(record channel.Record) ch.Record {
	msg, err := decodeDBCompatibleMessage(record.Payload)
	if err == nil {
		return ch.Record{
			ID:                msg.MessageID,
			Index:             record.Index,
			Epoch:             record.Epoch,
			FromUID:           msg.FromUID,
			ClientMsgNo:       msg.ClientMsgNo,
			Setting:           uint8(msg.Setting),
			Payload:           cloneBytes(msg.Payload),
			SizeBytes:         len(msg.Payload),
			ServerTimestampMS: msg.ServerTimestampMS,
			SyncOnce:          msg.Framer.SyncOnce,
		}
	}
	return ch.Record{ID: record.ID, Index: record.Index, Epoch: record.Epoch, Payload: cloneBytes(record.Payload), SizeBytes: record.SizeBytes}
}

func fromDBMessage(msg channel.Message) ch.Message {
	return ch.Message{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, ChannelID: msg.ChannelID, ChannelType: msg.ChannelType, Setting: uint8(msg.Setting), FromUID: msg.FromUID, ClientMsgNo: msg.ClientMsgNo, Payload: cloneBytes(msg.Payload), ServerTimestampMS: msg.ServerTimestampMS, SyncOnce: msg.Framer.SyncOnce}
}

const durableMessageHeaderSize = 45

var durableServerTimestampMagic = [...]byte{'w', 'k', 't', 's'}

const durableServerTimestampSize = 12

func encodeDBCompatibleMessage(message channel.Message) ([]byte, error) {
	payloadHash := hashPayload(message.Payload)
	size := durableMessageHeaderSize + 4 + len(message.MsgKey) + 4 + len(message.ClientMsgNo) + 4 + len(message.StreamNo) + 4 + len(message.ChannelID) + 4 + len(message.Topic) + 4 + len(message.FromUID) + 4 + len(message.Payload)
	if message.ServerTimestampMS != 0 {
		size += durableServerTimestampSize
	}
	payload := make([]byte, 0, size)
	payload = append(payload, channel.DurableMessageCodecVersion)
	payload = binary.BigEndian.AppendUint64(payload, message.MessageID)
	payload = append(payload, encodeDBCompatibleFramerFlags(message.Framer), byte(message.Setting), byte(message.StreamFlag), message.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, message.Expire)
	payload = binary.BigEndian.AppendUint64(payload, message.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, message.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(message.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, payloadHash)
	payload = appendSizedString(payload, message.MsgKey)
	payload = appendSizedString(payload, message.ClientMsgNo)
	payload = appendSizedString(payload, message.StreamNo)
	payload = appendSizedString(payload, message.ChannelID)
	payload = appendSizedString(payload, message.Topic)
	payload = appendSizedString(payload, message.FromUID)
	payload = appendSizedBytes(payload, message.Payload)
	payload = appendServerTimestamp(payload, message.ServerTimestampMS)
	return payload, nil
}

func decodeDBCompatibleMessage(payload []byte) (channel.Message, error) {
	if len(payload) < durableMessageHeaderSize {
		return channel.Message{}, io.ErrUnexpectedEOF
	}
	if payload[0] != channel.DurableMessageCodecVersion {
		return channel.Message{}, ch.ErrInvalidConfig
	}
	msg := channel.Message{
		MessageID:   binary.BigEndian.Uint64(payload[1:9]),
		Framer:      decodeDBCompatibleFramerFlags(payload[9]),
		Setting:     frame.Setting(payload[10]),
		StreamFlag:  frame.StreamFlag(payload[11]),
		ChannelType: payload[12],
		Expire:      binary.BigEndian.Uint32(payload[13:17]),
		ClientSeq:   binary.BigEndian.Uint64(payload[17:25]),
		StreamID:    binary.BigEndian.Uint64(payload[25:33]),
		Timestamp:   int32(binary.BigEndian.Uint32(payload[33:37])),
	}
	pos := durableMessageHeaderSize
	var b []byte
	var err error
	if b, pos, err = readSizedBytes(payload, pos); err != nil {
		return channel.Message{}, err
	}
	msg.MsgKey = string(b)
	if b, pos, err = readSizedBytes(payload, pos); err != nil {
		return channel.Message{}, err
	}
	msg.ClientMsgNo = string(b)
	if b, pos, err = readSizedBytes(payload, pos); err != nil {
		return channel.Message{}, err
	}
	msg.StreamNo = string(b)
	if b, pos, err = readSizedBytes(payload, pos); err != nil {
		return channel.Message{}, err
	}
	msg.ChannelID = string(b)
	if b, pos, err = readSizedBytes(payload, pos); err != nil {
		return channel.Message{}, err
	}
	msg.Topic = string(b)
	if b, pos, err = readSizedBytes(payload, pos); err != nil {
		return channel.Message{}, err
	}
	msg.FromUID = string(b)
	if b, pos, err = readSizedBytes(payload, pos); err != nil {
		return channel.Message{}, err
	}
	msg.Payload = cloneBytes(b)
	if serverTimestampMS, ok := decodeServerTimestamp(payload, pos); ok {
		msg.ServerTimestampMS = serverTimestampMS
	}
	return msg, nil
}

func encodeDBCompatibleFramerFlags(framer frame.Framer) uint8 {
	var flags uint8
	if framer.NoPersist {
		flags |= 1
	}
	if framer.RedDot {
		flags |= 2
	}
	if framer.SyncOnce {
		flags |= 4
	}
	if framer.DUP {
		flags |= 8
	}
	if framer.HasServerVersion {
		flags |= 16
	}
	if framer.End {
		flags |= 32
	}
	return flags
}

func decodeDBCompatibleFramerFlags(flags uint8) frame.Framer {
	return frame.Framer{
		NoPersist:        flags&1 != 0,
		RedDot:           flags&2 != 0,
		SyncOnce:         flags&4 != 0,
		DUP:              flags&8 != 0,
		HasServerVersion: flags&16 != 0,
		End:              flags&32 != 0,
	}
}

func appendSizedString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendSizedBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendServerTimestamp(dst []byte, serverTimestampMS int64) []byte {
	if serverTimestampMS == 0 {
		return dst
	}
	dst = append(dst, durableServerTimestampMagic[:]...)
	return binary.BigEndian.AppendUint64(dst, uint64(serverTimestampMS))
}

func decodeServerTimestamp(payload []byte, pos int) (int64, bool) {
	if len(payload)-pos < durableServerTimestampSize {
		return 0, false
	}
	if string(payload[pos:pos+len(durableServerTimestampMagic)]) != string(durableServerTimestampMagic[:]) {
		return 0, false
	}
	return int64(binary.BigEndian.Uint64(payload[pos+len(durableServerTimestampMagic) : pos+durableServerTimestampSize])), true
}

func readSizedBytes(payload []byte, pos int) ([]byte, int, error) {
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

func hashPayload(payload []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(payload)
	return h.Sum64()
}
