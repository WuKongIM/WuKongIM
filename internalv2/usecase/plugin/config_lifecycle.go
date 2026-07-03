package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

const PathConfigUpdate = "/plugin/config_update"

type runtimeSandboxResolver interface {
	SandboxDir(string) (string, error)
}

type runtimeRestarter interface {
	Restart(context.Context, string) error
}

type runtimeUninstaller interface {
	Uninstall(context.Context, string) error
}

// ListLocalPlugins returns node-local plugins with desired config metadata applied.
func (a *App) ListLocalPlugins(ctx context.Context) (LocalPluginList, error) {
	if err := ctxErr(ctx); err != nil {
		return LocalPluginList{}, err
	}
	if a == nil || a.runtime == nil {
		return LocalPluginList{}, ErrRuntimeRequired
	}
	observed := a.runtime.List()
	sort.Slice(observed, func(i, j int) bool { return observed[i].No < observed[j].No })
	out := make([]LocalPlugin, 0, len(observed))
	for _, item := range observed {
		plugin, err := a.localPluginFromObserved(ctx, item)
		if err != nil {
			return LocalPluginList{}, err
		}
		out = append(out, plugin)
	}
	return LocalPluginList{NodeID: a.nodeID, Plugins: out}, nil
}

// GetLocalPlugin returns one node-local plugin with desired config metadata applied.
func (a *App) GetLocalPlugin(ctx context.Context, pluginNo string) (LocalPluginDetail, error) {
	if err := ctxErr(ctx); err != nil {
		return LocalPluginDetail{}, err
	}
	no := strings.TrimSpace(pluginNo)
	if no == "" {
		return LocalPluginDetail{}, ErrPluginNoRequired
	}
	if err := validatePluginNo(no); err != nil {
		return LocalPluginDetail{}, err
	}
	observed, ok := a.observedPlugin(no)
	if !ok {
		return LocalPluginDetail{}, ErrPluginNotFound
	}
	return a.localPluginFromObserved(ctx, observed)
}

// UpdateLocalConfig persists desired config and notifies a running ConfigUpdate hook.
func (a *App) UpdateLocalConfig(ctx context.Context, pluginNo string, config json.RawMessage) (LocalPluginDetail, error) {
	if err := ctxErr(ctx); err != nil {
		return LocalPluginDetail{}, err
	}
	no := strings.TrimSpace(pluginNo)
	if no == "" {
		return LocalPluginDetail{}, ErrPluginNoRequired
	}
	if err := validatePluginNo(no); err != nil {
		return LocalPluginDetail{}, err
	}
	if a == nil || a.desired == nil {
		return LocalPluginDetail{}, ErrDesiredStoreRequired
	}
	observed, ok := a.observedPlugin(no)
	if !ok {
		return LocalPluginDetail{}, ErrPluginNotFound
	}
	template, err := decodeConfigTemplate(observed.ConfigTemplateRaw)
	if err != nil {
		return LocalPluginDetail{}, fmt.Errorf("decode config template %q: %w", no, err)
	}
	existing, found, err := a.loadDesiredState(ctx, no)
	if err != nil {
		return LocalPluginDetail{}, err
	}
	merged, err := mergeHiddenSecrets(config, existing.Config, template)
	if err != nil {
		return LocalPluginDetail{}, err
	}
	now := a.now()
	state := DesiredPlugin{No: no, Config: merged, Enabled: observed.Enabled, CreatedAt: now, UpdatedAt: now}
	if found {
		state.Enabled = existing.Enabled
		state.CreatedAt = existing.CreatedAt
		if state.CreatedAt.IsZero() {
			state.CreatedAt = now
		}
	}
	if err := a.desired.Save(ctx, state); err != nil {
		return LocalPluginDetail{}, err
	}
	a.cacheDesired(no, state, true)
	effective := applyDesiredToObserved(observed, state, true)
	if isRunnableForMethod(effective, MethodConfigUpdate) {
		if a.invoker == nil {
			return LocalPluginDetail{}, ErrInvokerRequired
		}
		if _, err := a.invoker.RequestPlugin(ctx, no, PathConfigUpdate, merged); err != nil {
			return LocalPluginDetail{}, err
		}
	}
	return a.localPluginFromObservedAndDesired(effective, &state)
}

// RestartLocalPlugin restarts one node-local plugin process and returns latest metadata.
func (a *App) RestartLocalPlugin(ctx context.Context, pluginNo string) (LocalPluginDetail, error) {
	no := strings.TrimSpace(pluginNo)
	if no == "" {
		return LocalPluginDetail{}, ErrPluginNoRequired
	}
	if err := validatePluginNo(no); err != nil {
		return LocalPluginDetail{}, err
	}
	restarter, ok := a.runtime.(runtimeRestarter)
	if a == nil || !ok {
		return LocalPluginDetail{}, ErrRuntimeRequired
	}
	if err := restarter.Restart(ctx, no); err != nil {
		return LocalPluginDetail{}, err
	}
	return a.GetLocalPlugin(ctx, no)
}

// UninstallLocalPlugin disables desired state and removes one node-local plugin process.
func (a *App) UninstallLocalPlugin(ctx context.Context, pluginNo string) error {
	no := strings.TrimSpace(pluginNo)
	if no == "" {
		return ErrPluginNoRequired
	}
	if err := validatePluginNo(no); err != nil {
		return err
	}
	if a == nil || a.desired == nil {
		return ErrDesiredStoreRequired
	}
	uninstaller, ok := a.runtime.(runtimeUninstaller)
	if !ok {
		return ErrRuntimeRequired
	}
	existing, found, err := a.loadDesiredState(ctx, no)
	if err != nil {
		return err
	}
	now := a.now()
	state := DesiredPlugin{No: no, Enabled: false, CreatedAt: now, UpdatedAt: now}
	if found {
		state = existing
		state.Enabled = false
		state.UpdatedAt = now
		if state.CreatedAt.IsZero() {
			state.CreatedAt = now
		}
	}
	if err := a.desired.Save(ctx, state); err != nil {
		return err
	}
	a.cacheDesired(no, state, true)
	return uninstaller.Uninstall(ctx, no)
}

func (a *App) localPluginFromObserved(ctx context.Context, observed ObservedPlugin) (LocalPluginDetail, error) {
	desired, found, err := a.desiredState(ctx, observed.No)
	if err != nil {
		return LocalPluginDetail{}, err
	}
	if found {
		return a.localPluginFromObservedAndDesired(applyDesiredToObserved(observed, desired, true), &desired)
	}
	return a.localPluginFromObservedAndDesired(observed, nil)
}

func (a *App) localPluginFromObservedAndDesired(observed ObservedPlugin, desired *DesiredPlugin) (LocalPluginDetail, error) {
	template, err := decodeConfigTemplate(observed.ConfigTemplateRaw)
	if err != nil {
		return LocalPluginDetail{}, err
	}
	var config map[string]any
	var createdAt, updatedAt *time.Time
	if desired != nil {
		config, err = redactedConfig(desired.Config, template)
		if err != nil {
			return LocalPluginDetail{}, err
		}
		if !desired.CreatedAt.IsZero() {
			t := desired.CreatedAt
			createdAt = &t
		}
		if !desired.UpdatedAt.IsZero() {
			t := desired.UpdatedAt
			updatedAt = &t
		}
	}
	return LocalPlugin{
		NodeID:           a.nodeID,
		No:               observed.No,
		Name:             observed.Name,
		Version:          observed.Version,
		ConfigTemplate:   template,
		Config:           config,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		Methods:          append([]Method(nil), observed.Methods...),
		Priority:         observed.Priority,
		PersistAfterSync: observed.PersistAfterSync,
		ReplySync:        observed.ReplySync,
		Status:           observed.Status,
		Enabled:          observed.Enabled,
		PID:              observed.PID,
		LastSeenAt:       observed.LastSeenAt,
		LastError:        observed.LastError,
	}, nil
}

func (a *App) desiredState(ctx context.Context, no string) (DesiredPlugin, bool, error) {
	if a == nil || a.desired == nil || no == "" {
		return DesiredPlugin{}, false, nil
	}
	a.desiredMu.RLock()
	if entry, ok := a.desiredCache[no]; ok {
		a.desiredMu.RUnlock()
		return cloneDesiredPlugin(entry.state), entry.found, nil
	}
	a.desiredMu.RUnlock()
	state, found, err := a.loadDesiredState(ctx, no)
	if err != nil {
		return DesiredPlugin{}, false, err
	}
	a.cacheDesired(no, state, found)
	return state, found, nil
}

func (a *App) loadDesiredState(ctx context.Context, no string) (DesiredPlugin, bool, error) {
	if a == nil || a.desired == nil || no == "" {
		return DesiredPlugin{}, false, nil
	}
	state, err := a.desired.Get(ctx, no)
	if errors.Is(err, ErrDesiredPluginNotFound) {
		return DesiredPlugin{}, false, nil
	}
	if err != nil {
		return DesiredPlugin{}, false, err
	}
	return cloneDesiredPlugin(state), true, nil
}

func (a *App) cacheDesired(no string, state DesiredPlugin, found bool) {
	if a == nil {
		return
	}
	a.desiredMu.Lock()
	defer a.desiredMu.Unlock()
	if a.desiredCache == nil {
		a.desiredCache = make(map[string]desiredCacheEntry)
	}
	a.desiredCache[no] = desiredCacheEntry{state: cloneDesiredPlugin(state), found: found}
}

func (a *App) observedPlugin(no string) (ObservedPlugin, bool) {
	if a == nil || a.runtime == nil {
		return ObservedPlugin{}, false
	}
	for _, plugin := range a.runtime.List() {
		if plugin.No == no {
			return cloneObservedPlugin(plugin), true
		}
	}
	return ObservedPlugin{}, false
}

func (a *App) sandboxDir(no string) string {
	resolver, ok := a.runtime.(runtimeSandboxResolver)
	if a == nil || !ok {
		return ""
	}
	dir, err := resolver.SandboxDir(no)
	if err != nil {
		return ""
	}
	return dir
}

func applyDesiredToObserved(observed ObservedPlugin, desired DesiredPlugin, found bool) ObservedPlugin {
	if !found {
		return observed
	}
	observed.Enabled = desired.Enabled
	if !desired.Enabled {
		observed.Status = StatusDisabled
		observed.PID = 0
	}
	return observed
}

func isRunnableForMethod(plugin ObservedPlugin, method Method) bool {
	return plugin.Enabled && plugin.Status == StatusRunning && hasMethod(plugin, method)
}

func (a *App) applyDesiredToPlugins(ctx context.Context, plugins []ObservedPlugin) ([]ObservedPlugin, error) {
	out := make([]ObservedPlugin, 0, len(plugins))
	for _, plugin := range plugins {
		desired, found, err := a.desiredState(ctx, plugin.No)
		if err != nil {
			return nil, err
		}
		out = append(out, applyDesiredToObserved(plugin, desired, found))
	}
	return out, nil
}

func decodeConfigTemplate(raw []byte) (*pluginproto.ConfigTemplate, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var template pluginproto.ConfigTemplate
	if err := template.Unmarshal(raw); err != nil {
		return nil, err
	}
	return &template, nil
}

func marshalConfigTemplate(template *pluginproto.ConfigTemplate) ([]byte, error) {
	if template == nil {
		return nil, nil
	}
	return template.Marshal()
}

func redactedConfig(raw json.RawMessage, template *pluginproto.ConfigTemplate) (map[string]any, error) {
	config, err := decodeConfigObject(raw)
	if err != nil || config == nil {
		return config, err
	}
	for _, field := range secretFields(template) {
		if _, ok := config[field]; ok {
			config[field] = SecretHidden
		}
	}
	return config, nil
}

func mergeHiddenSecrets(nextRaw, existingRaw json.RawMessage, template *pluginproto.ConfigTemplate) (json.RawMessage, error) {
	next, err := decodeConfigObject(nextRaw)
	if err != nil {
		return nil, fmt.Errorf("decode plugin config: %w", err)
	}
	if next == nil {
		next = map[string]any{}
	}
	existing, err := decodeConfigObject(existingRaw)
	if err != nil {
		return nil, fmt.Errorf("decode existing plugin config: %w", err)
	}
	for _, field := range secretFields(template) {
		if value, ok := next[field].(string); ok && value == SecretHidden && existing != nil {
			if old, ok := existing[field]; ok {
				next[field] = old
			}
		}
	}
	data, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func decodeConfigObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if config == nil {
		return map[string]any{}, nil
	}
	return config, nil
}

func secretFields(template *pluginproto.ConfigTemplate) []string {
	if template == nil {
		return nil
	}
	fields := make([]string, 0, len(template.GetFields()))
	for _, field := range template.GetFields() {
		if field.GetName() != "" && field.GetType() == pluginproto.FieldTypeSecret.String() {
			fields = append(fields, field.GetName())
		}
	}
	return fields
}

func validatePluginNo(no string) error {
	if no == "." || no == ".." || !pluginNoPattern.MatchString(no) {
		return fmt.Errorf("%w: %q", ErrInvalidPluginNo, no)
	}
	return nil
}

func cloneDesiredPlugin(state DesiredPlugin) DesiredPlugin {
	state.Config = append(json.RawMessage(nil), state.Config...)
	return state
}
