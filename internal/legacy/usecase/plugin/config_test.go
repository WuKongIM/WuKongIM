package plugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestUpdateLocalConfigRetainsHiddenSecretAndInvokesRunningConfigUpdate(t *testing.T) {
	created := time.Date(2026, 5, 15, 7, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 5, 15, 8, 0, 0, 0, time.UTC)
	rt := newFakeRuntime(t.TempDir())
	templateBytes, err := testConfigTemplate().Marshal()
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	rt.plugins["wk.plugin.ai"] = ObservedPlugin{
		No:                "wk.plugin.ai",
		Name:              "AI",
		Status:            StatusRunning,
		Enabled:           true,
		Methods:           []Method{MethodConfigUpdate},
		ConfigTemplateRaw: templateBytes,
	}
	store := newFakeDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{
		No:        "wk.plugin.ai",
		Config:    json.RawMessage(`{"name":"old","api_key":"real-secret"}`),
		Enabled:   true,
		CreatedAt: created,
		UpdatedAt: created,
	}
	invoker := &fakeInvoker{}
	app := mustNewTestApp(t, Options{
		Runtime:      rt,
		DesiredStore: store,
		Invoker:      invoker,
		NodeID:       1,
		Clock:        func() time.Time { return updated },
	})

	detail, err := app.UpdateLocalConfig(context.Background(), "wk.plugin.ai", json.RawMessage(`{"name":"new","api_key":"******"}`))
	if err != nil {
		t.Fatalf("UpdateLocalConfig returned error: %v", err)
	}

	saved, ok := store.saved("wk.plugin.ai")
	if !ok {
		t.Fatal("desired config was not saved")
	}
	assertJSONEqual(t, saved.Config, []byte(`{"name":"new","api_key":"real-secret"}`))
	if !saved.CreatedAt.Equal(created) || !saved.UpdatedAt.Equal(updated) || !saved.Enabled {
		t.Fatalf("saved metadata = %#v", saved)
	}
	if detail.Config["api_key"] != SecretHidden || detail.Config["name"] != "new" {
		t.Fatalf("returned redacted config = %#v", detail.Config)
	}

	req, ok := invoker.lastRequest()
	if !ok {
		t.Fatal("ConfigUpdate was not invoked")
	}
	if req.No != "wk.plugin.ai" || req.Path != PathConfigUpdate {
		t.Fatalf("ConfigUpdate request target = %#v", req)
	}
	assertJSONEqual(t, req.Body, []byte(`{"name":"new","api_key":"real-secret"}`))
}

func TestUpdateLocalConfigSkipsConfigUpdateWhenPluginIsNotRunnableForMethod(t *testing.T) {
	cases := []struct {
		name     string
		observed ObservedPlugin
	}{
		{
			name:     "offline",
			observed: ObservedPlugin{No: "wk.plugin.ai", Status: StatusOffline, Enabled: true, Methods: []Method{MethodConfigUpdate}},
		},
		{
			name:     "disabled",
			observed: ObservedPlugin{No: "wk.plugin.ai", Status: StatusRunning, Enabled: false, Methods: []Method{MethodConfigUpdate}},
		},
		{
			name:     "method missing",
			observed: ObservedPlugin{No: "wk.plugin.ai", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newFakeRuntime(t.TempDir())
			rt.plugins["wk.plugin.ai"] = tc.observed
			store := newFakeDesiredStore()
			invoker := &fakeInvoker{}
			app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: store, Invoker: invoker, NodeID: 1})

			if _, err := app.UpdateLocalConfig(context.Background(), "wk.plugin.ai", json.RawMessage(`{"name":"new"}`)); err != nil {
				t.Fatalf("UpdateLocalConfig returned error: %v", err)
			}
			if len(invoker.requests) != 0 {
				t.Fatalf("ConfigUpdate requests = %#v, want none", invoker.requests)
			}
		})
	}
}

func TestUpdateLocalConfigSkipsConfigUpdateWhenDesiredStateDisabled(t *testing.T) {
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["wk.plugin.ai"] = ObservedPlugin{No: "wk.plugin.ai", Status: StatusRunning, Enabled: true, Methods: []Method{MethodConfigUpdate}}
	store := newFakeDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{No: "wk.plugin.ai", Enabled: false, Config: json.RawMessage(`{"name":"old"}`)}
	invoker := &fakeInvoker{}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: store, Invoker: invoker, NodeID: 1})

	detail, err := app.UpdateLocalConfig(context.Background(), "wk.plugin.ai", json.RawMessage(`{"name":"new"}`))
	if err != nil {
		t.Fatalf("UpdateLocalConfig returned error: %v", err)
	}
	if detail.Enabled || detail.Status != StatusDisabled {
		t.Fatalf("detail enabled/status = %v/%s, want false/%s", detail.Enabled, detail.Status, StatusDisabled)
	}
	if len(invoker.requests) != 0 {
		t.Fatalf("ConfigUpdate requests = %#v, want none", invoker.requests)
	}
}
