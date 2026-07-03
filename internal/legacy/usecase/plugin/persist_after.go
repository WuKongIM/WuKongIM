package plugin

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// PersistAfterCommitted invokes local PersistAfter plugins for one committed message.
func (a *App) PersistAfterCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	plugins, err := a.PersistAfterPluginCandidates(ctx)
	if err != nil {
		return err
	}
	if len(plugins) == 0 {
		return nil
	}
	batch := persistAfterBatchFromCommitted(event)
	var joined error
	for _, plugin := range plugins {
		if err := a.InvokePersistAfter(ctx, plugin, batch); err != nil {
			a.logPersistAfterFailure(event, plugin, err)
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func persistAfterBatchFromCommitted(event messageevents.MessageCommitted) *pluginproto.MessageBatch {
	return &pluginproto.MessageBatch{Messages: []*pluginproto.Message{pluginMessageFromChannelMessage(event.Message)}}
}

func (a *App) logPersistAfterFailure(event messageevents.MessageCommitted, plugin ObservedPlugin, err error) {
	if a == nil || a.logger == nil || err == nil {
		return
	}
	a.logger.Warn("plugin PersistAfter hook failed",
		wklog.Event("plugin.persist_after.failed"),
		wklog.String("pluginNo", plugin.No),
		wklog.String("channelID", event.Message.ChannelID),
		wklog.Int("channelType", int(event.Message.ChannelType)),
		wklog.Uint64("messageID", event.Message.MessageID),
		wklog.Uint64("messageSeq", event.Message.MessageSeq),
		wklog.Error(err),
	)
}
