//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

// A new endpoint is probed through a private one-address transport, not inserted
// into discovery before the Controller membership CAS accepts it.
func TestPresenceCapabilityProbeReachesUnjoinedExactEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	remote := &Node{cfg: Config{NodeID: 5}, presenceBoot: 105}
	if err := remote.EnablePresenceProtocol(1); err != nil {
		t.Fatal(err)
	}
	server := clusternet.NewTransportServer(clusternet.TransportServerConfig{})
	server.Register(clusternet.RPCPresenceControl, control.NewPresenceControlHandler(presenceControlBackend{node: remote}))
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	local := &Node{cfg: Config{NodeID: 1}, discovery: clusternet.NewDiscovery()}
	caps, err := local.probePresenceCapabilities(ctx, 5, server.Addr())
	if err != nil || caps.NodeID != 5 || caps.BootID != 105 || caps.PresenceProtocolVersion != 1 {
		t.Fatalf("probe=%+v/%v", caps, err)
	}
	if _, present := local.discovery.Addr(5); present {
		t.Fatal("probe changed authoritative discovery")
	}
	caps, err = local.probePresenceCapabilities(ctx, 6, server.Addr())
	if err == nil {
		t.Fatalf("wrong target identity accepted %+v", caps)
	}
}
