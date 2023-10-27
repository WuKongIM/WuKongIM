package cluster

import (
	"context"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/pb"
	"github.com/WuKongIM/WuKongIM/pkg/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	bolt "go.etcd.io/bbolt"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type Cluster struct {
	raft   *multiraft.Raft // 节点之间的raft服务
	opts   *Options
	boltdb *bolt.DB
	wklog.Log

	hardStateKey []byte
	confStateKey []byte
	appliedKey   []byte
	raftStorage  *multiraft.WALStorage // raft 数据存储

	clusterManager *ClusterManager // 分布式配置管理

	leaderID    atomic.Uint64 // 领导ID
	appiedIndex atomic.Uint64 // 最后一次日志应用下标

	slotRaftServer *multiraft.Server // slot 多组raft服务
	transporter    *Transporter      // 网络

	stopChan  chan struct{}
	cancelCtx context.Context
	cancelFnc context.CancelFunc

	sendRequestAllocateSlotSet bool
}

func New(opts *Options) *Cluster {

	c := &Cluster{
		Log:          wklog.NewWKLog(fmt.Sprintf("Cluster[%d]", opts.NodeID)),
		hardStateKey: []byte("hardState"),
		confStateKey: []byte("confState"),
		appliedKey:   []byte("appliedKey"),
		stopChan:     make(chan struct{}),
	}
	c.cancelCtx, c.cancelFnc = context.WithCancel(context.Background())

	c.transporter = NewTransporter(opts.NodeID, opts.Addr)
	c.transporter.onNodeRaftMessage = func(m raftpb.Message) {
		c.raft.OnRaftMessage(m)
	}

	err := os.MkdirAll(opts.DataDir, 0755)
	if err != nil {
		c.Panic("mkdir data dir is error", zap.Error(err))
	}
	clusterPath := path.Join(opts.DataDir, "cluster.json")

	// -------------------- 分布式配置管理者 --------------------
	clusterManagerOpts := NewClusterManagerOptions()
	clusterManagerOpts.ConfigPath = clusterPath
	clusterManagerOpts.SlotCount = opts.SlotCount
	clusterManagerOpts.ReplicaCount = opts.ReplicaCount
	clusterManagerOpts.NodeID = opts.NodeID
	clusterManagerOpts.GetSlotState = func(slotID uint32) SlotState {
		replica := c.slotRaftServer.GetReplica(slotID)
		if replica == nil {
			return SlotStateNotExist
		}
		return SlotStateNormal
	}
	c.clusterManager = NewClusterManager(clusterManagerOpts)

	// -------------------- 节点之间的raft --------------------
	raftOpts := multiraft.NewRaftOptions()
	raftOpts.DataDir = opts.DataDir
	c.raftStorage = multiraft.NewWALStorage(path.Join(opts.DataDir, "wal"))
	raftOpts.ID = opts.NodeID
	raftOpts.RaftStorage = c
	raftOpts.Peers = opts.Peers
	raftOpts.OnSend = func(ctx context.Context, m raftpb.Message) error {
		err := c.transporter.SendToNode(ctx, m)
		if err != nil {
			return err
		}
		return nil
	}
	// 领导改变
	raftOpts.LeaderChange = func(newLeaderID, oldLeaderID uint64) {
		c.leaderID.Store(newLeaderID)
		if opts.LeaderChange != nil {
			if newLeaderID != oldLeaderID {
				opts.LeaderChange(newLeaderID)
			}
		}
		c.clusterManager.SetLeaderID(newLeaderID)
	}
	// 应用日志
	raftOpts.OnApply = func(m []raftpb.Entry) error {
		return c.nodeApply(m)
	}

	rft := multiraft.NewRaft(raftOpts)
	c.raft = rft
	c.opts = opts

	// -------------------- slot之间的raft --------------------
	slotRaftOpts := multiraft.NewOptions()
	slotRaftOpts.Addr = opts.Addr
	slotRaftOpts.PeerID = opts.NodeID
	slotRaftOpts.Transporter = c.transporter
	slotRaftOpts.RootDir = path.Join(opts.DataDir, "slots")
	slotRaftOpts.StateMachine = c
	c.slotRaftServer = multiraft.New(slotRaftOpts)

	return c
}

func (c *Cluster) Start() error {
	var err error
	c.boltdb, err = bolt.Open(path.Join(c.opts.DataDir, "cluster.db"), 0755, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return err
	}

	err = c.raftStorage.Open()
	if err != nil {
		return err
	}
	err = c.boltdb.Batch(func(t *bolt.Tx) error {
		_, err := t.CreateBucketIfNotExists(c.hardStateKey)
		if err != nil {
			return err
		}
		_, err = t.CreateBucketIfNotExists(c.confStateKey)
		if err != nil {
			return err
		}
		_, err = t.CreateBucketIfNotExists(c.appliedKey)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	err = c.transporter.Start()
	if err != nil {
		return err
	}

	err = c.clusterManager.Start()
	if err != nil {
		return err
	}

	isRestart, err := c.isRestart()
	if err != nil {
		return err
	}
	err = c.setApplied()
	if err != nil {
		return err
	}

	if len(c.opts.Peers) > 0 {
		for _, peer := range c.opts.Peers {
			if peer.ID == c.opts.NodeID {
				continue
			}
			err = c.transporter.AddPeer(peer)
			if err != nil {
				return err
			}
		}
	}
	c.raft.GetOptions().Restart = isRestart

	err = c.raft.Start()
	if err != nil {
		return err
	}

	err = c.slotRaftServer.Start()
	if err != nil {
		return err
	}

	go c.loopClusterConfig()

	return nil
}

func (c *Cluster) Stop() {

	close(c.stopChan)

	c.cancelFnc()

	c.slotRaftServer.Stop()

	c.raft.Stop()

	c.clusterManager.Stop()

	c.transporter.Stop()

	err := c.raftStorage.Close()
	if err != nil {
		c.Warn("raft storage close error", zap.Error(err))
	}

	err = c.boltdb.Close()
	if err != nil {
		c.Warn("boltdb close error", zap.Error(err))
	}
}

func (c *Cluster) setApplied() error {
	hardState, err := c.HardState()
	if err != nil {
		return err
	}
	lastCommitIndex := hardState.Commit
	appliedIndex, err := c.Applied()
	if err != nil {
		return err
	}
	if appliedIndex > lastCommitIndex {
		appliedIndex = lastCommitIndex
	}
	c.appiedIndex.Store(appliedIndex)
	c.raft.GetOptions().Applied = appliedIndex
	return nil
}

func (c *Cluster) isRestart() (bool, error) {
	lastLogIndex, err := c.raftStorage.LastIndex()
	if err != nil {
		return false, err
	}
	if lastLogIndex > 0 {
		return true, nil
	}
	return false, nil
}

func (c *Cluster) loopClusterConfig() {
	for {
		select {
		case clusterReady := <-c.clusterManager.readyChan:
			if clusterReady.AllocateSlotSet != nil {
				c.requestAllocateSlotSet(clusterReady.AllocateSlotSet)
			}

		case <-c.stopChan:
			return
		}
	}
}

func (c *Cluster) requestAllocateSlotSet(allocateSlotSet *pb.AllocateSlotSet) {
	if c.sendRequestAllocateSlotSet {
		return
	}
	fmt.Println("requestAllocateSlotSet------>1")
	req := pb.NewCMDReq(pb.CMDAllocateSlot.Uint32())

	param, err := allocateSlotSet.Marshal()
	if err != nil {
		c.Error("request allocate slot marshal error", zap.Error(err))
		return
	}
	req.Param = param
	data, err := req.Marshal()
	if err != nil {
		c.Error("request init slot marshal error", zap.Error(err))
		return
	}
	fmt.Println("requestAllocateSlotSet------>2")
	c.sendRequestAllocateSlotSet = true
	err = c.ProposeForNode(data)
	if err != nil {
		c.Error("request init slot propose error", zap.Error(err))
		return
	}
}

func (c *Cluster) ProposeForNode(data []byte) error {
	return c.raft.Propose(c.cancelCtx, data)
}

func (c *Cluster) stateTypeToRole(role raft.StateType) pb.Role {
	switch role {
	case raft.StateFollower:
		return pb.Role_Follower
	case raft.StateCandidate:
		return pb.Role_Candidate
	case raft.StateLeader:
		return pb.Role_Leader
	default:
		return pb.Role_Follower
	}
}
