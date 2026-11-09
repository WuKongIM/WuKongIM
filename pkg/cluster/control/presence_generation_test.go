package control_test

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// These are public codec/adapter checks; the in-process caller is not network proof.
func TestPresenceControlRejectsUnknownAndPreservesExactReceipts(t *testing.T) {
	receipt := controller.PresenceGeneration{Fence: controller.PresenceFence{HashSlot: 37, SlotID: 1, LeaderNodeID: 3, LeaderTerm: 7, ConfigEpoch: 2}, OwnerSetEpoch: 11, ServingBoot: 13, Generation: 17, PreviousGeneration: 9, AppliedRaftIndex: 17}
	server := &presenceControlFixture{receipt: receipt}
	handler := control.NewPresenceControlHandler(server)
	client := control.NewPresenceControlClient(presenceControlCaller{t: t, handler: handler})
	caps, err := client.Capabilities(context.Background(), 3)
	if err != nil || caps.NodeID != 3 || caps.PresenceProtocolVersion != 0 {
		t.Fatalf("unwired capability = %+v, %v", caps, err)
	}
	got, err := client.Claim(context.Background(), 3, controller.ClaimPresenceGenerationRequest{Fence: receipt.Fence, OwnerSetEpoch: 11, ServingBoot: 13, ExpectedGeneration: 9})
	if err != nil || got != receipt {
		t.Fatalf("claim = %+v,%v", got, err)
	}
	server.fail = controller.ErrPresenceGenerationConflict
	if _, err := client.Claim(context.Background(), 3, controller.ClaimPresenceGenerationRequest{}); !errors.Is(err, controller.ErrPresenceGenerationConflict) {
		t.Fatalf("conflict = %v", err)
	}
	if _, err := handler.HandleRPC(context.Background(), []byte(`{"version":1,"operation":"unknown"}`)); err == nil {
		t.Fatal("unknown operation accepted")
	}
	if _, err := handler.HandleRPC(context.Background(), []byte(`{"version":2,"operation":"capabilities"}`)); err == nil {
		t.Fatal("unknown version accepted")
	}
	if _, err := handler.HandleRPC(context.Background(), []byte(`{"version":1,"operation":"capabilities","future":1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
}

type presenceControlCaller struct {
	t       *testing.T
	handler clusternet.Handler
}

func (c presenceControlCaller) Call(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if nodeID != 3 || serviceID != clusternet.RPCPresenceControl {
		c.t.Fatalf("target = %d/%d", nodeID, serviceID)
	}
	return c.handler.HandleRPC(ctx, payload)
}

type presenceControlFixture struct {
	receipt controller.PresenceGeneration
	fail    error
}

func (*presenceControlFixture) PresenceCapabilities() controller.PresenceCapabilities {
	return controller.PresenceCapabilities{NodeID: 3, BootID: 13, ControllerStateVersion: 2, ControllerCommandVersion: 2}
}
func (*presenceControlFixture) EnsurePresenceOwnerSet(context.Context) (controller.PresenceOwnerSet, error) {
	return controller.PresenceOwnerSet{}, controller.ErrPresenceGenerationNotReady
}
func (s *presenceControlFixture) ClaimPresenceGeneration(context.Context, controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
	return s.receipt, s.fail
}
func (s *presenceControlFixture) GetPresenceGeneration(context.Context, uint16) (controller.PresenceGenerationSnapshot, error) {
	return controller.PresenceGenerationSnapshot{Generation: s.receipt}, s.fail
}

// Controller not-ready reasons survive the control wire so a remote owner can
// log why its committed read failed (#1097). Plain and unknown reasons stay a
// plain not-ready, which is also how older peers decode the new strings.
func TestPresenceControlCarriesClosedNotReadyReason(t *testing.T) {
	server := &presenceControlFixture{}
	handler := control.NewPresenceControlHandler(server)
	client := control.NewPresenceControlClient(presenceControlCaller{t: t, handler: handler})
	for _, tc := range []struct {
		fail error
		want controller.PresenceRejectReason
		is   error
	}{
		{controller.PresenceRejection(controller.PresenceRejectRosterNotCurrent), controller.PresenceRejectRosterNotCurrent, controller.ErrPresenceGenerationNotReady},
		{errors.Join(errors.New("private"), controller.PresenceRejection(controller.PresenceRejectNoPresenceState)), controller.PresenceRejectNoPresenceState, controller.ErrPresenceGenerationNotReady},
		{controller.ErrPresenceGenerationNotReady, controller.PresenceRejectUnknown, controller.ErrPresenceGenerationNotReady},
		{controller.PresenceRejection(controller.PresenceRejectCommittedCutMismatch), controller.PresenceRejectUnknown, controller.ErrPresenceGenerationConflict},
	} {
		server.fail = tc.fail
		_, err := client.Get(context.Background(), 3, 7)
		if !errors.Is(err, tc.is) || controller.PresenceRejectReasonOf(err) != tc.want {
			t.Fatalf("Get(%v) = %v reason %s, want %v/%s", tc.fail, err, controller.PresenceRejectReasonOf(err), tc.is, tc.want)
		}
	}
	for _, wire := range []string{`{"version":1,"error":"not_ready:bogus"}`, `{"version":1,"error":"not_ready:"}`, `{"version":1,"error":"not_ready:unknown"}`} {
		_, err := control.NewPresenceControlClient(rawPresenceCaller(wire)).Get(context.Background(), 3, 7)
		if !errors.Is(err, controller.ErrPresenceGenerationNotReady) || controller.PresenceRejectReasonOf(err) != controller.PresenceRejectUnknown {
			t.Fatalf("%s decoded as %v", wire, err)
		}
	}
	if got, ok := controller.ParsePresenceRejectReason("roster_not_current"); !ok || got != controller.PresenceRejectRosterNotCurrent {
		t.Fatal("stable reason name does not parse")
	}
}

type rawPresenceCaller string

func (r rawPresenceCaller) Call(context.Context, uint64, uint8, []byte) ([]byte, error) {
	return []byte(r), nil
}
