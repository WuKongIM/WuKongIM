package plugin

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/stretchr/testify/require"
)

func TestBeforeSendRunsCandidatesInPriorityOrderAndMutatesPayload(t *testing.T) {
	runtime := &sendHookRuntime{plugins: []ObservedPlugin{
		{No: "low", Methods: []Method{MethodSend}, Priority: 1, Status: StatusRunning, Enabled: true},
		{No: "persist-only", Methods: []Method{MethodPersistAfter}, Priority: 100, Status: StatusRunning, Enabled: true},
		{No: "offline", Methods: []Method{MethodSend}, Priority: 99, Status: StatusOffline, Enabled: true},
		{No: "high", Methods: []Method{MethodSend}, Priority: 9, Status: StatusRunning, Enabled: true},
	}}
	invoker := &sendHookInvoker{
		responses: map[string]*pluginproto.SendPacket{
			"high": {Payload: []byte("high"), Reason: uint32(message.ReasonSuccess)},
			"low":  {Payload: []byte("low"), Reason: uint32(message.ReasonSuccess)},
		},
	}
	app, err := NewApp(Options{Runtime: runtime, Invoker: invoker})
	require.NoError(t, err)

	cmd, reason, err := app.BeforeSend(context.Background(), message.SendCommand{
		FromUID: "u1", DeviceID: "d1", DeviceFlag: 5, SenderSessionID: 42, ChannelID: "g1", ChannelType: 2, Payload: []byte("original"),
	})
	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, reason)
	require.Equal(t, []byte("low"), cmd.Payload)
	require.Equal(t, []string{"high:" + PathSend, "low:" + PathSend}, invoker.requestKeys())
	require.Equal(t, []string{"original", "high"}, invoker.requestPayloads())
	require.Equal(t, "u1", invoker.requests[0].packet.GetConn().GetUid())
	require.Equal(t, int64(42), invoker.requests[0].packet.GetConn().GetConnId())
	require.Equal(t, "d1", invoker.requests[0].packet.GetConn().GetDeviceId())
	require.Equal(t, uint32(5), invoker.requests[0].packet.GetConn().GetDeviceFlag())
}

func TestBeforeSendRejectsOnPluginReasonAndStopsChain(t *testing.T) {
	runtime := &sendHookRuntime{plugins: []ObservedPlugin{
		{No: "reject", Methods: []Method{MethodSend}, Priority: 10, Status: StatusRunning, Enabled: true},
		{No: "later", Methods: []Method{MethodSend}, Priority: 1, Status: StatusRunning, Enabled: true},
	}}
	invoker := &sendHookInvoker{
		responses: map[string]*pluginproto.SendPacket{
			"reject": {Reason: uint32(message.ReasonNotAllowSend)},
			"later":  {Payload: []byte("later"), Reason: uint32(message.ReasonSuccess)},
		},
	}
	app, err := NewApp(Options{Runtime: runtime, Invoker: invoker, FailOpen: true})
	require.NoError(t, err)

	cmd, reason, err := app.BeforeSend(context.Background(), message.SendCommand{FromUID: "u1", Payload: []byte("original")})
	require.NoError(t, err)
	require.Equal(t, message.ReasonNotAllowSend, reason)
	require.Equal(t, []byte("original"), cmd.Payload)
	require.Equal(t, []string{"reject:" + PathSend}, invoker.requestKeys())
}

func TestBeforeSendFailClosedAndFailOpen(t *testing.T) {
	sentinel := errors.New("plugin down")
	runtime := &sendHookRuntime{plugins: []ObservedPlugin{{
		No: "broken", Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true,
	}}}
	failClosed, err := NewApp(Options{
		Runtime: runtime,
		Invoker: &sendHookInvoker{errors: map[string]error{"broken": sentinel}},
	})
	require.NoError(t, err)

	cmd, reason, err := failClosed.BeforeSend(context.Background(), message.SendCommand{FromUID: "u1", Payload: []byte("original")})
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, message.ReasonSystemError, reason)
	require.Equal(t, []byte("original"), cmd.Payload)

	failOpen, err := NewApp(Options{
		Runtime:  runtime,
		Invoker:  &sendHookInvoker{errors: map[string]error{"broken": sentinel}},
		FailOpen: true,
	})
	require.NoError(t, err)

	cmd, reason, err = failOpen.BeforeSend(context.Background(), message.SendCommand{FromUID: "u1", Payload: []byte("original")})
	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, reason)
	require.Equal(t, []byte("original"), cmd.Payload)
}

func TestBeforeSendInvalidReasonReturnsSystemError(t *testing.T) {
	app, err := NewApp(Options{
		Runtime: &sendHookRuntime{plugins: []ObservedPlugin{{
			No: "bad", Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true,
		}}},
		Invoker: &sendHookInvoker{responses: map[string]*pluginproto.SendPacket{
			"bad": {Reason: 256},
		}},
	})
	require.NoError(t, err)

	_, reason, err := app.BeforeSend(context.Background(), message.SendCommand{FromUID: "u1", Payload: []byte("original")})
	require.NoError(t, err)
	require.Equal(t, message.ReasonSystemError, reason)
}

func TestBeforeSendClonesPayloadAcrossPluginBoundary(t *testing.T) {
	sourcePayload := []byte("source")
	responsePayload := []byte("response")
	invoker := &sendHookInvoker{responses: map[string]*pluginproto.SendPacket{
		"clone": {Payload: responsePayload, Reason: uint32(message.ReasonSuccess)},
	}}
	app, err := NewApp(Options{
		Runtime: &sendHookRuntime{plugins: []ObservedPlugin{{
			No: "clone", Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true,
		}}},
		Invoker: invoker,
	})
	require.NoError(t, err)

	cmd, reason, err := app.BeforeSend(context.Background(), message.SendCommand{FromUID: "u1", Payload: sourcePayload})
	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, reason)
	require.Equal(t, []byte("response"), cmd.Payload)

	sourcePayload[0] = 'S'
	responsePayload[0] = 'R'
	invoker.requests[0].packet.Payload[0] = 'Q'

	require.Equal(t, []byte("response"), cmd.Payload)
	require.Equal(t, []byte("Source"), sourcePayload)
}

func TestBeforeSendObservesInvokeResult(t *testing.T) {
	observer := &sendHookObserver{}
	app, err := NewApp(Options{
		Runtime: &sendHookRuntime{plugins: []ObservedPlugin{{
			No: "observed", Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true,
		}}},
		Invoker: &sendHookInvoker{responses: map[string]*pluginproto.SendPacket{
			"observed": {Reason: uint32(message.ReasonSuccess)},
		}},
		Observer: observer,
	})
	require.NoError(t, err)

	_, reason, err := app.BeforeSend(context.Background(), message.SendCommand{FromUID: "u1", Payload: []byte("hello")})
	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, reason)
	require.Equal(t, []string{"ok"}, observer.results)
}

type sendHookRuntime struct {
	plugins []ObservedPlugin
}

func (r *sendHookRuntime) RegisterObserved(context.Context, ObservedPlugin) error {
	return nil
}

func (r *sendHookRuntime) MarkClosed(context.Context, string) error {
	return nil
}

func (r *sendHookRuntime) List() []ObservedPlugin {
	return append([]ObservedPlugin(nil), r.plugins...)
}

type sendHookInvoker struct {
	requests  []sendHookRequest
	responses map[string]*pluginproto.SendPacket
	errors    map[string]error
}

type sendHookRequest struct {
	pluginNo string
	path     string
	packet   *pluginproto.SendPacket
}

func (i *sendHookInvoker) RequestPlugin(_ context.Context, no, path string, body []byte) ([]byte, error) {
	var packet pluginproto.SendPacket
	if err := packet.Unmarshal(body); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}
	i.requests = append(i.requests, sendHookRequest{
		pluginNo: no,
		path:     path,
		packet:   &packet,
	})
	if err := i.errors[no]; err != nil {
		return nil, err
	}
	resp := i.responses[no]
	if resp == nil {
		resp = &pluginproto.SendPacket{Reason: uint32(message.ReasonSuccess)}
	}
	data, err := resp.Marshal()
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (i *sendHookInvoker) SendPlugin(string, uint32, []byte) error {
	return nil
}

func (i *sendHookInvoker) requestKeys() []string {
	keys := make([]string, 0, len(i.requests))
	for _, req := range i.requests {
		keys = append(keys, req.pluginNo+":"+req.path)
	}
	return keys
}

func (i *sendHookInvoker) requestPayloads() []string {
	payloads := make([]string, 0, len(i.requests))
	for _, req := range i.requests {
		payloads = append(payloads, string(req.packet.GetPayload()))
	}
	return payloads
}

type sendHookObserver struct {
	results []string
}

func (o *sendHookObserver) ObserveSendInvoke(result string, _ time.Duration) {
	o.results = append(o.results, result)
}

func (o *sendHookObserver) ObserveReceiveInvoke(string, time.Duration) {}
