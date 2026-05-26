package store

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"io"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

// MessageDBFactory adapts the shared message DB engine to channelv2.
type MessageDBFactory struct {
	engine          *messagedb.Engine
	mu              sync.Mutex
	checkpointLocks map[ch.ChannelKey]*sync.Mutex
}

// NewMessageDBFactory opens a message DB engine behind the v2 adapter.
func NewMessageDBFactory(path string) *MessageDBFactory {
	engine, err := messagedb.Open(path)
	if err != nil {
		return &MessageDBFactory{}
	}
	return &MessageDBFactory{engine: engine, checkpointLocks: make(map[ch.ChannelKey]*sync.Mutex)}
}

// ChannelStore returns an adapter for one message DB channel store.
func (f *MessageDBFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (ChannelStore, error) {
	if f == nil || f.engine == nil {
		return nil, ch.ErrInvalidConfig
	}
	dbStore := f.engine.ForChannel(channel.ChannelKey(key), channel.ChannelID{ID: id.ID, Type: id.Type})
	return &messageDBChannelStoreAdapter{store: dbStore, id: id, checkpointMu: f.checkpointLock(key)}, nil
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
	leo, err := a.store.StoreApplyFetch(channel.ApplyFetchStoreRequest{Records: a.encodeRecords(req.Records)})
	if err != nil {
		return ApplyFollowerResult{}, err
	}
	return ApplyFollowerResult{LEO: leo}, nil
}

func (a *messageDBChannelStoreAdapter) ReadCommitted(ctx context.Context, req ReadCommittedRequest) (ReadCommittedResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadCommittedResult{}, err
	}
	messages, err := a.store.ListMessagesBySeq(req.FromSeq, req.Limit, req.MaxBytes, false)
	if err != nil {
		return ReadCommittedResult{}, err
	}
	out := make([]ch.Message, 0, len(messages))
	next := req.FromSeq
	for _, msg := range messages {
		if msg.MessageSeq > req.MaxSeq {
			break
		}
		out = append(out, fromDBMessage(msg))
		next = msg.MessageSeq + 1
	}
	return ReadCommittedResult{Messages: out, NextSeq: next}, nil
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
	out := make([]channel.Record, len(records))
	for i, record := range records {
		msg := channel.Message{MessageID: record.ID, MessageSeq: record.Index, ChannelID: a.id.ID, ChannelType: a.id.Type, Payload: cloneBytes(record.Payload)}
		payload, _ := encodeDBCompatibleMessage(msg)
		out[i] = channel.Record{ID: record.ID, Index: record.Index, Epoch: record.Epoch, Payload: payload, SizeBytes: len(payload)}
	}
	return out
}

func fromDBRecord(record channel.Record) ch.Record {
	msg, err := decodeDBCompatibleMessage(record.Payload)
	if err == nil {
		return ch.Record{ID: msg.MessageID, Index: record.Index, Epoch: record.Epoch, Payload: cloneBytes(msg.Payload), SizeBytes: len(msg.Payload)}
	}
	return ch.Record{ID: record.ID, Index: record.Index, Epoch: record.Epoch, Payload: cloneBytes(record.Payload), SizeBytes: record.SizeBytes}
}

func fromDBMessage(msg channel.Message) ch.Message {
	return ch.Message{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, ChannelID: msg.ChannelID, ChannelType: msg.ChannelType, FromUID: msg.FromUID, ClientMsgNo: msg.ClientMsgNo, Payload: cloneBytes(msg.Payload)}
}

const durableMessageHeaderSize = 45

func encodeDBCompatibleMessage(message channel.Message) ([]byte, error) {
	payloadHash := hashPayload(message.Payload)
	size := durableMessageHeaderSize + 4 + len(message.MsgKey) + 4 + len(message.ClientMsgNo) + 4 + len(message.StreamNo) + 4 + len(message.ChannelID) + 4 + len(message.Topic) + 4 + len(message.FromUID) + 4 + len(message.Payload)
	payload := make([]byte, 0, size)
	payload = append(payload, channel.DurableMessageCodecVersion)
	payload = binary.BigEndian.AppendUint64(payload, message.MessageID)
	payload = append(payload, 0, byte(message.Setting), byte(message.StreamFlag), message.ChannelType)
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
		Setting:     channel.Message{}.Setting,
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
	if b, _, err = readSizedBytes(payload, pos); err != nil {
		return channel.Message{}, err
	}
	msg.Payload = cloneBytes(b)
	return msg, nil
}

func appendSizedString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendSizedBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
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
