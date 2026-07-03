package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// App orchestrates plugin lifecycle, config, and hook selection usecases.
type App struct {
	runtime          Runtime
	store            DesiredStore
	bindingStore     BindingStore
	bindingCache     *BindingCache
	bindingMu        sync.RWMutex
	bindingEpoch     uint64
	invoker          Invoker
	messages         MessageSender
	messageReader    MessageReader
	clusterReader    ClusterReader
	channelOwners    ChannelOwnerReader
	conversations    ConversationReader
	httpForwarder    HTTPForwarder
	httpForwardLimit int64
	failOpen         bool
	systemUIDs       SystemUIDChecker
	defaultSenderUID string
	receiveDedupeTTL time.Duration
	receiveDedupe    map[string]time.Time
	receiveDedupeMu  sync.Mutex
	nodeID           uint64
	clock            func() time.Time
	logger           wklog.Logger
}

// NewApp creates a plugin usecase with explicit ports.
func NewApp(opts Options) (*App, error) {
	if opts.Runtime == nil {
		return nil, ErrRuntimeRequired
	}
	if opts.DesiredStore == nil {
		return nil, ErrDesiredStoreRequired
	}
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	if opts.ReceiveDedupeTTL <= 0 {
		opts.ReceiveDedupeTTL = 5 * time.Minute
	}
	return &App{
		runtime:          opts.Runtime,
		store:            opts.DesiredStore,
		bindingStore:     opts.BindingStore,
		bindingCache:     opts.BindingCache,
		invoker:          opts.Invoker,
		messages:         opts.Messages,
		messageReader:    opts.MessageReader,
		clusterReader:    opts.ClusterReader,
		channelOwners:    opts.ChannelOwners,
		conversations:    opts.Conversations,
		httpForwarder:    opts.HTTPForwarder,
		httpForwardLimit: opts.HTTPForwardMaxBodyBytes,
		failOpen:         opts.FailOpen,
		systemUIDs:       opts.SystemUIDs,
		defaultSenderUID: opts.DefaultSenderUID,
		receiveDedupeTTL: opts.ReceiveDedupeTTL,
		receiveDedupe:    make(map[string]time.Time),
		nodeID:           opts.NodeID,
		clock:            clock,
		logger:           opts.Logger,
	}, nil
}

// StartPlugin records plugin startup metadata and returns node-local startup settings.
func (a *App) StartPlugin(ctx context.Context, info *pluginproto.PluginInfo, callerUID string) (*pluginproto.StartupResp, error) {
	if info == nil {
		return nil, ErrPluginInfoRequired
	}
	if info.GetNo() == "" {
		return nil, ErrPluginNoRequired
	}
	if err := validatePluginNo(info.GetNo()); err != nil {
		return nil, err
	}
	if callerUID == "" {
		return nil, ErrPluginIdentityRequired
	}
	if callerUID != info.GetNo() {
		return nil, fmt.Errorf("%w: caller %q plugin %q", ErrPluginIdentityMismatch, callerUID, info.GetNo())
	}

	sandboxDir, err := a.runtime.SandboxDir(info.GetNo())
	if err != nil {
		return nil, fmt.Errorf("resolve plugin sandbox %q: %w", info.GetNo(), err)
	}
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return nil, fmt.Errorf("create plugin sandbox %q: %w", sandboxDir, err)
	}

	configTemplateRaw, err := marshalConfigTemplate(info.GetConfigTemplate())
	if err != nil {
		return nil, err
	}
	observed := ObservedPlugin{
		No:                info.GetNo(),
		Name:              info.GetName(),
		Version:           info.GetVersion(),
		Methods:           methodsFromStrings(info.GetMethods()),
		Priority:          int(info.GetPriority()),
		PersistAfterSync:  info.GetPersistAfterSync(),
		ReplySync:         info.GetReplySync(),
		ConfigTemplateRaw: configTemplateRaw,
		Status:            StatusRunning,
		Enabled:           true,
		LastSeenAt:        a.now(),
	}

	state, err := a.store.Get(ctx, info.GetNo())
	if err != nil && !errors.Is(err, ErrDesiredPluginNotFound) {
		return nil, fmt.Errorf("load desired plugin %q: %w", info.GetNo(), err)
	}
	enabled := true
	status := StatusRunning
	if err == nil && !state.Enabled {
		enabled = false
		status = StatusDisabled
	}
	observed.Enabled = enabled
	observed.Status = status
	if !enabled {
		observed.PID = 0
	}
	if err := a.runtime.RegisterObserved(ctx, observed); err != nil {
		return nil, fmt.Errorf("register observed plugin %q: %w", info.GetNo(), err)
	}
	var config []byte
	if err == nil && len(state.Config) > 0 {
		config = append([]byte(nil), state.Config...)
	}
	return &pluginproto.StartupResp{
		NodeId:     a.nodeID,
		Success:    true,
		SandboxDir: sandboxDir,
		Config:     config,
	}, nil
}

// ClosePlugin marks an observed plugin connection offline on this node.
func (a *App) ClosePlugin(ctx context.Context, no string, _ string) error {
	if no == "" {
		return ErrPluginNoRequired
	}
	if err := validatePluginNo(no); err != nil {
		return err
	}
	observed, ok := a.runtime.Get(no)
	if !ok {
		return nil
	}
	observed.Status = StatusOffline
	observed.PID = 0
	observed.LastSeenAt = a.now()
	return a.runtime.RegisterObserved(ctx, observed)
}

// ListLocalPlugins returns node-local observed plugins with desired config applied.
func (a *App) ListLocalPlugins(ctx context.Context) (LocalPluginList, error) {
	observed := a.runtime.List()
	sort.Slice(observed, func(i, j int) bool { return observed[i].No < observed[j].No })
	plugins := make([]LocalPlugin, 0, len(observed))
	for _, plugin := range observed {
		detail, err := a.localPluginFromObserved(ctx, plugin)
		if err != nil {
			return LocalPluginList{}, err
		}
		plugins = append(plugins, detail)
	}
	return LocalPluginList{NodeID: a.nodeID, Plugins: plugins}, nil
}

// GetLocalPlugin returns one node-local observed plugin with desired config applied.
func (a *App) GetLocalPlugin(ctx context.Context, no string) (LocalPluginDetail, error) {
	if no == "" {
		return LocalPluginDetail{}, ErrPluginNoRequired
	}
	if err := validatePluginNo(no); err != nil {
		return LocalPluginDetail{}, err
	}
	observed, ok := a.runtime.Get(no)
	if !ok {
		return LocalPluginDetail{}, fmt.Errorf("%w: %s", ErrPluginNotFound, no)
	}
	return a.localPluginFromObserved(ctx, observed)
}

// RestartLocalPlugin restarts one local plugin process and returns its latest detail.
func (a *App) RestartLocalPlugin(ctx context.Context, no string) (LocalPluginDetail, error) {
	if no == "" {
		return LocalPluginDetail{}, ErrPluginNoRequired
	}
	if err := validatePluginNo(no); err != nil {
		return LocalPluginDetail{}, err
	}
	if err := a.runtime.Restart(ctx, no); err != nil {
		return LocalPluginDetail{}, err
	}
	return a.GetLocalPlugin(ctx, no)
}

// UninstallLocalPlugin disables and removes one local plugin process.
func (a *App) UninstallLocalPlugin(ctx context.Context, no string) error {
	if no == "" {
		return ErrPluginNoRequired
	}
	if err := validatePluginNo(no); err != nil {
		return err
	}
	existing, err := a.store.Get(ctx, no)
	if err != nil && !errors.Is(err, ErrDesiredPluginNotFound) {
		return fmt.Errorf("load desired plugin %q: %w", no, err)
	}
	now := a.now()
	state := DesiredPlugin{No: no, Enabled: false, CreatedAt: now, UpdatedAt: now}
	if err == nil {
		state = existing
		state.Enabled = false
		state.UpdatedAt = now
		if state.CreatedAt.IsZero() {
			state.CreatedAt = now
		}
	}
	if err := a.store.Save(ctx, state); err != nil {
		return fmt.Errorf("save disabled desired plugin %q: %w", no, err)
	}
	return a.runtime.Uninstall(ctx, no)
}

// SendPluginCandidates returns running local Send plugins in hook order.
func (a *App) SendPluginCandidates(ctx context.Context) ([]ObservedPlugin, error) {
	return a.runningCandidates(ctx, MethodSend)
}

// PersistAfterPluginCandidates returns running local PersistAfter plugins in hook order.
func (a *App) PersistAfterPluginCandidates(ctx context.Context) ([]ObservedPlugin, error) {
	return a.runningCandidates(ctx, MethodPersistAfter)
}

// BoundReceivePlugin returns the highest-priority running Receive plugin in the bound set.
func (a *App) BoundReceivePlugin(ctx context.Context, boundPluginNos []string) (ObservedPlugin, bool, error) {
	plugins, err := a.applyDesiredEnabled(ctx, a.runtime.List())
	if err != nil {
		return ObservedPlugin{}, false, err
	}
	plugin, ok := SelectReceivePlugin(plugins, boundPluginNos)
	return plugin, ok, nil
}

func (a *App) localPluginFromObserved(ctx context.Context, observed ObservedPlugin) (LocalPluginDetail, error) {
	state, err := a.store.Get(ctx, observed.No)
	if err != nil && !errors.Is(err, ErrDesiredPluginNotFound) {
		return LocalPluginDetail{}, fmt.Errorf("load desired plugin %q: %w", observed.No, err)
	}
	var desired *DesiredPlugin
	if err == nil {
		desired = &state
	}
	return a.localPluginFromObservedAndDesired(observed, desired)
}

func (a *App) localPluginFromObservedAndDesired(observed ObservedPlugin, desired *DesiredPlugin) (LocalPluginDetail, error) {
	template, err := decodeConfigTemplate(observed.ConfigTemplateRaw)
	if err != nil {
		return LocalPluginDetail{}, fmt.Errorf("decode config template %q: %w", observed.No, err)
	}
	var config map[string]any
	var createdAt *time.Time
	var updatedAt *time.Time
	enabled := observed.Enabled
	status := observed.Status
	if desired != nil {
		enabled = desired.Enabled
		if !enabled {
			status = StatusDisabled
		}
		config, err = redactedConfig(desired.Config, template)
		if err != nil {
			return LocalPluginDetail{}, fmt.Errorf("decode desired config %q: %w", observed.No, err)
		}
		if !desired.CreatedAt.IsZero() {
			created := desired.CreatedAt
			createdAt = &created
		}
		if !desired.UpdatedAt.IsZero() {
			updated := desired.UpdatedAt
			updatedAt = &updated
		}
	}
	return LocalPluginDetail{
		NodeID:           a.nodeID,
		No:               observed.No,
		Name:             observed.Name,
		Version:          observed.Version,
		ConfigTemplate:   template,
		Config:           config,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		Status:           status,
		Enabled:          enabled,
		Methods:          append([]Method(nil), observed.Methods...),
		Priority:         observed.Priority,
		PersistAfterSync: observed.PersistAfterSync,
		ReplySync:        observed.ReplySync,
		IsAI:             isAI(observed),
		PID:              observed.PID,
		LastSeenAt:       observed.LastSeenAt,
		LastError:        observed.LastError,
	}, nil
}

func (a *App) now() time.Time {
	return a.clock().UTC()
}

func marshalConfigTemplate(template *pluginproto.ConfigTemplate) ([]byte, error) {
	if template == nil {
		return nil, nil
	}
	data, err := template.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal plugin config template: %w", err)
	}
	return data, nil
}

func methodsFromStrings(methods []string) []Method {
	out := make([]Method, 0, len(methods))
	for _, method := range methods {
		if method == "" {
			continue
		}
		out = append(out, Method(method))
	}
	return out
}

func isAI(plugin ObservedPlugin) uint8 {
	if hasMethod(plugin, MethodReceive) {
		return 1
	}
	return 0
}

func (a *App) runningCandidates(ctx context.Context, method Method) ([]ObservedPlugin, error) {
	plugins, err := a.applyDesiredEnabled(ctx, a.runtime.List())
	if err != nil {
		return nil, err
	}
	return RunningPluginsByMethod(plugins, method), nil
}

func (a *App) applyDesiredEnabled(ctx context.Context, observed []ObservedPlugin) ([]ObservedPlugin, error) {
	plugins := make([]ObservedPlugin, 0, len(observed))
	for _, plugin := range observed {
		effective, err := a.applyDesiredEnabledToPlugin(ctx, plugin)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, effective)
	}
	return plugins, nil
}

func (a *App) applyDesiredEnabledToPlugin(ctx context.Context, observed ObservedPlugin) (ObservedPlugin, error) {
	state, err := a.store.Get(ctx, observed.No)
	if errors.Is(err, ErrDesiredPluginNotFound) {
		return observed, nil
	}
	if err != nil {
		return ObservedPlugin{}, fmt.Errorf("load desired plugin %q: %w", observed.No, err)
	}
	if state.Enabled {
		return observed, nil
	}
	observed.Enabled = false
	observed.Status = StatusDisabled
	return observed, nil
}

func validatePluginNo(no string) error {
	if no == "." || no == ".." || !pluginNoPattern.MatchString(no) {
		return fmt.Errorf("%w: %q", ErrInvalidPluginNo, no)
	}
	return nil
}
