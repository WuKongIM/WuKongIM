package plugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestListLocalPluginsRedactsSecretConfigWithoutMutatingStoredConfig(t *testing.T) {
	templateBytes, err := testConfigTemplate().Marshal()
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["wk.plugin.ai"] = ObservedPlugin{
		No:                "wk.plugin.ai",
		Name:              "AI",
		Version:           "1.0.0",
		Status:            StatusRunning,
		Enabled:           true,
		Methods:           []Method{MethodReceive},
		ConfigTemplateRaw: templateBytes,
	}
	store := newFakeDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{
		No:        "wk.plugin.ai",
		Config:    json.RawMessage(`{"name":"assistant","api_key":"real-secret"}`),
		Enabled:   true,
		CreatedAt: time.Date(2026, 5, 15, 7, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 5, 15, 8, 0, 0, 0, time.UTC),
	}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: store, NodeID: 9})

	list, err := app.ListLocalPlugins(context.Background())
	if err != nil {
		t.Fatalf("ListLocalPlugins returned error: %v", err)
	}
	if list.NodeID != 9 || len(list.Plugins) != 1 {
		t.Fatalf("plugin list = %#v", list)
	}
	plugin := list.Plugins[0]
	if plugin.Config["api_key"] != SecretHidden || plugin.Config["name"] != "assistant" {
		t.Fatalf("redacted config = %#v", plugin.Config)
	}
	if plugin.IsAI != 1 {
		t.Fatalf("IsAI = %d, want 1", plugin.IsAI)
	}

	saved, ok := store.saved("wk.plugin.ai")
	if !ok {
		t.Fatal("desired state missing")
	}
	assertJSONEqual(t, saved.Config, []byte(`{"name":"assistant","api_key":"real-secret"}`))
}

func TestGetLocalPluginRedactsSecretConfig(t *testing.T) {
	templateBytes, err := testConfigTemplate().Marshal()
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["wk.plugin.ai"] = ObservedPlugin{No: "wk.plugin.ai", Status: StatusRunning, Enabled: true, ConfigTemplateRaw: templateBytes}
	store := newFakeDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{No: "wk.plugin.ai", Config: json.RawMessage(`{"api_key":"real-secret"}`), Enabled: true}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: store, NodeID: 1})

	detail, err := app.GetLocalPlugin(context.Background(), "wk.plugin.ai")
	if err != nil {
		t.Fatalf("GetLocalPlugin returned error: %v", err)
	}
	if detail.Config["api_key"] != SecretHidden {
		t.Fatalf("detail config = %#v", detail.Config)
	}
}
