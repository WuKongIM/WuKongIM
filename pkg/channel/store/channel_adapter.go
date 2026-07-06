package store

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"io"
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// MessageDBFactory adapts the shared message DB engine to the channel runtime.
type MessageDBFactory struct {
	engine          *messagedb.Engine
	mu              sync.Mutex
	checkpointLocks map[ch.ChannelKey]*sync.Mutex
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
	return &MessageDBFactory{engine: engine, checkpointLocks: make(map[ch.ChannelKey]*sync.Mutex)}
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
	if f == nil || f.engine == nil {
		return nil, ch.ErrInvalidConfig
	}
	dbStore := f.engine.ForChannel(channel.ChannelKey(key), channel.ChannelID{ID: id.ID, Type: id.Type})
	return &messageDBChannelStoreAdapter{store: dbStore, id: id, checkpointMu: f.checkpointLock(key)}, nil
}

// ListChannelsPage returns one ordered page from the local message channel catalog.
func (f *MessageDBFactory) ListChannelsPage(ctx context.Context, after ch.ChannelKey, limit int) ([]ChannelCatalogEntry, ch.ChannelKey, bool, error) {
	if f == nil || f.engine == nil {
		return nil, "", false, ch.ErrInvalidConfig
	}
	entries, cursor, more, err := f.engine.ListChannelsPage(ctx, messagedb.ChannelKey(after), limit)
	if err != nil {
		return nil, "", false, err
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

// AppendLeaderBatch appends leader records for multiple channels through one message DB batch request when possible.
func (f *MessageDBFactory) AppendLeaderBatch(ctx context.Context, items []AppendLeaderBatchItem) []AppendLeaderBatchResult {
	results := make([]AppendLeaderBatchResult, len(items))
	if len(items) == 0 {
		return results
	}
	if f == nil || f.engine == nil {
		for i := range results {
			results[i].Err = ch.ErrInvalidConfig
		}
		return results
	}
	dbItems := make([]messagedb.AppendBatchItem, 0, len(items))
	active := make([]int, 0, len(items))
	for i, item := range items {
		dbStore := f.engine.ForChannel(channel.ChannelKey(item.ChannelKey), channel.ChannelID{ID: item.ChannelID.ID, Type: item.ChannelID.Type})
		dbItems = append(dbItems, messagedb.AppendBatchItem{
			Store:   dbStore,
			Records: encodeRecordsForMessageDB(item.ChannelID, item.Request.Records),
		})
		active = append(active, i)
	}
	dbResults := messagedb.StoreAppendBatch(ctx, dbItems)
	if len(dbResults) != len(active) {
		for _, index := range active {
			results[index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	for i, index := range active {
		item := dbResults[i]
		if len(items[index].Request.Records) == 0 {
			results[index] = AppendLeaderBatchResult{BaseOffset: item.BaseOffset + 1, LastOffset: item.BaseOffset, Err: item.Err}
			continue
		}
		results[index] = AppendLeaderBatchResult{BaseOffset: item.BaseOffset + 1, LastOffset: item.LastOffset, Err: item.Err}
	}
	return results
}

// ApplyFollowerBatch applies follower records for multiple channels through one message DB batch request when possible.
func (f *MessageDBFactory) ApplyFollowerBatch(ctx context.Context, items []ApplyFollowerBatchItem) []ApplyFollowerBatchResult {
	results := make([]ApplyFollowerBatchResult, len(items))
	if len(items) == 0 {
		return results
	}
	if f == nil || f.engine == nil {
		for i := range results {
			results[i].Err = ch.ErrInvalidConfig
		}
		return results
	}
	dbItems := make([]messagedb.ApplyFetchBatchItem, 0, len(items))
	active := make([]int, 0, len(items))
	for i, item := range items {
		dbStore := f.engine.ForChannel(channel.ChannelKey(item.ChannelKey), channel.ChannelID{ID: item.ChannelID.ID, Type: item.ChannelID.Type})
		dbItems = append(dbItems, messagedb.ApplyFetchBatchItem{
			Store: dbStore,
			Request: channel.ApplyFetchStoreRequest{
				Records: encodeRecordsForMessageDB(item.ChannelID, item.Request.Records),
			},
		})
		active = append(active, i)
	}
	dbResults := messagedb.StoreApplyFetchTrustedBatch(ctx, dbItems)
	if len(dbResults) != len(active) {
		for _, index := range active {
			results[index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	for i, index := range active {
		results[index] = ApplyFollowerBatchResult{LEO: dbResults[i].LEO, Err: dbResults[i].Err}
	}
	return results
}

// Close closes the wrapped message DB engine.
func (f *MessageDBFactory) Close() error {
	if f == nil || f.engine == nil {
		return nil
	}
	return f.engine.Close()
}

func (f *MessageDBFactory) checkpointLock(key ch.ChannelKey) *sync.Mutex {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checkpointLocks == nil {
		f.checkpointLocks = make(map[ch.ChannelKey]*sync.Mutex)
	}
	lock := f.checkpointLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		f.checkpointLocks[key] = lock
	}
	return lock
}

type messageDBChannelStoreAdapter struct {
	store        *messagedb.ChannelStore
	id           ch.ChannelID
	checkpointMu *sync.Mutex
}

func (a *messageDBChannelStoreAdapter) Load(ctx context.Context) (InitialState, error) {
	if err := ctx.Err(); err != nil {
		return InitialState{}, err
	}
	leo, err := a.store.LEOWithError()
	if err != nil {
		return InitialState{}, err
	}
	checkpoint, err := a.store.LoadCheckpoint()
	if err != nil && !errors.Is(err, channel.ErrEmptyState) {
		return InitialState{}, err
	}
	hw := minUint64(checkpoint.HW, leo)
	return InitialState{LEO: leo, HW: hw, CheckpointHW: hw}, nil
}

func (a *messageDBChannelStoreAdapter) AppendLeader(ctx context.Context, req AppendLeaderRequest) (AppendLeaderResult, error) {
	if err := ctx.Err(); err != nil {
		return AppendLeaderResult{}, err
	}
	records := a.encodeRecords(req.Records)
	base, err := a.store.Append(records)
	if err != nil {
		return AppendLeaderResult{}, err
	}
	if len(records) == 0 {
		return AppendLeaderResult{BaseOffset: base + 1, LastOffset: base}, nil
	}
	return AppendLeaderResult{BaseOffset: base + 1, LastOffset: base + uint64(len(records))}, nil
}

func (a *messageDBChannelStoreAdapter) ApplyFollower(ctx context.Context, req ApplyFollowerRequest) (ApplyFollowerResult, error) {
	if err := ctx.Err(); err != nil {
		return ApplyFollowerResult{}, err
	}
	leo, err := storeApplyFetchRecords(a.store, channel.ApplyFetchStoreRequest{Records: encodeRecordsForMessageDB(a.id, req.Records)})
	if err != nil {
		return ApplyFollowerResult{}, err
	}
	return ApplyFollowerResult{LEO: leo}, nil
}

func (a *messageDBChannelStoreAdapter) ReadCommitted(ctx context.Context, req ReadCommittedRequest) (ReadCommittedResult, error) {
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
	messages, err := a.store.ListMessagesBySeq(readFrom, req.Limit, req.MaxBytes, req.Reverse)
	if err != nil {
		return ReadCommittedResult{}, err
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
	if err := ctx.Err(); err != nil {
		return ch.Message{}, false, err
	}
	if messageID == 0 {
		return ch.Message{}, false, nil
	}
	msg, ok, err := a.store.GetMessageByMessageID(messageID)
	if err != nil || !ok {
		return ch.Message{}, ok, err
	}
	return fromDBMessage(msg), true, nil
}

func (a *messageDBChannelStoreAdapter) LookupIdempotency(ctx context.Context, fromUID string, clientMsgNo string) (IdempotencyHit, bool, error) {
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
		return IdempotencyHit{}, ok, err
	}
	msg, ok, err := a.store.GetMessageBySeq(entry.MessageSeq)
	if err != nil || !ok {
		return IdempotencyHit{}, ok, err
	}
	msg.MessageSeq = entry.MessageSeq
	msg.MessageID = entry.MessageID
	return IdempotencyHit{Message: fromDBMessage(msg), PayloadHash: payloadHash}, true, nil
}

func (a *messageDBChannelStoreAdapter) ReadLog(ctx context.Context, req ReadLogRequest) (ReadLogResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadLogResult{}, err
	}
	fromZeroBased := uint64(0)
	if req.FromOffset > 0 {
		fromZeroBased = req.FromOffset - 1
	}
	records, err := a.store.Read(fromZeroBased, req.MaxBytes)
	if err != nil {
		return ReadLogResult{}, err
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
	if err := ctx.Err(); err != nil {
		return RetentionState{}, err
	}
	state, err := a.store.LoadRetentionState()
	if err != nil {
		return RetentionState{}, err
	}
	return RetentionState{
		LocalRetentionThroughSeq:    state.LocalRetentionThroughSeq,
		PhysicalRetentionThroughSeq: state.PhysicalRetentionThroughSeq,
		RetainedMaxSeq:              state.RetainedMaxSeq,
	}, nil
}

func (a *messageDBChannelStoreAdapter) AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := a.store.AdoptRetentionBoundary(ctx, throughSeq, cursorName); err != nil {
		return 0, err
	}
	state, err := a.LoadRetentionState(ctx)
	if err != nil {
		return 0, err
	}
	return state.RetainedMaxSeq, nil
}

func (a *messageDBChannelStoreAdapter) TrimMessagesThrough(ctx context.Context, throughSeq uint64, opts RetentionTrimOptions) (RetentionTrimResult, error) {
	if err := ctx.Err(); err != nil {
		return RetentionTrimResult{}, err
	}
	result, err := a.store.TrimMessagesThroughLimit(ctx, throughSeq, messagedb.RetentionTrimOptions{
		MaxMessages: opts.MaxMessages,
		MaxBytes:    opts.MaxBytes,
	})
	if err != nil {
		return RetentionTrimResult{}, err
	}
	return RetentionTrimResult{
		DeletedThroughSeq: result.DeletedThroughSeq,
		Deleted:           result.Deleted,
		More:              result.More,
	}, nil
}

func (a *messageDBChannelStoreAdapter) StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.checkpointMu != nil {
		a.checkpointMu.Lock()
		defer a.checkpointMu.Unlock()
	}
	current, err := a.store.LoadCheckpoint()
	if err != nil {
		if errors.Is(err, channel.ErrEmptyState) {
			return a.store.StoreCheckpoint(channel.Checkpoint{HW: checkpoint.HW})
		}
		return err
	}
	if checkpoint.HW <= current.HW {
		return nil
	}
	current.HW = checkpoint.HW
	return a.store.StoreCheckpoint(current)
}

func (a *messageDBChannelStoreAdapter) Close() error { return nil }

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
