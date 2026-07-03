package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

func TestStartPluginRegistersObservedCreatesSandboxAndReturnsDesiredConfig(t *testing.T) {
	now := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	rt := newFakeRuntime(t.TempDir())
	store := newFakeDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{
		No:        "wk.plugin.ai",
		Config:    json.RawMessage(`{"name":"assistant","api_key":"real-secret"}`),
		Enabled:   true,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Minute),
	}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: store, NodeID: 42, Clock: func() time.Time { return now }})

	resp, err := app.StartPlugin(context.Background(), &pluginproto.PluginInfo{
		No:               "wk.plugin.ai",
		Name:             "AI Assistant",
		Version:          "1.2.3",
		Methods:          []string{"Receive", "ConfigUpdate"},
		Priority:         7,
		PersistAfterSync: true,
		ReplySync:        true,
		ConfigTemplate:   testConfigTemplate(),
	}, "wk.plugin.ai")
	if err != nil {
		t.Fatalf("StartPlugin returned error: %v", err)
	}
	if resp.GetNodeId() != 42 || !resp.GetSuccess() {
		t.Fatalf("startup response = node %d success %v", resp.GetNodeId(), resp.GetSuccess())
	}
	if resp.GetSandboxDir() == "" {
		t.Fatal("sandbox dir is empty")
	}
	if stat, err := os.Stat(resp.GetSandboxDir()); err != nil || !stat.IsDir() {
		t.Fatalf("sandbox dir was not created: stat=%v err=%v", stat, err)
	}
	assertJSONEqual(t, resp.GetConfig(), []byte(`{"name":"assistant","api_key":"real-secret"}`))

	observed, ok := rt.Get("wk.plugin.ai")
	if !ok {
		t.Fatal("observed plugin was not registered")
	}
	if observed.No != "wk.plugin.ai" || observed.Name != "AI Assistant" || observed.Version != "1.2.3" {
		t.Fatalf("observed identity = %#v", observed)
	}
	if observed.Status != StatusRunning || !observed.Enabled {
		t.Fatalf("observed status/enabled = %s/%v", observed.Status, observed.Enabled)
	}
	if observed.Priority != 7 || !observed.PersistAfterSync || !observed.ReplySync {
		t.Fatalf("observed execution flags = priority %d persistAfterSync %v replySync %v", observed.Priority, observed.PersistAfterSync, observed.ReplySync)
	}
	if !reflect.DeepEqual(observed.Methods, []Method{MethodReceive, MethodConfigUpdate}) {
		t.Fatalf("observed methods = %#v", observed.Methods)
	}
	var template pluginproto.ConfigTemplate
	if err := template.Unmarshal(observed.ConfigTemplateRaw); err != nil {
		t.Fatalf("observed config template is not pluginproto bytes: %v", err)
	}
	if len(template.GetFields()) != 2 {
		t.Fatalf("observed config template fields = %d", len(template.GetFields()))
	}
}

func TestClosePluginMarksObservedOffline(t *testing.T) {
	now := time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["wk.plugin.ai"] = ObservedPlugin{No: "wk.plugin.ai", Name: "AI", Status: StatusRunning, Enabled: true, PID: 123, Methods: []Method{MethodReceive}}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: newFakeDesiredStore(), NodeID: 1, Clock: func() time.Time { return now }})

	if err := app.ClosePlugin(context.Background(), "wk.plugin.ai", "wk.plugin.ai"); err != nil {
		t.Fatalf("ClosePlugin returned error: %v", err)
	}
	observed, ok := rt.Get("wk.plugin.ai")
	if !ok {
		t.Fatal("observed plugin missing after close")
	}
	if observed.Status != StatusOffline || observed.PID != 0 || !observed.Enabled {
		t.Fatalf("observed after close = %#v", observed)
	}
	if !observed.LastSeenAt.Equal(now) {
		t.Fatalf("LastSeenAt = %v, want %v", observed.LastSeenAt, now)
	}
}

func mustNewTestApp(t *testing.T, opts Options) *App {
	t.Helper()
	app, err := NewApp(opts)
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}
	return app
}

func testConfigTemplate() *pluginproto.ConfigTemplate {
	return &pluginproto.ConfigTemplate{Fields: []*pluginproto.Field{
		{Name: "name", Type: pluginproto.FieldTypeString.String(), Label: "Name"},
		{Name: "api_key", Type: pluginproto.FieldTypeSecret.String(), Label: "API Key"},
	}}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotAny any
	if err := json.Unmarshal(got, &gotAny); err != nil {
		t.Fatalf("got is not JSON: %s: %v", string(got), err)
	}
	var wantAny any
	if err := json.Unmarshal(want, &wantAny); err != nil {
		t.Fatalf("want is not JSON: %s: %v", string(want), err)
	}
	if !reflect.DeepEqual(gotAny, wantAny) {
		t.Fatalf("JSON mismatch\ngot:  %s\nwant: %s", string(got), string(want))
	}
}

type fakeRuntime struct {
	mu          sync.Mutex
	sandboxRoot string
	plugins     map[string]ObservedPlugin
	restarts    []string
	uninstalls  []string
}

func newFakeRuntime(root string) *fakeRuntime {
	return &fakeRuntime{sandboxRoot: root, plugins: make(map[string]ObservedPlugin)}
}

func (f *fakeRuntime) RegisterObserved(_ context.Context, info ObservedPlugin) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plugins[info.No] = cloneObservedForTest(info)
	return nil
}

func (f *fakeRuntime) Get(no string) (ObservedPlugin, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	plugin, ok := f.plugins[no]
	if !ok {
		return ObservedPlugin{}, false
	}
	return cloneObservedForTest(plugin), true
}

func (f *fakeRuntime) List() []ObservedPlugin {
	f.mu.Lock()
	defer f.mu.Unlock()
	plugins := make([]ObservedPlugin, 0, len(f.plugins))
	for _, plugin := range f.plugins {
		plugins = append(plugins, cloneObservedForTest(plugin))
	}
	return plugins
}

func (f *fakeRuntime) Restart(_ context.Context, no string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts = append(f.restarts, no)
	return nil
}

func (f *fakeRuntime) Uninstall(_ context.Context, no string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uninstalls = append(f.uninstalls, no)
	return nil
}

func (f *fakeRuntime) SandboxDir(no string) (string, error) {
	return filepath.Join(f.sandboxRoot, no), nil
}

func cloneObservedForTest(plugin ObservedPlugin) ObservedPlugin {
	plugin.Methods = append([]Method(nil), plugin.Methods...)
	plugin.ConfigTemplateRaw = append([]byte(nil), plugin.ConfigTemplateRaw...)
	return plugin
}

type fakeDesiredStore struct {
	mu      sync.Mutex
	states  map[string]DesiredPlugin
	saves   []DesiredPlugin
	deletes []string
}

func newFakeDesiredStore() *fakeDesiredStore {
	return &fakeDesiredStore{states: make(map[string]DesiredPlugin)}
}

func (f *fakeDesiredStore) Get(_ context.Context, no string) (DesiredPlugin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.states[no]
	if !ok {
		return DesiredPlugin{}, ErrDesiredPluginNotFound
	}
	return cloneDesiredForTest(state), nil
}

func (f *fakeDesiredStore) Save(_ context.Context, state DesiredPlugin) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cloned := cloneDesiredForTest(state)
	f.states[state.No] = cloned
	f.saves = append(f.saves, cloned)
	return nil
}

func (f *fakeDesiredStore) Delete(_ context.Context, no string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.states, no)
	f.deletes = append(f.deletes, no)
	return nil
}

func (f *fakeDesiredStore) saved(no string) (DesiredPlugin, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.states[no]
	return cloneDesiredForTest(state), ok
}

func cloneDesiredForTest(state DesiredPlugin) DesiredPlugin {
	state.Config = append(json.RawMessage(nil), state.Config...)
	return state
}

type fakeInvoker struct {
	mu       sync.Mutex
	requests []fakeRequest
	sends    []fakeSend
	stops    []string
}

type fakeRequest struct {
	No   string
	Path string
	Body []byte
}

type fakeSend struct {
	No      string
	MsgType uint32
	Body    []byte
}

func (f *fakeInvoker) RequestPlugin(_ context.Context, no, path string, body []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, fakeRequest{No: no, Path: path, Body: append([]byte(nil), body...)})
	return nil, nil
}

func (f *fakeInvoker) SendPlugin(no string, msgType uint32, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, fakeSend{No: no, MsgType: msgType, Body: append([]byte(nil), body...)})
	return nil
}

func (f *fakeInvoker) Stop(_ context.Context, no string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, no)
	return nil
}

func (f *fakeInvoker) lastRequest() (fakeRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return fakeRequest{}, false
	}
	return f.requests[len(f.requests)-1], true
}

func assertErrorIs(t *testing.T, got error, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func TestStartPluginRejectsCallerMismatchAndInvalidPluginNo(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), NodeID: 1})

	_, err := app.StartPlugin(context.Background(), &pluginproto.PluginInfo{No: "plugin-b"}, "plugin-a")
	assertErrorIs(t, err, ErrPluginIdentityMismatch)

	_, err = app.StartPlugin(context.Background(), &pluginproto.PluginInfo{No: "../escape"}, "../escape")
	assertErrorIs(t, err, ErrInvalidPluginNo)
}

func TestStartPluginHonorsDisabledDesiredState(t *testing.T) {
	rt := newFakeRuntime(t.TempDir())
	store := newFakeDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{No: "wk.plugin.ai", Enabled: false, Config: json.RawMessage(`{"api_key":"real-secret"}`)}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: store, NodeID: 1})

	_, err := app.StartPlugin(context.Background(), &pluginproto.PluginInfo{No: "wk.plugin.ai", Methods: []string{"Receive"}}, "wk.plugin.ai")
	if err != nil {
		t.Fatalf("StartPlugin returned error: %v", err)
	}
	observed, ok := rt.Get("wk.plugin.ai")
	if !ok {
		t.Fatal("observed plugin was not registered")
	}
	if observed.Enabled || observed.Status != StatusDisabled {
		t.Fatalf("observed enabled/status = %v/%s, want false/%s", observed.Enabled, observed.Status, StatusDisabled)
	}
}

func TestListAndGetLocalPluginUseDesiredEnabledState(t *testing.T) {
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["wk.plugin.ai"] = ObservedPlugin{No: "wk.plugin.ai", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}}
	store := newFakeDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{No: "wk.plugin.ai", Enabled: false}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: store, NodeID: 1})

	list, err := app.ListLocalPlugins(context.Background())
	if err != nil {
		t.Fatalf("ListLocalPlugins returned error: %v", err)
	}
	if len(list.Plugins) != 1 || list.Plugins[0].Enabled || list.Plugins[0].Status != StatusDisabled {
		t.Fatalf("list plugin = %#v, want desired disabled", list.Plugins)
	}
	detail, err := app.GetLocalPlugin(context.Background(), "wk.plugin.ai")
	if err != nil {
		t.Fatalf("GetLocalPlugin returned error: %v", err)
	}
	if detail.Enabled || detail.Status != StatusDisabled {
		t.Fatalf("detail enabled/status = %v/%s, want false/%s", detail.Enabled, detail.Status, StatusDisabled)
	}
}

func TestUninstallLocalPluginPersistsDisabledDesiredState(t *testing.T) {
	now := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	created := now.Add(-time.Hour)
	rt := newFakeRuntime(t.TempDir())
	store := newFakeDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{No: "wk.plugin.ai", Enabled: true, Config: json.RawMessage(`{"api_key":"real-secret"}`), CreatedAt: created, UpdatedAt: created}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: store, NodeID: 1, Clock: func() time.Time { return now }})

	if err := app.UninstallLocalPlugin(context.Background(), "wk.plugin.ai"); err != nil {
		t.Fatalf("UninstallLocalPlugin returned error: %v", err)
	}
	saved, ok := store.saved("wk.plugin.ai")
	if !ok {
		t.Fatal("disabled desired state was not saved")
	}
	if saved.Enabled || !saved.CreatedAt.Equal(created) || !saved.UpdatedAt.Equal(now) {
		t.Fatalf("saved desired state = %#v", saved)
	}
	assertJSONEqual(t, saved.Config, []byte(`{"api_key":"real-secret"}`))
	if len(rt.uninstalls) != 1 || rt.uninstalls[0] != "wk.plugin.ai" {
		t.Fatalf("runtime uninstalls = %#v", rt.uninstalls)
	}
}

func TestStartPluginRejectsEmptyCallerUID(t *testing.T) {
	store := newFakeDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{No: "wk.plugin.ai", Config: json.RawMessage(`{"api_key":"real-secret"}`), Enabled: true}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: store, NodeID: 1})

	_, err := app.StartPlugin(context.Background(), &pluginproto.PluginInfo{No: "wk.plugin.ai"}, "")
	assertErrorIs(t, err, ErrPluginIdentityRequired)
}

func TestStartPluginRejectsDotSegmentPluginNo(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), NodeID: 1})

	for _, no := range []string{".", ".."} {
		t.Run(no, func(t *testing.T) {
			_, err := app.StartPlugin(context.Background(), &pluginproto.PluginInfo{No: no}, no)
			assertErrorIs(t, err, ErrInvalidPluginNo)
		})
	}
}
