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
		Log: wklog.NewWKLog("slotTransportSync"),
	}
}

// 发送通知
func (t *slotTransportSync) SendSyncNotify(toNodeID uint64, r *replica.SyncNotify) error {
	return t.s.nodeManager.sendSlotSyncNotify(toNodeID, r)
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

func (t *channelMetaTransportSync) SendSyncNotify(toNodeID uint64, r *replica.SyncNotify) error {
	return t.s.nodeManager.sendChannelMetaLogSyncNotify(toNodeID, r)
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

func (t *channelMessageTransportSync) SendSyncNotify(toNodeID uint64, r *replica.SyncNotify) error {
	t.Debug("发送同步消息日志通知给副本", zap.Uint64("replicaNodeID", toNodeID), zap.String("shardNo", r.ShardNo))
	return t.s.nodeManager.sendChannelMessageLogSyncNotify(toNodeID, r)
}

func (t *channelMessageTransportSync) SyncLog(fromNodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	t.Debug("开始向领导同步消息日志", zap.Uint64("startLogIndex", r.StartLogIndex), zap.Uint64("fromNodeID", fromNodeID), zap.String("shardNo", r.ShardNo))
	resp, err := t.s.nodeManager.requestChannelMessageSyncLog(t.s.cancelCtx, fromNodeID, r)
	if err != nil {
		return nil, err
	}
	t.Debug("向领导同步消息日志完成", zap.Uint64("startLogIndex", r.StartLogIndex), zap.Uint64("fromNodeID", fromNodeID), zap.String("shardNo", r.ShardNo), zap.Int("logs", len(resp.Logs)))
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

	s.Debug("handleChannelMetaLogSyncNotify", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))

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
	channel.hasMetaEvent.Store(true)
	s.channelManager.triggerHandleSyncNotify(channel, req)
}

func (s *Server) handleChannelMessageLogSyncNotify(fromNodeID uint64, msg *proto.Message) {
	req := &replica.SyncNotify{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("unmarshal syncNotify failed", zap.Error(err))
		return
	}

	s.Debug("handleChannelMessageLogSyncNotify.........", zap.String("channelID", req.ShardNo))

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
	channel.hasMessageEvent.Store(true)
	s.channelManager.triggerHandleSyncNotify(channel, req)
}
