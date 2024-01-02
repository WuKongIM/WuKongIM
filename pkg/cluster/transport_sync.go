package cluster

import (
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

type transportSync struct {
	s *Server
	wklog.Log
}

func newTransportSync(s *Server) *transportSync {
	return &transportSync{
		s:   s,
		Log: wklog.NewWKLog("transportSync"),
	}
}

// 发送通知
func (t *transportSync) SendSyncNotify(toNodeID uint64, shardNo string, r *replica.SyncNotify) error {
	return t.s.nodeManager.sendSyncNotify(toNodeID, r)
}

// 同步日志
func (t *transportSync) SyncLog(fromNodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	resp, err := t.s.nodeManager.requestSyncLog(fromNodeID, r)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Server) handleLogSyncNotify(fromNodeID uint64, msg *proto.Message) {
	req := &replica.SyncNotify{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("unmarshal syncNotify failed", zap.Error(err))
		return
	}
	slotID, _ := strconv.ParseUint(req.ShardNo, 10, 32)
	slot := s.slotManager.GetSlot(uint32(slotID))
	if slot == nil {
		s.Error("slot not found handleLogSyncNotify failed", zap.Uint32("slotID", uint32(slotID)))
		return
	}
	slot.replicaServer.TriggerHandleSyncNotify(req)
}
