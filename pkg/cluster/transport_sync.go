package cluster

import (
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

// slot同步协议
type slotTransportSync struct {
	s *Server
	wklog.Log
}

func newSlotTransportSync(s *Server) *slotTransportSync {
	return &slotTransportSync{
		s:   s,
		Log: wklog.NewWKLog("slotTransportSync"),
	}
}

// 发送通知
func (t *slotTransportSync) SendSyncNotify(toNodeID uint64, r *replica.SyncNotify) error {
	return t.s.nodeManager.sendSlotSyncNotify(toNodeID, r)
}

// 同步日志
func (t *slotTransportSync) SyncLog(fromNodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	resp, err := t.s.nodeManager.requestSlotSyncLog(t.s.cancelCtx, fromNodeID, r)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Server) handleSlotLogSyncNotify(fromNodeID uint64, msg *proto.Message) {
	req := &replica.SyncNotify{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("unmarshal syncNotify failed", zap.Error(err))
		return
	}
	slotID, _ := strconv.ParseUint(req.ShardNo, 10, 32)
	slot := s.slotManager.GetSlot(uint32(slotID))
	if slot == nil {
		s.Error("slot not found handleSlotLogSyncNotify failed", zap.Uint32("slotID", uint32(slotID)))
		return
	}
	s.Debug("handleSlotLogSyncNotify", zap.Uint32("slotID", uint32(slotID)), zap.Uint64("fromNodeID", fromNodeID), zap.Uint64("logIndex", req.LogIndex))
	// 去同步日志
	slot.replicaServer.TriggerHandleSyncNotify(req)
}

// channel同步协议
type channelTransportSync struct {
	s *Server
	wklog.Log
}

func newChannelTransportSync(s *Server) *channelTransportSync {
	return &channelTransportSync{
		s:   s,
		Log: wklog.NewWKLog("channelTransportSync"),
	}
}

func (t *channelTransportSync) SendSyncNotify(toNodeID uint64, r *replica.SyncNotify) error {
	return t.s.nodeManager.sendChannelSyncNotify(toNodeID, r)
}

func (t *channelTransportSync) SyncLog(fromNodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	resp, err := t.s.nodeManager.requestChannelSyncLog(t.s.cancelCtx, fromNodeID, r)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Server) handleChannelLogSyncNotify(fromNodeID uint64, msg *proto.Message) {
	req := &replica.SyncNotify{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("unmarshal syncNotify failed", zap.Error(err))
		return
	}
	channelID, channelType := GetChannelFromChannelKey(req.ShardNo)
	channel := s.channelManager.GetChannel(channelID, channelType)
	if channel == nil {
		s.Error("channel not found handleChannelLogSyncNotify failed", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return
	}

	// 去同步频道的日志
	s.channelManager.triggerHandleSyncNotify(channel, req)
}
