package plugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/stretchr/testify/require"
)

func TestStartPluginAppliesDesiredStateAndReturnsStartupConfig(t *testing.T) {
	store := newRecordingDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{
		No:        "wk.plugin.ai",
		Config:    json.RawMessage(`{"api_key":"secret"}`),
		Enabled:   false,
		CreatedAt: time.Unix(1, 0).UTC(),
		UpdatedAt: time.Unix(2, 0).UTC(),
	}
	runtime := &recordingRuntime{sandboxDirs: map[string]string{"wk.plugin.ai": "/tmp/plugin-sandbox/wk.plugin.ai"}}
	app, err := NewApp(Options{Runtime: runtime, Invoker: &recordingInvoker{}, DesiredStore: store, NodeID: 7})
	require.NoError(t, err)

	resp, err := app.StartPlugin(context.Background(), &pluginproto.PluginInfo{
		No:      "wk.plugin.ai",
		Name:    "AI",
		Methods: []string{"Send", "ConfigUpdate"},
		ConfigTemplate: &pluginproto.ConfigTemplate{Fields: []*pluginproto.Field{{
			Name:  "api_key",
			Type:  pluginproto.FieldTypeSecret.String(),
			Label: "API Key",
		}}},
	}, "wk.plugin.ai")

	require.NoError(t, err)
	require.True(t, resp.GetSuccess())
	require.Equal(t, uint64(7), resp.GetNodeId())
	require.Equal(t, "/tmp/plugin-sandbox/wk.plugin.ai", resp.GetSandboxDir())
	require.JSONEq(t, `{"api_key":"secret"}`, string(resp.GetConfig()))
	registered := runtime.registeredPlugins()
	require.Len(t, registered, 1)
	require.False(t, registered[0].Enabled)
	require.Equal(t, StatusDisabled, registered[0].Status)
	require.Contains(t, registered[0].Methods, MethodConfigUpdate)
	require.NotEmpty(t, registered[0].ConfigTemplateRaw)
}

func TestListPluginsAppliesDesiredConfigAndRedactsSecrets(t *testing.T) {
	templateRaw, err := (&pluginproto.ConfigTemplate{Fields: []*pluginproto.Field{{
		Name:  "api_key",
		Type:  pluginproto.FieldTypeSecret.String(),
		Label: "API Key",
	}}}).Marshal()
	require.NoError(t, err)
	store := newRecordingDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{
		No:        "wk.plugin.ai",
		Config:    json.RawMessage(`{"api_key":"secret","mode":"fast"}`),
		Enabled:   true,
		CreatedAt: time.Unix(1, 0).UTC(),
		UpdatedAt: time.Unix(2, 0).UTC(),
	}
	app, err := NewApp(Options{
		Runtime: &recordingRuntime{plugins: []ObservedPlugin{{
			No:                "wk.plugin.ai",
			Name:              "AI",
			Methods:           []Method{MethodSend},
			ConfigTemplateRaw: templateRaw,
			Status:            StatusRunning,
			Enabled:           true,
		}}},
		Invoker:      &recordingInvoker{},
		DesiredStore: store,
		NodeID:       9,
	})
	require.NoError(t, err)

	plugins, err := app.ListLocalPlugins(context.Background())

	require.NoError(t, err)
	require.Equal(t, uint64(9), plugins.NodeID)
	require.Len(t, plugins.Plugins, 1)
	plugin := plugins.Plugins[0]
	require.Equal(t, "wk.plugin.ai", plugin.No)
	require.Equal(t, StatusRunning, plugin.Status)
	require.True(t, plugin.Enabled)
	require.Equal(t, SecretHidden, plugin.Config["api_key"])
	require.Equal(t, "fast", plugin.Config["mode"])
	require.NotNil(t, plugin.ConfigTemplate)
	require.Equal(t, "api_key", plugin.ConfigTemplate.GetFields()[0].GetName())
	require.Equal(t, time.Unix(1, 0).UTC(), *plugin.CreatedAt)
	require.Equal(t, time.Unix(2, 0).UTC(), *plugin.UpdatedAt)
}

func TestUpdateLocalConfigPreservesHiddenSecretsAndInvokesConfigUpdate(t *testing.T) {
	templateRaw, err := (&pluginproto.ConfigTemplate{Fields: []*pluginproto.Field{{
		Name: "api_key",
		Type: pluginproto.FieldTypeSecret.String(),
	}}}).Marshal()
	require.NoError(t, err)
	store := newRecordingDesiredStore()
	store.states["wk.plugin.ai"] = DesiredPlugin{
		No:      "wk.plugin.ai",
		Config:  json.RawMessage(`{"api_key":"old","mode":"slow"}`),
		Enabled: true,
	}
	invoker := &recordingInvoker{}
	app, err := NewApp(Options{
		Runtime: &recordingRuntime{plugins: []ObservedPlugin{{
			No:                "wk.plugin.ai",
			Methods:           []Method{MethodConfigUpdate},
			ConfigTemplateRaw: templateRaw,
			Status:            StatusRunning,
			Enabled:           true,
		}}},
		Invoker:      invoker,
		DesiredStore: store,
		Clock:        func() time.Time { return time.Unix(10, 0).UTC() },
	})
	require.NoError(t, err)

	detail, err := app.UpdateLocalConfig(context.Background(), "wk.plugin.ai", json.RawMessage(`{"api_key":"******","mode":"fast"}`))

	require.NoError(t, err)
	require.Equal(t, SecretHidden, detail.Config["api_key"])
	require.Equal(t, "fast", detail.Config["mode"])
	require.JSONEq(t, `{"api_key":"old","mode":"fast"}`, string(store.states["wk.plugin.ai"].Config))
	require.Equal(t, []string{"wk.plugin.ai:" + PathConfigUpdate}, invoker.requests)
	require.JSONEq(t, `{"api_key":"old","mode":"fast"}`, string(invoker.requestBodies[0]))
}

func TestDesiredStateDisablesHookCandidates(t *testing.T) {
	store := newRecordingDesiredStore()
	store.states["disabled"] = DesiredPlugin{No: "disabled", Enabled: false}
	invoker := &sendHookInvoker{responses: map[string]*pluginproto.SendPacket{
		"enabled":  {Payload: []byte("enabled"), Reason: uint32(message.ReasonSuccess)},
		"disabled": {Payload: []byte("disabled"), Reason: uint32(message.ReasonSuccess)},
	}}
	app, err := NewApp(Options{
		Runtime: &recordingRuntime{plugins: []ObservedPlugin{
			{No: "enabled", Methods: []Method{MethodSend}, Priority: 1, Status: StatusRunning, Enabled: true},
			{No: "disabled", Methods: []Method{MethodSend}, Priority: 10, Status: StatusRunning, Enabled: true},
		}},
		Invoker:      invoker,
		DesiredStore: store,
	})
	require.NoError(t, err)

	cmd, reason, err := app.BeforeSend(context.Background(), message.SendCommand{FromUID: "u1", Payload: []byte("original")})

	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, reason)
	require.Equal(t, []byte("enabled"), cmd.Payload)
	require.Equal(t, []string{"enabled:" + PathSend}, invoker.requestKeys())
}

func TestRestartAndUninstallDelegateToRuntime(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{{
		No:      "wk.plugin.ai",
		Methods: []Method{MethodSend},
		Status:  StatusRunning,
		Enabled: true,
	}}}
	store := newRecordingDesiredStore()
	app, err := NewApp(Options{Runtime: runtime, Invoker: &recordingInvoker{}, DesiredStore: store})
	require.NoError(t, err)

	_, err = app.RestartLocalPlugin(context.Background(), "wk.plugin.ai")
	require.NoError(t, err)
	require.Equal(t, []string{"wk.plugin.ai"}, runtime.restarted)

	require.NoError(t, app.UninstallLocalPlugin(context.Background(), "wk.plugin.ai"))
	require.Equal(t, []string{"wk.plugin.ai"}, runtime.uninstalled)
	require.False(t, store.states["wk.plugin.ai"].Enabled)
}

type recordingDesiredStore struct {
	states map[string]DesiredPlugin
	err    error
}

func newRecordingDesiredStore() *recordingDesiredStore {
	return &recordingDesiredStore{states: make(map[string]DesiredPlugin)}
}

func (s *recordingDesiredStore) Get(_ context.Context, no string) (DesiredPlugin, error) {
	if s == nil {
		return DesiredPlugin{}, ErrDesiredPluginNotFound
	}
	if s.err != nil {
		return DesiredPlugin{}, s.err
	}
	state, ok := s.states[no]
	if !ok {
		return DesiredPlugin{}, ErrDesiredPluginNotFound
	}
	state.Config = append(json.RawMessage(nil), state.Config...)
	return state, nil
}

func (s *recordingDesiredStore) Save(_ context.Context, state DesiredPlugin) error {
	if s.err != nil {
		return s.err
	}
	if s.states == nil {
		s.states = make(map[string]DesiredPlugin)
	}
	state.Config = append(json.RawMessage(nil), state.Config...)
	s.states[state.No] = state
	return nil
}

func (s *recordingDesiredStore) Delete(_ context.Context, no string) error {
	if s.err != nil {
		return s.err
	}
	delete(s.states, no)
	return nil
}

var _ DesiredStore = (*recordingDesiredStore)(nil)
