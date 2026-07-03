package pluginhost

import (
	"sort"
	"sync"
)

// Registry keeps a concurrency-safe node-local snapshot of observed plugins.
// It must not be copied after first use.
type Registry struct {
	noCopy  noCopy
	mu      sync.RWMutex
	plugins map[string]ObservedPlugin
}

// noCopy marks structs that must not be copied after first use.
type noCopy struct{}

// Lock is used by go vet's copylocks analyzer.
func (*noCopy) Lock() {}

// Unlock is used by go vet's copylocks analyzer.
func (*noCopy) Unlock() {}

// NewRegistry creates an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{plugins: make(map[string]ObservedPlugin)}
}

// Upsert inserts or replaces a plugin observation.
func (r *Registry) Upsert(plugin ObservedPlugin) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.plugins[plugin.No] = cloneObservedPlugin(plugin)
}

// Remove deletes a plugin observation by plugin number.
func (r *Registry) Remove(no string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.plugins, no)
}

// Get returns a copy of a plugin observation.
func (r *Registry) Get(no string) (ObservedPlugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	plugin, ok := r.plugins[no]
	if !ok {
		return ObservedPlugin{}, false
	}
	return cloneObservedPlugin(plugin), true
}

// List returns copies of all plugin observations ordered by plugin number.
func (r *Registry) List() []ObservedPlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	plugins := make([]ObservedPlugin, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		plugins = append(plugins, cloneObservedPlugin(plugin))
	}
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].No < plugins[j].No
	})
	return plugins
}

// RunningByMethod returns enabled, running plugins supporting method in hook order.
func (r *Registry) RunningByMethod(method Method) []ObservedPlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	plugins := make([]ObservedPlugin, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		if isRunnableForMethod(plugin, method) {
			plugins = append(plugins, cloneObservedPlugin(plugin))
		}
	}
	sortPluginsByHookOrder(plugins)
	return plugins
}

// HighestRunningForUID returns the highest-priority runnable candidate for method.
func (r *Registry) HighestRunningForUID(candidates []string, method Method) (ObservedPlugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matches := make([]ObservedPlugin, 0, len(candidates))
	for _, no := range candidates {
		plugin, ok := r.plugins[no]
		if !ok || !isRunnableForMethod(plugin, method) {
			continue
		}
		matches = append(matches, cloneObservedPlugin(plugin))
	}
	if len(matches) == 0 {
		return ObservedPlugin{}, false
	}
	sortPluginsByHookOrder(matches)
	return matches[0], true
}

func isRunnableForMethod(plugin ObservedPlugin, method Method) bool {
	return plugin.Enabled && plugin.Status == StatusRunning && hasMethod(plugin.Methods, method)
}

func hasMethod(methods []Method, method Method) bool {
	for _, candidate := range methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func sortPluginsByHookOrder(plugins []ObservedPlugin) {
	sort.Slice(plugins, func(i, j int) bool {
		if plugins[i].Priority != plugins[j].Priority {
			return plugins[i].Priority > plugins[j].Priority
		}
		return plugins[i].No < plugins[j].No
	})
}

func cloneObservedPlugin(plugin ObservedPlugin) ObservedPlugin {
	plugin.Methods = append([]Method(nil), plugin.Methods...)
	plugin.ConfigTemplateRaw = append([]byte(nil), plugin.ConfigTemplateRaw...)
	return plugin
}
