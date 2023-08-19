package wraft

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wraft/wpb"
)

type ClusterFSMManager struct {
	clusterConfigManager *ClusterConfigManager
	stopped              chan struct{}
	nodeID               uint64
	TickerDuration       time.Duration
	readyChan            chan ReadyCluster
}

func NewClusterFSMManager(nodeID uint64, clusterConfigManager *ClusterConfigManager) *ClusterFSMManager {

	return &ClusterFSMManager{
		clusterConfigManager: clusterConfigManager,
		stopped:              make(chan struct{}),
		nodeID:               nodeID,
		TickerDuration:       time.Second,
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

	tick := time.NewTicker(c.TickerDuration)
	var readyCluster ReadyCluster

	for {

		select {
		case <-tick.C:
			clusterConfig := c.clusterConfigManager.GetClusterConfig()
			readyCluster = c.checkPeerStatus(clusterConfig)
			if readyCluster.State == ClusterStateNone {
				continue
			}
			c.readyChan <- readyCluster
		case <-c.stopped:
			return
		}
	}
}

func (c *ClusterFSMManager) checkPeerStatus(clusterConfig *wpb.ClusterConfig) ReadyCluster {

	var (
		peers = clusterConfig.Peers
	)
	for _, peer := range peers {
		if peer.Id == c.nodeID {
			continue
		}
		if peer.Status == wpb.Status_WillRemove {
			return ReadyCluster{
				State: ClusterStateWillRemove,
				Peer:  peer,
			}
		} else if peer.Status == wpb.Status_WillJoin {
			return ReadyCluster{
				State: ClusterStateWillJoin,
				Peer:  peer,
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
	ClusterStateWillRemove
	ClusterStateWillJoin
)

type ReadyCluster struct {
	State ClusterState
	Peer  *wpb.Peer
}
