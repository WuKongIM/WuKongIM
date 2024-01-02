package cluster

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type Slot struct {
	nodeID        uint64
	slotID        uint32
	dataDir       string
	replicaServer *replica.Replica
}

func NewSlot(nodeID uint64, slotID uint32, replicaNodeIDs []uint64, dataDir string, trans replica.ITransport) *Slot {
	return &Slot{
		nodeID:        nodeID,
		slotID:        slotID,
		dataDir:       dataDir,
		replicaServer: replica.New(nodeID, fmt.Sprintf("%d", slotID), replica.WithDataDir(dataDir), replica.WithReplicas(replicaNodeIDs), replica.WithTransport(trans)),
	}
}

func (s *Slot) Start() error {
	return s.replicaServer.Start()
}

func (s *Slot) Stop() {
	s.replicaServer.Stop()
}

func (s *Slot) AppendLog(lg replica.Log) error {

	return s.replicaServer.AppendLog(lg)
}

func (s *Slot) LastLogIndex() (uint64, error) {
	return s.replicaServer.LastIndex()
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
