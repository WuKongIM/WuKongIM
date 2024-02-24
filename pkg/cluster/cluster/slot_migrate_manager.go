package cluster

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type slotMigrateManager struct {
	s       *Server
	imports []*SlotMigrate
	stopper *syncutil.Stopper
	wklog.Log
}

func newSlotMigrateManager(s *Server) *slotMigrateManager {
	return &slotMigrateManager{
		s:       s,
		stopper: syncutil.NewStopper(),
		Log:     wklog.NewWKLog("slotMigrateManager"),
	}
}

func (s *slotMigrateManager) start() {
	s.stopper.RunWorker(s.run)
}

func (s *slotMigrateManager) stop() {
	s.stopper.Stop()
}

func (s *slotMigrateManager) run() {
	tk := time.NewTicker(time.Second * 2)
	var err error
	for {
		select {
		case <-tk.C:
			for _, m := range s.imports {
				if m.Status == pb.MigrateStatus_MigrateStatusDone {
					// node := s.s.getClusterConfigManager().node(s.s.opts.NodeID)
					// if node != nil {
					// 	if len(node.Imports) == 0 {
					// 		s.remove(m)
					// 	}
					// }
					continue
				}
				slot := s.s.getClusterConfigManager().slot(m.Slot)
				if slot == nil {
					s.Warn("slot not found", zap.Uint32("slot", m.Slot))
					continue
				}
				if !s.s.slotManager.exist(m.Slot) {
					slotClone := slot.Clone()
					if !wkutil.ArrayContainsUint64(slotClone.Replicas, s.s.opts.NodeID) {
						slotClone.Replicas = append(slotClone.Replicas, s.s.opts.NodeID)
					}
					err = s.s.addSlots([]*pb.Slot{slotClone})
					if err != nil {
						s.Error("add slot error", zap.Error(err))
					}
				} else {
					st := s.s.slotManager.slot(m.Slot)
					if st.rc.HasFirstSyncResp() { // 等领导第一次相应同步请求后，follower才做迁移判断，要不然空的日志无法确定是否迁移完成
						leaderId := s.s.getClusterConfigManager().leaderId()
						if leaderId == 0 || leaderId == s.s.opts.NodeID {
							continue
						}
						if st.rc.LastLogIndex()-st.rc.LeaderLastLogIndex() < uint64(s.s.opts.LogCaughtUpWithLeaderNum) { // 如果follower追上leader的日志，则可以通知节点领导去迁移
							err = s.s.nodeManager.requestSlotMigrateFinished(s.s.getClusterConfigManager().leaderId(), &SlotMigrateFinishReq{
								SlotId: m.Slot,
								From:   m.From,
								To:     m.To,
							})
							if err != nil {
								s.Error("requestSlotMigrateFinished error", zap.Error(err))
								continue
							}
							m.Status = pb.MigrateStatus_MigrateStatusDone
							fmt.Println("migrate done-->", m.Slot, m.From, m.To)
						}
					}

				}
			}
		case <-s.stopper.ShouldStop():
			return
		}

	}
}

func (s *slotMigrateManager) add(migrate *SlotMigrate) {
	s.imports = append(s.imports, migrate)
}

func (s *slotMigrateManager) remove(slotId uint32) {
	for i, m := range s.imports {
		if m.Slot == slotId {
			s.imports = append(s.imports[:i], s.imports[i+1:]...)
			break
		}
	}
}

func (s *slotMigrateManager) get(slotId uint32) *SlotMigrate {
	for _, m := range s.imports {
		if m.Slot == slotId {
			return m
		}
	}
	return nil
}

func (s *slotMigrateManager) exist(slotId uint32) bool {
	if len(s.imports) == 0 {
		return false
	}
	for _, m := range s.imports {
		if m.Slot == slotId {
			return true
		}
	}
	return false
}
