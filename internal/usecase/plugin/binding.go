package plugin

import (
	"context"
	"errors"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// BindingWarningPluginMissing reports that a durable binding references no observed local plugin.
	BindingWarningPluginMissing = "plugin_missing"
	// BindingWarningPluginUnavailable reports that a bound plugin is not currently runnable.
	BindingWarningPluginUnavailable = "plugin_unavailable"
	// BindingWarningPluginDisabled reports that desired state disables a bound plugin.
	BindingWarningPluginDisabled = "plugin_disabled"
	// BindingWarningReceiveUnsupported reports that a bound plugin does not advertise Receive.
	BindingWarningReceiveUnsupported = "receive_unsupported"
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

// NewSlotBindingStoreAdapter converts the slot proxy binding API into the plugin usecase port.
func NewSlotBindingStoreAdapter(store SlotBindingStore) BindingStore {
	if store == nil {
		return nil
	}
	return slotBindingStoreAdapter{store: store}
}

type slotBindingStoreAdapter struct {
	store SlotBindingStore
}

func (s slotBindingStoreAdapter) BindPluginUser(ctx context.Context, uid, pluginNo string) error {
	return s.store.BindPluginUser(ctx, uid, pluginNo)
}

func (s slotBindingStoreAdapter) UnbindPluginUser(ctx context.Context, uid, pluginNo string) error {
	return s.store.UnbindPluginUser(ctx, uid, pluginNo)
}

func (s slotBindingStoreAdapter) ListPluginBindingsByUID(ctx context.Context, uid string) ([]PluginBinding, error) {
	bindings, err := s.store.ListPluginBindingsByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	return pluginBindingsFromSlot(bindings), nil
}

func (s slotBindingStoreAdapter) ListPluginBindingsByPluginNo(ctx context.Context, pluginNo, cursor string, limit int) (BindingPage, error) {
	bindings, nextCursor, hasMore, err := s.store.ListPluginBindingsByPluginNo(ctx, pluginNo, cursor, limit)
	if err != nil {
		return BindingPage{}, err
	}
	return BindingPage{Bindings: pluginBindingsFromSlot(bindings), Cursor: nextCursor, HasMore: hasMore}, nil
}

func (s slotBindingStoreAdapter) ExistPluginBindingByUID(ctx context.Context, uid string) (bool, error) {
	return s.store.ExistPluginBindingByUID(ctx, uid)
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
	a.bindingMu.Lock()
	defer a.bindingMu.Unlock()
	if err := a.bindingStore.BindPluginUser(ctx, uid, pluginNo); err != nil {
		return BindingMutationResult{}, err
	}
	a.invalidateBindingCache(uid)
	a.bindingEpoch++
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
	a.bindingMu.Lock()
	defer a.bindingMu.Unlock()
	if err := a.bindingStore.UnbindPluginUser(ctx, uid, pluginNo); err != nil {
		return err
	}
	a.invalidateBindingCache(uid)
	a.bindingEpoch++
	return nil
}

// ListBindingsByUID lists enriched bindings for one UID and records stale local-plugin warnings.
func (a *App) ListBindingsByUID(ctx context.Context, uid string) (BindingList, error) {
	if uid == "" {
		return BindingList{}, ErrBindingUIDRequired
	}
	lookup, err := a.listBindingsByUID(ctx, uid, false)
	if err != nil {
		return BindingList{}, err
	}
	details, warnings, err := a.bindingDetails(ctx, lookup.bindings)
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
	lookup, err := a.listBindingsByUID(ctx, uid, true)
	if err != nil {
		return ObservedPlugin{}, false, err
	}
	selected, selectedOK, err := a.selectBoundReceivePlugin(ctx, lookup.bindings)
	if err != nil {
		return ObservedPlugin{}, false, err
	}
	if lookup.cacheable {
		a.setBindingCacheIfEpoch(uid, lookup.bindings, selected, selectedOK, lookup.epoch)
	}
	return selected, selectedOK, nil
}

type bindingLookup struct {
	bindings  []PluginBinding
	epoch     uint64
	cacheable bool
	fromCache bool
}

func (a *App) listBindingsByUID(ctx context.Context, uid string, useCache bool) (bindingLookup, error) {
	if a.bindingStore == nil {
		return bindingLookup{}, ErrBindingStoreRequired
	}
	if useCache {
		a.bindingMu.RLock()
		if bindings, _, _, hit := a.bindingCache.Get(uid); hit {
			a.bindingMu.RUnlock()
			return bindingLookup{bindings: bindings, fromCache: true}, nil
		}
		epoch := a.bindingEpoch
		bindings, err := a.bindingStore.ListPluginBindingsByUID(ctx, uid)
		a.bindingMu.RUnlock()
		if err != nil {
			return bindingLookup{}, err
		}
		bindings = clonePluginBindings(bindings)
		a.bindingMu.RLock()
		cacheable := epoch == a.bindingEpoch
		a.bindingMu.RUnlock()
		return bindingLookup{bindings: bindings, epoch: epoch, cacheable: cacheable}, nil
	}
	a.bindingMu.RLock()
	bindings, err := a.bindingStore.ListPluginBindingsByUID(ctx, uid)
	a.bindingMu.RUnlock()
	if err != nil {
		return bindingLookup{}, err
	}
	return bindingLookup{bindings: clonePluginBindings(bindings)}, nil
}

func (a *App) setBindingCacheIfEpoch(uid string, bindings []PluginBinding, selected ObservedPlugin, selectedOK bool, epoch uint64) bool {
	a.bindingMu.RLock()
	defer a.bindingMu.RUnlock()
	if epoch != a.bindingEpoch {
		return false
	}
	a.bindingCache.Set(uid, bindings, selected, selectedOK)
	return true
}

func (a *App) selectBoundReceivePlugin(ctx context.Context, bindings []PluginBinding) (ObservedPlugin, bool, error) {
	bound := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding.PluginNo != "" {
			bound[binding.PluginNo] = struct{}{}
		}
	}
	if len(bound) == 0 {
		return ObservedPlugin{}, false, nil
	}
	plugins := make([]ObservedPlugin, 0, len(bound))
	for _, plugin := range a.runtime.List() {
		if _, ok := bound[plugin.No]; !ok {
			continue
		}
		effective, err := a.applyDesiredEnabledToPlugin(ctx, plugin)
		if err != nil {
			return ObservedPlugin{}, false, err
		}
		plugins = append(plugins, effective)
	}
	plugin, ok := SelectReceivePlugin(plugins, keysFromPluginBindingSet(bound))
	return plugin, ok, nil
}

func (a *App) bindingDetails(ctx context.Context, bindings []PluginBinding) ([]BindingDetail, []BindingWarning, error) {
	details := make([]BindingDetail, 0, len(bindings))
	warnings := make([]BindingWarning, 0)
	for _, binding := range bindings {
		observed, ok := a.runtime.Get(binding.PluginNo)
		rowWarnings, err := a.bindingWarningsForObserved(ctx, binding, observed, ok)
		if err != nil {
			return nil, nil, err
		}
		detail := BindingDetail{Binding: binding, Warnings: cloneBindingWarnings(rowWarnings)}
		if ok {
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

func (a *App) bindingWarnings(ctx context.Context, binding PluginBinding) ([]BindingWarning, error) {
	observed, ok := a.runtime.Get(binding.PluginNo)
	return a.bindingWarningsForObserved(ctx, binding, observed, ok)
}

func (a *App) bindingWarningsForObserved(ctx context.Context, binding PluginBinding, observed ObservedPlugin, ok bool) ([]BindingWarning, error) {
	if !ok {
		return []BindingWarning{missingPluginWarning(binding)}, nil
	}
	effective, err := a.applyDesiredEnabledToPlugin(ctx, observed)
	if err != nil {
		return nil, err
	}
	switch {
	case !effective.Enabled || effective.Status == StatusDisabled:
		return []BindingWarning{bindingWarning(binding, BindingWarningPluginDisabled, fmt.Sprintf("plugin %q is disabled on this node", binding.PluginNo))}, nil
	case effective.Status != StatusRunning:
		return []BindingWarning{bindingWarning(binding, BindingWarningPluginUnavailable, fmt.Sprintf("plugin %q is %s on this node", binding.PluginNo, effective.Status))}, nil
	case !hasMethod(effective, MethodReceive):
		return []BindingWarning{bindingWarning(binding, BindingWarningReceiveUnsupported, fmt.Sprintf("plugin %q does not support Receive", binding.PluginNo))}, nil
	default:
		return nil, nil
	}
}

func missingPluginWarning(binding PluginBinding) BindingWarning {
	return BindingWarning{
		Code:     BindingWarningPluginMissing,
		Message:  fmt.Sprintf("plugin %q is not observed on this node", binding.PluginNo),
		UID:      binding.UID,
		PluginNo: binding.PluginNo,
	}
}

func bindingWarning(binding PluginBinding, code, message string) BindingWarning {
	return BindingWarning{Code: code, Message: message, UID: binding.UID, PluginNo: binding.PluginNo}
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

func pluginBindingsFromSlot(bindings []metadb.PluginUserBinding) []PluginBinding {
	out := make([]PluginBinding, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, PluginBinding{UID: binding.UID, PluginNo: binding.PluginNo})
	}
	return out
}

func keysFromPluginBindingSet(bound map[string]struct{}) []string {
	keys := make([]string, 0, len(bound))
	for key := range bound {
		keys = append(keys, key)
	}
	return keys
}
