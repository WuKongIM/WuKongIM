package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

// UpdateLocalConfig persists desired config and notifies a running ConfigUpdate hook.
func (a *App) UpdateLocalConfig(ctx context.Context, no string, config json.RawMessage) (LocalPluginDetail, error) {
	if no == "" {
		return LocalPluginDetail{}, ErrPluginNoRequired
	}
	if err := validatePluginNo(no); err != nil {
		return LocalPluginDetail{}, err
	}
	observed, observedOK := a.runtime.Get(no)
	if !observedOK {
		return LocalPluginDetail{}, fmt.Errorf("%w: %s", ErrPluginNotFound, no)
	}
	template, err := decodeConfigTemplate(observed.ConfigTemplateRaw)
	if err != nil {
		return LocalPluginDetail{}, fmt.Errorf("decode config template %q: %w", no, err)
	}

	existing, err := a.store.Get(ctx, no)
	if err != nil && !errors.Is(err, ErrDesiredPluginNotFound) {
		return LocalPluginDetail{}, fmt.Errorf("load desired plugin %q: %w", no, err)
	}
	hasExisting := err == nil
	mergedConfig, err := mergeHiddenSecrets(config, existing.Config, template)
	if err != nil {
		return LocalPluginDetail{}, err
	}

	now := a.now()
	state := DesiredPlugin{No: no, Config: mergedConfig, Enabled: observed.Enabled, CreatedAt: now, UpdatedAt: now}
	if hasExisting {
		state.Enabled = existing.Enabled
		state.CreatedAt = existing.CreatedAt
		if state.CreatedAt.IsZero() {
			state.CreatedAt = now
		}
	}
	effectiveObserved := observed
	effectiveObserved.Enabled = state.Enabled
	if !state.Enabled {
		effectiveObserved.Status = StatusDisabled
	}
	if err := a.store.Save(ctx, state); err != nil {
		return LocalPluginDetail{}, fmt.Errorf("save desired plugin %q: %w", no, err)
	}

	if isRunnableForMethod(effectiveObserved, MethodConfigUpdate) {
		if a.invoker == nil {
			return LocalPluginDetail{}, ErrInvokerRequired
		}
		if _, err := a.invoker.RequestPlugin(ctx, no, PathConfigUpdate, mergedConfig); err != nil {
			return LocalPluginDetail{}, fmt.Errorf("notify plugin config update %q: %w", no, err)
		}
	}
	return a.localPluginFromObservedAndDesired(effectiveObserved, &state)
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
		return nil, fmt.Errorf("marshal plugin config: %w", err)
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
