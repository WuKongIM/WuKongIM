package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSendHookRunsSendPluginsInOrderAndMutatesPayload(t *testing.T) {
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["low"] = ObservedPlugin{No: "low", Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend}, Priority: 1}
	rt.plugins["high"] = ObservedPlugin{No: "high", Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend}, Priority: 10}
	invoker := &sendHookInvoker{responses: map[string]*pluginproto.SendPacket{
		"high": {Payload: []byte("from-high")},
		"low":  {Payload: []byte("from-low")},
	}}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: newFakeDesiredStore(), Invoker: invoker})

	cmd, reason, err := app.BeforeSend(context.Background(), message.SendCommand{
		FromUID:         "u1",
		SenderSessionID: 42,
		DeviceID:        "web-1",
		DeviceFlag:      frame.WEB,
		ChannelID:       "g1",
		ChannelType:     frame.ChannelTypeGroup,
		Payload:         []byte("original"),
	})

	if err != nil {
		t.Fatalf("BeforeSend: %v", err)
	}
	if reason != frame.ReasonSuccess {
		t.Fatalf("reason = %v, want success", reason)
	}
	if string(cmd.Payload) != "from-low" {
		t.Fatalf("payload = %q, want from-low", string(cmd.Payload))
	}
	if got, want := invoker.pluginNos(), []string{"high", "low"}; !equalStrings(got, want) {
		t.Fatalf("plugin order = %#v, want %#v", got, want)
	}
	var first pluginproto.SendPacket
	if err := first.Unmarshal(invoker.requests[0].Body); err != nil {
		t.Fatalf("unmarshal first request: %v", err)
	}
	if first.GetConn().GetUid() != "u1" || first.GetConn().GetConnId() != 42 || first.GetConn().GetDeviceId() != "web-1" || first.GetConn().GetDeviceFlag() != uint32(frame.WEB) {
		t.Fatalf("conn = %#v", first.GetConn())
	}
	if first.GetFromUid() != "u1" || first.GetChannelId() != "g1" || first.GetChannelType() != uint32(frame.ChannelTypeGroup) || string(first.GetPayload()) != "original" {
		t.Fatalf("first send packet = %#v", &first)
	}
}

func TestSendHookReasonRejectsChain(t *testing.T) {
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["mod"] = ObservedPlugin{No: "mod", Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend}}
	invoker := &sendHookInvoker{responses: map[string]*pluginproto.SendPacket{"mod": {Reason: uint32(frame.ReasonPayloadDecodeError), Payload: []byte("blocked")}}}
	app := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: newFakeDesiredStore(), Invoker: invoker})

	_, reason, err := app.BeforeSend(context.Background(), message.SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("bad")})

	if err != nil {
		t.Fatalf("BeforeSend: %v", err)
	}
	if reason != frame.ReasonPayloadDecodeError {
		t.Fatalf("reason = %v, want payload decode error", reason)
	}
}

func TestSendHookFailClosedAndFailOpen(t *testing.T) {
	expected := errors.New("plugin unavailable")
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["mod"] = ObservedPlugin{No: "mod", Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend}}
	invoker := &sendHookInvoker{err: expected}
	failClosed := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: newFakeDesiredStore(), Invoker: invoker})

	cmd, reason, err := failClosed.BeforeSend(context.Background(), message.SendCommand{FromUID: "u1", Payload: []byte("original")})
	if !errors.Is(err, expected) || reason != 0 || string(cmd.Payload) != "" {
		t.Fatalf("fail-closed = cmd %#v reason %v err %v", cmd, reason, err)
	}

	failOpen := mustNewTestApp(t, Options{Runtime: rt, DesiredStore: newFakeDesiredStore(), Invoker: invoker, FailOpen: true})
	cmd, reason, err = failOpen.BeforeSend(context.Background(), message.SendCommand{FromUID: "u1", Payload: []byte("original")})
	if err != nil || reason != frame.ReasonSuccess || string(cmd.Payload) != "original" {
		t.Fatalf("fail-open = cmd %#v reason %v err %v", cmd, reason, err)
	}
}

type sendHookInvoker struct {
	fakeInvoker
	responses map[string]*pluginproto.SendPacket
	err       error
}

func (f *sendHookInvoker) RequestPlugin(ctx context.Context, no, path string, body []byte) ([]byte, error) {
	_, err := f.fakeInvoker.RequestPlugin(ctx, no, path, body)
	if err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	resp := f.responses[no]
	if resp == nil {
		resp = &pluginproto.SendPacket{}
	}
	return resp.Marshal()
}

func (f *sendHookInvoker) pluginNos() []string {
	out := make([]string, 0, len(f.requests))
	for _, req := range f.requests {
		out = append(out, req.No)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
