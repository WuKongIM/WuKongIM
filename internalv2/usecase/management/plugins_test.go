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
