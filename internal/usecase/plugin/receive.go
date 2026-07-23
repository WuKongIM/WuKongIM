package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	pluginevents "github.com/WuKongIM/WuKongIM/internal/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ReceiveOffline invokes the highest-priority bound local Receive plugin for one offline recipient.
func (a *App) ReceiveOffline(ctx context.Context, event pluginevents.ReceiveOffline) error {
	if a == nil || !a.offlineReceiveEligible(event) {
		return nil
	}
	candidates, err := a.receivePluginCandidates(ctx)
	if err != nil {
		return err
	}
	plugin, ok, err := a.boundReceivePluginForUIDFromCandidates(ctx, event.UID, candidates)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return a.invokeReceiveOffline(ctx, event, plugin)
}

// ReceiveOfflineBatch invokes bound local Receive plugins for one committed message batch.
func (a *App) ReceiveOfflineBatch(ctx context.Context, batch pluginevents.ReceiveOfflineBatch) error {
	return a.receiveOfflineBatch(ctx, batch, 0)
}

// ReceiveOfflineBatchWithTimeout invokes a batch with an independent timeout for each recipient.
func (a *App) ReceiveOfflineBatchWithTimeout(
	ctx context.Context,
	batch pluginevents.ReceiveOfflineBatch,
	recipientTimeout time.Duration,
) error {
	return a.receiveOfflineBatch(ctx, batch, recipientTimeout)
}

func (a *App) receiveOfflineBatch(
	ctx context.Context,
	batch pluginevents.ReceiveOfflineBatch,
	recipientTimeout time.Duration,
) error {
	if a == nil || !a.offlineReceiveBatchEligible(batch) || len(batch.UIDs) == 0 {
		return nil
	}
	setupCtx, cancelSetup := receiveContextWithTimeout(ctx, recipientTimeout)
	candidates, err := a.receivePluginCandidates(setupCtx)
	cancelSetup()
	if err != nil || len(candidates) == 0 {
		return err
	}
	if a.receiveBindings == nil {
		return ErrReceiveBindingReaderRequired
	}

	seen := make(map[string]struct{}, len(batch.UIDs))
	var joined error
	for _, uid := range batch.UIDs {
		if uid == "" || uid == batch.FromUID {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}

		recipientCtx, cancelRecipient := receiveContextWithTimeout(ctx, recipientTimeout)
		plugin, ok, err := a.boundReceivePluginForUIDFromCandidates(recipientCtx, uid, candidates)
		if err != nil {
			cancelRecipient()
			joined = errors.Join(joined, err)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if !ok {
			cancelRecipient()
			continue
		}
		event := batch.ForUID(uid)
		err = a.invokeReceiveOffline(recipientCtx, event, plugin)
		cancelRecipient()
		if err != nil {
			joined = errors.Join(joined, err)
			if ctx.Err() != nil {
				break
			}
		}
	}
	return joined
}

func receiveContextWithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (a *App) invokeReceiveOffline(ctx context.Context, event pluginevents.ReceiveOffline, plugin ObservedPlugin) error {
	if !a.markReceiveDedupe(event.MessageID, event.UID) {
		return nil
	}
	packet := receivePacketFromOfflineEvent(event)
	startedAt := time.Now()
	err := a.InvokeReceive(ctx, plugin, packet)
	result := "ok"
	if err != nil {
		result = "error"
		a.forgetReceiveDedupe(event.MessageID, event.UID)
		a.logReceiveFailure(event, plugin, err)
	}
	a.observeReceiveInvoke(result, time.Since(startedAt))
	return err
}

func (a *App) offlineReceiveBatchEligible(event pluginevents.ReceiveOfflineBatch) bool {
	if event.MessageID == 0 || event.MessageSeq == 0 {
		return false
	}
	if len(event.MessageScopedUIDs) > 0 || protocolmeta.ChannelType(event.ChannelType) == protocolmeta.ChannelTypeTemp {
		return false
	}
	if event.NoPersist || event.SyncOnce {
		return false
	}
	return event.FromUID != "" && !a.isSystemSender(event.FromUID)
}

func (a *App) offlineReceiveEligible(event pluginevents.ReceiveOffline) bool {
	if event.UID == "" || event.MessageID == 0 || event.MessageSeq == 0 {
		return false
	}
	if len(event.MessageScopedUIDs) > 0 || protocolmeta.ChannelType(event.ChannelType) == protocolmeta.ChannelTypeTemp {
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
	candidates, err := a.receivePluginCandidates(ctx)
	if err != nil {
		return ObservedPlugin{}, false, err
	}
	return a.boundReceivePluginForUIDFromCandidates(ctx, uid, candidates)
}

func (a *App) receivePluginCandidates(ctx context.Context) ([]ObservedPlugin, error) {
	if a == nil || a.runtime == nil {
		return nil, ErrRuntimeRequired
	}
	plugins := a.runtime.List()
	receivePlugins := plugins[:0]
	for _, plugin := range plugins {
		if hasMethod(plugin, MethodReceive) {
			receivePlugins = append(receivePlugins, plugin)
		}
	}
	plugins, err := a.applyDesiredToPlugins(ctx, receivePlugins)
	if err != nil {
		return nil, err
	}
	return runningPluginsByMethod(plugins, MethodReceive), nil
}

func (a *App) boundReceivePluginForUIDFromCandidates(
	ctx context.Context,
	uid string,
	candidates []ObservedPlugin,
) (ObservedPlugin, bool, error) {
	if a == nil || a.receiveBindings == nil {
		return ObservedPlugin{}, false, ErrReceiveBindingReaderRequired
	}
	if uid == "" || len(candidates) == 0 {
		return ObservedPlugin{}, false, nil
	}
	bindings, err := a.receiveBindings.ListPluginBindingsByUID(ctx, uid)
	if err != nil {
		return ObservedPlugin{}, false, err
	}
	for _, candidate := range candidates {
		for _, binding := range bindings {
			if binding.PluginNo == candidate.No {
				return candidate, true, nil
			}
		}
	}
	return ObservedPlugin{}, false, nil
}

func receivePacketFromOfflineEvent(event pluginevents.ReceiveOffline) *pluginproto.RecvPacket {
	return &pluginproto.RecvPacket{
		FromUid:     event.FromUID,
		ToUid:       event.UID,
		ChannelId:   receiveChannelIDForRecipient(event),
		ChannelType: uint32(event.ChannelType),
		// Marshal synchronously owns the wire copy; the batch payload is immutable here.
		Payload: event.Payload,
	}
}

func receiveChannelIDForRecipient(event pluginevents.ReceiveOffline) string {
	sourceChannelID, _ := runtimechannelid.FromCommandChannel(event.ChannelID)
	if protocolmeta.ChannelType(event.ChannelType) != protocolmeta.ChannelTypePerson || event.UID == "" {
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
