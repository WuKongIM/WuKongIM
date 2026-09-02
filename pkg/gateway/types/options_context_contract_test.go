package types

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type optionsHandlerStub struct{}

func (optionsHandlerStub) OnListenerError(string, error)      {}
func (optionsHandlerStub) OnSessionOpen(Context) error        { return nil }
func (optionsHandlerStub) OnFrame(Context, frame.Frame) error { return nil }
func (optionsHandlerStub) OnSessionClose(Context) error       { return nil }
func (optionsHandlerStub) OnSessionError(Context, error)      {}

func TestOptionDefaultsAndNormalizationKeepExplicitValues(t *testing.T) {
	sessionDefaults := DefaultSessionOptions()
	if sessionDefaults.MaxInboundBytes != 1<<20 || sessionDefaults.MaxOutboundBytes != 1<<20 ||
		sessionDefaults.IdleTimeout != 3*time.Minute || sessionDefaults.AsyncSendBatchMaxWait != defaultAsyncSendBatchMaxWait ||
		sessionDefaults.AsyncSendBatchMaxRecords != defaultAsyncSendBatchMaxRecords ||
		sessionDefaults.AsyncSendBatchMaxBytes != defaultAsyncSendBatchMaxBytes ||
		sessionDefaults.CloseOnHandlerError == nil || !*sessionDefaults.CloseOnHandlerError {
		t.Fatalf("DefaultSessionOptions() = %+v", sessionDefaults)
	}
	if got := NormalizeSessionOptions(SessionOptions{}); got.MaxInboundBytes != sessionDefaults.MaxInboundBytes || got.CloseOnHandlerError == nil {
		t.Fatalf("NormalizeSessionOptions(zero) = %+v", got)
	}
	closeOnError := false
	normalized := NormalizeSessionOptions(SessionOptions{
		MaxInboundBytes: 7, MaxOutboundBytes: 8, IdleTimeout: 9 * time.Second,
		AsyncSendBatchMaxWait: -time.Second, AsyncSendBatchMaxRecords: -1,
		AsyncSendBatchMaxBytes: -1, CloseOnHandlerError: &closeOnError,
	})
	if normalized.MaxInboundBytes != 7 || normalized.MaxOutboundBytes != 8 || normalized.IdleTimeout != 9*time.Second ||
		normalized.AsyncSendBatchMaxWait != 0 || normalized.AsyncSendBatchMaxRecords != defaultAsyncSendBatchMaxRecords ||
		normalized.AsyncSendBatchMaxBytes != defaultAsyncSendBatchMaxBytes || normalized.CloseOnHandlerError != &closeOnError {
		t.Fatalf("normalized session options = %+v", normalized)
	}
	partial := NormalizeSessionOptions(SessionOptions{AsyncSendBatchMaxWait: 2 * time.Millisecond, AsyncSendBatchMaxRecords: 3, AsyncSendBatchMaxBytes: 4})
	if partial.MaxInboundBytes != sessionDefaults.MaxInboundBytes || partial.MaxOutboundBytes != sessionDefaults.MaxOutboundBytes ||
		partial.IdleTimeout != sessionDefaults.IdleTimeout || partial.AsyncSendBatchMaxWait != 2*time.Millisecond ||
		partial.AsyncSendBatchMaxRecords != 3 || partial.AsyncSendBatchMaxBytes != 4 || partial.CloseOnHandlerError == nil {
		t.Fatalf("partial session options = %+v", partial)
	}

	runtimeDefaults := DefaultRuntimeOptions()
	if runtimeDefaults.AsyncSendWorkers != defaultAsyncSendWorkers || runtimeDefaults.AsyncSendQueueCapacity != defaultAsyncSendQueueCapacity ||
		runtimeDefaults.AsyncAuthWorkers != defaultAsyncAuthWorkers || runtimeDefaults.AsyncAuthQueueCapacity != defaultAsyncAuthQueueCapacity ||
		runtimeDefaults.AsyncPoolReleaseTimeout != defaultAsyncPoolReleaseTimeout {
		t.Fatalf("DefaultRuntimeOptions() = %+v", runtimeDefaults)
	}
	if got := NormalizeRuntimeOptions(RuntimeOptions{}); got.AsyncSendWorkers != defaultAsyncSendWorkers {
		t.Fatalf("NormalizeRuntimeOptions(zero) = %+v", got)
	}
	runtime := NormalizeRuntimeOptions(RuntimeOptions{
		AsyncSendWorkers: -1, AsyncSendQueueCapacity: -1, AsyncAuthWorkers: -1,
		AsyncAuthQueueCapacity: -1, AsyncPoolReleaseTimeout: -time.Second,
	})
	if runtime != runtimeDefaults {
		t.Fatalf("normalized runtime options = %+v, want %+v", runtime, runtimeDefaults)
	}
	explicitRuntime := NormalizeRuntimeOptions(RuntimeOptions{
		AsyncSendWorkers: 1, AsyncSendQueueCapacity: 2, AsyncAuthWorkers: 3,
		AsyncAuthQueueCapacity: 4, AsyncPoolReleaseTimeout: 5 * time.Second,
	})
	if explicitRuntime.AsyncSendWorkers != 1 || explicitRuntime.AsyncSendQueueCapacity != 2 || explicitRuntime.AsyncAuthWorkers != 3 ||
		explicitRuntime.AsyncAuthQueueCapacity != 4 || explicitRuntime.AsyncPoolReleaseTimeout != 5*time.Second {
		t.Fatalf("explicit runtime options = %+v", explicitRuntime)
	}
}

func validGatewayOptionsContract() Options {
	return Options{
		Handler: optionsHandlerStub{},
		Listeners: []ListenerOptions{
			{Name: " tcp ", Network: " tcp ", Address: " 127.0.0.1:5100 ", Path: " /ignored ", Transport: " gnet ", Protocol: " wkproto "},
			{Name: " ws ", Network: " websocket ", Address: " 127.0.0.1:5200 ", Path: " /ws ", Transport: " gnet ", Protocol: " wsmux "},
		},
	}
}

func TestOptionsValidateNormalizesListenersAndRejectsAmbiguousBindings(t *testing.T) {
	if err := (*Options)(nil).Validate(); err == nil {
		t.Fatal("nil Options.Validate() error = nil")
	}
	valid := validGatewayOptionsContract()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid Options.Validate(): %v", err)
	}
	if valid.Listeners[0] != (ListenerOptions{Name: "tcp", Network: "tcp", Address: "127.0.0.1:5100", Path: "/ignored", Transport: "gnet", Protocol: "wkproto"}) ||
		valid.DefaultSession.MaxInboundBytes == 0 || valid.Runtime.AsyncSendWorkers == 0 {
		t.Fatalf("normalized options = %+v", valid)
	}

	tests := []struct {
		name   string
		mutate func(*Options)
		want   error
	}{
		{name: "empty name", mutate: func(o *Options) { o.Listeners[0].Name = " " }, want: ErrListenerNameEmpty},
		{name: "duplicate name", mutate: func(o *Options) { o.Listeners[1].Name = "tcp" }, want: ErrListenerNameDuplicate},
		{name: "empty address", mutate: func(o *Options) { o.Listeners[0].Address = " " }, want: ErrListenerAddressEmpty},
		{name: "duplicate address", mutate: func(o *Options) { o.Listeners[1].Address = "127.0.0.1:5100" }, want: ErrListenerAddressDuplicate},
		{name: "empty network", mutate: func(o *Options) { o.Listeners[0].Network = " " }, want: ErrListenerNetworkEmpty},
		{name: "empty transport", mutate: func(o *Options) { o.Listeners[0].Transport = " " }, want: ErrListenerTransportEmpty},
		{name: "empty protocol", mutate: func(o *Options) { o.Listeners[0].Protocol = " " }, want: ErrListenerProtocolEmpty},
		{name: "nil handler", mutate: func(o *Options) { o.Handler = nil }, want: ErrNilHandler},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validGatewayOptionsContract()
			test.mutate(&options)
			if err := options.Validate(); !errors.Is(err, test.want) {
				t.Fatalf("Options.Validate() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestAuthenticatorAndContextPreserveReplyAndPhysicalCloseOwnership(t *testing.T) {
	if result, err := AuthenticatorFunc(nil).Authenticate(nil, nil); result != nil || err != nil {
		t.Fatalf("nil authenticator = (%+v, %v)", result, err)
	}
	wantAuth := &AuthResult{Connack: &frame.ConnackPacket{}, SessionValues: map[string]any{SessionValueUID: "u1"}}
	auth := AuthenticatorFunc(func(ctx *Context, connect *frame.ConnectPacket) (*AuthResult, error) {
		if ctx == nil || connect == nil {
			t.Fatal("authenticator lost request values")
		}
		return wantAuth, nil
	})
	if got, err := auth.Authenticate(&Context{}, &frame.ConnectPacket{}); err != nil || got != wantAuth {
		t.Fatalf("Authenticate() = (%+v, %v)", got, err)
	}

	if err := (*Context)(nil).WriteFrame(&frame.PongPacket{}); !errors.Is(err, session.ErrSessionClosed) {
		t.Fatalf("nil WriteFrame() error = %v", err)
	}
	if err := (&Context{}).SealOutboundAndWrite(&frame.PongPacket{}); !errors.Is(err, session.ErrSessionClosed) {
		t.Fatalf("missing-session SealOutboundAndWrite() error = %v", err)
	}
	if (*Context)(nil).OutboundSealed() || (&Context{}).OutboundSealed() {
		t.Fatal("missing session reported outbound sealed")
	}
	if err := (*Context)(nil).CloseSession(CloseReasonServerStop, nil); !errors.Is(err, session.ErrSessionClosed) {
		t.Fatalf("nil CloseSession() error = %v", err)
	}
	if err := (&Context{}).CloseSession(CloseReasonServerStop, nil); !errors.Is(err, session.ErrSessionClosed) {
		t.Fatalf("missing-session CloseSession() error = %v", err)
	}

	var written frame.Frame
	var replyToken string
	sess := session.New(session.Config{ID: 1, WriteFrameFn: func(f frame.Frame, meta session.OutboundMeta) error {
		written, replyToken = f, meta.ReplyToken
		return nil
	}})
	ctx := &Context{Session: sess, ReplyToken: "request-7"}
	pong := &frame.PongPacket{}
	if err := ctx.WriteFrame(pong); err != nil || written != pong || replyToken != "request-7" {
		t.Fatalf("WriteFrame() = error %v frame %#v token %q", err, written, replyToken)
	}

	var closeReason CloseReason
	closeErr := errors.New("policy")
	ctx.CloseSessionFn = func(reason CloseReason, err error) {
		closeReason = reason
		if !errors.Is(err, closeErr) {
			t.Errorf("close callback error = %v", err)
		}
	}
	if err := ctx.CloseSession(CloseReasonPolicyViolation, closeErr); err != nil || closeReason != CloseReasonPolicyViolation {
		t.Fatalf("physical CloseSession() = (%q, %v)", closeReason, err)
	}
	ctx.CloseSessionFn = nil
	if err := ctx.CloseSession(CloseReasonServerStop, nil); err != nil {
		t.Fatalf("session fallback CloseSession(): %v", err)
	}
}
