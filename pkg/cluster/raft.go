package cluster

import (
	"context"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/dragonboat/v4"
	"github.com/lni/dragonboat/v4/config"
	"github.com/lni/dragonboat/v4/raftio"
	sm "github.com/lni/dragonboat/v4/statemachine"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type IRaft interface {
	Start() error
}

var _ IRaft = (*Raft)(nil)

const PeerShardID uint32 = 1000000 // 将1000000设置为peer之间的shardID，slot不可能大于等于1000000

type Raft struct {
	nodehost *dragonboat.NodeHost
	wklog.Log
	opts *RaftOptions

	leaderChangeChan chan struct{}
	leaderID         atomic.Uint64
}

func NewRaft(opts *RaftOptions) *Raft {

	lg := wklog.NewWKLog("Raft")
	r := &Raft{
		Log:              lg,
		opts:             opts,
		leaderChangeChan: make(chan struct{}),
	}
	nhc := config.NodeHostConfig{
		WALDir:            opts.DataDir,
		NodeHostDir:       opts.DataDir,
		RaftAddress:       opts.ServerAddr,
		ListenAddress:     opts.Addr,
		RTTMillisecond:    200,
		RaftEventListener: r,
	}
	nhc.Expert.NodeRegistryFactory = r
	nodehost, err := dragonboat.NewNodeHost(nhc)
	if err != nil {
		lg.Panic(err.Error())
	}
	r.nodehost = nodehost
	return r
}

func (r *Raft) Start() error {
	rc := r.getDefaultRaftConfig()
	rc.ShardID = uint64(r.opts.ShardID)

	var initialMembers = make(map[uint64]string)
	join := strings.TrimSpace(r.opts.Join) != ""
	if !join { // 后续加入的节点，不需要初始化成员
		if len(r.opts.InitNodes) > 0 {
			for nodeID, nodeAddr := range r.opts.InitNodes {
				initialMembers[nodeID.Uint64()] = nodeAddr
			}
		}
	}
	// 判断是否包含自己本身,如果没包含则加入
	_, ok := initialMembers[r.opts.ID.Uint64()]
	if !ok {
		initialMembers[r.opts.ID.Uint64()] = r.opts.ServerAddr
	}
	err := r.nodehost.StartOnDiskReplica(initialMembers, join, func(shardID, replicaID uint64) sm.IOnDiskStateMachine {
		return newStateMachine(shardID, replicaID, r.opts)
	}, rc)
	if err != nil {
		r.Panic("StartReplica error", zap.Error(err))
		return err
	}
	return nil
}

func (r *Raft) Stop() {
	r.nodehost.Close()
}

func (r *Raft) LeaderUpdated(info raftio.LeaderInfo) {
	r.leaderID.Store(info.LeaderID)
	select {
	case r.leaderChangeChan <- struct{}{}:
	default:
	}
}

func (r *Raft) WaitLeader(timeout time.Duration) error {
	if r.leaderID.Load() > 0 {
		return nil
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case <-r.leaderChangeChan:
		return nil
	case <-timeoutCtx.Done():
		return timeoutCtx.Err()
	}
}

func (r *Raft) MustWaitLeader(timeout time.Duration) {
	err := r.WaitLeader(timeout)
	if err != nil {
		r.Panic("WaitLeader error", zap.Error(err))
	}
}

func (r *Raft) Create(nhid string, streamConnections uint64, v config.TargetValidator) (raftio.INodeRegistry, error) {

	return r.opts.NodeRegistry, nil
}

func (r *Raft) getDefaultRaftConfig() config.Config {
	return config.Config{
		ReplicaID:              r.opts.ID.Uint64(),
		ElectionRTT:            5,
		HeartbeatRTT:           1,
		CheckQuorum:            true,
		DisableAutoCompactions: true,
		CompactionOverhead:     0, // 不压缩
		SnapshotEntries:        0,
	}
}
