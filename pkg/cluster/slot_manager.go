package cluster

import (
	"fmt"
	"path"
	"strconv"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"
)

type SlotManager struct {
	sync.RWMutex
	slotMap          map[uint32]*Slot
	transportSync    *slotTransportSync
	s                *Server
	shardLogStorage  *PebbleStorage // 分区日志存储
	slotStateMachine *slotStateMachine
}

func NewSlotManager(s *Server) *SlotManager {

	return &SlotManager{
		slotMap:          make(map[uint32]*Slot),
		s:                s,
		transportSync:    newSlotTransportSync(s),
		shardLogStorage:  NewPebbleStorage(path.Join(s.opts.DataDir, "logs")),
		slotStateMachine: newSlotStateMachine(path.Join(s.opts.DataDir, "slotdb")),
	}
}

func (s *SlotManager) AddSlot(st *Slot) {
	s.Lock()
	defer s.Unlock()
	s.slotMap[st.slotID] = st
}

func (s *SlotManager) RemoveSlot(slotID uint32) {
	s.Lock()
	defer s.Unlock()
	delete(s.slotMap, slotID)
}

func (s *SlotManager) GetSlot(slotID uint32) *Slot {
	s.RLock()
	defer s.RUnlock()
	return s.slotMap[slotID]
}

func (s *SlotManager) GetSlots() []*Slot {
	s.RLock()
	defer s.RUnlock()
	slots := make([]*Slot, 0, len(s.slotMap))
	for _, slot := range s.slotMap {
		slots = append(slots, slot)
	}
	return slots
}

func (s *SlotManager) NewSlot(slot *pb.Slot) (*Slot, error) {
	shardNo := fmt.Sprintf("%d", slot.Id)
	appliedIndex, err := s.shardLogStorage.GetAppliedIndex(shardNo)
	if err != nil {
		return nil, err
	}
	syncInfos, err := s.slotStateMachine.getSlotSyncInfos(slot.Id)
	if err != nil {
		return nil, err
	}

	lastSyncInfoMap := make(map[uint64]replica.SyncInfo)

	for _, syncInfo := range syncInfos {
		lastSyncInfoMap[syncInfo.NodeID] = *syncInfo
	}

	st := NewSlot(s.s.opts.NodeID, slot.Id, slot.Replicas, lastSyncInfoMap, appliedIndex, path.Join(s.s.opts.DataDir, "slots", strconv.FormatUint(uint64(slot.Id), 10)), s.transportSync, s.shardLogStorage, s.handleApplyLog(slot.Id, nil))
	st.replicaServer.SetLeaderID(slot.Leader)
	err = st.Start()
	return st, err
}

func (s *SlotManager) Start() error {
	err := s.shardLogStorage.Open()
	if err != nil {
		return err
	}
	err = s.slotStateMachine.open()
	return err
}

func (s *SlotManager) Stop() {
	for _, v := range s.slotMap {
		v.Stop()
	}
	s.shardLogStorage.Close()
	s.slotStateMachine.close()
}

func (s *SlotManager) handleApplyLog(slotID uint32, logs []replica.Log) func(logs []replica.Log) (uint64, error) {
	return func(logs []replica.Log) (uint64, error) {
		if len(logs) == 0 {
			return 0, nil
		}
		appliedIdx, err := s.slotStateMachine.applyLogs(slotID, logs)
		return appliedIdx, err
	}
}
