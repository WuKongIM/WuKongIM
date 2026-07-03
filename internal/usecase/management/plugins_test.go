package management

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

func TestListNodePluginsReadsLocalPluginReader(t *testing.T) {
	lastSeen := time.Unix(1713859200, 0).UTC()
	createdAt := time.Unix(1713859100, 0).UTC()
	updatedAt := time.Unix(1713859150, 0).UTC()
	local := &fakePluginReader{plugins: []pluginusecase.LocalPlugin{{
		NodeID:           1,
		No:               "wk.persist",
		Name:             "Persist",
		Version:          "v1",
		ConfigTemplate:   &pluginproto.ConfigTemplate{Fields: []*pluginproto.Field{{Name: "mode", Type: "string", Label: "Mode"}}},
		Config:           map[string]any{"mode": "fast"},
		CreatedAt:        &createdAt,
		UpdatedAt:        &updatedAt,
		Methods:          []pluginusecase.Method{pluginusecase.MethodPersistAfter},
		Priority:         9,
		PersistAfterSync: true,
		Status:           pluginusecase.StatusRunning,
		Enabled:          true,
		PID:              101,
		LastSeenAt:       lastSeen,
		LastError:        "last warning",
	}}}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{nodeID: 1},
		Plugins: local,
	})

	got, err := app.ListNodePlugins(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListNodePlugins() error = %v", err)
	}

	if local.listCalls != 1 {
		t.Fatalf("local list calls = %d, want 1", local.listCalls)
	}
	want := NodePluginList{NodeID: 1, Plugins: []Plugin{{
		NodeID: 1, No: "wk.persist", Name: "Persist", Version: "v1",
		ConfigTemplate: &pluginproto.ConfigTemplate{Fields: []*pluginproto.Field{{Name: "mode", Type: "string", Label: "Mode"}}},
		Config:         map[string]any{"mode": "fast"},
		Methods:        []pluginusecase.Method{pluginusecase.MethodPersistAfter},
		Priority:       9, PersistAfterSync: true, Status: "running", Enabled: true,
		PID: 101, LastSeenAt: lastSeen, LastError: "last warning",
	}}}
	if !sameNodePluginList(got, want) {
		t.Fatalf("plugins = %#v, want %#v", got, want)
	}
	if got.Plugins[0].Config["mode"] != "fast" || got.Plugins[0].ConfigTemplate.GetFields()[0].GetName() != "mode" {
		t.Fatalf("plugin config metadata = %#v template=%#v, want redacted config/template", got.Plugins[0].Config, got.Plugins[0].ConfigTemplate)
	}
}

func TestListNodePluginsRoutesRemoteNodeReads(t *testing.T) {
	local := &fakePluginReader{}
	remote := &fakeRemotePluginReader{
		plugins: []Plugin{{NodeID: 2, No: "wk.remote", Status: "running", Enabled: true}},
	}
	app := New(Options{
		Cluster:       fakeNodeSnapshotReader{nodeID: 1},
		Plugins:       local,
		RemotePlugins: remote,
	})

	got, err := app.ListNodePlugins(context.Background(), 2)
	if err != nil {
		t.Fatalf("ListNodePlugins(remote) error = %v", err)
	}

	if local.listCalls != 0 {
		t.Fatalf("local list calls = %d, want 0 for remote node", local.listCalls)
	}
	if remote.listNodeID != 2 {
		t.Fatalf("remote list node = %d, want 2", remote.listNodeID)
	}
	want := NodePluginList{NodeID: 2, Plugins: remote.plugins}
	if !sameNodePluginList(got, want) {
		t.Fatalf("plugins = %#v, want %#v", got, want)
	}
}

func TestGetNodePluginReturnsLocalDetailAndNotFound(t *testing.T) {
	local := &fakePluginReader{plugin: pluginusecase.LocalPluginDetail{
		NodeID:  1,
		No:      "wk.persist",
		Name:    "Persist",
		Status:  pluginusecase.StatusRunning,
		Enabled: true,
	}, pluginErr: pluginusecase.ErrPluginNotFound}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{nodeID: 1},
		Plugins: local,
	})

	got, err := app.GetNodePlugin(context.Background(), 1, "wk.persist")
	if err != nil {
		t.Fatalf("GetNodePlugin() error = %v", err)
	}
	if got.NodeID != 1 || got.No != "wk.persist" || got.Status != "running" {
		t.Fatalf("plugin = %#v, want local detail for wk.persist", got)
	}

	_, err = app.GetNodePlugin(context.Background(), 1, "missing")
	if !errors.Is(err, pluginusecase.ErrPluginNotFound) {
		t.Fatalf("GetNodePlugin(missing) error = %v, want %v", err, pluginusecase.ErrPluginNotFound)
	}
}

func TestPluginMutationsRouteLocalNode(t *testing.T) {
	local := &fakePluginReader{
		updatePlugin:  pluginusecase.LocalPluginDetail{NodeID: 1, No: "wk.persist", Status: pluginusecase.StatusRunning, Enabled: true, Config: map[string]any{"mode": "fast"}},
		restartPlugin: pluginusecase.LocalPluginDetail{NodeID: 1, No: "wk.persist", Status: pluginusecase.StatusStarting, Enabled: true},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{nodeID: 1},
		Plugins: local,
	})

	updated, err := app.UpdateNodePluginConfig(context.Background(), 1, " wk.persist ", json.RawMessage(`{"mode":"fast"}`))
	if err != nil {
		t.Fatalf("UpdateNodePluginConfig(local) error = %v", err)
	}
	if local.updatePluginNo != "wk.persist" || string(local.updateConfig) != `{"mode":"fast"}` {
		t.Fatalf("local update call = plugin:%q config:%s, want wk.persist fast config", local.updatePluginNo, string(local.updateConfig))
	}
	if updated.NodeID != 1 || updated.Config["mode"] != "fast" {
		t.Fatalf("updated = %#v, want local updated plugin config", updated)
	}

	restarted, err := app.RestartNodePlugin(context.Background(), 1, "wk.persist")
	if err != nil {
		t.Fatalf("RestartNodePlugin(local) error = %v", err)
	}
	if local.restartPluginNo != "wk.persist" || restarted.Status != string(pluginusecase.StatusStarting) {
		t.Fatalf("restart call = %q detail=%#v, want restarting wk.persist", local.restartPluginNo, restarted)
	}

	if err := app.UninstallNodePlugin(context.Background(), 1, "wk.persist"); err != nil {
		t.Fatalf("UninstallNodePlugin(local) error = %v", err)
	}
	if local.uninstallPluginNo != "wk.persist" {
		t.Fatalf("uninstall plugin = %q, want wk.persist", local.uninstallPluginNo)
	}
}

func TestPluginMutationsRouteRemoteNode(t *testing.T) {
	local := &fakePluginReader{}
	remote := &fakeRemotePluginReader{
		updatePlugin:  Plugin{NodeID: 3, No: "wk.remote", Status: "running", Enabled: true, Config: map[string]any{"mode": "safe"}},
		restartPlugin: Plugin{NodeID: 3, No: "wk.remote", Status: "starting", Enabled: true},
	}
	app := New(Options{
		Cluster:       fakeNodeSnapshotReader{nodeID: 1},
		Plugins:       local,
		RemotePlugins: remote,
	})

	updated, err := app.UpdateNodePluginConfig(context.Background(), 3, "wk.remote", json.RawMessage(`{"mode":"safe"}`))
	if err != nil {
		t.Fatalf("UpdateNodePluginConfig(remote) error = %v", err)
	}
	if local.updatePluginNo != "" {
		t.Fatalf("local update plugin = %q, want no local mutation for remote node", local.updatePluginNo)
	}
	if remote.updateNodeID != 3 || remote.updatePluginNo != "wk.remote" || string(remote.updateConfig) != `{"mode":"safe"}` {
		t.Fatalf("remote update call = node:%d plugin:%q config:%s", remote.updateNodeID, remote.updatePluginNo, string(remote.updateConfig))
	}
	if updated.NodeID != 3 || updated.Config["mode"] != "safe" {
		t.Fatalf("updated = %#v, want remote updated plugin", updated)
	}

	restarted, err := app.RestartNodePlugin(context.Background(), 3, "wk.remote")
	if err != nil {
		t.Fatalf("RestartNodePlugin(remote) error = %v", err)
	}
	if remote.restartNodeID != 3 || remote.restartPluginNo != "wk.remote" || restarted.Status != "starting" {
		t.Fatalf("remote restart call = node:%d plugin:%q detail=%#v", remote.restartNodeID, remote.restartPluginNo, restarted)
	}

	if err := app.UninstallNodePlugin(context.Background(), 3, "wk.remote"); err != nil {
		t.Fatalf("UninstallNodePlugin(remote) error = %v", err)
	}
	if remote.uninstallNodeID != 3 || remote.uninstallPluginNo != "wk.remote" {
		t.Fatalf("remote uninstall call = node:%d plugin:%q", remote.uninstallNodeID, remote.uninstallPluginNo)
	}
}

func TestNodePluginReadsRequireNodeID(t *testing.T) {
	app := New(Options{Cluster: fakeNodeSnapshotReader{nodeID: 1}, Plugins: &fakePluginReader{}})

	_, err := app.ListNodePlugins(context.Background(), 0)
	if !errors.Is(err, ErrPluginNodeIDRequired) {
		t.Fatalf("ListNodePlugins(0) error = %v, want %v", err, ErrPluginNodeIDRequired)
	}

	_, err = app.GetNodePlugin(context.Background(), 0, "wk.persist")
	if !errors.Is(err, ErrPluginNodeIDRequired) {
		t.Fatalf("GetNodePlugin(0) error = %v, want %v", err, ErrPluginNodeIDRequired)
	}
}

func TestPluginBindingsUseClusterAuthoritativeStore(t *testing.T) {
	store := &fakePluginBindingStore{
		byUID: map[string][]PluginBinding{
			"u1": {{UID: "u1", PluginNo: "wk.receive"}},
		},
		byPlugin: map[string]pluginBindingScanPage{
			"wk.receive": {
				bindings: []PluginBinding{{UID: "u2", PluginNo: "wk.receive"}},
				cursor:   "cursor-2",
				hasMore:  true,
			},
		},
	}
	app := New(Options{
		PluginBindings: store,
		Now:            func() time.Time { return time.UnixMilli(1700000000123).UTC() },
	})

	byUID, err := app.ListPluginBindings(context.Background(), PluginBindingListRequest{UID: " u1 "})
	if err != nil {
		t.Fatalf("ListPluginBindings(uid) error = %v", err)
	}
	if byUID.UID != "u1" || len(byUID.Bindings) != 1 || byUID.Bindings[0] != (PluginBinding{UID: "u1", PluginNo: "wk.receive"}) {
		t.Fatalf("uid page = %#v, want one u1/wk.receive row", byUID)
	}

	byPlugin, err := app.ListPluginBindings(context.Background(), PluginBindingListRequest{PluginNo: " wk.receive ", Cursor: "c1", Limit: 25})
	if err != nil {
		t.Fatalf("ListPluginBindings(plugin) error = %v", err)
	}
	if store.lastPluginNo != "wk.receive" || store.lastCursor != "c1" || store.lastLimit != 25 {
		t.Fatalf("scan args = plugin=%q cursor=%q limit=%d, want wk.receive/c1/25", store.lastPluginNo, store.lastCursor, store.lastLimit)
	}
	if byPlugin.PluginNo != "wk.receive" || byPlugin.Cursor != "cursor-2" || !byPlugin.HasMore || len(byPlugin.Bindings) != 1 {
		t.Fatalf("plugin page = %#v, want one row with cursor", byPlugin)
	}

	mutation, err := app.BindPluginUser(context.Background(), PluginBindingMutationRequest{UID: " u3 ", PluginNo: " wk.receive "})
	if err != nil {
		t.Fatalf("BindPluginUser() error = %v", err)
	}
	if mutation.Binding.UID != "u3" || mutation.Binding.PluginNo != "wk.receive" || !mutation.Changed {
		t.Fatalf("mutation = %#v, want changed u3/wk.receive", mutation)
	}
	if got := store.bound; len(got) != 1 || got[0].UID != "u3" || got[0].PluginNo != "wk.receive" || got[0].CreatedAt.IsZero() || got[0].UpdatedAt.IsZero() {
		t.Fatalf("bound rows = %#v, want one timestamped u3/wk.receive", got)
	}

	if err := app.UnbindPluginUser(context.Background(), PluginBindingMutationRequest{UID: "u3", PluginNo: "wk.receive"}); err != nil {
		t.Fatalf("UnbindPluginUser() error = %v", err)
	}
	if got := store.unbound; len(got) != 1 || got[0] != (PluginBinding{UID: "u3", PluginNo: "wk.receive"}) {
		t.Fatalf("unbound rows = %#v, want u3/wk.receive", got)
	}
}

func TestPluginBindingsRejectMissingOrAmbiguousSelector(t *testing.T) {
	app := New(Options{PluginBindings: &fakePluginBindingStore{}})

	_, err := app.ListPluginBindings(context.Background(), PluginBindingListRequest{})
	if !errors.Is(err, ErrPluginBindingSelectorRequired) {
		t.Fatalf("ListPluginBindings(empty) error = %v, want %v", err, ErrPluginBindingSelectorRequired)
	}
	_, err = app.ListPluginBindings(context.Background(), PluginBindingListRequest{UID: "u1", PluginNo: "wk.receive"})
	if !errors.Is(err, ErrPluginBindingSelectorAmbiguous) {
		t.Fatalf("ListPluginBindings(ambiguous) error = %v, want %v", err, ErrPluginBindingSelectorAmbiguous)
	}
}

func TestPluginBindingsRequireAuthoritativeStore(t *testing.T) {
	app := New(Options{})

	_, err := app.ListPluginBindings(context.Background(), PluginBindingListRequest{UID: "u1"})
	if !errors.Is(err, ErrPluginBindingsUnavailable) {
		t.Fatalf("ListPluginBindings() error = %v, want %v", err, ErrPluginBindingsUnavailable)
	}
	_, err = app.BindPluginUser(context.Background(), PluginBindingMutationRequest{UID: "u1", PluginNo: "wk.receive"})
	if !errors.Is(err, ErrPluginBindingsUnavailable) {
		t.Fatalf("BindPluginUser() error = %v, want %v", err, ErrPluginBindingsUnavailable)
	}
}

func TestPluginBindingWarningsUseLocalRuntimeWhenAvailable(t *testing.T) {
	reader := &fakePluginReader{plugin: pluginusecase.LocalPluginDetail{
		No:      "wk.persist",
		Status:  pluginusecase.StatusRunning,
		Enabled: true,
		Methods: []pluginusecase.Method{
			pluginusecase.MethodPersistAfter,
		},
	}}
	store := &fakePluginBindingStore{
		byUID: map[string][]PluginBinding{
			"u1": {{UID: "u1", PluginNo: "wk.persist"}},
		},
	}
	app := New(Options{Plugins: reader, PluginBindings: store})

	got, err := app.ListPluginBindings(context.Background(), PluginBindingListRequest{UID: "u1"})
	if err != nil {
		t.Fatalf("ListPluginBindings() error = %v", err)
	}
	if len(got.Details) != 1 || len(got.Details[0].Warnings) != 1 {
		t.Fatalf("details = %#v, want one Receive warning", got.Details)
	}
	if got.Details[0].Warnings[0].Code != BindingWarningReceiveUnsupported {
		t.Fatalf("warning = %#v, want %s", got.Details[0].Warnings[0], BindingWarningReceiveUnsupported)
	}
}

type fakePluginReader struct {
	listCalls         int
	plugins           []pluginusecase.LocalPlugin
	plugin            pluginusecase.LocalPluginDetail
	updatePlugin      pluginusecase.LocalPluginDetail
	restartPlugin     pluginusecase.LocalPluginDetail
	pluginErr         error
	updatePluginNo    string
	updateConfig      json.RawMessage
	restartPluginNo   string
	uninstallPluginNo string
}

func (f *fakePluginReader) ListLocalPlugins(context.Context) (pluginusecase.LocalPluginList, error) {
	f.listCalls++
	return pluginusecase.LocalPluginList{NodeID: 1, Plugins: append([]pluginusecase.LocalPlugin(nil), f.plugins...)}, nil
}

func (f *fakePluginReader) GetLocalPlugin(_ context.Context, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	if pluginNo == "missing" {
		return pluginusecase.LocalPluginDetail{}, f.pluginErr
	}
	return f.plugin, nil
}

func (f *fakePluginReader) UpdateLocalConfig(_ context.Context, pluginNo string, config json.RawMessage) (pluginusecase.LocalPluginDetail, error) {
	f.updatePluginNo = pluginNo
	f.updateConfig = append(json.RawMessage(nil), config...)
	return f.updatePlugin, nil
}

func (f *fakePluginReader) RestartLocalPlugin(_ context.Context, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	f.restartPluginNo = pluginNo
	return f.restartPlugin, nil
}

func (f *fakePluginReader) UninstallLocalPlugin(_ context.Context, pluginNo string) error {
	f.uninstallPluginNo = pluginNo
	return nil
}

func (f *fakePluginReader) ListPlugins(ctx context.Context) ([]pluginusecase.ObservedPlugin, error) {
	list, err := f.ListLocalPlugins(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]pluginusecase.ObservedPlugin, 0, len(list.Plugins))
	for _, item := range list.Plugins {
		out = append(out, observedFromLocalPlugin(item))
	}
	return out, nil
}

func (f *fakePluginReader) GetPlugin(ctx context.Context, pluginNo string) (pluginusecase.ObservedPlugin, error) {
	item, err := f.GetLocalPlugin(ctx, pluginNo)
	if err != nil {
		return pluginusecase.ObservedPlugin{}, err
	}
	return observedFromLocalPlugin(item), nil
}

func observedFromLocalPlugin(item pluginusecase.LocalPlugin) pluginusecase.ObservedPlugin {
	return pluginusecase.ObservedPlugin{
		No:               item.No,
		Name:             item.Name,
		Version:          item.Version,
		Methods:          append([]pluginusecase.Method(nil), item.Methods...),
		Priority:         item.Priority,
		PersistAfterSync: item.PersistAfterSync,
		ReplySync:        item.ReplySync,
		Status:           item.Status,
		Enabled:          item.Enabled,
		PID:              item.PID,
		LastSeenAt:       item.LastSeenAt,
		LastError:        item.LastError,
	}
}

type pluginBindingScanPage struct {
	bindings []PluginBinding
	cursor   string
	hasMore  bool
}

type fakePluginBindingStore struct {
	byUID        map[string][]PluginBinding
	byPlugin     map[string]pluginBindingScanPage
	bound        []PluginBinding
	unbound      []PluginBinding
	lastPluginNo string
	lastCursor   string
	lastLimit    int
}

func (f *fakePluginBindingStore) ListPluginBindingsByUID(_ context.Context, uid string) ([]PluginBinding, error) {
	return append([]PluginBinding(nil), f.byUID[uid]...), nil
}

func (f *fakePluginBindingStore) ListPluginBindingsByPluginNo(_ context.Context, pluginNo, cursor string, limit int) ([]PluginBinding, string, bool, error) {
	f.lastPluginNo = pluginNo
	f.lastCursor = cursor
	f.lastLimit = limit
	page := f.byPlugin[pluginNo]
	return append([]PluginBinding(nil), page.bindings...), page.cursor, page.hasMore, nil
}

func (f *fakePluginBindingStore) BindPluginUser(_ context.Context, binding PluginBinding) error {
	f.bound = append(f.bound, binding)
	return nil
}

func (f *fakePluginBindingStore) UnbindPluginUser(_ context.Context, uid, pluginNo string) error {
	f.unbound = append(f.unbound, PluginBinding{UID: uid, PluginNo: pluginNo})
	return nil
}

type fakeRemotePluginReader struct {
	listNodeID        uint64
	detailNodeID      uint64
	detailPluginNo    string
	updateNodeID      uint64
	updatePluginNo    string
	updateConfig      json.RawMessage
	restartNodeID     uint64
	restartPluginNo   string
	uninstallNodeID   uint64
	uninstallPluginNo string
	plugins           []Plugin
	plugin            Plugin
	updatePlugin      Plugin
	restartPlugin     Plugin
}

func (f *fakeRemotePluginReader) NodePlugins(_ context.Context, nodeID uint64) ([]Plugin, error) {
	f.listNodeID = nodeID
	return append([]Plugin(nil), f.plugins...), nil
}

func (f *fakeRemotePluginReader) NodePlugin(_ context.Context, nodeID uint64, pluginNo string) (Plugin, error) {
	f.detailNodeID = nodeID
	f.detailPluginNo = pluginNo
	return f.plugin, nil
}

func (f *fakeRemotePluginReader) UpdateNodePluginConfig(_ context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (Plugin, error) {
	f.updateNodeID = nodeID
	f.updatePluginNo = pluginNo
	f.updateConfig = append(json.RawMessage(nil), config...)
	return f.updatePlugin, nil
}

func (f *fakeRemotePluginReader) RestartNodePlugin(_ context.Context, nodeID uint64, pluginNo string) (Plugin, error) {
	f.restartNodeID = nodeID
	f.restartPluginNo = pluginNo
	return f.restartPlugin, nil
}

func (f *fakeRemotePluginReader) UninstallNodePlugin(_ context.Context, nodeID uint64, pluginNo string) error {
	f.uninstallNodeID = nodeID
	f.uninstallPluginNo = pluginNo
	return nil
}

func sameNodePluginList(left, right NodePluginList) bool {
	if left.NodeID != right.NodeID || len(left.Plugins) != len(right.Plugins) {
		return false
	}
	for i := range left.Plugins {
		if !samePlugin(left.Plugins[i], right.Plugins[i]) {
			return false
		}
	}
	return true
}

func samePlugin(left, right Plugin) bool {
	if left.NodeID != right.NodeID ||
		left.No != right.No ||
		left.Name != right.Name ||
		left.Version != right.Version ||
		left.Priority != right.Priority ||
		left.PersistAfterSync != right.PersistAfterSync ||
		left.ReplySync != right.ReplySync ||
		left.Status != right.Status ||
		left.Enabled != right.Enabled ||
		left.IsAI != right.IsAI ||
		left.PID != right.PID ||
		!left.LastSeenAt.Equal(right.LastSeenAt) ||
		left.LastError != right.LastError ||
		!sameAnyMap(left.Config, right.Config) ||
		len(left.Methods) != len(right.Methods) {
		return false
	}
	for i := range left.Methods {
		if left.Methods[i] != right.Methods[i] {
			return false
		}
	}
	return true
}

func sameAnyMap(left, right map[string]any) bool {
	leftRaw, _ := json.Marshal(left)
	rightRaw, _ := json.Marshal(right)
	return string(leftRaw) == string(rightRaw)
}
