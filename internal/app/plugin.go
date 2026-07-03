package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	accessplugin "github.com/WuKongIM/WuKongIM/internal/access/plugin"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	pluginhost "github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
)

func (a *App) buildPluginSubsystem(cfg Config, systemUIDs pluginusecase.SystemUIDChecker) error {
	socket := pluginhost.NewSocketServer(cfg.Plugin.SocketPath)
	invoker := pluginhost.NewInvoker(socket, pluginhost.WithTimeout(cfg.Plugin.Timeout))
	store := pluginhost.NewStore(cfg.Plugin.StateDir)
	runtime := pluginhost.NewRuntime(pluginhost.RuntimeOptions{
		Enable:     cfg.Plugin.Enable,
		HotReload:  cfg.Plugin.HotReload,
		Dir:        cfg.Plugin.Dir,
		SocketPath: cfg.Plugin.SocketPath,
		SandboxDir: cfg.Plugin.SandboxDir,
		StateDir:   cfg.Plugin.StateDir,
		Timeout:    cfg.Plugin.Timeout,
		Store:      store,
		Socket:     socket,
		Invoker:    invoker,
	})
	pluginApp, err := pluginusecase.NewApp(pluginusecase.Options{
		Runtime:          pluginRuntimeAdapter{runtime: runtime, sandboxDir: cfg.Plugin.SandboxDir},
		DesiredStore:     pluginDesiredStoreAdapter{store: store},
		BindingStore:     pluginusecase.NewSlotBindingStoreAdapter(a.store),
		Invoker:          invoker,
		Messages:         pluginMessageSender{app: a},
		MessageReader:    pluginMessageReader(a),
		ChannelOwners:    pluginChannelOwnerReader{channelLog: a.channelLog},
		HTTPForwarder:    a.nodeClient,
		FailOpen:         cfg.Plugin.FailOpen,
		SystemUIDs:       systemUIDs,
		DefaultSenderUID: userusecase.DefaultSystemUID,
		NodeID:           cfg.Node.ID,
		Logger:           a.logger.Named("plugin"),
	})
	if err != nil {
		return err
	}
	pluginAccess, err := accessplugin.NewServer(accessplugin.Options{
		Routes:       socket,
		Usecase:      pluginApp,
		MaxBodyBytes: accessplugin.DefaultHostRPCMaxBodyBytes,
		Timeout:      cfg.Plugin.Timeout,
		Logger:       a.logger.Named("access.plugin"),
	})
	if err != nil {
		return err
	}
	a.pluginRuntime = runtime
	a.pluginApp = pluginApp
	a.pluginAccess = pluginAccess
	return nil
}

type pluginSystemUIDCheckerRef struct {
	target pluginusecase.SystemUIDChecker
}

func (r *pluginSystemUIDCheckerRef) IsSystemUID(uid string) bool {
	if r == nil || r.target == nil {
		return false
	}
	return r.target.IsSystemUID(uid)
}

func pluginMessageReader(a *App) managerMessageReader {
	if a == nil {
		return managerMessageReader{}
	}
	return managerMessageReader{
		localNodeID: a.cfg.Node.ID,
		channelLog:  a.channelLogDB,
		metas:       a.store,
		remote:      a.nodeClient,
	}
}

type pluginRuntimeAdapter struct {
	runtime    *pluginhost.Runtime
	sandboxDir string
}

func (a pluginRuntimeAdapter) RegisterObserved(ctx context.Context, info pluginusecase.ObservedPlugin) error {
	if a.runtime == nil || a.runtime.Registry() == nil {
		return nil
	}
	a.runtime.Registry().Upsert(runtimeObservedFromUsecase(info))
	return nil
}

func (a pluginRuntimeAdapter) Get(no string) (pluginusecase.ObservedPlugin, bool) {
	if a.runtime == nil || a.runtime.Registry() == nil {
		return pluginusecase.ObservedPlugin{}, false
	}
	plugin, ok := a.runtime.Registry().Get(no)
	if !ok {
		return pluginusecase.ObservedPlugin{}, false
	}
	return usecaseObservedFromRuntime(plugin), true
}

func (a pluginRuntimeAdapter) List() []pluginusecase.ObservedPlugin {
	if a.runtime == nil || a.runtime.Registry() == nil {
		return nil
	}
	plugins := a.runtime.Registry().List()
	out := make([]pluginusecase.ObservedPlugin, 0, len(plugins))
	for _, plugin := range plugins {
		out = append(out, usecaseObservedFromRuntime(plugin))
	}
	return out
}

func (a pluginRuntimeAdapter) Restart(ctx context.Context, no string) error {
	if a.runtime == nil {
		return nil
	}
	return a.runtime.Restart(ctx, no)
}

func (a pluginRuntimeAdapter) Uninstall(ctx context.Context, no string) error {
	if a.runtime == nil {
		return nil
	}
	return a.runtime.Uninstall(ctx, no)
}

func (a pluginRuntimeAdapter) SandboxDir(no string) (string, error) {
	if no == "" {
		return "", pluginusecase.ErrPluginNoRequired
	}
	if err := validatePluginSandboxName(no); err != nil {
		return "", err
	}
	return filepath.Join(a.sandboxDir, no), nil
}

type pluginDesiredStoreAdapter struct {
	store *pluginhost.Store
}

func (a pluginDesiredStoreAdapter) Get(ctx context.Context, no string) (pluginusecase.DesiredPlugin, error) {
	if a.store == nil {
		return pluginusecase.DesiredPlugin{}, pluginusecase.ErrDesiredPluginNotFound
	}
	state, err := a.store.Load(no)
	if err != nil {
		if errors.Is(err, pluginhost.ErrDesiredStateNotFound) {
			return pluginusecase.DesiredPlugin{}, pluginusecase.ErrDesiredPluginNotFound
		}
		return pluginusecase.DesiredPlugin{}, err
	}
	return pluginusecase.DesiredPlugin{
		No:        state.No,
		Config:    append(json.RawMessage(nil), state.Config...),
		Enabled:   state.Enabled,
		CreatedAt: state.CreatedAt,
		UpdatedAt: state.UpdatedAt,
	}, nil
}

func (a pluginDesiredStoreAdapter) Save(ctx context.Context, state pluginusecase.DesiredPlugin) error {
	if a.store == nil {
		return nil
	}
	return a.store.Save(pluginhost.DesiredState{
		No:        state.No,
		Config:    append(json.RawMessage(nil), state.Config...),
		Enabled:   state.Enabled,
		CreatedAt: state.CreatedAt,
		UpdatedAt: state.UpdatedAt,
	})
}

func (a pluginDesiredStoreAdapter) Delete(ctx context.Context, no string) error {
	if a.store == nil {
		return nil
	}
	return a.store.Delete(no)
}

type pluginMessageSender struct {
	app *App
}

func (s pluginMessageSender) Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error) {
	if s.app == nil || s.app.messageApp == nil {
		return message.SendResult{}, pluginusecase.ErrMessageSenderRequired
	}
	return s.app.messageApp.Send(ctx, cmd)
}

type pluginManagementNodeClient struct {
	localNodeID uint64
	local       *pluginusecase.App
	remote      *accessnode.Client
}

func (c pluginManagementNodeClient) ListNodePlugins(ctx context.Context, nodeID uint64) (pluginusecase.LocalPluginList, error) {
	if nodeID == c.localNodeID {
		if c.local == nil {
			return pluginusecase.LocalPluginList{}, accessnode.ErrPluginManagementUnavailable
		}
		return c.local.ListLocalPlugins(ctx)
	}
	if c.remote == nil {
		return pluginusecase.LocalPluginList{}, accessnode.ErrPluginManagementUnavailable
	}
	return c.remote.ListNodePlugins(ctx, nodeID)
}

func (c pluginManagementNodeClient) GetNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	if nodeID == c.localNodeID {
		if c.local == nil {
			return pluginusecase.LocalPluginDetail{}, accessnode.ErrPluginManagementUnavailable
		}
		return c.local.GetLocalPlugin(ctx, pluginNo)
	}
	if c.remote == nil {
		return pluginusecase.LocalPluginDetail{}, accessnode.ErrPluginManagementUnavailable
	}
	return c.remote.GetNodePlugin(ctx, nodeID, pluginNo)
}

func (c pluginManagementNodeClient) UpdateNodePluginConfig(ctx context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (pluginusecase.LocalPluginDetail, error) {
	if nodeID == c.localNodeID {
		if c.local == nil {
			return pluginusecase.LocalPluginDetail{}, accessnode.ErrPluginManagementUnavailable
		}
		return c.local.UpdateLocalConfig(ctx, pluginNo, config)
	}
	if c.remote == nil {
		return pluginusecase.LocalPluginDetail{}, accessnode.ErrPluginManagementUnavailable
	}
	return c.remote.UpdateNodePluginConfig(ctx, nodeID, pluginNo, config)
}

func (c pluginManagementNodeClient) RestartNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	if nodeID == c.localNodeID {
		if c.local == nil {
			return pluginusecase.LocalPluginDetail{}, accessnode.ErrPluginManagementUnavailable
		}
		return c.local.RestartLocalPlugin(ctx, pluginNo)
	}
	if c.remote == nil {
		return pluginusecase.LocalPluginDetail{}, accessnode.ErrPluginManagementUnavailable
	}
	return c.remote.RestartNodePlugin(ctx, nodeID, pluginNo)
}

func (c pluginManagementNodeClient) UninstallNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) error {
	if nodeID == c.localNodeID {
		if c.local == nil {
			return accessnode.ErrPluginManagementUnavailable
		}
		return c.local.UninstallLocalPlugin(ctx, pluginNo)
	}
	if c.remote == nil {
		return accessnode.ErrPluginManagementUnavailable
	}
	return c.remote.UninstallNodePlugin(ctx, nodeID, pluginNo)
}

type pluginChannelOwnerReader struct {
	channelLog pluginCommittedOwnerResolver
}

func (r pluginChannelOwnerReader) ChannelOwnerNode(ctx context.Context, id channel.ChannelID) (uint64, error) {
	if r.channelLog == nil {
		return 0, pluginusecase.ErrChannelOwnerReaderRequired
	}
	status, err := r.channelLog.Status(id)
	if err != nil {
		return 0, err
	}
	return uint64(status.Leader), nil
}

func runtimeObservedFromUsecase(plugin pluginusecase.ObservedPlugin) pluginhost.ObservedPlugin {
	return pluginhost.ObservedPlugin{
		No:                plugin.No,
		Name:              plugin.Name,
		Version:           plugin.Version,
		Methods:           runtimeMethodsFromUsecase(plugin.Methods),
		Priority:          plugin.Priority,
		PersistAfterSync:  plugin.PersistAfterSync,
		ReplySync:         plugin.ReplySync,
		ConfigTemplateRaw: append([]byte(nil), plugin.ConfigTemplateRaw...),
		Status:            runtimeStatusFromUsecase(plugin.Status),
		Enabled:           plugin.Enabled,
		PID:               plugin.PID,
		LastSeenAt:        plugin.LastSeenAt,
		LastError:         plugin.LastError,
	}
}

func usecaseObservedFromRuntime(plugin pluginhost.ObservedPlugin) pluginusecase.ObservedPlugin {
	return pluginusecase.ObservedPlugin{
		No:                plugin.No,
		Name:              plugin.Name,
		Version:           plugin.Version,
		Methods:           usecaseMethodsFromRuntime(plugin.Methods),
		Priority:          plugin.Priority,
		PersistAfterSync:  plugin.PersistAfterSync,
		ReplySync:         plugin.ReplySync,
		ConfigTemplateRaw: append([]byte(nil), plugin.ConfigTemplateRaw...),
		Status:            usecaseStatusFromRuntime(plugin.Status),
		Enabled:           plugin.Enabled,
		PID:               plugin.PID,
		LastSeenAt:        plugin.LastSeenAt,
		LastError:         plugin.LastError,
	}
}

func runtimeMethodsFromUsecase(methods []pluginusecase.Method) []pluginhost.Method {
	out := make([]pluginhost.Method, 0, len(methods))
	for _, method := range methods {
		out = append(out, pluginhost.Method(method))
	}
	return out
}

func usecaseMethodsFromRuntime(methods []pluginhost.Method) []pluginusecase.Method {
	out := make([]pluginusecase.Method, 0, len(methods))
	for _, method := range methods {
		out = append(out, pluginusecase.Method(method))
	}
	return out
}

func runtimeStatusFromUsecase(status pluginusecase.Status) pluginhost.Status {
	return pluginhost.Status(status)
}

func usecaseStatusFromRuntime(status pluginhost.Status) pluginusecase.Status {
	return pluginusecase.Status(status)
}

func validatePluginSandboxName(no string) error {
	for _, part := range []string{".", ".."} {
		if no == part {
			return fmt.Errorf("%w: %q", pluginusecase.ErrInvalidPluginNo, no)
		}
	}
	if filepath.Base(no) != no {
		return fmt.Errorf("%w: %q", pluginusecase.ErrInvalidPluginNo, no)
	}
	return nil
}
