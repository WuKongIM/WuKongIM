//go:build integration

package node

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	appendruntime "github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/transport/testkit"
)

// These tests use real loopback TCP, the production SID21 codec/adapter, and
// the real admitted Future. Controlled observation states below are explicitly
// in-memory controls; they are not presented as network reproductions.
func TestChannelAppendReferenceTCPBatchAndRetry(t *testing.T) {
	f := newAppendReferenceTCP(t, true, nil)
	var previous string
	for call := range 2 {
		items := []channelappend.SendBatchItem{referenceItem("upper-a-"+string(rune('0'+call)), "logical-a"), referenceItem("upper-b-"+string(rune('0'+call)), "logical-b")}
		got := f.client.ForwardSendBatch(context.Background(), referenceTarget(), items)
		for _, result := range got {
			if result.Err != nil || result.Result.Reason != channelappend.ReasonSuccess {
				t.Fatalf("actual TCP result changed: %+v", got)
			}
		}
		var physical string
		for _, item := range items {
			forward := f.sink.find(t, sendtrace.StageChannelAppendForward, item.Command.TraceID)
			durable := f.sink.find(t, sendtrace.StageMessageSendDurable, item.Command.TraceID)
			for _, event := range []sendtrace.Event{forward, durable} {
				ref := referenceJSON(t, event)
				if ref.State != "bound" || ref.TraceID == "" {
					t.Fatalf("exact TCP reference = %+v, want bound", ref)
				}
				if physical == "" {
					physical = ref.TraceID
				} else if physical != ref.TraceID {
					t.Fatal("one physical batch did not keep both distinct original upper roots")
				}
			}
		}
		if physical == previous {
			t.Fatal("two calls with the same logical keys reused a physical request identity")
		}
		previous = physical
	}
	f.transport.Stop()
	f.server.Stop()
	for _, sample := range f.observer.snapshot() {
		if sample.ParentTraceID == "upper-a-0" || sample.ParentTraceID == "upper-b-0" {
			t.Fatal("shared RPC falsely selected one original item as its parent")
		}
	}
	for _, event := range f.sink.snapshot() {
		if event.Stage != sendtrace.StageChannelAppendForward {
			continue
		}
		ref := referenceJSON(t, event)
		matches := 0
		for _, sample := range f.observer.snapshot() {
			if sample.TraceID == ref.TraceID && sample.ServiceID == uint16(ChannelAppendRPCServiceID) {
				matches++
			}
		}
		if matches != 2 {
			t.Fatalf("physical reference joins %d endpoint samples, want exactly client+server", matches)
		}
	}
}

func TestChannelAppendReferenceTCPAlignedInactiveItems(t *testing.T) {
	f := newAppendReferenceTCP(t, true, nil)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	items := []channelappend.SendBatchItem{referenceItem("inactive-canceled", "a"), referenceItem("active-b", "b"), referenceItem("inactive-expired", "c"), referenceItem("active-d", "d")}
	items[0].Context = canceled
	items[2].Deadline = time.Now().Add(-time.Second)
	got := f.client.ForwardSendBatch(context.Background(), referenceTarget(), items)
	if !errors.Is(got[0].Err, context.Canceled) || !errors.Is(got[2].Err, context.DeadlineExceeded) || got[1].Err != nil || got[3].Err != nil {
		t.Fatalf("aligned results changed: %+v", got)
	}
	for _, index := range []int{1, 3} {
		event := f.sink.find(t, sendtrace.StageChannelAppendForward, items[index].Command.TraceID)
		if referenceJSON(t, event).State != "bound" {
			t.Fatal("active item lost its exact call reference")
		}
	}
	for _, event := range f.sink.snapshot() {
		if event.TraceID == items[0].Command.TraceID || event.TraceID == items[2].Command.TraceID {
			t.Fatal("inactive item inherited a sibling's physical call")
		}
	}
}

func TestChannelAppendReferenceTCPDisabledAndCanceled(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		f := newAppendReferenceTCP(t, false, nil)
		item := referenceItem("disabled-upper", "disabled-logical")
		got := f.client.ForwardSendBatch(context.Background(), referenceTarget(), []channelappend.SendBatchItem{item})
		if got[0].Err != nil {
			t.Fatal(got[0].Err)
		}
		ref := referenceJSON(t, f.sink.find(t, sendtrace.StageChannelAppendForward, item.Command.TraceID))
		if ref.State != "recorder_disabled" || ref.TraceID != "" {
			t.Fatalf("disabled observation became a wire proof: %+v", ref)
		}
		// A remote receiver without a trace cannot infer disabled/exhausted.
		receiver := referenceJSON(t, f.sink.find(t, sendtrace.StageMessageSendDurable, item.Command.TraceID))
		if receiver.State != "remote_untraced" || receiver.TraceID != "" {
			t.Fatalf("missing receiver evidence was guessed: %+v", receiver)
		}
	})
	t.Run("canceled_waiter_does_not_cancel_admitted_append", func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		// Function defers precede fixture Cleanup even after Fatal/Goexit.
		defer once.Do(func() { close(release) })
		f := newAppendReferenceTCP(t, true, func() { close(entered); <-release })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		item := referenceItem("canceled-upper", "canceled-logical")
		done := make(chan []channelappend.SendBatchItemResult, 1)
		go func() { done <- f.client.ForwardSendBatch(ctx, referenceTarget(), []channelappend.SendBatchItem{item}) }()
		referenceWait(t, entered)
		cancel()
		select {
		case got := <-done:
			if !errors.Is(got[0].Err, context.Canceled) {
				t.Fatalf("actual call cancellation changed: %+v", got)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("canceled forwarding did not join")
		}
		forward := referenceJSON(t, f.sink.find(t, sendtrace.StageChannelAppendForward, item.Command.TraceID))
		once.Do(func() { close(release) })
		waitCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if err := f.group.WaitIdle(waitCtx); err != nil {
			t.Fatal(err)
		}
		durable := referenceJSON(t, f.sink.find(t, sendtrace.StageMessageSendDurable, item.Command.TraceID))
		if forward.State != "bound" || durable.State != "bound" || forward.TraceID != durable.TraceID {
			t.Fatal("late admitted completion lost/changed its actual RPC identity")
		}
	})
}

func TestChannelAppendReferenceObservationControls(t *testing.T) {
	for _, state := range []string{"recorder_exhausted", "unbound"} {
		t.Run(state, func(t *testing.T) {
			sink := &appendReferenceSink{}
			restore := sendtrace.SetSink(sink)
			defer restore()
			recorder, err := rpctrace.NewRecorder(1)
			if err != nil {
				t.Fatal(err)
			}
			node := referenceControlNode(func(ctx context.Context, _ uint64, _ uint8, _ []byte) ([]byte, error) {
				if state == "recorder_exhausted" {
					held := recorder.Start(context.Background(), rpctrace.SideClient, uint16(ChannelAppendRPCServiceID))
					defer held.Finish(rpctrace.ResultOK)
					if recorder.Start(ctx, rpctrace.SideClient, uint16(ChannelAppendRPCServiceID)) != nil {
						t.Fatal("exhaustion control did not exhaust its actual recorder")
					}
				} else {
					trace := recorder.Start(ctx, rpctrace.SideClient, uint16(ChannelAppendRPCServiceID))
					trace.Finish(rpctrace.ResultError)
				}
				return nil, transport.ErrDialFailed
			})
			item := referenceItem("control-upper", "control-logical")
			got := NewClient(node).ForwardSendBatch(context.Background(), referenceTarget(), []channelappend.SendBatchItem{item})
			if !errors.Is(got[0].Err, channelappend.ErrRouteNotReady) {
				t.Fatalf("pre-dispatch business error changed: %+v", got)
			}
			ref := referenceJSON(t, sink.find(t, sendtrace.StageChannelAppendForward, item.Command.TraceID))
			if ref.State != state || (state == "unbound") != (ref.TraceID != "") {
				t.Fatalf("explicit observation state = %+v, want %s", ref, state)
			}
		})
	}
}

func TestChannelAppendReferenceDisabledSinkDoesNotRequestCapture(t *testing.T) {
	restore := sendtrace.SetSink(nil)
	defer restore()
	node := referenceControlNode(func(ctx context.Context, _ uint64, _ uint8, _ []byte) ([]byte, error) {
		if rpctrace.TerminalCaptureFrom(ctx) != nil {
			t.Fatal("disabled SEND diagnostics requested an RPC capture")
		}
		return encodeChannelAppendResponse(channelAppendResponse{Status: rpcStatusOK, Results: []channelappend.SendBatchItemResult{{Result: channelappend.SendResult{Reason: channelappend.ReasonSuccess}}}})
	})
	item := referenceItem("disabled-sink-upper", "disabled-sink-logical")
	got := NewClient(node).ForwardSendBatch(context.Background(), referenceTarget(), []channelappend.SendBatchItem{item})
	if got[0].Err != nil {
		t.Fatal(got[0].Err)
	}
}

func TestChannelAppendReferencePreservesRejectedBusinessOutcome(t *testing.T) {
	sink := &appendReferenceSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()
	node := referenceControlNode(func(context.Context, uint64, uint8, []byte) ([]byte, error) {
		return encodeChannelAppendResponse(channelAppendResponse{Status: rpcStatusOK, Results: []channelappend.SendBatchItemResult{{Result: channelappend.SendResult{Reason: channelappend.ReasonBan}}}})
	})
	item := referenceItem("rejected-upper", "rejected-logical")
	got := NewClient(node).ForwardSendBatch(context.Background(), referenceTarget(), []channelappend.SendBatchItem{item})
	if got[0].Err != nil || got[0].Result.Reason != channelappend.ReasonBan {
		t.Fatal("observation changed original rejection")
	}
	if sink.find(t, sendtrace.StageChannelAppendForward, item.Command.TraceID).Result != sendtrace.ResultError {
		t.Fatal("successful RPC transport hid an original business rejection")
	}
}

func TestChannelAppendReferenceKnownLocalAndUnknownDirect(t *testing.T) {
	f := newAppendReferenceTCP(t, false, nil)
	router := appendruntime.NewRouter(appendruntime.RouterOptions{
		LocalNodeID: 2, Local: f.group,
		Resolver: referenceResolver(func(context.Context, channelappend.ChannelID) (channelappend.AuthorityTarget, error) {
			return referenceTarget(), nil
		}),
	})
	item := referenceItem("known-local-upper", "known-local-logical")
	got := router.SendBatch([]channelappend.SendBatchItem{item})
	if got[0].Err != nil {
		t.Fatal(got[0].Err)
	}
	ref := referenceJSON(t, f.sink.find(t, sendtrace.StageMessageSendDurable, item.Command.TraceID))
	if ref.State != "local_untraced" || ref.TraceID != "" {
		t.Fatalf("known local path invented a wire identity: %+v", ref)
	}
	item = referenceItem("unmarked-upper", "unmarked-logical")
	future, err := f.group.SubmitLocal(context.Background(), referenceTarget(), []channelappend.SendBatchItem{item})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = future.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	event := f.sink.find(t, sendtrace.StageMessageSendDurable, item.Command.TraceID)
	b, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"rpc_reference", "RPCReference"} {
		if raw := fields[key]; len(raw) > 0 && string(raw) != "null" {
			t.Fatal("an unmarked nil trace was guessed local/disabled instead of unknown")
		}
	}
}

func TestChannelAppendReferenceUnrelatedTraceCannotBecomeSID21Proof(t *testing.T) {
	f := newAppendReferenceTCP(t, false, nil)
	recorder, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := recorder.Start(context.Background(), rpctrace.SideServer, 14)
	defer trace.Finish(rpctrace.ResultOK)
	var token [16]byte
	token[0] = 1
	if !trace.Bind(token, 1) {
		t.Fatal("observation control did not bind its unrelated service")
	}
	item := referenceItem("wrong-service-upper", "wrong-service-logical")
	body, err := encodeChannelAppendRequest(channelAppendRequest{Target: referenceTarget(), Items: []channelAppendItem{{Command: item.Command}}})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewChannelAppendAdapter(ChannelAppendOptions{ChannelAppend: referenceLocal{group: f.group}})
	if _, err = adapter.HandleChannelAppendRPC(rpctrace.With(context.Background(), trace), body); err != nil {
		t.Fatal(err)
	}
	ref := referenceJSON(t, f.sink.find(t, sendtrace.StageMessageSendDurable, item.Command.TraceID))
	if ref.State != "remote_untraced" || ref.TraceID != "" {
		t.Fatal("unrelated service trace became a false SID21 wire proof")
	}
}

type referenceResolver func(context.Context, channelappend.ChannelID) (channelappend.AuthorityTarget, error)

func (f referenceResolver) ResolveAppendAuthority(ctx context.Context, id channelappend.ChannelID) (channelappend.AuthorityTarget, error) {
	return f(ctx, id)
}

type appendReferenceJSON struct {
	State   string `json:"state"`
	TraceID string `json:"trace_id"`
}

// Decode the optional JSON field so the unchanged baseline compiles and fails
// for missing behavior, rather than failing to compile against new symbols.
func referenceJSON(t *testing.T, event sendtrace.Event) appendReferenceJSON {
	t.Helper()
	b, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	raw := fields["rpc_reference"]
	if len(raw) == 0 {
		raw = fields["RPCReference"]
	}
	if len(raw) == 0 || string(raw) == "null" {
		t.Fatal("missing typed SEND-to-RPC reference")
	}
	var ref appendReferenceJSON
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

type appendReferenceSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *appendReferenceSink) RecordSendTrace(e sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}
func (s *appendReferenceSink) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}
func (s *appendReferenceSink) find(t *testing.T, stage sendtrace.Stage, trace string) sendtrace.Event {
	t.Helper()
	for _, e := range s.snapshot() {
		if e.Stage == stage && e.TraceID == trace {
			return e
		}
	}
	t.Fatalf("missing %s observation for an original upper root", stage)
	return sendtrace.Event{}
}

type referenceControlNode func(context.Context, uint64, uint8, []byte) ([]byte, error)

func (f referenceControlNode) CallRPC(ctx context.Context, n uint64, s uint8, b []byte) ([]byte, error) {
	return f(ctx, n, s, b)
}

type referenceTCPNode struct{ client *transport.Client }

func (n referenceTCPNode) CallRPC(ctx context.Context, node uint64, service uint8, body []byte) ([]byte, error) {
	return n.client.Call(ctx, transport.NodeID(node), uint64(service)+3, transport.PriorityRPC, uint16(service), body)
}

type referenceObserver struct {
	mu      sync.Mutex
	samples []rpctrace.Sample
}

func (o *referenceObserver) ObserveTransport(e transport.Event) {
	if e.RPCTrace != nil {
		o.mu.Lock()
		defer o.mu.Unlock()
		o.samples = append(o.samples, e.RPCTrace.Clone())
	}
}
func (o *referenceObserver) snapshot() []rpctrace.Sample {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]rpctrace.Sample(nil), o.samples...)
}

type referenceIDs struct{ next atomic.Uint64 }

func (a *referenceIDs) Next() uint64 { return a.next.Add(1) }

type referenceAppender struct {
	gate  func()
	next  atomic.Uint64
	mu    sync.Mutex
	sizes []int
}

func (a *referenceAppender) AppendBatch(_ context.Context, req channelappend.AppendBatchRequest) (channelappend.AppendBatchResult, error) {
	a.mu.Lock()
	a.sizes = append(a.sizes, len(req.Messages))
	a.mu.Unlock()
	if a.gate != nil {
		a.gate()
	}
	result := channelappend.AppendBatchResult{Items: make([]channelappend.AppendBatchItemResult, len(req.Messages))}
	for i, m := range req.Messages {
		result.Items[i] = channelappend.AppendBatchItemResult{MessageID: m.MessageID, MessageSeq: a.next.Add(1)}
	}
	return result, nil
}

func (a *referenceAppender) snapshot() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int(nil), a.sizes...)
}

type referenceLocal struct {
	group    *appendruntime.Group
	admitted chan struct{}
}

func (s referenceLocal) SubmitForAuthority(ctx context.Context, target channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	f, err := s.group.SubmitLocal(ctx, target, items)
	if err == nil && s.admitted != nil {
		s.admitted <- struct{}{}
	}
	if err == nil {
		var results []channelappend.SendBatchItemResult
		results, err = f.Wait(ctx)
		if err == nil {
			return results
		}
	}
	results := make([]channelappend.SendBatchItemResult, len(items))
	for i := range results {
		results[i].Err = err
	}
	return results
}

type appendReferenceFixture struct {
	client    *Client
	transport *transport.Client
	server    *transport.Server
	group     *appendruntime.Group
	observer  *referenceObserver
	sink      *appendReferenceSink
	admitted  chan struct{}
	appender  *referenceAppender
}

func newAppendReferenceTCP(t *testing.T, enabled bool, gate func()) *appendReferenceFixture {
	return newAppendReferenceTCPOptions(t, enabled, gate, nil)
}

func newAppendReferenceTCPOptions(t *testing.T, enabled bool, gate func(), authorizer appendruntime.Authorizer) *appendReferenceFixture {
	t.Helper()
	sink := &appendReferenceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)
	appender := &referenceAppender{gate: gate}
	group := appendruntime.New(appendruntime.Options{LocalNodeID: 2, MessageID: &referenceIDs{}, Appender: appender, Authorizer: authorizer, AdvancePoolSize: 1, InboxCoalesceWindow: -time.Nanosecond})
	if err := group.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Errorf("append owner cleanup: %v", err)
		}
	})
	admitted := make(chan struct{}, 8)
	adapter := NewChannelAppendAdapter(ChannelAppendOptions{ChannelAppend: referenceLocal{group: group, admitted: admitted}})
	observer := &referenceObserver{}
	server, err := transport.NewServer(transport.ServerConfig{NodeID: 2, RPCTracing: enabled, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	if err = server.Handle(uint16(ChannelAppendRPCServiceID), adapter.HandleChannelAppendRPC, transport.ServiceOptions{Concurrency: 4, QueueSize: 32, MaxQueueBytes: 4 << 20, Timeout: 5 * time.Second, QueueTimeout: 5 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if err = server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	every := uint32(0)
	if enabled {
		every = 32
	}
	client, err := transport.NewClient(transport.ClientConfig{NodeID: 1, RPCTraceEvery: every, RequestBudgets: true, PoolSize: 16, Discovery: testkit.StaticDiscovery{2: server.Addr()}, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Stop)
	return &appendReferenceFixture{client: NewClient(referenceTCPNode{client}), transport: client, server: server, group: group, observer: observer, sink: sink, admitted: admitted, appender: appender}
}
func referenceTarget() channelappend.AuthorityTarget {
	target := channelAppendTestTarget()
	target.LeaderNodeID = 2
	return target
}
func referenceItem(trace, logical string) channelappend.SendBatchItem {
	return channelappend.SendBatchItem{Context: context.Background(), Command: channelappend.SendCommand{FromUID: "reference-sender", SenderNodeID: 1, TraceID: trace, ClientMsgNo: logical, ChannelID: "g1", ChannelType: 2, Payload: []byte("private-fixture-payload")}}
}
func referenceWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("bounded real TCP fixture did not progress")
	}
}

func TestChannelAppendReferenceTCPSharedDuplicateEffectKeepsOriginalOwners(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := newAppendReferenceTCPOptions(t, true, nil, referenceAuthorizer(func(_ context.Context, cmd channelappend.SendCommand) (channelappend.Decision, error) {
		if cmd.ChannelID == "block-writer" {
			close(entered)
			<-release
			return channelappend.Decision{Allowed: false, Reason: channelappend.ReasonAuthFail}, nil
		}
		return channelappend.Decision{Allowed: true, Reason: channelappend.ReasonSuccess}, nil
	}))
	blockTarget := referenceTarget()
	blockTarget.ChannelID.ID = "block-writer"
	blockTarget.ChannelKey = "block-writer"
	blockItem := referenceItem("block-upper", "block-logical")
	blockItem.Command.ChannelID = "block-writer"
	blocker, err := f.group.SubmitLocal(context.Background(), blockTarget, []channelappend.SendBatchItem{blockItem})
	if err != nil {
		t.Fatal(err)
	}
	referenceWait(t, entered)
	results := make(chan []channelappend.SendBatchItemResult, 2)
	for _, root := range []string{"shared-a", "shared-b"} {
		items := []channelappend.SendBatchItem{referenceItem(root+"-one", "same-logical-one"), referenceItem(root+"-two", "same-logical-two")}
		go func() { results <- f.client.ForwardSendBatch(context.Background(), referenceTarget(), items) }()
	}
	referenceWait(t, f.admitted)
	referenceWait(t, f.admitted)
	once.Do(func() { close(release) })
	if _, err = blocker.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case got := <-results:
			for _, r := range got {
				if r.Err != nil {
					t.Fatal(r.Err)
				}
			}
		case <-time.After(3 * time.Second):
			t.Fatal("shared real TCP calls did not join")
		}
	}
	var requestIDs []string
	for _, root := range []string{"shared-a", "shared-b"} {
		first := ""
		for _, suffix := range []string{"-one", "-two"} {
			forward := referenceJSON(t, f.sink.find(t, sendtrace.StageChannelAppendForward, root+suffix))
			durable := referenceJSON(t, f.sink.find(t, sendtrace.StageMessageSendDurable, root+suffix))
			if forward.State != "bound" || durable != forward {
				t.Fatal("coalescing/dedup attributed another original Future's request")
			}
			if first == "" {
				first = forward.TraceID
			} else if first != forward.TraceID {
				t.Fatal("one request split original ownership")
			}
		}
		requestIDs = append(requestIDs, first)
	}
	if requestIDs[0] == requestIDs[1] {
		t.Fatal("two shared Futures lost their distinct wire calls")
	}
	sizes := f.appender.snapshot()
	if len(sizes) != 1 || sizes[0] != 2 {
		t.Fatalf("fixture did not execute one coalesced, deduplicated effect: %v", sizes)
	}
}

type referenceAuthorizer func(context.Context, channelappend.SendCommand) (channelappend.Decision, error)

func (f referenceAuthorizer) AuthorizeSend(ctx context.Context, cmd channelappend.SendCommand) (channelappend.Decision, error) {
	return f(ctx, cmd)
}
