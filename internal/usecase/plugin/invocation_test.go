package plugin

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

func TestInvokeSendUsesLegacyPathAndReturnsMutatedPacket(t *testing.T) {
	invoker := &fakeInvokerWithResponses{responses: map[string][]byte{PathSend: mustMarshalProto(t, &pluginproto.SendPacket{Payload: []byte("mutated"), Reason: 9})}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: invoker, NodeID: 1})

	got, err := app.InvokeSend(context.Background(), "p1", &pluginproto.SendPacket{Payload: []byte("original")})
	if err != nil {
		t.Fatalf("InvokeSend returned error: %v", err)
	}
	if got.GetReason() != 9 || string(got.GetPayload()) != "mutated" {
		t.Fatalf("send response = %#v", got)
	}
	if len(invoker.requests) != 1 || invoker.requests[0].No != "p1" || invoker.requests[0].Path != PathSend {
		t.Fatalf("send request = %#v", invoker.requests)
	}
	var sent pluginproto.SendPacket
	if err := sent.Unmarshal(invoker.requests[0].Body); err != nil {
		t.Fatalf("unmarshal send body: %v", err)
	}
	if string(sent.GetPayload()) != "original" {
		t.Fatalf("send body payload = %q", string(sent.GetPayload()))
	}
}

func TestInvokePersistAfterUsesLegacySyncAndAsyncMappings(t *testing.T) {
	invoker := &fakeInvokerWithResponses{responses: map[string][]byte{PathPersistAfter: nil}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: invoker, NodeID: 1})
	batch := &pluginproto.MessageBatch{Messages: []*pluginproto.Message{{MessageId: 11}}}

	if err := app.InvokePersistAfter(context.Background(), ObservedPlugin{No: "sync", PersistAfterSync: true}, batch); err != nil {
		t.Fatalf("InvokePersistAfter sync returned error: %v", err)
	}
	if len(invoker.requests) != 1 || invoker.requests[0].No != "sync" || invoker.requests[0].Path != PathPersistAfter {
		t.Fatalf("sync persist request = %#v", invoker.requests)
	}
	if err := app.InvokePersistAfter(context.Background(), ObservedPlugin{No: "async"}, batch); err != nil {
		t.Fatalf("InvokePersistAfter async returned error: %v", err)
	}
	if len(invoker.sends) != 1 || invoker.sends[0].No != "async" || invoker.sends[0].MsgType != MsgTypePersistAfter {
		t.Fatalf("async persist send = %#v", invoker.sends)
	}
}

func TestInvokeReceiveUsesLegacySyncAndAsyncMappings(t *testing.T) {
	invoker := &fakeInvokerWithResponses{responses: map[string][]byte{PathReceive: nil}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: invoker, NodeID: 1})
	packet := &pluginproto.RecvPacket{FromUid: "alice", ToUid: "bot"}

	if err := app.InvokeReceive(context.Background(), ObservedPlugin{No: "sync", ReplySync: true}, packet); err != nil {
		t.Fatalf("InvokeReceive sync returned error: %v", err)
	}
	if len(invoker.requests) != 1 || invoker.requests[0].No != "sync" || invoker.requests[0].Path != PathReceive {
		t.Fatalf("sync receive request = %#v", invoker.requests)
	}
	if err := app.InvokeReceive(context.Background(), ObservedPlugin{No: "async"}, packet); err != nil {
		t.Fatalf("InvokeReceive async returned error: %v", err)
	}
	if len(invoker.sends) != 1 || invoker.sends[0].No != "async" || invoker.sends[0].MsgType != MsgTypeReceive {
		t.Fatalf("async receive send = %#v", invoker.sends)
	}
}

func TestRouteUsesLegacyPathAndDecodesHTTPResponse(t *testing.T) {
	invoker := &fakeInvokerWithResponses{responses: map[string][]byte{PathRoute: mustMarshalProto(t, &pluginproto.HttpResponse{Status: 201, Body: []byte("created")})}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: invoker, NodeID: 1})

	resp, err := app.Route(context.Background(), "p1", &pluginproto.HttpRequest{Method: "POST", Path: "/x", Body: []byte("body")})
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if resp.GetStatus() != 201 || string(resp.GetBody()) != "created" {
		t.Fatalf("route response = %#v", resp)
	}
	if len(invoker.requests) != 1 || invoker.requests[0].No != "p1" || invoker.requests[0].Path != PathRoute {
		t.Fatalf("route request = %#v", invoker.requests)
	}
}

func TestRouteRejectsOversizedPluginResponse(t *testing.T) {
	invoker := &fakeInvokerWithResponses{responses: map[string][]byte{PathRoute: mustMarshalProto(t, &pluginproto.HttpResponse{Status: 200, Body: []byte("12345")})}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: invoker, HTTPForwardMaxBodyBytes: 4})

	_, err := app.Route(context.Background(), "p1", &pluginproto.HttpRequest{Path: "/x"})

	assertErrorIs(t, err, ErrHTTPForwardBodyTooLarge)
}

func mustMarshalProto(t *testing.T, msg interface{ Marshal() ([]byte, error) }) []byte {
	t.Helper()
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("marshal proto: %v", err)
	}
	return data
}

type fakeInvokerWithResponses struct {
	fakeInvoker
	responses map[string][]byte
}

func (f *fakeInvokerWithResponses) RequestPlugin(ctx context.Context, no, path string, body []byte) ([]byte, error) {
	_, err := f.fakeInvoker.RequestPlugin(ctx, no, path, body)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), f.responses[path]...), nil
}

func TestStopPluginUsesInvokerStop(t *testing.T) {
	invoker := &fakeInvoker{}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: invoker, NodeID: 1})

	if err := app.StopPlugin(context.Background(), "p1"); err != nil {
		t.Fatalf("StopPlugin returned error: %v", err)
	}
	if len(invoker.stops) != 1 || invoker.stops[0] != "p1" {
		t.Fatalf("stops = %#v, want [p1]", invoker.stops)
	}
}
