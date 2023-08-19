package wraft

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wraft/wpb"
)

type ClusterFSMManager struct {
	clusterConfigManager *ClusterConfigManager
	stopped              chan struct{}
	peerID               uint64
	IntervalDuration     time.Duration
	readyChan            chan ReadyCluster
}

func NewClusterFSMManager(peerID uint64, clusterConfigManager *ClusterConfigManager) *ClusterFSMManager {

	return &ClusterFSMManager{
		clusterConfigManager: clusterConfigManager,
		stopped:              make(chan struct{}),
		peerID:               peerID,
		IntervalDuration:     time.Second,
		readyChan:            make(chan ReadyCluster),
	}
}

func (c *ClusterFSMManager) Start() {

	go c.loopClusterConfigChange()
}

func (c *ClusterFSMManager) Stop() {
	close(c.stopped)

}

func (c *ClusterFSMManager) loopClusterConfigChange() {

	tick := time.NewTicker(c.IntervalDuration)
	var readyCluster ReadyCluster

	for {

		select {
		case <-tick.C:
			clusterConfig := c.clusterConfigManager.GetClusterConfig()
			readyCluster = c.checkClusterStatus(clusterConfig)
			if readyCluster.State == ClusterStateNone {
				continue
			}
			c.readyChan <- readyCluster
		case <-c.stopped:
			return
		}
	}
}

func (c *ClusterFSMManager) checkClusterStatus(clusterConfig *wpb.ClusterConfig) ReadyCluster {
	readyCluster := c.checkPeerStatus(clusterConfig)
	if readyCluster.State != ClusterStateNone {
		return readyCluster
	}
	readyCluster = c.checkPeerConfigUpdate(clusterConfig)
	if readyCluster.State != ClusterStateNone {
		return readyCluster
	}
	return emptyReadyCluster
}

func (c *ClusterFSMManager) checkPeerStatus(clusterConfig *wpb.ClusterConfig) ReadyCluster {
	var (
		peers = clusterConfig.Peers
	)
	for _, peer := range peers {
		if peer.Id == c.peerID {
			continue
		}
		if peer.Status == wpb.Status_WillRemove || peer.Status == wpb.Status_WillJoin || peer.Status == wpb.Status_Joining {
			return ReadyCluster{
				State: ClusterStatePeerStatusChange,
				Peer:  peer,
			}
		}
	}
	return emptyReadyCluster
}

func (c *ClusterFSMManager) checkPeerConfigUpdate(clusterConfig *wpb.ClusterConfig) ReadyCluster {
	selfPeer := c.clusterConfigManager.GetPeer(c.peerID)
	for _, peer := range clusterConfig.Peers {
		if selfPeer.Id != peer.Id {
			if peer.Term < selfPeer.Term {
				return ReadyCluster{
					State: ClusterStatePeerConfigUpdate,
					Peer:  peer,
				}
			}
		}
	}
	return emptyReadyCluster
}

func (c *ClusterFSMManager) ReadyChan() chan ReadyCluster {
	return c.readyChan
}

type ClusterState uint32

var emptyReadyCluster ReadyCluster

const (
	ClusterStateNone ClusterState = iota
	ClusterStatePeerStatusChange
	ClusterStatePeerConfigUpdate
)

type ReadyCluster struct {
	State ClusterState
	Peer  *wpb.Peer
}
