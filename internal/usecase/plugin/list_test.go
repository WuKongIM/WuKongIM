package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListPluginsReturnsClonedObservedPlugins(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{{
		No:       "persist",
		Methods:  []Method{MethodPersistAfter},
		Priority: 8,
		Status:   StatusRunning,
		Enabled:  true,
	}}}
	app, err := NewApp(Options{Runtime: runtime, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	plugins, err := app.ListPlugins(context.Background())
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	plugins[0].Methods[0] = MethodSend

	plugins, err = app.ListPlugins(context.Background())
	require.NoError(t, err)
	require.Equal(t, []Method{MethodPersistAfter}, plugins[0].Methods)
}

func TestGetPluginReturnsClonedObservedPlugin(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{{
		No:      "persist",
		Methods: []Method{MethodPersistAfter},
		Status:  StatusRunning,
		Enabled: true,
	}}}
	app, err := NewApp(Options{Runtime: runtime, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	plugin, err := app.GetPlugin(context.Background(), "persist")
	require.NoError(t, err)
	require.Equal(t, "persist", plugin.No)
	plugin.Methods[0] = MethodSend

	plugin, err = app.GetPlugin(context.Background(), "persist")
	require.NoError(t, err)
	require.Equal(t, []Method{MethodPersistAfter}, plugin.Methods)
}

func TestGetPluginReturnsNotFoundForMissingPlugin(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	_, err = app.GetPlugin(context.Background(), "missing")
	require.True(t, errors.Is(err, ErrPluginNotFound))
}

func TestGetPluginRequiresPluginNo(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	_, err = app.GetPlugin(context.Background(), "  ")
	require.True(t, errors.Is(err, ErrPluginNoRequired))
}
