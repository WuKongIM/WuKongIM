package plugin

import (
	"context"
	"strings"
)

// ListPlugins returns node-local observed plugin runtime rows.
func (a *App) ListPlugins(ctx context.Context) ([]ObservedPlugin, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if a == nil || a.runtime == nil {
		return nil, ErrRuntimeRequired
	}
	plugins := a.runtime.List()
	out := make([]ObservedPlugin, 0, len(plugins))
	for _, plugin := range plugins {
		out = append(out, cloneObservedPlugin(plugin))
	}
	return out, nil
}

// GetPlugin returns one node-local observed plugin runtime row.
func (a *App) GetPlugin(ctx context.Context, pluginNo string) (ObservedPlugin, error) {
	if err := ctxErr(ctx); err != nil {
		return ObservedPlugin{}, err
	}
	no := strings.TrimSpace(pluginNo)
	if no == "" {
		return ObservedPlugin{}, ErrPluginNoRequired
	}
	plugins, err := a.ListPlugins(ctx)
	if err != nil {
		return ObservedPlugin{}, err
	}
	for _, plugin := range plugins {
		if plugin.No == no {
			return plugin, nil
		}
	}
	return ObservedPlugin{}, ErrPluginNotFound
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
