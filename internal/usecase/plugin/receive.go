package plugin

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// OfflineReceiveEvent describes one offline recipient eligible for the legacy Receive hook.
type OfflineReceiveEvent struct {
	// Message is the committed message observed by delivery resolution.
	Message channel.Message
	// UID is the offline recipient UID.
	UID string
	// RequestScoped reports that the delivery envelope was scoped to request subscribers only.
	RequestScoped bool
}

// ReceiveOffline invokes the highest-priority bound local Receive plugin for one offline recipient.
func (a *App) ReceiveOffline(ctx context.Context, event OfflineReceiveEvent) error {
	if a == nil || !a.offlineReceiveEligible(event) {
		return nil
	}
	plugin, ok, err := a.BoundReceivePluginForUID(ctx, event.UID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if !a.markReceiveDedupe(event.Message.MessageID, event.UID) {
		return nil
	}
	packet := receivePacketFromOfflineEvent(event)
	if err := a.InvokeReceive(ctx, plugin, packet); err != nil {
		a.forgetReceiveDedupe(event.Message.MessageID, event.UID)
		a.logReceiveFailure(event, plugin, err)
		return err
	}
	return nil
}

func (a *App) offlineReceiveEligible(event OfflineReceiveEvent) bool {
	msg := event.Message
	if event.UID == "" || msg.MessageID == 0 {
		return false
	}
	if event.RequestScoped || msg.ChannelType == frame.ChannelTypeTemp {
		return false
	}
	if msg.Framer.NoPersist || msg.Framer.SyncOnce {
		return false
	}
	if msg.FromUID == "" || msg.FromUID == event.UID || a.isSystemSender(msg.FromUID) {
		return false
	}
	return true
}

func (a *App) isSystemSender(uid string) bool {
	if uid == "" {
		return false
	}
	if a != nil && a.defaultSenderUID != "" && uid == a.defaultSenderUID {
		return true
	}
	return a != nil && a.systemUIDs != nil && a.systemUIDs.IsSystemUID(uid)
}

func receivePacketFromOfflineEvent(event OfflineReceiveEvent) *pluginproto.RecvPacket {
	msg := event.Message
	return &pluginproto.RecvPacket{
		FromUid:     msg.FromUID,
		ToUid:       event.UID,
		ChannelId:   receiveChannelIDForRecipient(msg, event.UID),
		ChannelType: uint32(msg.ChannelType),
		Payload:     append([]byte(nil), msg.Payload...),
	}
}

func receiveChannelIDForRecipient(msg channel.Message, recipientUID string) string {
	sourceChannelID, _ := runtimechannelid.FromCommandChannel(msg.ChannelID)
	if msg.ChannelType != frame.ChannelTypePerson || recipientUID == "" {
		return sourceChannelID
	}
	left, right, err := runtimechannelid.DecodePersonChannel(sourceChannelID)
	if err != nil {
		return msg.FromUID
	}
	switch recipientUID {
	case left:
		return right
	case right:
		return left
	default:
		return msg.FromUID
	}
}

func (a *App) markReceiveDedupe(messageID uint64, uid string) bool {
	if a == nil || a.receiveDedupeTTL <= 0 || messageID == 0 || uid == "" {
		return true
	}
	now := a.now()
	key := fmt.Sprintf("%d:%s", messageID, uid)
	a.receiveDedupeMu.Lock()
	defer a.receiveDedupeMu.Unlock()
	for existingKey, expiresAt := range a.receiveDedupe {
		if !now.Before(expiresAt) {
			delete(a.receiveDedupe, existingKey)
		}
	}
	if expiresAt, ok := a.receiveDedupe[key]; ok && now.Before(expiresAt) {
		return false
	}
	a.receiveDedupe[key] = now.Add(a.receiveDedupeTTL)
	return true
}

func (a *App) forgetReceiveDedupe(messageID uint64, uid string) {
	if a == nil || a.receiveDedupeTTL <= 0 || messageID == 0 || uid == "" {
		return
	}
	key := fmt.Sprintf("%d:%s", messageID, uid)
	a.receiveDedupeMu.Lock()
	defer a.receiveDedupeMu.Unlock()
	delete(a.receiveDedupe, key)
}

func (a *App) logReceiveFailure(event OfflineReceiveEvent, plugin ObservedPlugin, err error) {
	if a == nil || a.logger == nil || err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	a.logger.Warn("plugin Receive hook failed",
		wklog.Event("plugin.receive.failed"),
		wklog.String("pluginNo", plugin.No),
		wklog.String("uid", event.UID),
		wklog.String("channelID", event.Message.ChannelID),
		wklog.Int("channelType", int(event.Message.ChannelType)),
		wklog.Uint64("messageID", event.Message.MessageID),
		wklog.Uint64("messageSeq", event.Message.MessageSeq),
		wklog.Error(err),
	)
}
