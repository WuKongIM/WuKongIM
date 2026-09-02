package app

import (
	"context"
	"errors"
	"testing"
	"time"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestCompositionMetricClassifiersKeepBoundedStableTaxonomy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		reason messageusecase.Reason
		want   string
	}{
		{name: "success", reason: messageusecase.ReasonSuccess, want: "success"},
		{name: "invalid request", reason: messageusecase.ReasonInvalidRequest, want: "invalid_request"},
		{name: "auth failure", reason: messageusecase.ReasonAuthFail, want: "auth_fail"},
		{name: "channel missing", reason: messageusecase.ReasonChannelNotExist, want: "channel_not_exist"},
		{name: "stale node route", reason: messageusecase.ReasonNodeNotMatch, want: "node_not_match"},
		{name: "subscriber missing", reason: messageusecase.ReasonSubscriberNotExist, want: "subscriber_not_exist"},
		{name: "blacklisted", reason: messageusecase.ReasonInBlacklist, want: "in_blacklist"},
		{name: "send denied", reason: messageusecase.ReasonNotAllowSend, want: "not_allow_send"},
		{name: "allowlist missing", reason: messageusecase.ReasonNotInWhitelist, want: "not_in_whitelist"},
		{name: "channel banned", reason: messageusecase.ReasonBan, want: "ban"},
		{name: "channel disbanded", reason: messageusecase.ReasonDisband, want: "disband"},
		{name: "sender banned", reason: messageusecase.ReasonSendBan, want: "send_ban"},
		{name: "system failure", reason: messageusecase.ReasonSystemError, want: "system_error"},
		{name: "unsupported", reason: messageusecase.ReasonUnsupported, want: "unsupported"},
		{name: "future reason", reason: messageusecase.Reason(255), want: "unknown"},
	} {
		t.Run("sendack/"+test.name, func(t *testing.T) {
			if got := gatewaySendackReasonLabel(test.reason); got != test.want {
				t.Fatalf("gatewaySendackReasonLabel(%d) = %q, want %q", test.reason, got, test.want)
			}
		})
	}

	for _, test := range []struct {
		name     string
		priority transport.Priority
		want     string
	}{
		{name: "raft", priority: transport.PriorityRaft, want: "raft"},
		{name: "control", priority: transport.PriorityControl, want: "control"},
		{name: "rpc", priority: transport.PriorityRPC, want: "rpc"},
		{name: "bulk", priority: transport.PriorityBulk, want: "bulk"},
		{name: "unset", priority: transport.Priority(0), want: "none"},
	} {
		t.Run("transport priority/"+test.name, func(t *testing.T) {
			if got := transportPriorityLabel(test.priority); got != test.want {
				t.Fatalf("transportPriorityLabel(%d) = %q, want %q", test.priority, got, test.want)
			}
		})
	}

	for _, test := range []struct {
		name string
		kind transport.FrameKind
		want string
	}{
		{name: "data", kind: transport.FrameKindData, want: "data"},
		{name: "notify", kind: transport.FrameKindNotify, want: "notify"},
		{name: "rpc request", kind: transport.FrameKindRPCRequest, want: "rpc_request"},
		{name: "rpc response", kind: transport.FrameKindRPCResponse, want: "rpc_response"},
		{name: "control", kind: transport.FrameKindControl, want: "control"},
		{name: "future frame", kind: transport.FrameKind(255), want: "unknown"},
	} {
		t.Run("transport frame/"+test.name, func(t *testing.T) {
			if got := transportFrameKindLabel(test.kind); got != test.want {
				t.Fatalf("transportFrameKindLabel(%d) = %q, want %q", test.kind, got, test.want)
			}
		})
	}

	for _, test := range []struct {
		name string
		kind worker.TaskKind
		want string
	}{
		{name: "function", kind: worker.TaskFunc, want: "func"},
		{name: "append", kind: worker.TaskStoreAppend, want: "store_append"},
		{name: "quorum install", kind: worker.TaskQuorumInstall, want: "quorum_install"},
		{name: "quorum commit", kind: worker.TaskQuorumCommit, want: "quorum_commit"},
		{name: "apply", kind: worker.TaskStoreApply, want: "store_apply"},
		{name: "read log", kind: worker.TaskStoreReadLog, want: "store_read_log"},
		{name: "pull", kind: worker.TaskRPCPull, want: "rpc_pull"},
		{name: "ack", kind: worker.TaskRPCAck, want: "rpc_ack"},
		{name: "notify", kind: worker.TaskRPCNotify, want: "rpc_notify"},
		{name: "checkpoint", kind: worker.TaskStoreCheckpoint, want: "store_checkpoint"},
		{name: "pull hint", kind: worker.TaskRPCPullHint, want: "rpc_pull_hint"},
		{name: "meta resolve", kind: worker.TaskMetaResolve, want: "meta_resolve"},
		{name: "cold meta resolve", kind: worker.TaskColdMetaResolve, want: "cold_meta_resolve"},
		{name: "cold store load", kind: worker.TaskColdStoreLoad, want: "cold_store_load"},
		{name: "future task", kind: worker.TaskKind(255), want: "unknown"},
	} {
		t.Run("channel worker/"+test.name, func(t *testing.T) {
			if got := channelWorkerKindLabel(test.kind); got != test.want {
				t.Fatalf("channelWorkerKindLabel(%d) = %q, want %q", test.kind, got, test.want)
			}
		})
	}

	for _, test := range []struct {
		name string
		got  string
		want string
	}{
		{name: "local commit", got: channelCommitModeLabel(ch.CommitModeLocal), want: "local"},
		{name: "quorum commit", got: channelCommitModeLabel(ch.CommitModeQuorum), want: "quorum"},
		{name: "unknown commit", got: channelCommitModeLabel(ch.CommitMode(255)), want: "unknown"},
		{name: "leader", got: channelRoleLabel(ch.RoleLeader), want: "leader"},
		{name: "follower", got: channelRoleLabel(ch.RoleFollower), want: "follower"},
		{name: "unknown role", got: channelRoleLabel(ch.Role(255)), want: "unknown"},
		{name: "idle eviction", got: channelRuntimeEvictionReasonLabel(reactor.RuntimeEvictionReasonIdle), want: "idle"},
		{name: "bench eviction", got: channelRuntimeEvictionReasonLabel(reactor.RuntimeEvictionReasonBench), want: "bench"},
		{name: "unknown eviction", got: channelRuntimeEvictionReasonLabel(reactor.RuntimeEvictionReason("future")), want: "unknown"},
		{name: "append pull hint", got: channelPullHintReasonLabel(channeltransport.PullHintReasonAppend), want: "append"},
		{name: "resume pull hint", got: channelPullHintReasonLabel(channeltransport.PullHintReasonResume), want: "resume"},
		{name: "unknown pull hint", got: channelPullHintReasonLabel(channeltransport.PullHintReason(255)), want: "unknown"},
		{name: "successful pull batch", got: channelPullBatchResultLabel(ch.PullBatchObservation{Items: 3}), want: "ok"},
		{name: "failed pull batch", got: channelPullBatchResultLabel(ch.PullBatchObservation{Items: 3, Errors: 3}), want: "err"},
		{name: "partial pull batch", got: channelPullBatchResultLabel(ch.PullBatchObservation{Items: 3, Errors: 1}), want: "partial"},
	} {
		t.Run("channel result/"+test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("classification = %q, want %q", test.got, test.want)
			}
		})
	}
}

func TestActivationTimeoutPresencePreservesOptionalAndBoundedComposition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	activate := presenceusecase.ActivateCommand{UID: "u1", SessionID: 11}
	deactivate := presenceusecase.DeactivateCommand{UID: "u1", SessionID: 11}
	touch := presenceusecase.TouchCommand{SessionID: 11, ActivityUnix: 22}

	optional := activationTimeoutPresence{}
	if err := optional.Activate(ctx, activate); err != nil {
		t.Fatalf("nil optional Activate returned %v", err)
	}
	if err := optional.Deactivate(ctx, deactivate); err != nil {
		t.Fatalf("nil optional Deactivate returned %v", err)
	}
	if err := optional.Touch(ctx, touch); err != nil {
		t.Fatalf("nil optional Touch returned %v", err)
	}

	directProbe := &activationPresenceProbe{}
	direct := activationTimeoutPresence{next: directProbe}
	if err := direct.Activate(ctx, activate); err != nil {
		t.Fatalf("direct Activate returned %v", err)
	}
	if directProbe.activateContext != ctx || directProbe.activate != activate {
		t.Fatalf("direct Activate did not preserve context and command: %#v", directProbe)
	}

	sentinel := errors.New("presence unavailable")
	boundedProbe := &activationPresenceProbe{err: sentinel}
	bounded := activationTimeoutPresence{next: boundedProbe, timeout: 5 * time.Second}
	if err := bounded.Activate(ctx, activate); !errors.Is(err, sentinel) {
		t.Fatalf("bounded Activate error = %v, want %v", err, sentinel)
	}
	if boundedProbe.activateContext == nil {
		t.Fatal("bounded Activate did not delegate")
	}
	if _, ok := boundedProbe.activateContext.Deadline(); !ok {
		t.Fatal("bounded Activate did not install a deadline")
	}
	if err := bounded.Deactivate(ctx, deactivate); !errors.Is(err, sentinel) {
		t.Fatalf("Deactivate error = %v, want %v", err, sentinel)
	}
	if err := bounded.Touch(ctx, touch); !errors.Is(err, sentinel) {
		t.Fatalf("Touch error = %v, want %v", err, sentinel)
	}
	if boundedProbe.deactivate != deactivate || boundedProbe.touch != touch {
		t.Fatalf("lifecycle commands were not preserved: %#v", boundedProbe)
	}
}

func TestGatewayAddressCompositionPreservesAdvertisedAndListenerBoundaries(t *testing.T) {
	t.Parallel()
	listeners := []gateway.ListenerOptions{
		{Name: "tcp", Network: "tcp", Address: " tcp://0.0.0.0:5100 "},
		{Name: "ws", Network: " websocket ", Address: " edge.internal:5200 "},
		{Name: "wss", Network: "WEBSOCKET", Address: " WSS://secure.internal:5300 "},
		{Name: "duplicate-tcp", Network: "tcp", Address: "127.0.0.1:6100"},
	}

	derived := gatewayAddressesFromListeners(listeners)
	if derived.TCPAddr != "0.0.0.0:5100" || derived.WSAddr != "ws://edge.internal:5200" || derived.WSSAddr != "WSS://secure.internal:5300" {
		t.Fatalf("listener-derived addresses = %#v", derived)
	}

	advertised := apiGatewayAddresses(APIConfig{
		ExternalTCPAddr: " public.example:15100 ",
		ExternalWSAddr:  " ws://public.example:15200 ",
		ExternalWSSAddr: " wss://public.example:15300 ",
	}, listeners)
	if advertised.TCPAddr != "public.example:15100" || advertised.WSAddr != "ws://public.example:15200" || advertised.WSSAddr != "wss://public.example:15300" {
		t.Fatalf("advertised addresses = %#v", advertised)
	}
}

func TestLegacyRouteAddressRewriteIsSchemeAndIPv6Safe(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		addr string
		want string
	}{
		{name: "raw host and port", addr: " node.internal:7000 ", want: "node.internal"},
		{name: "URL IPv6 host", addr: "https://[2001:db8::1]:7000/control", want: "2001:db8::1"},
		{name: "host without port", addr: "node.internal", want: "node.internal"},
		{name: "bracketed IPv6 without port", addr: "[2001:db8::2]", want: "2001:db8::2"},
		{name: "malformed authority", addr: "node:bad:authority", want: ""},
	} {
		t.Run("node host/"+test.name, func(t *testing.T) {
			if got := legacyRouteNodeHost(test.addr); got != test.want {
				t.Fatalf("legacyRouteNodeHost(%q) = %q, want %q", test.addr, got, test.want)
			}
		})
	}

	for _, test := range []struct {
		name string
		addr string
		host string
		want string
		url  bool
	}{
		{name: "TCP port", addr: " old.internal:5100 ", host: "2001:db8::3", want: "[2001:db8::3]:5100"},
		{name: "empty TCP endpoint", addr: "", host: "new.internal", want: ""},
		{name: "malformed TCP endpoint", addr: "old.internal", host: "new.internal", want: "old.internal"},
		{name: "websocket port and path", addr: "ws://old.internal:5200/path?mode=1", host: "new.internal", want: "ws://new.internal:5200/path?mode=1", url: true},
		{name: "secure websocket without port", addr: "wss://old.internal/path", host: "new.internal", want: "wss://new.internal/path", url: true},
		{name: "malformed URL", addr: "://bad", host: "new.internal", want: "://bad", url: true},
		{name: "empty replacement host", addr: "ws://old.internal:5200", host: " ", want: "ws://old.internal:5200", url: true},
	} {
		t.Run("rewrite/"+test.name, func(t *testing.T) {
			var got string
			if test.url {
				got = legacyRouteURLHost(test.addr, test.host)
			} else {
				got = legacyRouteHostPort(test.addr, test.host)
			}
			if got != test.want {
				t.Fatalf("rewrite(%q, %q) = %q, want %q", test.addr, test.host, got, test.want)
			}
		})
	}
}

type activationPresenceProbe struct {
	activateContext context.Context
	activate        presenceusecase.ActivateCommand
	deactivate      presenceusecase.DeactivateCommand
	touch           presenceusecase.TouchCommand
	err             error
}

func (p *activationPresenceProbe) Activate(ctx context.Context, cmd presenceusecase.ActivateCommand) error {
	p.activateContext = ctx
	p.activate = cmd
	return p.err
}

func (p *activationPresenceProbe) Deactivate(_ context.Context, cmd presenceusecase.DeactivateCommand) error {
	p.deactivate = cmd
	return p.err
}

func (p *activationPresenceProbe) Touch(_ context.Context, cmd presenceusecase.TouchCommand) error {
	p.touch = cmd
	return p.err
}
