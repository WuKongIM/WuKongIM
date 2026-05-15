package plugin

import (
	"context"
	"testing"
)

func TestSelectionOrdersSendAndPersistAfterGlobalPlugins(t *testing.T) {
	plugins := []ObservedPlugin{
		{No: "p-low", Priority: 1, Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend, MethodPersistAfter}},
		{No: "p-high", Priority: 10, Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend, MethodPersistAfter}},
		{No: "p-tie-b", Priority: 5, Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend, MethodPersistAfter}},
		{No: "p-tie-a", Priority: 5, Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend, MethodPersistAfter}},
		{No: "p-disabled", Priority: 99, Status: StatusRunning, Enabled: false, Methods: []Method{MethodSend, MethodPersistAfter}},
		{No: "p-offline", Priority: 99, Status: StatusOffline, Enabled: true, Methods: []Method{MethodSend, MethodPersistAfter}},
	}

	assertPluginNos(t, RunningPluginsByMethod(plugins, MethodSend), []string{"p-high", "p-tie-a", "p-tie-b", "p-low"})
	assertPluginNos(t, RunningPluginsByMethod(plugins, MethodPersistAfter), []string{"p-high", "p-tie-a", "p-tie-b", "p-low"})
}

func TestSelectionChoosesHighestPriorityBoundReceivePlugin(t *testing.T) {
	plugins := []ObservedPlugin{
		{No: "bot-unbound-high", Priority: 99, Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}},
		{No: "bot-a", Priority: 5, Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}},
		{No: "bot-z", Priority: 5, Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}},
		{No: "bot-low", Priority: 1, Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}},
		{No: "bot-offline", Priority: 50, Status: StatusOffline, Enabled: true, Methods: []Method{MethodReceive}},
		{No: "bot-disabled", Priority: 50, Status: StatusRunning, Enabled: false, Methods: []Method{MethodReceive}},
		{No: "bot-send-only", Priority: 50, Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend}},
	}

	selected, ok := SelectReceivePlugin(plugins, []string{"bot-z", "bot-a", "bot-low", "bot-offline", "bot-disabled", "bot-send-only"})
	if !ok {
		t.Fatal("SelectReceivePlugin returned no plugin")
	}
	if selected.No != "bot-a" {
		t.Fatalf("selected plugin = %q, want bot-a", selected.No)
	}
}

func TestAppSelectionUsesRuntimeRegistry(t *testing.T) {
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["p2"] = ObservedPlugin{No: "p2", Priority: 2, Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend}}
	rt.plugins["p1"] = ObservedPlugin{No: "p1", Priority: 1, Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend}}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: newFakeDesiredStore(), NodeID: 1})

	plugins, err := app.SendPluginCandidates(context.Background())
	if err != nil {
		t.Fatalf("SendPluginCandidates returned error: %v", err)
	}
	assertPluginNos(t, plugins, []string{"p2", "p1"})
}

func TestAppSelectionSkipsDesiredDisabledPlugins(t *testing.T) {
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["p2"] = ObservedPlugin{No: "p2", Priority: 2, Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend, MethodReceive}}
	rt.plugins["p1"] = ObservedPlugin{No: "p1", Priority: 1, Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend, MethodReceive}}
	store := newFakeDesiredStore()
	store.states["p2"] = DesiredPlugin{No: "p2", Enabled: false}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: store, NodeID: 1})

	send, err := app.SendPluginCandidates(context.Background())
	if err != nil {
		t.Fatalf("SendPluginCandidates returned error: %v", err)
	}
	assertPluginNos(t, send, []string{"p1"})

	recv, ok, err := app.BoundReceivePlugin(context.Background(), []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("BoundReceivePlugin returned error: %v", err)
	}
	if !ok || recv.No != "p1" {
		t.Fatalf("BoundReceivePlugin = (%#v, %v), want p1 true", recv, ok)
	}
}

func assertPluginNos(t *testing.T, plugins []ObservedPlugin, want []string) {
	t.Helper()
	got := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		got = append(got, plugin.No)
	}
	if len(got) != len(want) {
		t.Fatalf("plugin nos = %#v, want %#v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("plugin nos = %#v, want %#v", got, want)
		}
	}
}
