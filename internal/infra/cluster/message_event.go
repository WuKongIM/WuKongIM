package cluster

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// MessageEventNode is the cluster message event projection facade used by internal.
type MessageEventNode interface {
	AppendMessageEvent(context.Context, metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error)
	GetMessageEventStatesBatch(context.Context, []metadb.MessageEventMessageKey, int) (map[metadb.MessageEventMessageKey][]metadb.MessageEventState, error)
}

// MessageEventStore adapts cluster message event projections to the message usecase port.
type MessageEventStore struct {
	node MessageEventNode
}

var _ message.MessageEventStore = (*MessageEventStore)(nil)

// NewMessageEventStore creates a MessageEventStore.
func NewMessageEventStore(node MessageEventNode) *MessageEventStore {
	return &MessageEventStore{node: node}
}

// AppendMessageEvent persists one message event projection update.
func (s *MessageEventStore) AppendMessageEvent(ctx context.Context, event message.MessageEventAppend) (message.MessageEventAppendResult, error) {
	if s == nil || s.node == nil {
		return message.MessageEventAppendResult{}, message.ErrMessageEventStoreRequired
	}
	result, err := s.node.AppendMessageEvent(ctx, metadbMessageEventAppend(event))
	if err != nil {
		return message.MessageEventAppendResult{}, mapMessageEventError(err)
	}
	return messageEventAppendResultFromMeta(result), nil
}

// GetMessageEventStatesBatch reads compact event lane states for message keys.
func (s *MessageEventStore) GetMessageEventStatesBatch(ctx context.Context, keys []message.MessageEventMessageKey, limit int) (map[message.MessageEventMessageKey][]message.MessageEventState, error) {
	if len(keys) == 0 {
		return map[message.MessageEventMessageKey][]message.MessageEventState{}, nil
	}
	if s == nil || s.node == nil {
		return nil, message.ErrMessageEventStoreRequired
	}
	rows, err := s.node.GetMessageEventStatesBatch(ctx, metadbMessageEventKeys(keys), limit)
	if err != nil {
		return nil, mapMessageEventError(err)
	}
	return messageEventStateMapFromMeta(rows), nil
}

func mapMessageEventError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, metadb.ErrInvalidArgument) || errors.Is(err, metadb.ErrStaleMeta) || errors.Is(err, metadb.ErrCorruptValue) {
		return err
	}
	return mapAppendError(err)
}

func metadbMessageEventAppend(event message.MessageEventAppend) metadb.MessageEventAppend {
	return metadb.MessageEventAppend{
		ChannelID:   event.ChannelID,
		ChannelType: event.ChannelType,
		ClientMsgNo: event.ClientMsgNo,
		EventID:     event.EventID,
		EventKey:    event.EventKey,
		EventType:   event.EventType,
		Visibility:  event.Visibility,
		OccurredAt:  event.OccurredAt,
		Payload:     cloneBytes(event.Payload),
		UpdatedAt:   event.UpdatedAt,
	}
}

func messageEventAppendResultFromMeta(result metadb.MessageEventAppendResult) message.MessageEventAppendResult {
	return message.MessageEventAppendResult{
		ChannelID:   result.ChannelID,
		ChannelType: result.ChannelType,
		ClientMsgNo: result.ClientMsgNo,
		EventID:     result.EventID,
		EventKey:    result.EventKey,
		MsgEventSeq: result.MsgEventSeq,
		Status:      result.Status,
		State:       messageEventStateFromMeta(result.State),
	}
}

func metadbMessageEventKeys(keys []message.MessageEventMessageKey) []metadb.MessageEventMessageKey {
	out := make([]metadb.MessageEventMessageKey, len(keys))
	for i, key := range keys {
		out[i] = metadb.MessageEventMessageKey{
			ChannelID:   key.ChannelID,
			ChannelType: key.ChannelType,
			ClientMsgNo: key.ClientMsgNo,
		}
	}
	return out
}

func messageEventStateMapFromMeta(rows map[metadb.MessageEventMessageKey][]metadb.MessageEventState) map[message.MessageEventMessageKey][]message.MessageEventState {
	out := make(map[message.MessageEventMessageKey][]message.MessageEventState, len(rows))
	for key, states := range rows {
		out[message.MessageEventMessageKey{
			ChannelID:   key.ChannelID,
			ChannelType: key.ChannelType,
			ClientMsgNo: key.ClientMsgNo,
		}] = messageEventStatesFromMeta(states)
	}
	return out
}

func messageEventStatesFromMeta(states []metadb.MessageEventState) []message.MessageEventState {
	out := make([]message.MessageEventState, len(states))
	for i, state := range states {
		out[i] = messageEventStateFromMeta(state)
	}
	return out
}

func messageEventStateFromMeta(state metadb.MessageEventState) message.MessageEventState {
	return message.MessageEventState{
		ChannelID:       state.ChannelID,
		ChannelType:     state.ChannelType,
		ClientMsgNo:     state.ClientMsgNo,
		EventKey:        state.EventKey,
		Status:          state.Status,
		LastMsgEventSeq: state.LastMsgEventSeq,
		LastEventID:     state.LastEventID,
		LastEventType:   state.LastEventType,
		LastVisibility:  state.LastVisibility,
		LastOccurredAt:  state.LastOccurredAt,
		SnapshotPayload: cloneBytes(state.SnapshotPayload),
		EndReason:       state.EndReason,
		Error:           state.Error,
		UpdatedAt:       state.UpdatedAt,
	}
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
