package plugin

import (
	"context"
	"errors"
	"fmt"
)

const (
	// BindingWarningPluginMissing reports that a durable binding references no observed local plugin.
	BindingWarningPluginMissing = "plugin_missing"
)

var (
	// ErrBindingStoreRequired reports that binding operations need the cluster-authoritative store port.
	ErrBindingStoreRequired = errors.New("plugin binding store required")
	// ErrBindingUIDRequired reports that a binding operation did not include a UID.
	ErrBindingUIDRequired = errors.New("plugin binding uid required")
)

// PluginBinding records a cluster-authoritative UID to plugin association.
type PluginBinding struct {
	// PluginNo is the plugin selected for a UID.
	PluginNo string
	// UID is the user id whose offline Receive hook targets the plugin.
	UID string
}

// BindingWarning describes manager-visible non-fatal binding metadata.
type BindingWarning struct {
	// Code is a stable machine-readable warning code.
	Code string
	// Message is a human-readable warning summary.
	Message string
	// UID identifies the binding user when relevant.
	UID string
	// PluginNo identifies the binding plugin when relevant.
	PluginNo string
}

// BindingDetail is a manager-facing binding row with enrichment metadata.
type BindingDetail struct {
	// Binding is the durable UID to plugin association.
	Binding PluginBinding
	// Plugin is the node-local plugin detail when the plugin is observed locally.
	Plugin *LocalPlugin
	// Warnings contains row-scoped non-fatal metadata.
	Warnings []BindingWarning
}

// BindingList is the manager-facing UID binding list.
type BindingList struct {
	// Bindings contains enriched binding rows.
	Bindings []BindingDetail
	// Warnings contains list-level non-fatal metadata.
	Warnings []BindingWarning
}

// BindingPage is the manager-facing plugin-centric binding page.
type BindingPage struct {
	// Bindings contains durable binding rows in deterministic store order.
	Bindings []PluginBinding
	// Details contains enriched binding rows for Bindings.
	Details []BindingDetail
	// Cursor is an opaque cursor for the next page.
	Cursor string
	// HasMore reports whether another page is available.
	HasMore bool
	// Warnings contains page-level non-fatal metadata.
	Warnings []BindingWarning
}

// BindingMutationResult reports a completed bind mutation and non-fatal metadata.
type BindingMutationResult struct {
	// Binding is the mutated UID to plugin association.
	Binding PluginBinding
	// Warnings contains non-fatal metadata for the mutated binding.
	Warnings []BindingWarning
}

// BindPluginUser creates or updates one UID to plugin binding and invalidates its cache entry.
func (a *App) BindPluginUser(ctx context.Context, uid, pluginNo string) (BindingMutationResult, error) {
	if err := validateBindingIdentity(uid, pluginNo); err != nil {
		return BindingMutationResult{}, err
	}
	if a.bindingStore == nil {
		return BindingMutationResult{}, ErrBindingStoreRequired
	}
	binding := PluginBinding{UID: uid, PluginNo: pluginNo}
	warnings, err := a.bindingWarnings(ctx, binding)
	if err != nil {
		return BindingMutationResult{}, err
	}
	if err := a.bindingStore.BindPluginUser(ctx, uid, pluginNo); err != nil {
		return BindingMutationResult{}, err
	}
	a.invalidateBindingCache(uid)
	return BindingMutationResult{Binding: binding, Warnings: warnings}, nil
}

// UnbindPluginUser removes one UID to plugin binding and invalidates its cache entry.
func (a *App) UnbindPluginUser(ctx context.Context, uid, pluginNo string) error {
	if err := validateBindingIdentity(uid, pluginNo); err != nil {
		return err
	}
	if a.bindingStore == nil {
		return ErrBindingStoreRequired
	}
	if err := a.bindingStore.UnbindPluginUser(ctx, uid, pluginNo); err != nil {
		return err
	}
	a.invalidateBindingCache(uid)
	return nil
}

// ListBindingsByUID lists enriched bindings for one UID and records stale local-plugin warnings.
func (a *App) ListBindingsByUID(ctx context.Context, uid string) (BindingList, error) {
	if uid == "" {
		return BindingList{}, ErrBindingUIDRequired
	}
	bindings, _, _, err := a.bindingsForUID(ctx, uid)
	if err != nil {
		return BindingList{}, err
	}
	details, warnings, err := a.bindingDetails(ctx, bindings)
	if err != nil {
		return BindingList{}, err
	}
	return BindingList{Bindings: details, Warnings: warnings}, nil
}

// ListBindingsByPluginNo lists plugin-centric bindings using the store's opaque cursor.
func (a *App) ListBindingsByPluginNo(ctx context.Context, pluginNo, cursor string, limit int) (BindingPage, error) {
	if pluginNo == "" {
		return BindingPage{}, ErrPluginNoRequired
	}
	if err := validatePluginNo(pluginNo); err != nil {
		return BindingPage{}, err
	}
	if a.bindingStore == nil {
		return BindingPage{}, ErrBindingStoreRequired
	}
	page, err := a.bindingStore.ListPluginBindingsByPluginNo(ctx, pluginNo, cursor, limit)
	if err != nil {
		return BindingPage{}, err
	}
	page.Bindings = clonePluginBindings(page.Bindings)
	details, warnings, err := a.bindingDetails(ctx, page.Bindings)
	if err != nil {
		return BindingPage{}, err
	}
	page.Details = details
	page.Warnings = append(cloneBindingWarnings(page.Warnings), warnings...)
	return page, nil
}

// ExistPluginBindingByUID reports whether a UID has any durable plugin binding.
func (a *App) ExistPluginBindingByUID(ctx context.Context, uid string) (bool, error) {
	if uid == "" {
		return false, ErrBindingUIDRequired
	}
	if a.bindingStore == nil {
		return false, ErrBindingStoreRequired
	}
	return a.bindingStore.ExistPluginBindingByUID(ctx, uid)
}

// BoundReceivePluginForUID returns the highest-priority running Receive plugin bound to a UID.
func (a *App) BoundReceivePluginForUID(ctx context.Context, uid string) (ObservedPlugin, bool, error) {
	if uid == "" {
		return ObservedPlugin{}, false, ErrBindingUIDRequired
	}
	_, selected, selectedOK, err := a.bindingsForUID(ctx, uid)
	if err != nil {
		return ObservedPlugin{}, false, err
	}
	return selected, selectedOK, nil
}

func (a *App) bindingsForUID(ctx context.Context, uid string) ([]PluginBinding, ObservedPlugin, bool, error) {
	if a.bindingStore == nil {
		return nil, ObservedPlugin{}, false, ErrBindingStoreRequired
	}
	if bindings, selected, selectedOK, hit := a.bindingCache.Get(uid); hit {
		return bindings, selected, selectedOK, nil
	}
	bindings, err := a.bindingStore.ListPluginBindingsByUID(ctx, uid)
	if err != nil {
		return nil, ObservedPlugin{}, false, err
	}
	bindings = clonePluginBindings(bindings)
	selected, selectedOK, err := a.selectBoundReceivePlugin(ctx, bindings)
	if err != nil {
		return nil, ObservedPlugin{}, false, err
	}
	a.bindingCache.Set(uid, bindings, selected, selectedOK)
	return bindings, selected, selectedOK, nil
}

func (a *App) selectBoundReceivePlugin(ctx context.Context, bindings []PluginBinding) (ObservedPlugin, bool, error) {
	boundPluginNos := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		boundPluginNos = append(boundPluginNos, binding.PluginNo)
	}
	return a.BoundReceivePlugin(ctx, boundPluginNos)
}

func (a *App) bindingDetails(ctx context.Context, bindings []PluginBinding) ([]BindingDetail, []BindingWarning, error) {
	details := make([]BindingDetail, 0, len(bindings))
	warnings := make([]BindingWarning, 0)
	for _, binding := range bindings {
		rowWarnings, err := a.bindingWarnings(ctx, binding)
		if err != nil {
			return nil, nil, err
		}
		detail := BindingDetail{Binding: binding, Warnings: cloneBindingWarnings(rowWarnings)}
		if observed, ok := a.runtime.Get(binding.PluginNo); ok {
			local, err := a.localPluginFromObserved(ctx, observed)
			if err != nil {
				return nil, nil, err
			}
			detail.Plugin = &local
		}
		details = append(details, detail)
		warnings = append(warnings, rowWarnings...)
	}
	return details, warnings, nil
}

func (a *App) bindingWarnings(_ context.Context, binding PluginBinding) ([]BindingWarning, error) {
	if _, ok := a.runtime.Get(binding.PluginNo); ok {
		return nil, nil
	}
	return []BindingWarning{missingPluginWarning(binding)}, nil
}

func missingPluginWarning(binding PluginBinding) BindingWarning {
	return BindingWarning{
		Code:     BindingWarningPluginMissing,
		Message:  fmt.Sprintf("plugin %q is not observed on this node", binding.PluginNo),
		UID:      binding.UID,
		PluginNo: binding.PluginNo,
	}
}

func validateBindingIdentity(uid, pluginNo string) error {
	if uid == "" {
		return ErrBindingUIDRequired
	}
	if pluginNo == "" {
		return ErrPluginNoRequired
	}
	return validatePluginNo(pluginNo)
}

func (a *App) invalidateBindingCache(uid string) {
	if a.bindingCache != nil {
		a.bindingCache.Invalidate(uid)
	}
}

func clonePluginBindings(bindings []PluginBinding) []PluginBinding {
	return append([]PluginBinding(nil), bindings...)
}

func cloneBindingWarnings(warnings []BindingWarning) []BindingWarning {
	return append([]BindingWarning(nil), warnings...)
}
