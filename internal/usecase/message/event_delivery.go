package message

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// MessageEventNotification is an online projection of an accepted stream event.
// Event IDs deduplicate retries; producers must serialize events per message.
// Payloads are immutable and are never retained after synchronous dispatch.
type MessageEventNotification struct {
	ChannelID   string          `json:"channel_id"`
	ChannelType uint8           `json:"channel_type"`
	FromUID     string          `json:"from_uid"`
	MessageID   uint64          `json:"message_id,string"`
	MessageSeq  uint64          `json:"message_seq,string"`
	ClientMsgNo string          `json:"client_msg_no"`
	EventID     string          `json:"event_id"`
	EventKey    string          `json:"event_key"`
	EventType   string          `json:"event_type"`
	Timestamp   int64           `json:"timestamp"`
	TextOffset  *int            `json:"text_offset,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

// EventNotificationSender routes one bounded page to exact online sessions.
// No RECVACK state or offline event replay is allocated for these notifications.
type EventNotificationSender interface {
	SendMessageEvent(context.Context, []string, MessageEventNotification) error
}

var ErrStreamEventBusy = errors.New("stream event admission busy; retry the same event_id")
var ErrStreamBaseRequired = errors.New("committed stream base message required")

// finishedStreamResult keeps finished projections immutable under late retries.
func (a *App) finishedStreamResult(ctx context.Context, event MessageEventAppend, base SyncedMessage) (MessageEventAppendResult, bool, error) {
	key := MessageEventMessageKey{ChannelID: event.ChannelID, ChannelType: event.ChannelType, ClientMsgNo: event.ClientMsgNo}
	rows, err := a.eventStore.GetMessageEventStatesBatch(ctx, []MessageEventMessageKey{key}, 128)
	if err != nil {
		return MessageEventAppendResult{}, false, err
	}
	var finish, lane *MessageEventState
	for i := range rows[key] {
		state := &rows[key][i]
		if state.EventKey == EventKeyFinish && state.LastEventType == EventTypeStreamFinish {
			finish = state
		}
		if state.EventKey == event.EventKey {
			lane = state
		}
	}
	if finish == nil {
		return MessageEventAppendResult{}, false, nil
	}
	if lane == nil {
		lane = finish
	}
	return MessageEventAppendResult{ChannelID: event.ChannelID, ChannelType: event.ChannelType, FromUID: base.FromUID, MessageID: base.MessageID, ClientMsgNo: event.ClientMsgNo, EventID: event.EventID, EventKey: event.EventKey, MsgEventSeq: lane.LastMsgEventSeq, Status: lane.Status, State: *lane}, true, nil
}

// streamEventBase proves the notification is attached to the committed stream
// identity. Caller-supplied message IDs or senders never replace stored evidence.
func (a *App) streamEventBase(ctx context.Context, event MessageEventAppend) (SyncedMessage, error) {
	if a.lookupReader == nil {
		return SyncedMessage{}, ErrStreamBaseRequired
	}
	rows, err := a.readLookupMessages(ctx, MessageScanQuery{ChannelID: ChannelID{ID: event.ChannelID, Type: uint8(event.ChannelType)}, ClientMsgNo: event.ClientMsgNo, Limit: 1, MaxBytes: 1 << 20})
	if err != nil {
		return SyncedMessage{}, err
	}
	if len(rows) != 1 || !isLegacyStreamMessage(rows[0].Setting) || rows[0].Flags.NoPersist || rows[0].Flags.SyncOnce || event.FromUID != "" && rows[0].FromUID != event.FromUID || event.MessageID != 0 && rows[0].MessageID != event.MessageID {
		return SyncedMessage{}, ErrStreamBaseRequired
	}
	return rows[0], nil
}

// notifyStreamEvent bounds each fanout to five seconds and 128 members per page.
// Admission caps concurrent fanouts at four without a waiting queue or per-member
// goroutines. A notification failure cannot undo an accepted cache/durable write;
// disconnected or slow clients recover the final projection from history.
func (a *App) notifyStreamEvent(ctx context.Context, base SyncedMessage, event MessageEventAppend, result MessageEventAppendResult) {
	if event.Visibility != "" && event.Visibility != VisibilityPublic || result.State.LastVisibility != VisibilityPublic {
		return
	}
	// An old idempotency retry can observe a newer projection; never broadcast its
	// stale delta again under the new projection's identity.
	if result.State.LastEventID != event.EventID {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	notification := MessageEventNotification{ChannelID: event.ChannelID, ChannelType: uint8(event.ChannelType), FromUID: base.FromUID, MessageID: base.MessageID, MessageSeq: base.MessageSeq, ClientMsgNo: event.ClientMsgNo, EventID: event.EventID, EventKey: event.EventKey, EventType: event.EventType, Timestamp: event.UpdatedAt, Payload: event.Payload}
	// Text offsets measure UTF-8 bytes. Recovery can ignore deltas already in
	// a history snapshot without sending the growing snapshot for every token.
	if event.EventType == EventTypeStreamDelta {
		var state struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}
		var delta struct {
			Kind  string `json:"kind"`
			Delta string `json:"delta"`
		}
		if json.Unmarshal(result.State.SnapshotPayload, &state) == nil && json.Unmarshal(event.Payload, &delta) == nil && state.Kind == "text" && delta.Kind == "text" {
			offset := len(state.Text) - len(delta.Delta)
			if offset >= 0 {
				notification.TextOffset = &offset
			}
		}
	}
	var err error
	defer func() {
		if a.eventNotificationResult != nil {
			a.eventNotificationResult(err)
		}
	}()
	if event.ChannelType == 1 {
		left, right, e := runtimechannelid.DecodePersonChannel(event.ChannelID)
		if e != nil {
			err = e
			return
		}
		err = a.eventNotifications.SendMessageEvent(ctx, []string{left, right}, notification)
		return
	}
	after := ""
	for {
		var uids []string
		var next string
		var done bool
		uids, next, done, err = a.eventSubscribers.ListChannelSubscribersAuthoritative(ctx, event.ChannelID, event.ChannelType, after, 128)
		if err != nil {
			return
		}
		if len(uids) > 128 {
			err = errors.New("stream subscriber page exceeds budget")
			return
		}
		if err = a.eventNotifications.SendMessageEvent(ctx, uids, notification); err != nil {
			return
		}
		if done {
			return
		}
		if next == "" || next == after {
			err = errors.New("stream subscriber cursor did not advance")
			return
		}
		after = next
	}
}
