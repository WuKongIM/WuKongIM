package plugin

import "sort"

// RunningPluginsByMethod returns runnable plugins for method in hook order.
func RunningPluginsByMethod(plugins []ObservedPlugin, method Method) []ObservedPlugin {
	matches := make([]ObservedPlugin, 0, len(plugins))
	for _, plugin := range plugins {
		if isRunnableForMethod(plugin, method) {
			matches = append(matches, cloneObserved(plugin))
		}
	}
	sortPluginsByHookOrder(matches)
	return matches
}

// SelectReceivePlugin returns the highest-priority runnable Receive plugin in boundPluginNos.
func SelectReceivePlugin(plugins []ObservedPlugin, boundPluginNos []string) (ObservedPlugin, bool) {
	bound := make(map[string]struct{}, len(boundPluginNos))
	for _, no := range boundPluginNos {
		if no != "" {
			bound[no] = struct{}{}
		}
	}
	if len(bound) == 0 {
		return ObservedPlugin{}, false
	}
	for _, plugin := range RunningPluginsByMethod(plugins, MethodReceive) {
		if _, ok := bound[plugin.No]; ok {
			return plugin, true
		}
	}
	return ObservedPlugin{}, false
}

func isRunnableForMethod(plugin ObservedPlugin, method Method) bool {
	return plugin.Enabled && plugin.Status == StatusRunning && hasMethod(plugin, method)
}

func hasMethod(plugin ObservedPlugin, method Method) bool {
	for _, candidate := range plugin.Methods {
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

func cloneObserved(plugin ObservedPlugin) ObservedPlugin {
	plugin.Methods = append([]Method(nil), plugin.Methods...)
	plugin.ConfigTemplateRaw = append([]byte(nil), plugin.ConfigTemplateRaw...)
	return plugin
}
