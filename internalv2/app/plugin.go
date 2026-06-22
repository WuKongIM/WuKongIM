package app

import (
	"context"
	"fmt"

	runtimeplugin "github.com/WuKongIM/WuKongIM/internal/runtime/plugin"
	accessplugin "github.com/WuKongIM/WuKongIM/internalv2/access/plugin"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/pluginhook"
	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	userusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/user"
)

// pluginRuntimeAdapter adapts the reusable node-local runtime registry to the v2 plugin usecase port.
type pluginRuntimeAdapter struct {
	runtime *runtimeplugin.Runtime
}

// pluginPersistAfterWorker accepts one mapped PersistAfter event for asynchronous processing.
type pluginPersistAfterWorker interface {
	EnqueuePersistAfter(context.Context, pluginevents.PersistAfterCommitted)
}

// pluginPersistAfterEnqueuer adapts durable channelappend envelopes to plugin PersistAfter events.
type pluginPersistAfterEnqueuer struct {
	worker pluginPersistAfterWorker
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
	_ = nodeID
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
	plugins, err := pluginusecase.NewApp(pluginusecase.Options{
		Runtime:          pluginRuntimeAdapter{runtime: runtime},
		Invoker:          invoker,
		Messages:         pluginMessageSender{app: a},
		DefaultSenderUID: userusecase.DefaultSystemUID,
		FailOpen:         a.cfg.Plugin.FailOpen,
		Observer:         a.pluginUsecaseObserver(),
		Logger:           a.logger.Named("plugin"),
	})
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
		Usecase:   plugins,
		QueueSize: a.cfg.Plugin.PersistAfterQueueSize,
		Workers:   a.cfg.Plugin.PersistAfterWorkers,
		Timeout:   a.cfg.Plugin.Timeout,
		Observer:  a.pluginHookObserver(),
		Logger:    a.logger.Named("plugin.persist_after"),
	})
	a.pluginRuntime = runtime
	a.plugins = plugins
	a.pluginHook = hook
	a.pluginPersistAfter = pluginPersistAfterEnqueuer{worker: hook}
	return nil
}

func (a pluginRuntimeAdapter) RegisterObserved(_ context.Context, plugin pluginusecase.ObservedPlugin) error {
	if a.runtime == nil || a.runtime.Registry() == nil {
		return pluginusecase.ErrRuntimeRequired
	}
	a.runtime.Registry().Upsert(runtimeplugin.ObservedPlugin{
		No:               plugin.No,
		Name:             plugin.Name,
		Version:          plugin.Version,
		Methods:          runtimeMethodsFromUsecase(plugin.Methods),
		Priority:         plugin.Priority,
		PersistAfterSync: plugin.PersistAfterSync,
		ReplySync:        plugin.ReplySync,
		Status:           runtimeStatusFromUsecase(plugin.Status),
		Enabled:          plugin.Enabled,
		PID:              plugin.PID,
		LastSeenAt:       plugin.LastSeenAt,
		LastError:        plugin.LastError,
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
			No:               plugin.No,
			Name:             plugin.Name,
			Version:          plugin.Version,
			Methods:          usecaseMethodsFromRuntime(plugin.Methods),
			Priority:         plugin.Priority,
			PersistAfterSync: plugin.PersistAfterSync,
			ReplySync:        plugin.ReplySync,
			Status:           usecaseStatusFromRuntime(plugin.Status),
			Enabled:          plugin.Enabled,
			PID:              plugin.PID,
			LastSeenAt:       plugin.LastSeenAt,
			LastError:        plugin.LastError,
		})
	}
	return out
}

func runtimeMethodsFromUsecase(methods []pluginusecase.Method) []runtimeplugin.Method {
	out := make([]runtimeplugin.Method, 0, len(methods))
	for _, method := range methods {
		switch method {
		case pluginusecase.MethodSend, pluginusecase.MethodPersistAfter:
			out = append(out, runtimeplugin.Method(method))
		}
	}
	return out
}

func usecaseMethodsFromRuntime(methods []runtimeplugin.Method) []pluginusecase.Method {
	out := make([]pluginusecase.Method, 0, len(methods))
	for _, method := range methods {
		switch method {
		case runtimeplugin.MethodSend, runtimeplugin.MethodPersistAfter:
			out = append(out, pluginusecase.Method(method))
		}
	}
	return out
}

func runtimeStatusFromUsecase(status pluginusecase.Status) runtimeplugin.Status {
	switch status {
	case pluginusecase.StatusRunning:
		return runtimeplugin.StatusRunning
	case pluginusecase.StatusDisabled:
		return runtimeplugin.StatusDisabled
	default:
		return runtimeplugin.StatusOffline
	}
}

func usecaseStatusFromRuntime(status runtimeplugin.Status) pluginusecase.Status {
	switch status {
	case runtimeplugin.StatusRunning:
		return pluginusecase.StatusRunning
	case runtimeplugin.StatusDisabled:
		return pluginusecase.StatusDisabled
	default:
		return pluginusecase.StatusOffline
	}
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
