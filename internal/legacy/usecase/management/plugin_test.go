package management

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pluginusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/plugin"
	"github.com/stretchr/testify/require"
)

type fakePluginNodeClient struct {
	listNodeID        uint64
	listResp          pluginusecase.LocalPluginList
	listErr           error
	getNodeID         uint64
	getPluginNo       string
	getResp           pluginusecase.LocalPluginDetail
	getErr            error
	updateNodeID      uint64
	updatePluginNo    string
	updateConfig      json.RawMessage
	updateResp        pluginusecase.LocalPluginDetail
	updateErr         error
	restartNodeID     uint64
	restartPluginNo   string
	restartResp       pluginusecase.LocalPluginDetail
	restartErr        error
	uninstallNodeID   uint64
	uninstallPluginNo string
	uninstallErr      error
}

func (f *fakePluginNodeClient) ListNodePlugins(_ context.Context, nodeID uint64) (pluginusecase.LocalPluginList, error) {
	f.listNodeID = nodeID
	return f.listResp, f.listErr
}

func (f *fakePluginNodeClient) GetNodePlugin(_ context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	f.getNodeID = nodeID
	f.getPluginNo = pluginNo
	return f.getResp, f.getErr
}

func (f *fakePluginNodeClient) UpdateNodePluginConfig(_ context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (pluginusecase.LocalPluginDetail, error) {
	f.updateNodeID = nodeID
	f.updatePluginNo = pluginNo
	f.updateConfig = append(json.RawMessage(nil), config...)
	return f.updateResp, f.updateErr
}

func (f *fakePluginNodeClient) RestartNodePlugin(_ context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	f.restartNodeID = nodeID
	f.restartPluginNo = pluginNo
	return f.restartResp, f.restartErr
}

func (f *fakePluginNodeClient) UninstallNodePlugin(_ context.Context, nodeID uint64, pluginNo string) error {
	f.uninstallNodeID = nodeID
	f.uninstallPluginNo = pluginNo
	return f.uninstallErr
}

type fakePluginBindingUsecase struct {
	listUID      string
	listPluginNo string
	listCursor   string
	listLimit    int
	uidResp      pluginusecase.BindingList
	pluginResp   pluginusecase.BindingPage
	listErr      error
	bindReq      PluginBindingMutationRequest
	bindResp     pluginusecase.BindingMutationResult
	bindErr      error
	unbindReq    PluginBindingMutationRequest
	unbindErr    error
}

func (f *fakePluginBindingUsecase) ListBindingsByUID(_ context.Context, uid string) (pluginusecase.BindingList, error) {
	f.listUID = uid
	return f.uidResp, f.listErr
}

func (f *fakePluginBindingUsecase) ListBindingsByPluginNo(_ context.Context, pluginNo, cursor string, limit int) (pluginusecase.BindingPage, error) {
	f.listPluginNo = pluginNo
	f.listCursor = cursor
	f.listLimit = limit
	return f.pluginResp, f.listErr
}

func (f *fakePluginBindingUsecase) BindPluginUser(_ context.Context, uid, pluginNo string) (pluginusecase.BindingMutationResult, error) {
	f.bindReq = PluginBindingMutationRequest{UID: uid, PluginNo: pluginNo}
	return f.bindResp, f.bindErr
}

func (f *fakePluginBindingUsecase) UnbindPluginUser(_ context.Context, uid, pluginNo string) error {
	f.unbindReq = PluginBindingMutationRequest{UID: uid, PluginNo: pluginNo}
	return f.unbindErr
}

func TestPluginManagementDelegatesNodeScopedOperations(t *testing.T) {
	client := &fakePluginNodeClient{
		listResp:    pluginusecase.LocalPluginList{NodeID: 2, Plugins: []pluginusecase.LocalPlugin{{NodeID: 2, No: "wk.echo"}}},
		getResp:     pluginusecase.LocalPluginDetail{NodeID: 2, No: "wk.echo"},
		updateResp:  pluginusecase.LocalPluginDetail{NodeID: 2, No: "wk.echo", Config: map[string]any{"secret": pluginusecase.SecretHidden}},
		restartResp: pluginusecase.LocalPluginDetail{NodeID: 2, No: "wk.echo"},
	}
	app := New(Options{Plugins: client})

	list, err := app.ListNodePlugins(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), client.listNodeID)
	require.Equal(t, "wk.echo", list.Plugins[0].No)

	detail, err := app.GetNodePlugin(context.Background(), 2, " wk.echo ")
	require.NoError(t, err)
	require.Equal(t, uint64(2), client.getNodeID)
	require.Equal(t, "wk.echo", client.getPluginNo)
	require.Equal(t, "wk.echo", detail.No)

	updated, err := app.UpdateNodePluginConfig(context.Background(), 2, "wk.echo", json.RawMessage(`{"secret":"******"}`))
	require.NoError(t, err)
	require.Equal(t, uint64(2), client.updateNodeID)
	require.JSONEq(t, `{"secret":"******"}`, string(client.updateConfig))
	require.Equal(t, pluginusecase.SecretHidden, updated.Config["secret"])

	_, err = app.RestartNodePlugin(context.Background(), 2, "wk.echo")
	require.NoError(t, err)
	require.Equal(t, "wk.echo", client.restartPluginNo)

	require.NoError(t, app.UninstallNodePlugin(context.Background(), 2, "wk.echo"))
	require.Equal(t, uint64(2), client.uninstallNodeID)
	require.Equal(t, "wk.echo", client.uninstallPluginNo)
}

func TestPluginManagementRequiresNodeClient(t *testing.T) {
	app := New(Options{})

	_, err := app.ListNodePlugins(context.Background(), 1)

	require.ErrorIs(t, err, ErrPluginNodeUnavailable)
}

func TestPluginManagementPropagatesRemoteUnsupported(t *testing.T) {
	client := &fakePluginNodeClient{listErr: ErrPluginNodeUnsupported}
	app := New(Options{Plugins: client})

	_, err := app.ListNodePlugins(context.Background(), 3)

	require.ErrorIs(t, err, ErrPluginNodeUnsupported)
}

func TestPluginBindingsUseClusterAuthoritativePort(t *testing.T) {
	bindings := &fakePluginBindingUsecase{
		uidResp: pluginusecase.BindingList{Bindings: []pluginusecase.BindingDetail{{Binding: pluginusecase.PluginBinding{UID: "u1", PluginNo: "wk.echo"}}}},
		pluginResp: pluginusecase.BindingPage{
			Bindings: []pluginusecase.PluginBinding{{UID: "u2", PluginNo: "wk.echo"}},
			Cursor:   "next",
			HasMore:  true,
		},
		bindResp: pluginusecase.BindingMutationResult{Binding: pluginusecase.PluginBinding{UID: "u3", PluginNo: "wk.echo"}},
	}
	app := New(Options{PluginBindings: bindings})

	byUID, err := app.ListPluginBindings(context.Background(), PluginBindingListRequest{UID: " u1 "})
	require.NoError(t, err)
	require.Equal(t, "u1", bindings.listUID)
	require.Equal(t, []pluginusecase.PluginBinding{{UID: "u1", PluginNo: "wk.echo"}}, byUID.Bindings)

	byPlugin, err := app.ListPluginBindings(context.Background(), PluginBindingListRequest{PluginNo: " wk.echo ", Cursor: "c1", Limit: 25})
	require.NoError(t, err)
	require.Equal(t, "wk.echo", bindings.listPluginNo)
	require.Equal(t, "c1", bindings.listCursor)
	require.Equal(t, 25, bindings.listLimit)
	require.True(t, byPlugin.HasMore)
	require.Equal(t, "next", byPlugin.Cursor)

	_, err = app.BindPluginUser(context.Background(), PluginBindingMutationRequest{UID: " u3 ", PluginNo: " wk.echo "})
	require.NoError(t, err)
	require.Equal(t, PluginBindingMutationRequest{UID: "u3", PluginNo: "wk.echo"}, bindings.bindReq)

	require.NoError(t, app.UnbindPluginUser(context.Background(), PluginBindingMutationRequest{UID: "u3", PluginNo: "wk.echo"}))
	require.Equal(t, PluginBindingMutationRequest{UID: "u3", PluginNo: "wk.echo"}, bindings.unbindReq)
}

func TestPluginBindingsRejectMissingOrAmbiguousSelector(t *testing.T) {
	app := New(Options{PluginBindings: &fakePluginBindingUsecase{}})

	_, err := app.ListPluginBindings(context.Background(), PluginBindingListRequest{})
	require.ErrorIs(t, err, ErrPluginBindingSelectorRequired)

	_, err = app.ListPluginBindings(context.Background(), PluginBindingListRequest{UID: "u1", PluginNo: "wk.echo"})
	require.ErrorIs(t, err, ErrPluginBindingSelectorAmbiguous)
}

func TestPluginBindingsRequireAuthoritativePort(t *testing.T) {
	app := New(Options{})

	_, err := app.BindPluginUser(context.Background(), PluginBindingMutationRequest{UID: "u1", PluginNo: "wk.echo"})

	require.ErrorIs(t, err, ErrPluginBindingsUnavailable)
}

func TestPluginBindingsPropagatesAuthoritativeErrors(t *testing.T) {
	boom := errors.New("boom")
	app := New(Options{PluginBindings: &fakePluginBindingUsecase{bindErr: boom}})

	_, err := app.BindPluginUser(context.Background(), PluginBindingMutationRequest{UID: "u1", PluginNo: "wk.echo"})

	require.ErrorIs(t, err, boom)
}

type fakePluginNodeStatusError struct {
	status string
}

func (f fakePluginNodeStatusError) Error() string            { return "node status: " + f.status }
func (f fakePluginNodeStatusError) PluginNodeStatus() string { return f.status }

func TestPluginManagementNormalizesNodeStatusErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{name: "unsupported", err: fakePluginNodeStatusError{status: "unsupported"}, want: ErrPluginNodeUnsupported},
		{name: "unavailable", err: fakePluginNodeStatusError{status: "unavailable"}, want: ErrPluginNodeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := New(Options{Plugins: &fakePluginNodeClient{listErr: tc.err}})

			_, err := app.ListNodePlugins(context.Background(), 2)

			require.ErrorIs(t, err, tc.want)
		})
	}
}
