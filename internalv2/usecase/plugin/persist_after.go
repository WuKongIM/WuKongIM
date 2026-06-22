package plugin

import (
	"context"
	"errors"
	"sort"

	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// PersistAfterCommitted invokes local PersistAfter plugins for one committed message.
func (a *App) PersistAfterCommitted(ctx context.Context, event pluginevents.PersistAfterCommitted) error {
	plugins, err := a.PersistAfterPluginCandidates(ctx)
	if err != nil {
		return err
	}
	if len(plugins) == 0 {
		return nil
	}
	batch := messageBatchFromPersistAfter(event)
	var joined error
	for _, plugin := range plugins {
		if err := a.InvokePersistAfter(ctx, plugin, batch); err != nil {
			a.logPersistAfterFailure(event, plugin, err)
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

// PersistAfterPluginCandidates returns running local PersistAfter plugins in hook order.
func (a *App) PersistAfterPluginCandidates(_ context.Context) ([]ObservedPlugin, error) {
	return runningPluginsByMethod(a.runtime.List(), MethodPersistAfter), nil
}

func runningPluginsByMethod(plugins []ObservedPlugin, method Method) []ObservedPlugin {
	out := make([]ObservedPlugin, 0, len(plugins))
	for _, plugin := range plugins {
		if plugin.Enabled && plugin.Status == StatusRunning && hasMethod(plugin, method) {
			out = append(out, cloneObservedPlugin(plugin))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].No < out[j].No
	})
	return out
}

func cloneObservedPlugin(plugin ObservedPlugin) ObservedPlugin {
	plugin.Methods = append([]Method(nil), plugin.Methods...)
	return plugin
}

func hasMethod(plugin ObservedPlugin, method Method) bool {
	for _, candidate := range plugin.Methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func (a *App) logPersistAfterFailure(event pluginevents.PersistAfterCommitted, plugin ObservedPlugin, err error) {
	if a == nil || a.logger == nil || err == nil {
		return
	}
	a.logger.Warn("plugin PersistAfter hook failed",
		wklog.Event("plugin.persist_after.failed"),
		wklog.String("pluginNo", plugin.No),
		wklog.String("channelID", event.ChannelID),
		wklog.Int("channelType", int(event.ChannelType)),
		wklog.Uint64("messageID", event.MessageID),
		wklog.Uint64("messageSeq", event.MessageSeq),
		wklog.Error(err),
	)
}
