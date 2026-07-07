package propose

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestPayloadCodecRoundTripsHashSlotZero(t *testing.T) {
	payload := EncodePayload(0, []byte("cmd"))
	hashSlot, command, err := DecodePayload(payload)
	if err != nil {
		t.Fatalf("DecodePayload() error = %v", err)
	}
	if hashSlot != 0 || !bytes.Equal(command, []byte("cmd")) {
		t.Fatalf("decoded = %d,%q want 0,cmd", hashSlot, command)
	}
}

func TestServiceProposeLocalLeader(t *testing.T) {
	slots := &fakeSlots{local: true}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 2, SlotID: 10, Leader: 1}}, Slots: slots})
	if err := svc.Propose(context.Background(), Request{Key: "u1", Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if slots.calls != 1 || slots.slotID != 10 {
		t.Fatalf("slot calls = %d slotID=%d, want one call to slot 10", slots.calls, slots.slotID)
	}
	hashSlot, command, err := DecodePayload(slots.payload)
	if err != nil || hashSlot != 2 || !bytes.Equal(command, []byte("cmd")) {
		t.Fatalf("payload decoded = %d,%q,%v want 2,cmd,nil", hashSlot, command, err)
	}
}

func TestServiceProposeResultReturnsLocalApplyData(t *testing.T) {
	slots := &fakeResultSlots{fakeSlots: fakeSlots{local: true}, result: []byte("seq=7")}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 1}}, Slots: slots})

	got, err := svc.ProposeResult(context.Background(), Request{Key: "g1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if string(got) != "seq=7" {
		t.Fatalf("result = %q, want seq=7", got)
	}
	if slots.resultCalls != 1 || slots.calls != 0 {
		t.Fatalf("slot calls=%d resultCalls=%d, want result path only", slots.calls, slots.resultCalls)
	}
}

func TestServiceProposeUsesResultlessLocalPathWhenAvailable(t *testing.T) {
	slots := &fakeResultSlots{fakeSlots: fakeSlots{local: true}, result: []byte("seq=7")}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 1}}, Slots: slots})

	if err := svc.Propose(context.Background(), Request{Key: "g1", Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if slots.calls != 1 || slots.resultCalls != 0 {
		t.Fatalf("slot calls=%d resultCalls=%d, want resultless path only", slots.calls, slots.resultCalls)
	}
}

func TestServiceProposeResultFallsBackToLocalErrorOnlyRuntime(t *testing.T) {
	slots := &fakeSlots{local: true}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 1}}, Slots: slots})

	got, err := svc.ProposeResult(context.Background(), Request{Key: "g1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if got != nil {
		t.Fatalf("result = %q, want nil fallback result", got)
	}
	if slots.calls != 1 {
		t.Fatalf("slot calls=%d, want old Propose fallback", slots.calls)
	}
}

func TestServiceProposeLocalLeaderObservesStage(t *testing.T) {
	observer := &recordingStageObserver{}
	slots := &fakeSlots{local: true}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 2, SlotID: 10, Leader: 1}}, Slots: slots})

	err := svc.Propose(WithStageObserver(context.Background(), observer), Request{Key: "u1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	requireStage(t, observer.events, "meta_create_propose_local", "ok")
}

func TestServiceProposeRemoteLeader(t *testing.T) {
	forward := &fakeForward{}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2}}, Slots: &fakeSlots{}, Forward: forward})
	if err := svc.Propose(context.Background(), Request{Target: Target{HashSlot: 3, HasHashSlot: true}, Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if forward.calls != 1 || forward.nodeID != 2 || forward.req.SlotID != 11 || forward.req.HashSlot != 3 {
		t.Fatalf("forward = %#v, want one call to node 2 slot 11 hash 3", forward)
	}
}

func TestServiceProposeResultReturnsForwardedApplyData(t *testing.T) {
	forward := &fakeResultForward{result: []byte("remote-seq=9")}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2}}, Slots: &fakeSlots{}, Forward: forward})

	got, err := svc.ProposeResult(context.Background(), Request{Key: "g1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if string(got) != "remote-seq=9" {
		t.Fatalf("result = %q, want remote-seq=9", got)
	}
	if forward.resultCalls != 1 || forward.calls != 0 {
		t.Fatalf("forward calls=%d resultCalls=%d, want result path only", forward.calls, forward.resultCalls)
	}
}

func TestServiceProposeUsesResultlessForwardPathWhenAvailable(t *testing.T) {
	forward := &fakeResultForward{result: []byte("remote-seq=9")}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2}}, Slots: &fakeSlots{}, Forward: forward})

	if err := svc.Propose(context.Background(), Request{Key: "g1", Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if forward.calls != 1 || forward.resultCalls != 0 {
		t.Fatalf("forward calls=%d resultCalls=%d, want resultless path only", forward.calls, forward.resultCalls)
	}
	if forward.req.WantResult {
		t.Fatalf("forward WantResult = true, want false")
	}
}

func TestServiceProposeResultFallsBackToForwardPropose(t *testing.T) {
	forward := &fakeForward{}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2}}, Slots: &fakeSlots{}, Forward: forward})

	got, err := svc.ProposeResult(context.Background(), Request{Key: "g1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if got != nil {
		t.Fatalf("result = %q, want nil fallback result", got)
	}
	if forward.calls != 1 || forward.nodeID != 2 {
		t.Fatalf("forward calls=%d node=%d, want old ForwardPropose fallback to node 2", forward.calls, forward.nodeID)
	}
}

func TestServiceProposeResultReroutesAfterRemoteNotLeader(t *testing.T) {
	router := &sequenceRouter{routes: []routing.Route{
		{HashSlot: 3, SlotID: 11, Leader: 2},
		{HashSlot: 3, SlotID: 11, Leader: 3},
	}}
	forward := &fakeResultForward{
		fakeForward: fakeForward{errs: []error{ErrNotLeader, nil}},
		results:     [][]byte{nil, []byte("leader-3")},
	}
	svc := NewService(Config{LocalNode: 1, Router: router, Slots: &fakeSlots{}, Forward: forward})

	got, err := svc.ProposeResult(context.Background(), Request{Key: "u1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if string(got) != "leader-3" {
		t.Fatalf("result = %q, want leader-3", got)
	}
	if router.calls != 2 {
		t.Fatalf("route calls = %d, want 2", router.calls)
	}
	if forward.resultCalls != 2 || forward.nodeID != 3 {
		t.Fatalf("forward resultCalls=%d node=%d, want retry to node 3", forward.resultCalls, forward.nodeID)
	}
}

func TestServiceProposeRemoteLeaderCarriesProposalClass(t *testing.T) {
	forward := &fakeForward{}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2}}, Slots: &fakeSlots{}, Forward: forward})

	err := svc.Propose(WithProposalClass(context.Background(), ProposalClassBackground), Request{Key: "u1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if forward.req.Class != ProposalClassBackground {
		t.Fatalf("forward proposal class = %q, want %q", forward.req.Class, ProposalClassBackground)
	}
}

func TestNetworkForwardClientUsesOwnedCallerWhenAvailable(t *testing.T) {
	caller := &recordingOwnedForwardCaller{}
	client := NewNetworkForwardClient(caller)

	err := client.ForwardPropose(context.Background(), 2, ForwardRequest{SlotID: 11, HashSlot: 3, Payload: EncodePayload(3, []byte("cmd"))})

	if err != nil {
		t.Fatalf("ForwardPropose() error = %v", err)
	}
	if caller.callOwnedCount != 1 || caller.callCount != 0 {
		t.Fatalf("call counts owned=%d normal=%d, want owned only", caller.callOwnedCount, caller.callCount)
	}
	if caller.nodeID != 2 || caller.serviceID != clusternet.RPCSlotForwardPropose {
		t.Fatalf("target=(%d,%d), want node=2 service=%d", caller.nodeID, caller.serviceID, clusternet.RPCSlotForwardPropose)
	}
	req, err := DecodeForwardRequest(caller.payload)
	if err != nil {
		t.Fatalf("DecodeForwardRequest() error = %v", err)
	}
	if req.SlotID != 11 || req.HashSlot != 3 {
		t.Fatalf("decoded request = %#v, want slot=11 hash=3", req)
	}
	if req.WantResult {
		t.Fatalf("decoded WantResult = true, want false")
	}
}

func TestNetworkForwardClientForwardProposeResultReturnsPayload(t *testing.T) {
	caller := &recordingOwnedForwardCaller{result: []byte("applied")}
	client := NewNetworkForwardClient(caller)

	got, err := client.ForwardProposeResult(context.Background(), 2, ForwardRequest{SlotID: 11, HashSlot: 3, Payload: EncodePayload(3, []byte("cmd"))})
	if err != nil {
		t.Fatalf("ForwardProposeResult() error = %v", err)
	}
	if string(got) != "applied" {
		t.Fatalf("result = %q, want applied", got)
	}
	if caller.callOwnedCount != 1 || caller.nodeID != 2 {
		t.Fatalf("call count=%d node=%d, want owned call to node 2", caller.callOwnedCount, caller.nodeID)
	}
	req, err := DecodeForwardRequest(caller.payload)
	if err != nil {
		t.Fatalf("DecodeForwardRequest() error = %v", err)
	}
	if !req.WantResult {
		t.Fatalf("decoded WantResult = false, want true")
	}
}

func TestNetworkForwardClientMapsRemoteNotLeader(t *testing.T) {
	caller := &recordingOwnedForwardCaller{err: transport.RemoteError{Code: "remote_error", Message: ErrNotLeader.Error()}}
	client := NewNetworkForwardClient(caller)

	err := client.ForwardPropose(context.Background(), 2, ForwardRequest{SlotID: 11, HashSlot: 3, Payload: EncodePayload(3, []byte("cmd"))})

	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("ForwardPropose() error = %v, want ErrNotLeader", err)
	}
}

func TestNetworkForwardClientForwardProposeResultMapsRemoteNotLeader(t *testing.T) {
	caller := &recordingOwnedForwardCaller{err: transport.RemoteError{Code: "remote_error", Message: ErrNotLeader.Error()}}
	client := NewNetworkForwardClient(caller)

	_, err := client.ForwardProposeResult(context.Background(), 2, ForwardRequest{SlotID: 11, HashSlot: 3, Payload: EncodePayload(3, []byte("cmd"))})

	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("ForwardProposeResult() error = %v, want ErrNotLeader", err)
	}
}

func TestForwardCodecRoundTripsProposalClass(t *testing.T) {
	payload, err := EncodeForwardRequest(ForwardRequest{
		SlotID:   11,
		HashSlot: 3,
		Payload:  EncodePayload(3, []byte("cmd")),
		Class:    ProposalClassBackground,
	})
	if err != nil {
		t.Fatalf("EncodeForwardRequest() error = %v", err)
	}
	req, err := DecodeForwardRequest(payload)
	if err != nil {
		t.Fatalf("DecodeForwardRequest() error = %v", err)
	}
	if req.Class != ProposalClassBackground {
		t.Fatalf("decoded proposal class = %q, want %q", req.Class, ProposalClassBackground)
	}
	if req.WantResult {
		t.Fatalf("decoded WantResult = true, want false")
	}
}

func TestForwardCodecRoundTripsWantResult(t *testing.T) {
	payload, err := EncodeForwardRequest(ForwardRequest{
		SlotID:     11,
		HashSlot:   3,
		Payload:    EncodePayload(3, []byte("cmd")),
		Class:      ProposalClassBackground,
		WantResult: true,
	})
	if err != nil {
		t.Fatalf("EncodeForwardRequest() error = %v", err)
	}
	req, err := DecodeForwardRequest(payload)
	if err != nil {
		t.Fatalf("DecodeForwardRequest() error = %v", err)
	}
	if !req.WantResult {
		t.Fatalf("decoded WantResult = false, want true")
	}
	if req.Class != ProposalClassBackground {
		t.Fatalf("decoded proposal class = %q, want %q", req.Class, ProposalClassBackground)
	}
}

func TestServiceProposeRemoteLeaderObservesStage(t *testing.T) {
	observer := &recordingStageObserver{}
	forward := &fakeForward{}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2}}, Slots: &fakeSlots{}, Forward: forward})

	err := svc.Propose(WithStageObserver(context.Background(), observer), Request{Key: "u1", Command: []byte("cmd")})
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	requireStage(t, observer.events, "meta_create_propose_forward", "ok")
}

func TestServiceProposeRemoteNotLeader(t *testing.T) {
	forward := &fakeForward{err: ErrNotLeader}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2}}, Slots: &fakeSlots{}, Forward: forward})
	if err := svc.Propose(context.Background(), Request{Key: "u1", Command: []byte("cmd")}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Propose() error = %v, want ErrNotLeader", err)
	}
}

func TestServiceProposeRetriesAcrossShortLeaderElection(t *testing.T) {
	errs := make([]error, 13)
	for i := 0; i < 12; i++ {
		errs[i] = ErrNotLeader
	}
	forward := &fakeForward{errs: errs}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2}}, Slots: &fakeSlots{}, Forward: forward})

	if err := svc.Propose(context.Background(), Request{Key: "u1", Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if forward.calls != 13 {
		t.Fatalf("forward calls = %d, want 13", forward.calls)
	}
}

func TestServiceProposeReroutesAfterRemoteNotLeader(t *testing.T) {
	router := &sequenceRouter{routes: []routing.Route{
		{HashSlot: 3, SlotID: 11, Leader: 2},
		{HashSlot: 3, SlotID: 11, Leader: 3},
	}}
	forward := &fakeForward{errs: []error{ErrNotLeader, nil}}
	svc := NewService(Config{LocalNode: 1, Router: router, Slots: &fakeSlots{}, Forward: forward})

	if err := svc.Propose(context.Background(), Request{Key: "u1", Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if router.calls != 2 {
		t.Fatalf("route calls = %d, want 2", router.calls)
	}
	if forward.calls != 2 || forward.nodeID != 3 {
		t.Fatalf("forward calls=%d node=%d, want retry to node 3", forward.calls, forward.nodeID)
	}
}

func TestServiceProposeFallsBackToRoutePeerAfterRemoteNotLeader(t *testing.T) {
	router := fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2, Peers: []uint64{2, 3}}}
	forward := &fakeForward{errs: []error{ErrNotLeader, nil}}
	svc := NewService(Config{LocalNode: 1, Router: router, Slots: &fakeSlots{}, Forward: forward})

	if err := svc.Propose(context.Background(), Request{Key: "u1", Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if forward.calls != 2 || forward.nodeID != 3 {
		t.Fatalf("forward calls=%d node=%d, want peer fallback to node 3", forward.calls, forward.nodeID)
	}
}

func TestServiceProposeValidatesTarget(t *testing.T) {
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{}, Slots: &fakeSlots{}})
	if err := svc.Propose(context.Background(), Request{Command: []byte("cmd")}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Propose() error = %v, want ErrInvalidRequest", err)
	}
	if err := svc.Propose(context.Background(), Request{Command: []byte("cmd"), Target: Target{SlotID: 1, HasSlotID: true}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Propose() slot-only error = %v, want ErrInvalidRequest", err)
	}
}

func TestForwardHandlerChecksLocalLeader(t *testing.T) {
	handler := NewForwardHandler(&fakeSlots{local: false})
	payload, err := EncodeForwardRequest(ForwardRequest{SlotID: 1, HashSlot: 0, Payload: EncodePayload(0, []byte("cmd"))})
	if err != nil {
		t.Fatalf("EncodeForwardRequest() error = %v", err)
	}
	if _, err := handler.HandleRPC(context.Background(), payload); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("HandleRPC() error = %v, want ErrNotLeader", err)
	}
}

func TestForwardHandlerReturnsResultWhenAvailable(t *testing.T) {
	slots := &fakeResultSlots{fakeSlots: fakeSlots{local: true}, result: []byte("applied")}
	handler := NewForwardHandler(slots)
	payload, err := EncodeForwardRequest(ForwardRequest{SlotID: 1, HashSlot: 0, WantResult: true, Payload: EncodePayload(0, []byte("cmd"))})
	if err != nil {
		t.Fatalf("EncodeForwardRequest() error = %v", err)
	}
	got, err := handler.HandleRPC(context.Background(), payload)
	if err != nil {
		t.Fatalf("HandleRPC() error = %v", err)
	}
	if string(got) != "applied" {
		t.Fatalf("result = %q, want applied", got)
	}
	if slots.resultCalls != 1 || slots.calls != 0 {
		t.Fatalf("slot calls=%d resultCalls=%d, want result path only", slots.calls, slots.resultCalls)
	}
}

func TestForwardHandlerDoesNotReturnResultUnlessRequested(t *testing.T) {
	slots := &fakeResultSlots{fakeSlots: fakeSlots{local: true}, result: []byte("applied")}
	handler := NewForwardHandler(slots)
	payload, err := EncodeForwardRequest(ForwardRequest{SlotID: 1, HashSlot: 0, Payload: EncodePayload(0, []byte("cmd"))})
	if err != nil {
		t.Fatalf("EncodeForwardRequest() error = %v", err)
	}
	got, err := handler.HandleRPC(context.Background(), payload)
	if err != nil {
		t.Fatalf("HandleRPC() error = %v", err)
	}
	if got != nil {
		t.Fatalf("result = %q, want nil", got)
	}
	if slots.calls != 1 || slots.resultCalls != 0 {
		t.Fatalf("slot calls=%d resultCalls=%d, want resultless path only", slots.calls, slots.resultCalls)
	}
}

func TestForwardHandlerPassesProposalClassToSlots(t *testing.T) {
	slots := &fakeSlots{local: true}
	handler := NewForwardHandler(slots)
	payload, err := EncodeForwardRequest(ForwardRequest{
		SlotID:   1,
		HashSlot: 0,
		Payload:  EncodePayload(0, []byte("cmd")),
		Class:    ProposalClassBackground,
	})
	if err != nil {
		t.Fatalf("EncodeForwardRequest() error = %v", err)
	}
	if _, err := handler.HandleRPC(context.Background(), payload); err != nil {
		t.Fatalf("HandleRPC() error = %v", err)
	}
	if got := ProposalClassFromContext(slots.ctx); got != ProposalClassBackground {
		t.Fatalf("slot proposal class = %q, want %q", got, ProposalClassBackground)
	}
}

type fakeRouter struct {
	route routing.Route
	err   error
}

func (r fakeRouter) RouteKey(string) (routing.Route, error)          { return r.route, r.err }
func (r fakeRouter) RouteHashSlot(uint16) (routing.Route, error)     { return r.route, r.err }
func (r fakeRouter) RouteSlot(uint32, uint16) (routing.Route, error) { return r.route, r.err }

type sequenceRouter struct {
	routes []routing.Route
	errs   []error
	calls  int
}

func (r *sequenceRouter) RouteKey(string) (routing.Route, error)          { return r.next() }
func (r *sequenceRouter) RouteHashSlot(uint16) (routing.Route, error)     { return r.next() }
func (r *sequenceRouter) RouteSlot(uint32, uint16) (routing.Route, error) { return r.next() }

func (r *sequenceRouter) next() (routing.Route, error) {
	idx := r.calls
	r.calls++
	if idx < len(r.errs) && r.errs[idx] != nil {
		return routing.Route{}, r.errs[idx]
	}
	if len(r.routes) == 0 {
		return routing.Route{}, nil
	}
	if idx >= len(r.routes) {
		idx = len(r.routes) - 1
	}
	return r.routes[idx], nil
}

type fakeSlots struct {
	local   bool
	calls   int
	slotID  uint32
	payload []byte
	ctx     context.Context
	err     error
}

func (s *fakeSlots) IsLocalLeader(uint32) bool { return s.local }
func (s *fakeSlots) Propose(ctx context.Context, slotID uint32, payload []byte) error {
	s.calls++
	s.ctx = ctx
	s.slotID = slotID
	s.payload = append([]byte(nil), payload...)
	return s.err
}

type fakeResultSlots struct {
	fakeSlots
	result      []byte
	resultCalls int
}

func (s *fakeResultSlots) IsLocalLeader(uint32) bool { return s.local }
func (s *fakeResultSlots) ProposeResult(ctx context.Context, slotID uint32, payload []byte) ([]byte, error) {
	s.resultCalls++
	s.ctx = ctx
	s.slotID = slotID
	s.payload = append([]byte(nil), payload...)
	return append([]byte(nil), s.result...), s.err
}

type fakeForward struct {
	calls  int
	nodeID uint64
	req    ForwardRequest
	err    error
	errs   []error
}

func (f *fakeForward) ForwardPropose(_ context.Context, nodeID uint64, req ForwardRequest) error {
	f.calls++
	f.nodeID = nodeID
	f.req = req
	if len(f.errs) > 0 {
		idx := f.calls - 1
		if idx >= len(f.errs) {
			idx = len(f.errs) - 1
		}
		return f.errs[idx]
	}
	return f.err
}

type fakeResultForward struct {
	fakeForward
	result      []byte
	results     [][]byte
	resultCalls int
}

func (f *fakeResultForward) ForwardProposeResult(_ context.Context, nodeID uint64, req ForwardRequest) ([]byte, error) {
	f.resultCalls++
	f.nodeID = nodeID
	f.req = req
	if len(f.errs) > 0 {
		idx := f.resultCalls - 1
		if idx >= len(f.errs) {
			idx = len(f.errs) - 1
		}
		if f.errs[idx] != nil {
			return nil, f.errs[idx]
		}
	}
	if len(f.results) > 0 {
		idx := f.resultCalls - 1
		if idx >= len(f.results) {
			idx = len(f.results) - 1
		}
		return append([]byte(nil), f.results[idx]...), nil
	}
	return append([]byte(nil), f.result...), f.err
}

type recordingOwnedForwardCaller struct {
	nodeID         uint64
	serviceID      uint8
	payload        []byte
	callCount      int
	callOwnedCount int
	err            error
	result         []byte
}

func (c *recordingOwnedForwardCaller) Call(context.Context, uint64, uint8, []byte) ([]byte, error) {
	c.callCount++
	if c.err != nil {
		return nil, c.err
	}
	return nil, errors.New("normal call")
}

func (c *recordingOwnedForwardCaller) CallOwned(_ context.Context, nodeID uint64, serviceID uint8, payload transport.OwnedBuffer) ([]byte, error) {
	c.callOwnedCount++
	c.nodeID = nodeID
	c.serviceID = serviceID
	c.payload = append([]byte(nil), payload.Bytes()...)
	payload.Release()
	if c.err != nil {
		return nil, c.err
	}
	return append([]byte(nil), c.result...), nil
}

type recordingStageObserver struct {
	events []recordedStage
}

func (o *recordingStageObserver) ObserveChannelAppendStage(stage string, result string, _ time.Duration) {
	o.events = append(o.events, recordedStage{stage: stage, result: result})
}

type recordedStage struct {
	stage  string
	result string
}

func requireStage(t *testing.T, events []recordedStage, stage string, result string) {
	t.Helper()
	for _, event := range events {
		if event.stage == stage && event.result == result {
			return
		}
	}
	t.Fatalf("stage %s/%s not observed in %#v", stage, result, events)
}
