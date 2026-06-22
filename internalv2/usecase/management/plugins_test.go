package management

import (
	"context"
	"errors"
	"testing"
	"time"

	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
)

func TestListNodePluginsReadsLocalPluginReader(t *testing.T) {
	lastSeen := time.Unix(1713859200, 0).UTC()
	local := &fakePluginReader{plugins: []pluginusecase.ObservedPlugin{{
		No:               "wk.persist",
		Name:             "Persist",
		Version:          "v1",
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
		Methods:  []pluginusecase.Method{pluginusecase.MethodPersistAfter},
		Priority: 9, PersistAfterSync: true, Status: "running", Enabled: true,
		PID: 101, LastSeenAt: lastSeen, LastError: "last warning",
	}}}
	if !sameNodePluginList(got, want) {
		t.Fatalf("plugins = %#v, want %#v", got, want)
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
	local := &fakePluginReader{plugin: pluginusecase.ObservedPlugin{
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
	reader := &fakePluginReader{plugin: pluginusecase.ObservedPlugin{
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
	listCalls int
	plugins   []pluginusecase.ObservedPlugin
	plugin    pluginusecase.ObservedPlugin
	pluginErr error
}

func (f *fakePluginReader) ListPlugins(context.Context) ([]pluginusecase.ObservedPlugin, error) {
	f.listCalls++
	return append([]pluginusecase.ObservedPlugin(nil), f.plugins...), nil
}

func (f *fakePluginReader) GetPlugin(_ context.Context, pluginNo string) (pluginusecase.ObservedPlugin, error) {
	if pluginNo == "missing" {
		return pluginusecase.ObservedPlugin{}, f.pluginErr
	}
	return f.plugin, nil
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
	listNodeID     uint64
	detailNodeID   uint64
	detailPluginNo string
	plugins        []Plugin
	plugin         Plugin
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
