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
