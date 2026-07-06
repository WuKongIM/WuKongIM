package message

import (
	"context"
	"strings"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// AppendMessageEvent validates and persists one message-scoped event projection update.
func (a *App) AppendMessageEvent(ctx context.Context, event MessageEventAppend) (MessageEventAppendResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	event, err := normalizeMessageEventAppendCommand(event)
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	if a == nil || a.eventStore == nil {
		return MessageEventAppendResult{}, ErrMessageEventStoreRequired
	}
	if event.UpdatedAt == 0 {
		now := time.Now
		if a.now != nil {
			now = a.now
		}
		event.UpdatedAt = now().UnixMilli()
	}
	result, err := a.eventStore.AppendMessageEvent(ctx, event)
	if err != nil {
		return MessageEventAppendResult{}, err
	}
	result.FromUID = event.FromUID
	result.MessageID = event.MessageID
	result.State.SnapshotPayload = cloneBytes(result.State.SnapshotPayload)
	return result, nil
}

func normalizeMessageEventAppendCommand(event MessageEventAppend) (MessageEventAppend, error) {
	event.ChannelID = strings.TrimSpace(event.ChannelID)
	event.FromUID = strings.TrimSpace(event.FromUID)
	event.ClientMsgNo = strings.TrimSpace(event.ClientMsgNo)
	event.EventID = strings.TrimSpace(event.EventID)
	event.EventKey = strings.TrimSpace(event.EventKey)
	event.EventType = strings.ToLower(strings.TrimSpace(event.EventType))
	event.Visibility = strings.TrimSpace(event.Visibility)
	event.Payload = cloneBytes(event.Payload)

	if event.ChannelID == "" {
		return MessageEventAppend{}, ErrMessageEventChannelIDRequired
	}
	if event.ChannelType <= 0 {
		return MessageEventAppend{}, ErrMessageEventChannelTypeRequired
	}
	if event.ClientMsgNo == "" {
		return MessageEventAppend{}, ErrMessageEventClientMsgNoRequired
	}
	if event.EventID == "" {
		return MessageEventAppend{}, ErrMessageEventIDRequired
	}
	if event.EventType == "" {
		return MessageEventAppend{}, ErrMessageEventTypeRequired
	}
	if event.EventKey == "" {
		event.EventKey = EventKeyDefault
	}
	if event.EventType == EventTypeStreamFinish {
		event.EventKey = EventKeyFinish
	}

	channelID, err := normalizeMessageEventChannelID(event)
	if err != nil {
		return MessageEventAppend{}, err
	}
	event.ChannelID = channelID
	return event, nil
}

func normalizeMessageEventChannelID(event MessageEventAppend) (string, error) {
	switch event.ChannelType {
	case int64(channelTypePerson):
		return runtimechannelid.NormalizePersonChannel(event.FromUID, event.ChannelID)
	case int64(channelTypeAgent):
		if strings.Contains(event.ChannelID, "@") {
			uid, agentUID, err := runtimechannelid.DecodeAgentChannel(event.ChannelID)
			if err != nil {
				return "", err
			}
			if event.FromUID != "" && uid != event.FromUID {
				return "", runtimechannelid.ErrInvalidAgentChannel
			}
			return runtimechannelid.EncodeAgentChannel(uid, agentUID), nil
		}
		if event.FromUID == "" {
			return "", runtimechannelid.ErrInvalidAgentChannel
		}
		return runtimechannelid.EncodeAgentChannel(event.FromUID, event.ChannelID), nil
	default:
		return event.ChannelID, nil
	}
}
