package app

import (
	"reflect"
	"testing"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/plugin"
	"github.com/stretchr/testify/require"
)

func TestNewSkipsPluginSubsystemByDefault(t *testing.T) {
	cfg := testConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"
	cfg.Manager = validManagerConfigForTest()
	cfg.Manager.ListenAddr = "127.0.0.1:0"

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.Stop()) })

	require.Nil(t, app.pluginRuntime)
	require.Nil(t, app.pluginApp)
	require.Nil(t, app.pluginAccess)
	require.Nil(t, app.pluginReceiveObserver)
	require.Nil(t, unexportedFieldForTest(t, app.messageApp, "sendHook"))
	requireNilInterfaceField(t, app.messageApp, "sendHook")
	requireNilInterfaceField(t, app.nodeAccess, "pluginHTTPRoutes")
	requireNilInterfaceField(t, app.nodeAccess, "pluginManagement")
	requireNilInterfaceField(t, app.nodeAccess, "pluginCommitted")
	requireNilInterfaceField(t, app.api, "pluginRoutes")
	requireNilInterfaceField(t, app.managementApp, "plugins")
	requireNilInterfaceField(t, app.managementApp, "pluginBindings")
	require.NotContains(t, appLifecycleComponentNames(app.lifecycleComponents(false)), appLifecyclePluginRuntime)
}

func requireNilInterfaceField(t *testing.T, target any, name string) {
	t.Helper()
	value := reflect.ValueOf(target)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	field := value.FieldByName(name)
	require.Truef(t, field.IsValid(), "%T is missing field %s", target, name)
	require.Truef(t, field.Kind() == reflect.Interface, "%T.%s must be an interface field", target, name)
	require.Truef(t, field.IsNil(), "%T.%s interface must be nil when plugins are disabled", target, name)
}

func TestNewWiresPluginSubsystemWhenEnabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.Plugin.Enable = true
	cfg.API.ListenAddr = "127.0.0.1:0"
	cfg.Manager = validManagerConfigForTest()
	cfg.Manager.ListenAddr = "127.0.0.1:0"

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.Stop()) })

	require.NotNil(t, app.pluginRuntime)
	require.NotNil(t, app.pluginApp)
	require.NotNil(t, app.pluginAccess)
	require.NotNil(t, app.pluginReceiveObserver)

	sendHook, ok := unexportedFieldForTest(t, app.messageApp, "sendHook").(messageusecase.SendHook)
	require.Truef(t, ok, "message send hook should be plugin.App, got %T", sendHook)
	require.Same(t, app.pluginApp, sendHook)

	apiRoutes, ok := unexportedFieldForTest(t, app.api, "pluginRoutes").(*plugin.App)
	require.Truef(t, ok, "api plugin routes should be plugin.App, got %T", apiRoutes)
	require.Same(t, app.pluginApp, apiRoutes)

	nodeRoutes, ok := unexportedFieldForTest(t, app.nodeAccess, "pluginHTTPRoutes").(*plugin.App)
	require.Truef(t, ok, "node plugin HTTP routes should be plugin.App, got %T", nodeRoutes)
	require.Same(t, app.pluginApp, nodeRoutes)

	nodeManagement, ok := unexportedFieldForTest(t, app.nodeAccess, "pluginManagement").(*plugin.App)
	require.Truef(t, ok, "node plugin management should be plugin.App, got %T", nodeManagement)
	require.Same(t, app.pluginApp, nodeManagement)

	nodeCommitted, ok := unexportedFieldForTest(t, app.nodeAccess, "pluginCommitted").(*plugin.App)
	require.Truef(t, ok, "node plugin committed provider should be plugin.App, got %T", nodeCommitted)
	require.Same(t, app.pluginApp, nodeCommitted)

	managementPlugins, ok := unexportedFieldForTest(t, app.managementApp, "plugins").(pluginManagementNodeClient)
	require.Truef(t, ok, "management plugin node client should be pluginManagementNodeClient, got %T", managementPlugins)
	require.Same(t, app.pluginApp, managementPlugins.local)
	require.Same(t, app.nodeClient, managementPlugins.remote)

	managementBindings, ok := unexportedFieldForTest(t, app.managementApp, "pluginBindings").(*plugin.App)
	require.Truef(t, ok, "management plugin bindings should be plugin.App, got %T", managementBindings)
	require.Same(t, app.pluginApp, managementBindings)
}
