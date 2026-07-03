package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/stretchr/testify/require"
)

func TestStartPluginReturnsSuccessAndRegistersMappedMethods(t *testing.T) {
	runtime := &recordingRuntime{}
	app, err := NewApp(Options{Runtime: runtime, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	resp, err := app.StartPlugin(context.Background(), &pluginproto.PluginInfo{
		No:               "persist",
		Name:             "Persist",
		Version:          "v1",
		Methods:          []string{"PersistAfter", "Unknown", "Send"},
		Priority:         7,
		PersistAfterSync: true,
	}, "persist")
	require.NoError(t, err)
	require.True(t, resp.GetSuccess())

	registered := runtime.registeredPlugins()
	require.Len(t, registered, 1)
	require.Equal(t, ObservedPlugin{
		No:               "persist",
		Name:             "Persist",
		Version:          "v1",
		Methods:          []Method{MethodPersistAfter, MethodSend},
		Priority:         7,
		PersistAfterSync: true,
		Status:           StatusRunning,
		Enabled:          true,
	}, registered[0])
}

func TestStartPluginRejectsCallerMismatch(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	_, err = app.StartPlugin(context.Background(), &pluginproto.PluginInfo{No: "plugin-b"}, "plugin-a")
	require.True(t, errors.Is(err, ErrPluginCallerMismatch))
}

func TestNewAppRequiresRuntimeAndInvoker(t *testing.T) {
	_, err := NewApp(Options{Invoker: &recordingInvoker{}})
	require.True(t, errors.Is(err, ErrRuntimeRequired))

	_, err = NewApp(Options{Runtime: &recordingRuntime{}})
	require.True(t, errors.Is(err, ErrInvokerRequired))
}

func TestStartPluginRequiresPluginNo(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	_, err = app.StartPlugin(context.Background(), nil, "")
	require.True(t, errors.Is(err, ErrPluginNoRequired))

	_, err = app.StartPlugin(context.Background(), &pluginproto.PluginInfo{No: "   "}, "")
	require.True(t, errors.Is(err, ErrPluginNoRequired))
}

func TestClosePluginMarksClosedAndRejectsCallerMismatch(t *testing.T) {
	runtime := &recordingRuntime{}
	app, err := NewApp(Options{Runtime: runtime, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	err = app.ClosePlugin(context.Background(), "persist", "persist")
	require.NoError(t, err)
	require.Equal(t, []string{"persist"}, runtime.closedPlugins())

	err = app.ClosePlugin(context.Background(), "persist", "other")
	require.True(t, errors.Is(err, ErrPluginCallerMismatch))
}

func TestPersistAfterCommittedInvokesRunningCandidatesByPriority(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{
		{No: "low", Methods: []Method{MethodPersistAfter}, Priority: 1, Status: StatusRunning, Enabled: true},
		{No: "send-only", Methods: []Method{MethodSend}, Priority: 100, Status: StatusRunning, Enabled: true},
		{No: "offline", Methods: []Method{MethodPersistAfter}, Priority: 99, Status: StatusOffline, Enabled: true},
		{No: "high", Methods: []Method{MethodPersistAfter}, Priority: 9, Status: StatusRunning, Enabled: true},
	}}
	invoker := &recordingInvoker{}
	app, err := NewApp(Options{Runtime: runtime, Invoker: invoker})
	require.NoError(t, err)

	err = app.PersistAfterCommitted(context.Background(), pluginevents.PersistAfterCommitted{
		MessageID: 1, MessageSeq: 2, ChannelID: "room", ChannelType: 2, Payload: []byte("hello"),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"high", "low"}, invoker.asyncPluginNos())
}

func TestPersistAfterPluginCandidatesFiltersDisabledAndSortsTiesByPluginNo(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{
		{No: "tie-b", Methods: []Method{MethodPersistAfter}, Priority: 9, Status: StatusRunning, Enabled: true},
		{No: "disabled", Methods: []Method{MethodPersistAfter}, Priority: 100, Status: StatusRunning, Enabled: false},
		{No: "tie-a", Methods: []Method{MethodPersistAfter}, Priority: 9, Status: StatusRunning, Enabled: true},
		{No: "low", Methods: []Method{MethodPersistAfter}, Priority: 1, Status: StatusRunning, Enabled: true},
	}}
	app, err := NewApp(Options{Runtime: runtime, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	candidates, err := app.PersistAfterPluginCandidates(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"tie-a", "tie-b", "low"}, pluginNos(candidates))
}

func TestPersistAfterPluginCandidatesDeepCopiesMethods(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{{
		No: "persist", Methods: []Method{MethodPersistAfter}, Priority: 1, Status: StatusRunning, Enabled: true,
	}}}
	app, err := NewApp(Options{Runtime: runtime, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	candidates, err := app.PersistAfterPluginCandidates(context.Background())
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	candidates[0].Methods[0] = MethodSend

	candidates, err = app.PersistAfterPluginCandidates(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"persist"}, pluginNos(candidates))
	require.Equal(t, []Method{MethodPersistAfter}, candidates[0].Methods)
}

func TestInvokePersistAfterUsesSyncRequestWhenPluginRequiresIt(t *testing.T) {
	invoker := &recordingInvoker{}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: invoker})
	require.NoError(t, err)

	err = app.InvokePersistAfter(context.Background(), ObservedPlugin{
		No: "sync", PersistAfterSync: true, Status: StatusRunning, Enabled: true,
	}, &pluginproto.MessageBatch{})
	require.NoError(t, err)
	require.Equal(t, []string{"sync:/plugin/persist_after"}, invoker.requests)
	require.Empty(t, invoker.sends)
}

func TestInvokePersistAfterAsyncUsesPersistAfterMsgType(t *testing.T) {
	invoker := &recordingInvoker{}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: invoker})
	require.NoError(t, err)

	err = app.InvokePersistAfter(context.Background(), ObservedPlugin{
		No: "async", Status: StatusRunning, Enabled: true,
	}, &pluginproto.MessageBatch{})
	require.NoError(t, err)

	sends := invoker.sentMessages()
	require.Len(t, sends, 1)
	require.Equal(t, "async", sends[0].pluginNo)
	require.Equal(t, MsgTypePersistAfter, sends[0].msgType)
	require.Equal(t, uint32(2), sends[0].msgType)
}

func TestPersistAfterCommittedContinuesAfterFailureAndWrapsPluginError(t *testing.T) {
	sentinel := errors.New("sentinel persist failure")
	runtime := &recordingRuntime{plugins: []ObservedPlugin{
		{No: "bad", Methods: []Method{MethodPersistAfter}, Priority: 2, Status: StatusRunning, Enabled: true},
		{No: "good", Methods: []Method{MethodPersistAfter}, Priority: 1, Status: StatusRunning, Enabled: true},
	}}
	invoker := &recordingInvoker{sendErrors: map[string]error{"bad": sentinel}}
	app, err := NewApp(Options{Runtime: runtime, Invoker: invoker})
	require.NoError(t, err)

	err = app.PersistAfterCommitted(context.Background(), pluginevents.PersistAfterCommitted{
		MessageID: 1, MessageSeq: 2, ChannelID: "room", ChannelType: 2, Payload: []byte("hello"),
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, sentinel))
	require.Contains(t, err.Error(), "bad")
	require.Equal(t, []string{"bad", "good"}, invoker.asyncPluginNos())
}

func TestPersistAfterCommittedJoinedErrorPreservesMultiplePluginFailures(t *testing.T) {
	errBadA := errors.New("bad-a persist failure")
	errBadB := errors.New("bad-b persist failure")
	runtime := &recordingRuntime{plugins: []ObservedPlugin{
		{No: "bad-a", Methods: []Method{MethodPersistAfter}, Priority: 3, Status: StatusRunning, Enabled: true},
		{No: "good", Methods: []Method{MethodPersistAfter}, Priority: 2, Status: StatusRunning, Enabled: true},
		{No: "bad-b", Methods: []Method{MethodPersistAfter}, Priority: 1, Status: StatusRunning, Enabled: true},
	}}
	invoker := &recordingInvoker{sendErrors: map[string]error{
		"bad-a": errBadA,
		"bad-b": errBadB,
	}}
	app, err := NewApp(Options{Runtime: runtime, Invoker: invoker})
	require.NoError(t, err)

	err = app.PersistAfterCommitted(context.Background(), pluginevents.PersistAfterCommitted{
		MessageID: 1, MessageSeq: 2, ChannelID: "room", ChannelType: 2, Payload: []byte("hello"),
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, errBadA))
	require.True(t, errors.Is(err, errBadB))
	require.Contains(t, err.Error(), "bad-a")
	require.Contains(t, err.Error(), "bad-b")
	require.Equal(t, []string{"bad-a", "good", "bad-b"}, invoker.asyncPluginNos())
}

type recordingRuntime struct {
	mu          sync.Mutex
	plugins     []ObservedPlugin
	registered  []ObservedPlugin
	closed      []string
	restarted   []string
	uninstalled []string
	sandboxDirs map[string]string
}

func (r *recordingRuntime) RegisterObserved(_ context.Context, plugin ObservedPlugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registered = append(r.registered, plugin)
	for i := range r.plugins {
		if r.plugins[i].No == plugin.No {
			r.plugins[i] = plugin
			return nil
		}
	}
	r.plugins = append(r.plugins, plugin)
	return nil
}

func (r *recordingRuntime) MarkClosed(_ context.Context, pluginNo string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = append(r.closed, pluginNo)
	for i := range r.plugins {
		if r.plugins[i].No == pluginNo {
			r.plugins[i].Status = StatusOffline
			return nil
		}
	}
	return nil
}

func (r *recordingRuntime) List() []ObservedPlugin {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ObservedPlugin, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		out = append(out, cloneObservedPlugin(plugin))
	}
	return out
}

func (r *recordingRuntime) registeredPlugins() []ObservedPlugin {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ObservedPlugin, 0, len(r.registered))
	for _, plugin := range r.registered {
		out = append(out, cloneObservedPlugin(plugin))
	}
	return out
}

func (r *recordingRuntime) closedPlugins() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.closed...)
}

func (r *recordingRuntime) SandboxDir(no string) (string, error) {
	return r.sandboxDirs[no], nil
}

func (r *recordingRuntime) Restart(_ context.Context, no string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restarted = append(r.restarted, no)
	return nil
}

func (r *recordingRuntime) Uninstall(_ context.Context, no string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uninstalled = append(r.uninstalled, no)
	for i := range r.plugins {
		if r.plugins[i].No == no {
			r.plugins[i].Status = StatusDisabled
			r.plugins[i].Enabled = false
		}
	}
	return nil
}

type recordingInvoker struct {
	mu            sync.Mutex
	requests      []string
	requestBodies [][]byte
	sends         []recordingSend
	requestErrors map[string]error
	sendErrors    map[string]error
}

type recordingSend struct {
	pluginNo string
	msgType  uint32
	body     []byte
}

func (r *recordingInvoker) RequestPlugin(_ context.Context, no, path string, body []byte) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, fmt.Sprintf("%s:%s", no, path))
	r.requestBodies = append(r.requestBodies, append([]byte(nil), body...))
	if err := r.requestErrors[no]; err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *recordingInvoker) SendPlugin(no string, msgType uint32, body []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, recordingSend{
		pluginNo: no,
		msgType:  msgType,
		body:     append([]byte(nil), body...),
	})
	if err := r.sendErrors[no]; err != nil {
		return err
	}
	return nil
}

func (r *recordingInvoker) asyncPluginNos() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	nos := make([]string, 0, len(r.sends))
	for _, send := range r.sends {
		nos = append(nos, send.pluginNo)
	}
	return nos
}

func (r *recordingInvoker) sentMessages() []recordingSend {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordingSend(nil), r.sends...)
}

func pluginNos(plugins []ObservedPlugin) []string {
	nos := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		nos = append(nos, plugin.No)
	}
	return nos
}
