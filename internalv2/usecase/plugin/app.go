package plugin

import (
	"context"
	"strings"
	"time"

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
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.ReceiveDedupeTTL <= 0 {
		opts.ReceiveDedupeTTL = 5 * time.Minute
	}
	return &App{
		runtime:          opts.Runtime,
		invoker:          opts.Invoker,
		desired:          opts.DesiredStore,
		messages:         opts.Messages,
		messageReader:    opts.MessageReader,
		clusterReader:    opts.ClusterReader,
		channelOwners:    opts.ChannelOwners,
		conversations:    opts.Conversations,
		httpForwarder:    opts.HTTPForwarder,
		httpForwardLimit: opts.HTTPForwardMaxBodyBytes,
		receiveBindings:  opts.ReceiveBindings,
		systemUIDs:       opts.SystemUIDs,
		defaultSenderUID: strings.TrimSpace(opts.DefaultSenderUID),
		receiveDedupeTTL: opts.ReceiveDedupeTTL,
		receiveDedupe:    make(map[string]time.Time),
		desiredCache:     make(map[string]desiredCacheEntry),
		clock:            opts.Clock,
		failOpen:         opts.FailOpen,
		observer:         opts.Observer,
		logger:           opts.Logger,
		nodeID:           opts.NodeID,
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
	if err := validatePluginNo(no); err != nil {
		return nil, err
	}
	configTemplateRaw, err := marshalConfigTemplate(info.GetConfigTemplate())
	if err != nil {
		return nil, err
	}
	observed := observedFromPluginInfo(info)
	observed.No = no
	observed.ConfigTemplateRaw = configTemplateRaw
	observed.Status = StatusRunning
	observed.Enabled = true
	desired, found, err := a.desiredState(ctx, no)
	if err != nil {
		return nil, err
	}
	if found && !desired.Enabled {
		observed.Enabled = false
		observed.Status = StatusDisabled
		observed.PID = 0
	}
	if err := a.runtime.RegisterObserved(ctx, observed); err != nil {
		return nil, err
	}
	resp := &pluginproto.StartupResp{
		NodeId:     a.nodeID,
		Success:    true,
		SandboxDir: a.sandboxDir(no),
	}
	if found && len(desired.Config) > 0 {
		resp.Config = append([]byte(nil), desired.Config...)
	}
	return resp, nil
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
		case MethodSend, MethodPersistAfter, MethodReceive, MethodConfigUpdate:
			out = append(out, Method(method))
		}
	}
	return out
}
