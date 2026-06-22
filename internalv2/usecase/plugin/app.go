package plugin

import (
	"context"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// NewApp creates a v2 plugin usecase with explicit runtime and invoker ports.
func NewApp(opts Options) (*App, error) {
	if opts.Runtime == nil {
		return nil, ErrRuntimeRequired
	}
	if opts.Invoker == nil {
		return nil, ErrInvokerRequired
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &App{
		runtime:          opts.Runtime,
		invoker:          opts.Invoker,
		messages:         opts.Messages,
		messageReader:    opts.MessageReader,
		clusterReader:    opts.ClusterReader,
		channelOwners:    opts.ChannelOwners,
		defaultSenderUID: strings.TrimSpace(opts.DefaultSenderUID),
		failOpen:         opts.FailOpen,
		observer:         opts.Observer,
		logger:           opts.Logger,
	}, nil
}

// StartPlugin records plugin startup metadata observed from the host RPC handshake.
func (a *App) StartPlugin(ctx context.Context, info *pluginproto.PluginInfo, callerUID string) (*pluginproto.StartupResp, error) {
	if info == nil || strings.TrimSpace(info.GetNo()) == "" {
		return nil, ErrPluginNoRequired
	}
	no := strings.TrimSpace(info.GetNo())
	if callerUID != "" && callerUID != no {
		return nil, ErrPluginCallerMismatch
	}
	observed := observedFromPluginInfo(info)
	observed.No = no
	observed.Status = StatusRunning
	observed.Enabled = true
	if err := a.runtime.RegisterObserved(ctx, observed); err != nil {
		return nil, err
	}
	return &pluginproto.StartupResp{Success: true}, nil
}

// ClosePlugin marks a plugin connection closed after the host RPC close handshake.
func (a *App) ClosePlugin(ctx context.Context, pluginNo string, callerUID string) error {
	no := strings.TrimSpace(pluginNo)
	if no == "" {
		return ErrPluginNoRequired
	}
	if callerUID != "" && callerUID != no {
		return ErrPluginCallerMismatch
	}
	return a.runtime.MarkClosed(ctx, no)
}

func observedFromPluginInfo(info *pluginproto.PluginInfo) ObservedPlugin {
	return ObservedPlugin{
		No:               strings.TrimSpace(info.GetNo()),
		Name:             info.GetName(),
		Version:          info.GetVersion(),
		Methods:          methodsFromStrings(info.GetMethods()),
		Priority:         int(info.GetPriority()),
		PersistAfterSync: info.GetPersistAfterSync(),
		ReplySync:        info.GetReplySync(),
	}
}

func methodsFromStrings(methods []string) []Method {
	out := make([]Method, 0, len(methods))
	for _, method := range methods {
		switch Method(method) {
		case MethodSend, MethodPersistAfter:
			out = append(out, Method(method))
		}
	}
	return out
}
