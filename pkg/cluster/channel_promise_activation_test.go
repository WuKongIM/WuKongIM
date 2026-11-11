package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

type promiseActivationControl struct {
	control.Controller
	leader  uint64
	ensures int
	err     error
}

func (c *promiseActivationControl) LeaderID() uint64 { return c.leader }
func (c *promiseActivationControl) EnsureChannelAuthorityPromise(context.Context) (bool, error) {
	c.ensures++
	return c.err == nil, c.err
}

// Only the Controller leader attempts one-way activation, and only while the
// committed snapshot is inactive; failures stay quiet retries, never local
// activation.
func TestChannelPromiseActivationTickRunsOnlyOnInactiveLeader(t *testing.T) {
	fake := &promiseActivationControl{leader: 2}
	n := &Node{cfg: Config{NodeID: 1}, control: fake}
	if n.tickChannelPromiseActivation(context.Background()) || fake.ensures != 0 {
		t.Fatal("a follower attempted activation")
	}
	fake.leader = 1
	fake.err = errors.New("member 3 unsupported")
	if !n.tickChannelPromiseActivation(context.Background()) || fake.ensures != 1 {
		t.Fatalf("inactive leader attempts = %d, want 1", fake.ensures)
	}
	if n.ChannelAuthorityPromiseActive() {
		t.Fatal("a failed Ensure activated promises locally")
	}
	fake.err = nil
	n.observeChannelPromiseActivation(true)
	n.observeChannelPromiseActivation(false)
	if n.tickChannelPromiseActivation(context.Background()) || fake.ensures != 1 {
		t.Fatal("an active cluster attempted activation again")
	}
	if !n.ChannelAuthorityPromiseActive() {
		t.Fatal("committed activation not reported")
	}
}

// Nodes answer with the protocol their replication actually implements, so a
// cluster activates only after every node acts on activation.
func TestChannelReplicationCapabilityReportsTheImplementedProtocol(t *testing.T) {
	n := &Node{cfg: Config{NodeID: 7}}
	got := n.ChannelReplicationCapability()
	if got.NodeID != 7 || got.SupportsChannelAuthorityPromise() != channelReplicationImplementsPromises {
		t.Fatalf("capability = %+v, implements promises = %v", got, channelReplicationImplementsPromises)
	}
	if got.ProtocolVersion == 0 || got.ProtocolVersion > controller.ChannelAuthorityPromiseProtocolVersion {
		t.Fatalf("protocol version = %d", got.ProtocolVersion)
	}
}
