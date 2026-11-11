package control_test

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

type channelReplicationCaller struct {
	handler clusternet.Handler
	err     error
}

func (c channelReplicationCaller) Call(ctx context.Context, _ uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	if serviceID != clusternet.RPCChannelReplicationControl {
		return nil, errors.New("wrong service")
	}
	return c.handler.HandleRPC(ctx, payload)
}

// The capability probe gates one-way activation of Channel authority
// promises, so every unexpected shape or peer failure must surface as an
// error (unsupported), never as a fabricated capability.
func TestChannelReplicationControlCapabilityProbeFailsClosed(t *testing.T) {
	want := controller.ChannelReplicationCapability{NodeID: 3, ProtocolVersion: 1}
	handler := control.NewChannelReplicationControlHandler(func() controller.ChannelReplicationCapability { return want })
	client := control.NewChannelReplicationControlClient(channelReplicationCaller{handler: handler})
	got, err := client.Capabilities(context.Background(), 3)
	if err != nil || got != want {
		t.Fatalf("Capabilities() = %+v, %v; want %+v", got, err, want)
	}
	for _, payload := range []string{
		`{"version":1,"operation":"unknown"}`,
		`{"version":2,"operation":"capabilities"}`,
		`{"version":1,"operation":"capabilities","future":1}`,
		``,
	} {
		if _, err := handler.HandleRPC(context.Background(), []byte(payload)); err == nil {
			t.Fatalf("payload %q accepted", payload)
		}
	}
	old := control.NewChannelReplicationControlClient(channelReplicationCaller{err: errors.New("unknown rpc service")})
	if _, err := old.Capabilities(context.Background(), 3); err == nil {
		t.Fatal("an older peer without the service was reported as capable")
	}
	wrong := control.NewChannelReplicationControlHandler(func() controller.ChannelReplicationCapability {
		return controller.ChannelReplicationCapability{NodeID: 4, ProtocolVersion: 2}
	})
	if _, err := control.NewChannelReplicationControlClient(channelReplicationCaller{handler: wrong}).Capabilities(context.Background(), 3); err == nil {
		t.Fatal("a capability from another node identity was accepted")
	}
}
