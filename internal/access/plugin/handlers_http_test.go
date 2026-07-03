package plugin

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

func TestHTTPForwardHandlerDecodesRequestFillsCallerPluginAndWritesResponse(t *testing.T) {
	uc := &fakeUsecase{httpForwardResp: &pluginproto.HttpResponse{Status: http.StatusCreated, Headers: map[string]string{"X-Plugin": "ok"}, Body: []byte("created")}}
	srv := mustServer(t, uc)
	ctx := newFakeRPCContext(mustMarshal(t, &pluginproto.ForwardHttpReq{Request: &pluginproto.HttpRequest{Path: "/hello"}}))
	ctx.uid = "wk.plugin.echo"

	srv.handlePath("/plugin/httpForward", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if uc.httpForwardCalls != 1 {
		t.Fatalf("HTTPForward calls = %d, want 1", uc.httpForwardCalls)
	}
	if uc.httpForwardCaller != "wk.plugin.echo" {
		t.Fatalf("caller = %q", uc.httpForwardCaller)
	}
	if uc.httpForwardReq.GetPluginNo() != "wk.plugin.echo" {
		t.Fatalf("pluginNo = %q, want caller", uc.httpForwardReq.GetPluginNo())
	}
	if uc.httpForwardReq.GetRequest().GetPath() != "/hello" {
		t.Fatalf("request = %#v", uc.httpForwardReq.GetRequest())
	}
	deadline, ok := uc.httpForwardCtx.Deadline()
	if !ok {
		t.Fatal("HTTPForward context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Second {
		t.Fatalf("deadline remaining = %v, want within server timeout", remaining)
	}
	var got pluginproto.HttpResponse
	mustUnmarshal(t, ctx.written, &got)
	if got.GetStatus() != http.StatusCreated || string(got.GetBody()) != "created" || got.GetHeaders()["X-Plugin"] != "ok" {
		t.Fatalf("response = %#v", &got)
	}
}

func TestHTTPForwardHandlerPreservesExplicitPluginNo(t *testing.T) {
	uc := &fakeUsecase{}
	srv := mustServer(t, uc)
	ctx := newFakeRPCContext(mustMarshal(t, &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.other", Request: &pluginproto.HttpRequest{Path: "/hello"}}))
	ctx.uid = "wk.plugin.caller"

	srv.handlePath("/plugin/httpForward", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if uc.httpForwardReq.GetPluginNo() != "wk.plugin.other" {
		t.Fatalf("pluginNo = %q, want explicit", uc.httpForwardReq.GetPluginNo())
	}
}

func TestHTTPForwardHandlerKeepsShorterIncomingDeadline(t *testing.T) {
	uc := &fakeUsecase{}
	srv := mustServer(t, uc)
	incoming, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ctx := newFakeRPCContext(mustMarshal(t, &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo"}))
	ctx.uid = "wk.plugin.echo"
	ctx.ctx = incoming

	srv.handlePath("/plugin/httpForward", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	deadline, ok := uc.httpForwardCtx.Deadline()
	if !ok {
		t.Fatal("HTTPForward context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > 250*time.Millisecond {
		t.Fatalf("deadline remaining = %v, want incoming shorter deadline", remaining)
	}
}

func TestHTTPForwardHandlerRejectsOversizedBody(t *testing.T) {
	uc := &fakeUsecase{}
	srv, err := NewServer(Options{Routes: &fakeRoutes{}, Usecase: uc, Timeout: time.Second, MaxBodyBytes: 4})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	ctx := newFakeRPCContext([]byte("12345"))
	ctx.uid = "wk.plugin.echo"

	srv.handlePath("/plugin/httpForward", ctx)

	if ctx.err == nil {
		t.Fatal("expected WriteErr for oversized request")
	}
	if uc.httpForwardCalls != 0 {
		t.Fatalf("HTTPForward calls = %d, want 0", uc.httpForwardCalls)
	}
}
