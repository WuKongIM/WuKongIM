package cluster

import (
	"fmt"
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
		Log: wklog.NewWKLog(fmt.Sprintf("slotTransportSync[%d]", s.opts.NodeID)),
	}
}

// 发送通知
func (t *slotTransportSync) SendSyncNotify(toNodeIDs []uint64, r *replica.SyncNotify) {
	for _, toNodeID := range toNodeIDs {
		if !t.s.clusterEventManager.NodeIsOnline(toNodeID) {
			t.Debug("node is offline", zap.Uint64("toNodeID", toNodeID))
			continue
		}
		t.s.nodeManager.sendSlotSyncNotify(toNodeID, r)
	}
}

// 同步日志
func (t *slotTransportSync) SyncLog(fromNodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	t.Debug("开始同步槽日志", zap.Uint64("fromNodeID", fromNodeID), zap.String("shardNo", r.ShardNo))
	resp, err := t.s.nodeManager.requestSlotSyncLog(t.s.cancelCtx, fromNodeID, r)
	if err != nil {
		return nil, err
	}
	t.Debug("槽日志同步完成", zap.Uint64("fromNodeID", fromNodeID), zap.String("shardNo", r.ShardNo), zap.Int("logs", len(resp.Logs)))
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
	s.Debug("handleSlotLogSyncNotify", zap.Uint32("slotID", uint32(slotID)), zap.Uint64("fromNodeID", fromNodeID))
	// 去同步日志
	slot.replicaServer.TriggerHandleSyncNotify()
}

// channel元数据同步协议
type channelMetaTransportSync struct {
	s *Server
	wklog.Log
}

func newChannelMetaTransportSync(s *Server) *channelMetaTransportSync {
	return &channelMetaTransportSync{
		s:   s,
		Log: wklog.NewWKLog("channelMetaTransportSync"),
	}
}

func (t *channelMetaTransportSync) SendSyncNotify(toNodeIDs []uint64, r *replica.SyncNotify) {
	for _, toNodeID := range toNodeIDs {
		if !t.s.clusterEventManager.NodeIsOnline(toNodeID) {
			t.Debug("node is offline", zap.Uint64("toNodeID", toNodeID))
			continue
		}
		err := t.s.nodeManager.sendChannelMetaLogSyncNotify(toNodeID, r)
		if err != nil {
			t.Error("sendChannelMetaLogSyncNotify failed", zap.Error(err), zap.Uint64("toNodeID", toNodeID))
		}
	}
}

func (t *channelMetaTransportSync) SyncLog(fromNodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	resp, err := t.s.nodeManager.requestChannelMetaSyncLog(t.s.cancelCtx, fromNodeID, r)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// channel消息数据同步协议
type channelMessageTransportSync struct {
	s *Server
	wklog.Log
}

func newChannelMessageTransportSync(s *Server) *channelMessageTransportSync {
	return &channelMessageTransportSync{
		s:   s,
		Log: wklog.NewWKLog(fmt.Sprintf("channelMessageTransportSync[%d]", s.opts.NodeID)),
	}
}

func (t *channelMessageTransportSync) SendSyncNotify(toNodeIDs []uint64, r *replica.SyncNotify) {
	for _, toNodeID := range toNodeIDs {
		if !t.s.clusterEventManager.NodeIsOnline(toNodeID) {
			t.Debug("node is offline", zap.Uint64("toNodeID", toNodeID))
			continue
		}
		err := t.s.nodeManager.sendChannelMessageLogSyncNotify(toNodeID, r)
		if err != nil {
			t.Error("sendChannelMessageLogSyncNotify failed", zap.Error(err), zap.Uint64("toNodeID", toNodeID))
		}
	}
}

func (t *channelMessageTransportSync) SyncLog(leaderID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	resp, err := t.s.nodeManager.requestChannelMessageSyncLog(t.s.cancelCtx, leaderID, r)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Server) handleChannelMetaLogSyncNotify(fromNodeID uint64, msg *proto.Message) {
	req := &replica.SyncNotify{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("unmarshal syncNotify failed", zap.Error(err))
		return
	}
	channelID, channelType := GetChannelFromChannelKey(req.ShardNo)

	channel, err := s.channelManager.GetChannel(channelID, channelType)
	if err != nil {
		s.Error("get channnel failed", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return
	}
	if channel == nil {
		s.Error("channel not found handleChannelLogSyncNotify failed", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return
	}

	// 去同步频道的日志
	s.channelManager.recvMetaLogSyncNotify(channel, req)
}

func (s *Server) handleChannelMessageLogSyncNotify(fromNodeID uint64, msg *proto.Message) {
	req := &replica.SyncNotify{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("unmarshal syncNotify failed", zap.Error(err))
		return
	}

	s.Debug("收到领导通知同步频道消息日志的请求", zap.String("channelID", req.ShardNo))

	channelID, channelType := GetChannelFromChannelKey(req.ShardNo)

	s.Debug("handleChannelLogSyncNotify", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))

	channel, err := s.channelManager.GetChannel(channelID, channelType)
	if err != nil {
		s.Error("get channnel failed", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return
	}
	if channel == nil {
		s.Error("channel not found handleChannelLogSyncNotify failed", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return
	}

	// 去同步频道的日志
	s.channelManager.recvMessageLogSyncNotify(channel, req)
}
