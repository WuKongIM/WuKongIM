package wraft

import (
	"io"
	"os"
	"path"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/wpb"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type ClusterConfigManager struct {
	sync.RWMutex
	clusterConfig    *wpb.ClusterConfig
	clusterStorePath string
	wklog.Log
	stopChan     chan struct{}
	doneChan     chan struct{}
	saveInterval time.Duration
	change       atomic.Bool
}

func NewClusterConfigManager(clusterStorePath string) *ClusterConfigManager {
	c := &ClusterConfigManager{
		clusterStorePath: clusterStorePath,
		Log:              wklog.NewWKLog("ClusterConfigManager"),
		clusterConfig:    &wpb.ClusterConfig{},
		stopChan:         make(chan struct{}),
		doneChan:         make(chan struct{}),
		saveInterval:     time.Second * 2,
	}

	err := c.load()
	if err != nil {
		c.Panic("failed to load cluster config", zap.Error(err))
	}
	return c
}

func (c *ClusterConfigManager) load() error {
	clusterDir := path.Dir(c.clusterStorePath)
	err := os.MkdirAll(clusterDir, 0755)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(c.clusterStorePath, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	err = wkutil.ReadJSONByByte(data, c.clusterConfig)
	if err != nil {
		return err
	}
	return nil
}

func (c *ClusterConfigManager) Start() {
	go c.run()
}

func (c *ClusterConfigManager) run() {
	tick := time.NewTicker(c.saveInterval)

	defer c.onStop()
	for {
		select {
		case <-tick.C:
			if c.change.Load() {
				err := c.Save()
				if err != nil {
					c.Warn("failed to save cluster config", zap.Error(err))
				}
				c.change.Store(false)
			}

		case <-c.stopChan:
			return
		}
	}
}

func (c *ClusterConfigManager) Stop() {
	c.stopChan <- struct{}{}
	<-c.doneChan
}

func (c *ClusterConfigManager) onStop() {
	err := c.Save()
	if err != nil {
		c.Warn("failed to save cluster config", zap.Error(err))
	}

	close(c.doneChan)
}

func (c *ClusterConfigManager) AddOrUpdatePeer(peer *wpb.Peer) {
	c.Lock()
	defer c.Unlock()

	for _, p := range c.clusterConfig.Peers {
		if p.Id == peer.Id {
			*p = *peer.Clone()
			c.change.Store(true)
			return
		}
	}
	c.clusterConfig.Peers = append(c.clusterConfig.Peers, peer)
	c.change.Store(true)
}

func (c *ClusterConfigManager) RemovePeer(id uint64) {
	c.Lock()
	defer c.Unlock()

	for _, p := range c.clusterConfig.Peers {
		if p.Id == id {
			p.Status = wpb.Status_Removed
			c.change.Store(true)
			return
		}
	}
}

func (c *ClusterConfigManager) GetPeer(id uint64) *wpb.Peer {
	c.RLock()
	defer c.RUnlock()

	for _, p := range c.clusterConfig.Peers {
		if p.Id == id {
			return p.Clone()
		}
	}
	return nil

}

func (c *ClusterConfigManager) GetPeerNoClone(id uint64) *wpb.Peer {
	c.RLock()
	defer c.RUnlock()

	for _, p := range c.clusterConfig.Peers {
		if p.Id == id {
			return p
		}
	}
	return nil

}

func (c *ClusterConfigManager) GetPeers() []*wpb.Peer {
	c.RLock()
	defer c.RUnlock()
	var peers []*wpb.Peer
	for _, p := range c.clusterConfig.Peers {
		peers = append(peers, p.Clone())
	}
	return peers
}

func (c *ClusterConfigManager) UpdateHardState(peerID uint64, state raftpb.HardState) {
	peer := c.GetPeerNoClone(peerID)
	if peer != nil {
		if state.Term > peer.Term || peer.Vote != state.Vote {
			peer.Term = state.Term
			peer.Vote = state.Vote
			c.AddOrUpdatePeer(peer)
		}
	}
}

func (c *ClusterConfigManager) UpdateSoftState(peerID uint64, state *raft.SoftState) {
	peer := c.GetPeerNoClone(peerID)
	if peer != nil {
		peer.Role = c.raftStateTypeToRole(state.RaftState)
		peer.Lead = state.Lead
		c.AddOrUpdatePeer(peer)
	}
}

func (c *ClusterConfigManager) UpdatePeerStatus(peerID uint64, status wpb.Status) {
	peer := c.GetPeer(peerID)
	if peer != nil {
		peer.Status = status
		c.AddOrUpdatePeer(peer)
	}
}

func (c *ClusterConfigManager) Save() error {

	c.Lock()
	defer c.Unlock()

	clusterDir := path.Dir(c.clusterStorePath)
	err := os.MkdirAll(clusterDir, 0755)
	if err != nil {
		return err
	}
	f, err := os.Create(c.clusterStorePath)
	if err != nil {
		return err
	}
	defer f.Close()
	clusterConfigStr := wkutil.ToJSON(c.clusterConfig)
	_, err = f.WriteString(clusterConfigStr)
	return err
}

func (c *ClusterConfigManager) GetClusterConfig() *wpb.ClusterConfig {
	c.RLock()
	defer c.RUnlock()
	return c.clusterConfig.Clone()
}

func (c *ClusterConfigManager) raftStateTypeToRole(state raft.StateType) wpb.Role {
	switch state {
	case raft.StateLeader:
		return wpb.Role_Leader
	case raft.StatePreCandidate:
		return wpb.Role_PreCandidate
	case raft.StateCandidate:
		return wpb.Role_Candidate
	case raft.StateFollower:
		return wpb.Role_Follower
	default:
		return wpb.Role_Unknown
	}
}
