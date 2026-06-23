package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	runtimeplugin "github.com/WuKongIM/WuKongIM/internal/runtime/plugin"
	accessplugin "github.com/WuKongIM/WuKongIM/internalv2/access/plugin"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/pluginhook"
	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	userusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/user"
)

// pluginRuntimeAdapter adapts the reusable node-local runtime registry to the v2 plugin usecase port.
type pluginRuntimeAdapter struct {
	runtime    *runtimeplugin.Runtime
	sandboxDir string
}

// pluginPersistAfterWorker accepts one mapped PersistAfter event for asynchronous processing.
type pluginPersistAfterWorker interface {
	EnqueuePersistAfter(context.Context, pluginevents.PersistAfterCommitted)
}

// pluginReceiveWorker accepts one mapped Receive event for asynchronous processing.
type pluginReceiveWorker interface {
	EnqueueReceive(context.Context, pluginevents.ReceiveOffline)
}

// pluginPersistAfterEnqueuer adapts durable channelappend envelopes to plugin PersistAfter events.
type pluginPersistAfterEnqueuer struct {
	worker pluginPersistAfterWorker
}

// pluginReceiveObserver adapts durable offline recipients to plugin Receive events.
type pluginReceiveObserver struct {
	worker pluginReceiveWorker
}

type pluginMessageSender struct {
	app *App
}

func (s pluginMessageSender) Send(ctx context.Context, cmd messageusecase.SendCommand) (messageusecase.SendResult, error) {
	if s.app == nil || s.app.messages == nil {
		return messageusecase.SendResult{}, pluginusecase.ErrMessageSenderRequired
	}
	return s.app.messages.Send(ctx, cmd)
}

func (a *App) wirePluginSubsystem(nodeID uint64) error {
	if !a.cfg.Plugin.Enable || a.pluginRuntime != nil {
		return nil
	}

	socket := runtimeplugin.NewSocketServer(a.cfg.Plugin.SocketPath)
	invoker := runtimeplugin.NewInvoker(socket, runtimeplugin.WithTimeout(a.cfg.Plugin.Timeout))
	store := runtimeplugin.NewStore(a.cfg.Plugin.StateDir)
	runtime := runtimeplugin.NewRuntime(runtimeplugin.RuntimeOptions{
		Enable:     a.cfg.Plugin.Enable,
		HotReload:  a.cfg.Plugin.HotReload,
		Dir:        a.cfg.Plugin.Dir,
		SocketPath: a.cfg.Plugin.SocketPath,
		SandboxDir: a.cfg.Plugin.SandboxDir,
		StateDir:   a.cfg.Plugin.StateDir,
		Timeout:    a.cfg.Plugin.Timeout,
		Store:      store,
		Socket:     socket,
		Invoker:    invoker,
	})
	pluginOptions := pluginusecase.Options{
		Runtime:          pluginRuntimeAdapter{runtime: runtime, sandboxDir: a.cfg.Plugin.SandboxDir},
		Invoker:          invoker,
		DesiredStore:     pluginDesiredStoreAdapter{store: store},
		Messages:         pluginMessageSender{app: a},
		DefaultSenderUID: userusecase.DefaultSystemUID,
		SystemUIDs:       a.users,
		FailOpen:         a.cfg.Plugin.FailOpen,
		Observer:         a.pluginUsecaseObserver(),
		Logger:           a.logger.Named("plugin"),
		NodeID:           nodeID,
	}
	if bindingNode, ok := a.cluster.(clusterinfra.PluginBindingNode); ok {
		pluginOptions.ReceiveBindings = clusterinfra.NewPluginBindingReader(bindingNode)
	}
	if readNode, ok := a.cluster.(clusterinfra.ChannelMessageReadNode); ok {
		pluginOptions.MessageReader = clusterinfra.NewChannelMessageReader(readNode)
	}
	if clusterNode, ok := a.cluster.(clusterinfra.PluginClusterNode); ok {
		pluginOptions.ClusterReader = clusterinfra.NewPluginClusterReader(clusterNode)
	}
	if ownerNode, ok := a.cluster.(clusterinfra.PluginChannelOwnerNode); ok {
		pluginOptions.ChannelOwners = clusterinfra.NewPluginChannelOwnerReader(ownerNode)
	}
	if conversationNode, ok := a.cluster.(clusterinfra.PluginConversationNode); ok {
		pluginOptions.Conversations = clusterinfra.NewPluginConversationReader(conversationNode)
	}
	if pluginNode, ok := a.cluster.(clusterinfra.ManagementPluginNode); ok {
		pluginOptions.HTTPForwarder = clusterinfra.NewPluginHTTPForwarder(pluginNode)
	}
	plugins, err := pluginusecase.NewApp(pluginOptions)
	if err != nil {
		return fmt.Errorf("internalv2/app: create plugin usecase: %w", err)
	}
	if _, err := accessplugin.NewServer(accessplugin.Options{
		Routes:  socket,
		Usecase: plugins,
		Timeout: a.cfg.Plugin.Timeout,
	}); err != nil {
		return fmt.Errorf("internalv2/app: create plugin host rpc server: %w", err)
	}

	hook := pluginhook.NewWorker(pluginhook.Options{
		Usecase:        plugins,
		ReceiveUsecase: plugins,
		QueueSize:      a.cfg.Plugin.PersistAfterQueueSize,
		Workers:        a.cfg.Plugin.PersistAfterWorkers,
		Timeout:        a.cfg.Plugin.Timeout,
		Observer:       a.pluginHookObserver(),
		Logger:         a.logger.Named("plugin.hook"),
	})
	a.pluginRuntime = runtime
	a.plugins = plugins
	a.pluginHook = hook
	a.pluginPersistAfter = pluginPersistAfterEnqueuer{worker: hook}
	a.pluginReceive = pluginReceiveObserver{worker: hook}
	return nil
}

func (a pluginRuntimeAdapter) RegisterObserved(_ context.Context, plugin pluginusecase.ObservedPlugin) error {
	if a.runtime == nil || a.runtime.Registry() == nil {
		return pluginusecase.ErrRuntimeRequired
	}
	a.runtime.Registry().Upsert(runtimeplugin.ObservedPlugin{
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
	})
	return nil
}

func (a pluginRuntimeAdapter) MarkClosed(_ context.Context, pluginNo string) error {
	if a.runtime == nil || a.runtime.Registry() == nil {
		return pluginusecase.ErrRuntimeRequired
	}
	plugin, ok := a.runtime.Registry().Get(pluginNo)
	if !ok {
		plugin = runtimeplugin.ObservedPlugin{No: pluginNo}
	}
	plugin.Status = runtimeplugin.StatusOffline
	a.runtime.Registry().Upsert(plugin)
	return nil
}

func (a pluginRuntimeAdapter) List() []pluginusecase.ObservedPlugin {
	if a.runtime == nil || a.runtime.Registry() == nil {
		return nil
	}
	plugins := a.runtime.Registry().List()
	out := make([]pluginusecase.ObservedPlugin, 0, len(plugins))
	for _, plugin := range plugins {
		out = append(out, pluginusecase.ObservedPlugin{
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
		})
	}
	return out
}

func runtimeMethodsFromUsecase(methods []pluginusecase.Method) []runtimeplugin.Method {
	out := make([]runtimeplugin.Method, 0, len(methods))
	for _, method := range methods {
		switch method {
		case pluginusecase.MethodReceive, pluginusecase.MethodSend, pluginusecase.MethodPersistAfter, pluginusecase.MethodConfigUpdate:
			out = append(out, runtimeplugin.Method(method))
		}
	}
	return out
}

func usecaseMethodsFromRuntime(methods []runtimeplugin.Method) []pluginusecase.Method {
	out := make([]pluginusecase.Method, 0, len(methods))
	for _, method := range methods {
		switch method {
		case runtimeplugin.MethodReceive, runtimeplugin.MethodSend, runtimeplugin.MethodPersistAfter, runtimeplugin.MethodConfigUpdate:
			out = append(out, pluginusecase.Method(method))
		}
	}
	return out
}

func runtimeStatusFromUsecase(status pluginusecase.Status) runtimeplugin.Status {
	switch status {
	case pluginusecase.StatusStarting:
		return runtimeplugin.StatusStarting
	case pluginusecase.StatusRunning:
		return runtimeplugin.StatusRunning
	case pluginusecase.StatusError:
		return runtimeplugin.StatusError
	case pluginusecase.StatusDisabled:
		return runtimeplugin.StatusDisabled
	default:
		return runtimeplugin.StatusOffline
	}
}

func usecaseStatusFromRuntime(status runtimeplugin.Status) pluginusecase.Status {
	switch status {
	case runtimeplugin.StatusStarting:
		return pluginusecase.StatusStarting
	case runtimeplugin.StatusRunning:
		return pluginusecase.StatusRunning
	case runtimeplugin.StatusError:
		return pluginusecase.StatusError
	case runtimeplugin.StatusDisabled:
		return pluginusecase.StatusDisabled
	default:
		return pluginusecase.StatusOffline
	}
}

func (a pluginRuntimeAdapter) SandboxDir(no string) (string, error) {
	if a.runtime == nil || a.sandboxDir == "" {
		return "", pluginusecase.ErrRuntimeRequired
	}
	return filepath.Join(a.sandboxDir, no), nil
}

func (a pluginRuntimeAdapter) Restart(ctx context.Context, no string) error {
	if a.runtime == nil {
		return pluginusecase.ErrRuntimeRequired
	}
	return a.runtime.Restart(ctx, no)
}

func (a pluginRuntimeAdapter) Uninstall(ctx context.Context, no string) error {
	if a.runtime == nil {
		return pluginusecase.ErrRuntimeRequired
	}
	return a.runtime.Uninstall(ctx, no)
}

type pluginDesiredStoreAdapter struct {
	store *runtimeplugin.Store
}

func (a pluginDesiredStoreAdapter) Get(_ context.Context, no string) (pluginusecase.DesiredPlugin, error) {
	if a.store == nil {
		return pluginusecase.DesiredPlugin{}, pluginusecase.ErrDesiredPluginNotFound
	}
	state, err := a.store.Load(no)
	if err != nil {
		if errors.Is(err, runtimeplugin.ErrDesiredStateNotFound) {
			return pluginusecase.DesiredPlugin{}, pluginusecase.ErrDesiredPluginNotFound
		}
		return pluginusecase.DesiredPlugin{}, err
	}
	return pluginusecase.DesiredPlugin{
		No:        state.No,
		Config:    append([]byte(nil), state.Config...),
		Enabled:   state.Enabled,
		CreatedAt: state.CreatedAt,
		UpdatedAt: state.UpdatedAt,
	}, nil
}

func (a pluginDesiredStoreAdapter) Save(_ context.Context, state pluginusecase.DesiredPlugin) error {
	if a.store == nil {
		return pluginusecase.ErrDesiredStoreRequired
	}
	return a.store.Save(runtimeplugin.DesiredState{
		No:        state.No,
		Config:    append([]byte(nil), state.Config...),
		Enabled:   state.Enabled,
		CreatedAt: state.CreatedAt,
		UpdatedAt: state.UpdatedAt,
	})
}

func (a pluginDesiredStoreAdapter) Delete(_ context.Context, no string) error {
	if a.store == nil {
		return pluginusecase.ErrDesiredStoreRequired
	}
	return a.store.Delete(no)
}

// EnqueuePersistAfter converts one committed durable envelope into a plugin PersistAfter event.
func (e pluginPersistAfterEnqueuer) EnqueuePersistAfter(ctx context.Context, event channelappend.CommittedEnvelope) {
	if e.worker == nil {
		return
	}
	e.worker.EnqueuePersistAfter(ctx, pluginevents.PersistAfterCommitted{
		MessageID:         event.MessageID,
		MessageSeq:        event.MessageSeq,
		ChannelID:         event.ChannelID,
		ChannelType:       event.ChannelType,
		FromUID:           event.FromUID,
		SenderNodeID:      event.SenderNodeID,
		SenderSessionID:   event.SenderSessionID,
		ClientMsgNo:       event.ClientMsgNo,
		ServerTimestampMS: event.ServerTimestampMS,
		Payload:           append([]byte(nil), event.Payload...),
		RedDot:            event.RedDot,
		SyncOnce:          event.SyncOnce,
		MessageScopedUIDs: append([]string(nil), event.MessageScopedUIDs...),
	})
}

// ObserveOfflineRecipient converts one offline recipient into a plugin Receive event.
func (o pluginReceiveObserver) ObserveOfflineRecipient(ctx context.Context, event channelappend.OfflineRecipientEvent) {
	if o.worker == nil {
		return
	}
	source := event.Event
	o.worker.EnqueueReceive(ctx, pluginevents.ReceiveOffline{
		MessageID:         source.MessageID,
		MessageSeq:        source.MessageSeq,
		ChannelID:         source.ChannelID,
		ChannelType:       source.ChannelType,
		FromUID:           source.FromUID,
		UID:               event.UID,
		ClientMsgNo:       source.ClientMsgNo,
		ServerTimestampMS: source.ServerTimestampMS,
		Payload:           append([]byte(nil), source.Payload...),
		SyncOnce:          source.SyncOnce,
		NoPersist:         source.MessageSeq == 0,
		MessageScopedUIDs: append([]string(nil), source.MessageScopedUIDs...),
	})
}
