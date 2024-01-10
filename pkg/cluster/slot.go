package cluster

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type Slot struct {
	nodeID        uint64
	slotID        uint32
	dataDir       string
	replicaServer *replica.Replica

	wklog.Log
}

func NewSlot(nodeID uint64, slotID uint32, replicaNodeIDs []uint64, lastSyncInfoMap map[uint64]replica.SyncInfo, appliedIndex uint64, dataDir string, trans replica.ITransport, storage IShardLogStorage, onApply func(logs []replica.Log) (uint64, error)) *Slot {
	shardNo := fmt.Sprintf("%d", slotID)
	s := &Slot{
		nodeID:  nodeID,
		slotID:  slotID,
		dataDir: dataDir,
		Log:     wklog.NewWKLog(fmt.Sprintf("slot[%d]", slotID)),
	}
	s.replicaServer = replica.New(
		nodeID,
		shardNo,
		replica.WithAppliedIndex(appliedIndex),
		replica.WithReplicas(replicaNodeIDs),
		replica.WithLastSyncInfoMap(lastSyncInfoMap),
		replica.WithTransport(trans),
		replica.WithStorage(newProxyReplicaStorage(shardNo, storage)),
		replica.WithOnApply(onApply),
	)

	return s
}

func (s *Slot) Start() error {
	return s.replicaServer.Start()
}

func (s *Slot) Stop() {
	s.replicaServer.Stop()
}

func (s *Slot) Propose(data []byte) error {
	s.Debug("Propose", zap.Uint64("nodeID", s.nodeID), zap.ByteString("data", data))
	return s.replicaServer.Propose(data)
}

func (s *Slot) LastLogIndex() (uint64, error) {
	return s.replicaServer.LastIndex()
}
func (s *Slot) IsLeader() bool {
	return s.replicaServer.IsLeader()
}

func (s *Slot) LeaderID() uint64 {
	return s.replicaServer.LeaderID()
}

func (s *Slot) SyncLogs(nodeID uint64, startLogIndex uint64, limit uint32) ([]replica.Log, error) {
	return s.replicaServer.SyncLogs(nodeID, startLogIndex, limit)
}

func (s *Slot) SetLeaderID(v uint64) {
	s.replicaServer.SetLeaderID(v)
}

func (s *Slot) SetReplicas(replicas []uint64) {
	s.replicaServer.SetReplicas(replicas)
}

func (s *Slot) GetLogs(startLogIndex uint64, limit uint32) ([]replica.Log, error) {
	return s.replicaServer.GetLogs(startLogIndex, limit)
}
