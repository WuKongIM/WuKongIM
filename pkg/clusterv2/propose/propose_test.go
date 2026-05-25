package propose

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
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

func TestServiceProposeRemoteNotLeader(t *testing.T) {
	forward := &fakeForward{err: ErrNotLeader}
	svc := NewService(Config{LocalNode: 1, Router: fakeRouter{route: routing.Route{HashSlot: 3, SlotID: 11, Leader: 2}}, Slots: &fakeSlots{}, Forward: forward})
	if err := svc.Propose(context.Background(), Request{Key: "u1", Command: []byte("cmd")}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Propose() error = %v, want ErrNotLeader", err)
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

type fakeRouter struct {
	route routing.Route
	err   error
}

func (r fakeRouter) RouteKey(string) (routing.Route, error)          { return r.route, r.err }
func (r fakeRouter) RouteHashSlot(uint16) (routing.Route, error)     { return r.route, r.err }
func (r fakeRouter) RouteSlot(uint32, uint16) (routing.Route, error) { return r.route, r.err }

type fakeSlots struct {
	local   bool
	calls   int
	slotID  uint32
	payload []byte
	err     error
}

func (s *fakeSlots) IsLocalLeader(uint32) bool { return s.local }
func (s *fakeSlots) Propose(_ context.Context, slotID uint32, payload []byte) error {
	s.calls++
	s.slotID = slotID
	s.payload = append([]byte(nil), payload...)
	return s.err
}

type fakeForward struct {
	calls  int
	nodeID uint64
	req    ForwardRequest
	err    error
}

func (f *fakeForward) ForwardPropose(_ context.Context, nodeID uint64, req ForwardRequest) error {
	f.calls++
	f.nodeID = nodeID
	f.req = req
	return f.err
}
