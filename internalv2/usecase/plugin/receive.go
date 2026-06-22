package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ReceiveOffline invokes the highest-priority bound local Receive plugin for one offline recipient.
func (a *App) ReceiveOffline(ctx context.Context, event pluginevents.ReceiveOffline) error {
	if a == nil || !a.offlineReceiveEligible(event) {
		return nil
	}
	plugin, ok, err := a.boundReceivePluginForUID(ctx, event.UID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if !a.markReceiveDedupe(event.MessageID, event.UID) {
		return nil
	}
	packet := receivePacketFromOfflineEvent(event)
	startedAt := time.Now()
	err = a.InvokeReceive(ctx, plugin, packet)
	result := "ok"
	if err != nil {
		result = "error"
		a.forgetReceiveDedupe(event.MessageID, event.UID)
		a.logReceiveFailure(event, plugin, err)
	}
	a.observeReceiveInvoke(result, time.Since(startedAt))
	return err
}

func (a *App) offlineReceiveEligible(event pluginevents.ReceiveOffline) bool {
	if event.UID == "" || event.MessageID == 0 || event.MessageSeq == 0 {
		return false
	}
	if len(event.MessageScopedUIDs) > 0 || event.ChannelType == frame.ChannelTypeTemp {
		return false
	}
	if event.NoPersist || event.SyncOnce {
		return false
	}
	if event.FromUID == "" || event.FromUID == event.UID || a.isSystemSender(event.FromUID) {
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

func (a *App) boundReceivePluginForUID(ctx context.Context, uid string) (ObservedPlugin, bool, error) {
	if a == nil || a.receiveBindings == nil {
		return ObservedPlugin{}, false, ErrReceiveBindingReaderRequired
	}
	bindings, err := a.receiveBindings.ListPluginBindingsByUID(ctx, uid)
	if err != nil {
		return ObservedPlugin{}, false, err
	}
	bound := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding.PluginNo != "" {
			bound[binding.PluginNo] = struct{}{}
		}
	}
	if len(bound) == 0 {
		return ObservedPlugin{}, false, nil
	}
	plugins := a.runtime.List()
	candidates := make([]ObservedPlugin, 0, len(bound))
	for _, plugin := range plugins {
		if _, ok := bound[plugin.No]; ok {
			candidates = append(candidates, plugin)
		}
	}
	ordered := runningPluginsByMethod(candidates, MethodReceive)
	if len(ordered) == 0 {
		return ObservedPlugin{}, false, nil
	}
	return ordered[0], true, nil
}

func receivePacketFromOfflineEvent(event pluginevents.ReceiveOffline) *pluginproto.RecvPacket {
	return &pluginproto.RecvPacket{
		FromUid:     event.FromUID,
		ToUid:       event.UID,
		ChannelId:   receiveChannelIDForRecipient(event),
		ChannelType: uint32(event.ChannelType),
		Payload:     append([]byte(nil), event.Payload...),
	}
}

func receiveChannelIDForRecipient(event pluginevents.ReceiveOffline) string {
	sourceChannelID, _ := runtimechannelid.FromCommandChannel(event.ChannelID)
	if event.ChannelType != frame.ChannelTypePerson || event.UID == "" {
		return sourceChannelID
	}
	left, right, err := runtimechannelid.DecodePersonChannel(sourceChannelID)
	if err != nil {
		return event.FromUID
	}
	switch event.UID {
	case left:
		return right
	case right:
		return left
	default:
		return event.FromUID
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
	if expiresAt, ok := a.receiveDedupe[key]; ok && now.Before(expiresAt) {
		return false
	}
	if !now.Before(a.receiveDedupeNextSweep) {
		for existingKey, expiresAt := range a.receiveDedupe {
			if !now.Before(expiresAt) {
				delete(a.receiveDedupe, existingKey)
			}
		}
		a.receiveDedupeNextSweep = now.Add(a.receiveDedupeSweepInterval())
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

func (a *App) now() time.Time {
	if a == nil || a.clock == nil {
		return time.Now()
	}
	return a.clock()
}

func (a *App) receiveDedupeSweepInterval() time.Duration {
	if a == nil || a.receiveDedupeTTL <= 0 {
		return time.Minute
	}
	if a.receiveDedupeTTL < time.Minute {
		return a.receiveDedupeTTL
	}
	return time.Minute
}

func (a *App) observeReceiveInvoke(result string, d time.Duration) {
	if a == nil || a.observer == nil {
		return
	}
	a.observer.ObserveReceiveInvoke(result, d)
}

func (a *App) logReceiveFailure(event pluginevents.ReceiveOffline, plugin ObservedPlugin, err error) {
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
		wklog.String("channelID", event.ChannelID),
		wklog.Int("channelType", int(event.ChannelType)),
		wklog.Uint64("messageID", event.MessageID),
		wklog.Uint64("messageSeq", event.MessageSeq),
		wklog.Error(err),
	)
}
