package cluster

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// channelReplicationImplementsPromises reports whether this binary's Channel
// replication fences new authorities with durable promises once activation is
// committed. Older binaries answer the barrier protocol or not at all, so a
// cluster can never activate before every node acts on activation.
const channelReplicationImplementsPromises = true

const (
	// channelReplicationBarrierProtocolVersion fences authorities with in-log barriers.
	channelReplicationBarrierProtocolVersion uint32 = 1
	// channelPromiseActivationFirstDelay is the first attempt's delay after
	// start; later attempts double it up to channelPromiseActivationInterval,
	// so a freshly formed cluster activates within seconds of becoming ready.
	channelPromiseActivationFirstDelay = time.Second
	// channelPromiseActivationInterval caps the pace of activation attempts.
	channelPromiseActivationInterval = 30 * time.Second
	// channelPromiseActivationTimeout bounds one activation attempt.
	channelPromiseActivationTimeout = 10 * time.Second
)

// channelPromiseActivator is the hosted Controller runtime's activation port.
type channelPromiseActivator interface {
	EnsureChannelAuthorityPromise(context.Context) (bool, error)
}

// ChannelReplicationCapability is this running process's answer to the
// Channel replication capability probe.
func (n *Node) ChannelReplicationCapability() controller.ChannelReplicationCapability {
	version := channelReplicationBarrierProtocolVersion
	if channelReplicationImplementsPromises {
		version = controller.ChannelAuthorityPromiseProtocolVersion
	}
	return controller.ChannelReplicationCapability{NodeID: n.cfg.NodeID, ProtocolVersion: version}
}

// ChannelAuthorityPromiseActive reports committed one-way activation from an
// applied Controller snapshot. Channel replication reads it on every append,
// so it is one atomic load rather than the Node lock.
func (n *Node) ChannelAuthorityPromiseActive() bool {
	return n != nil && n.channelPromiseActive.Load()
}

// observeChannelPromiseActivation latches activation from an applied snapshot.
// Activation is one-way: a later snapshot can never clear it.
func (n *Node) observeChannelPromiseActivation(active bool) {
	if active {
		n.channelPromiseActive.Store(true)
	}
}

// tickChannelPromiseActivation attempts activation once when this node is the
// Controller leader and the snapshot is still inactive. It reports whether an
// attempt was made. Failures keep barrier mode and are retried next tick.
func (n *Node) tickChannelPromiseActivation(ctx context.Context) bool {
	if n == nil || n.control == nil || n.ChannelAuthorityPromiseActive() || n.control.LeaderID() != n.cfg.NodeID {
		return false
	}
	activator, ok := n.control.(channelPromiseActivator)
	if !ok {
		return false
	}
	callCtx, cancel := context.WithTimeout(ctx, channelPromiseActivationTimeout)
	defer cancel()
	active, err := activator.EnsureChannelAuthorityPromise(callCtx)
	if logger := namedLogger(n.cfg.Logger, "controller"); logger != nil {
		if err != nil {
			logger.Debug("channel authority promise activation deferred",
				wklog.Event("cluster.channel_promise.activation_deferred"), wklog.Error(err))
		} else if active {
			logger.Info("channel authority promises active", wklog.Event("cluster.channel_promise.activated"))
		}
	}
	return true
}

// startChannelPromiseActivationLoop runs bounded, paced activation attempts.
func (n *Node) startChannelPromiseActivationLoop() {
	if n == nil || n.control == nil || n.channelPromiseCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.channelPromiseCancel = cancel
	n.channelPromiseWG.Add(1)
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterChannelPromiseActivation, func() {
		defer n.channelPromiseWG.Done()
		delay := channelPromiseActivationFirstDelay
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if n.ChannelAuthorityPromiseActive() {
					return
				}
				n.tickChannelPromiseActivation(ctx)
				delay = min(2*delay, channelPromiseActivationInterval)
				timer.Reset(delay)
			}
		}
	})
}

// stopChannelPromiseActivationLoop stops and joins the activation loop.
func (n *Node) stopChannelPromiseActivationLoop() {
	if n == nil || n.channelPromiseCancel == nil {
		return
	}
	n.channelPromiseCancel()
	n.channelPromiseWG.Wait()
	n.channelPromiseCancel = nil
}

// probeChannelReplicationCapability asks one exact member endpoint. Self is
// answered locally only for the configured membership address; a joined peer
// uses its resolved address; an unjoined endpoint uses one private pinned
// client that is stopped before returning. Every failure is an error.
func (n *Node) probeChannelReplicationCapability(ctx context.Context, nodeID uint64, addr string) (controller.ChannelReplicationCapability, error) {
	if nodeID == 0 || addr == "" || len(addr) > 1024 {
		return controller.ChannelReplicationCapability{}, controller.ErrChannelPromiseNotReady
	}
	if nodeID == n.cfg.NodeID {
		if addr != n.selfMembershipAddr() {
			return controller.ChannelReplicationCapability{}, controller.ErrChannelPromiseNotReady
		}
		return n.ChannelReplicationCapability(), nil
	}
	if n.discovery != nil && n.transportClient != nil {
		if resolved, err := n.discovery.Resolve(transport.NodeID(nodeID)); err == nil && resolved == addr {
			return control.NewChannelReplicationControlClient(n.transportClient).Capabilities(ctx, nodeID)
		}
	}
	n.channelPromiseProbeMu.Lock()
	defer n.channelPromiseProbeMu.Unlock()
	if n.stopping.Load() {
		return controller.ChannelReplicationCapability{}, ErrStopping
	}
	discovery := clusternet.NewDiscovery()
	discovery.Update([]clusternet.NodeAddress{{NodeID: nodeID, Addr: addr}})
	client := clusternet.NewTransportClient(clusternet.TransportClientConfig{NodeID: n.cfg.NodeID, Discovery: discovery, PoolSize: 1, DialTimeout: time.Second, QueueSize: 1})
	proof, probeErr := control.NewChannelReplicationControlClient(client).Capabilities(ctx, nodeID)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if stopErr := client.StopContext(stopCtx); stopErr != nil || probeErr != nil {
		return controller.ChannelReplicationCapability{}, errors.Join(probeErr, stopErr)
	}
	return proof, nil
}

// selfMembershipAddr is the address this node is known by in membership,
// which may differ from the listener behind a relay or NAT.
func (n *Node) selfMembershipAddr() string {
	if n.cfg.seedJoinMode() {
		return n.cfg.Join.AdvertiseAddr
	}
	for _, voter := range n.cfg.Control.Voters {
		if voter.NodeID == n.cfg.NodeID {
			return voter.Addr
		}
	}
	return n.cfg.ListenAddr
}

// channelPromiseProbeState guards the private pinned probe client.
type channelPromiseProbeState struct {
	// channelPromiseActive latches committed one-way activation.
	channelPromiseActive  atomic.Bool
	channelPromiseProbeMu sync.Mutex
	channelPromiseCancel  context.CancelFunc
	channelPromiseWG      sync.WaitGroup
}
