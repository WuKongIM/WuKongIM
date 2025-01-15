package cluster

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	rafttypes "github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type rpcServer struct {
	s *Server
	wklog.Log
}

func newRpcServer(s *Server) *rpcServer {
	return &rpcServer{
		s:   s,
		Log: wklog.NewWKLog("rpcServer"),
	}
}

func (r *rpcServer) setRoutes() {
	// 频道提案
	r.s.netServer.Route("/rpc/channel/propose", r.handleChannelPropose)

	// 获取频道或创建频道配置
	r.s.netServer.Route("/rpc/channel/getOrCreateConfig", r.handleGetOrCreateChannelConfig)

	// 获取频道配置
	r.s.netServer.Route("/rpc/channel/getConfig", r.handleChannelConfig)

	// 获取槽最新日志信息
	r.s.netServer.Route("/rpc/slot/lastLogInfo", r.handleSlotLastLogInfo)
	// 请求唤醒频道领导
	r.s.netServer.Route("/rpc/channel/wakeLeaderIfNeed", r.handleWakeLeaderIfNeed)

	// 请求频道更新配置
	r.s.netServer.Route("/rpc/channel/switchConfig", r.handleChannelSwitchConfig)

	// 获取频道最新日志信息
	r.s.netServer.Route("/rpc/channel/lastLogInfo", r.handleChannelLastLogInfo)

	// 节点加入
	r.s.netServer.Route("/rpc/cluster/join", r.handleClusterJoin)
}

func (r *rpcServer) handleChannelPropose(c *wkserver.Context) {
	req := &channelProposeReq{}
	if err := req.decode(c.Body()); err != nil {
		r.Error("decode channel propose req failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	resps, err := r.s.channelServer.ProposeBatchUntilAppliedTimeout(timeoutCtx, req.channelId, req.channelType, req.reqs)
	if err != nil {
		r.Error("channel propose failed", zap.Error(err), zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType), zap.Uint64("nodeId", r.s.opts.ConfigOptions.NodeId))
		c.WriteErr(err)
		return
	}
	data, err := resps.Marshal()
	if err != nil {
		r.Error("channel propose marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (r *rpcServer) handleGetOrCreateChannelConfig(c *wkserver.Context) {
	req := &channelReq{}
	if err := req.decode(c.Body()); err != nil {
		r.Error("decode channel config req failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	cfg, err := r.s.getOrCreateChannelClusterConfigFromLocal(req.channelId, req.channelType)
	if err != nil {
		r.Error("get channel config failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	data, err := cfg.Marshal()
	if err != nil {
		r.Error("channel config marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	c.Write(data)
}

func (r *rpcServer) handleChannelConfig(c *wkserver.Context) {
	req := &channelReq{}
	if err := req.decode(c.Body()); err != nil {
		r.Error("decode channel config req failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	cfg, err := r.s.db.GetChannelClusterConfig(req.channelId, req.channelType)
	if err != nil && err != wkdb.ErrNotFound {
		r.Error("get channel config failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	data, err := cfg.Marshal()
	if err != nil {
		r.Error("channel config marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	c.Write(data)
}

func (r *rpcServer) handleSlotLastLogInfo(c *wkserver.Context) {
	req := &SlotLogInfoReq{}
	if err := req.Unmarshal(c.Body()); err != nil {
		r.Error("unmarshal SlotLogInfoReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	slotInfos, err := r.slotInfos(req.SlotIds)
	if err != nil {
		r.Error("get slotInfos failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	resp := &SlotLogInfoResp{
		NodeId: r.s.opts.ConfigOptions.NodeId,
		Slots:  slotInfos,
	}
	data, err := resp.Marshal()
	if err != nil {
		r.Error("marshal SlotLogInfoResp failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (r *rpcServer) slotInfos(slotIds []uint32) ([]SlotInfo, error) {
	slotInfos := make([]SlotInfo, 0, len(slotIds))
	for _, slotId := range slotIds {
		st := r.s.slotServer.GetSlotRaft(slotId)
		if st == nil {
			continue
		}
		lastLogIndex, term := st.LastLogIndexAndTerm()
		slotInfos = append(slotInfos, SlotInfo{
			SlotId:   slotId,
			LogIndex: lastLogIndex,
			LogTerm:  term,
			Term:     st.LastTerm(),
		})
	}
	return slotInfos, nil
}

type channelProposeReq struct {
	channelId   string
	channelType uint8
	reqs        rafttypes.ProposeReqSet
}

func (ch *channelProposeReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteString(ch.channelId)
	enc.WriteUint8(ch.channelType)

	data, err := ch.reqs.Marshal()
	if err != nil {
		return nil, err
	}
	enc.WriteBytes(data)

	return enc.Bytes(), nil
}

func (ch *channelProposeReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if ch.channelId, err = dec.String(); err != nil {
		return err
	}
	if ch.channelType, err = dec.Uint8(); err != nil {
		return err
	}
	data, err = dec.BinaryAll()
	if err != nil {
		return err
	}
	return ch.reqs.Unmarshal(data)
}

func (r *rpcServer) handleWakeLeaderIfNeed(c *wkserver.Context) {
	cfg := wkdb.ChannelClusterConfig{}
	if err := cfg.Unmarshal(c.Body()); err != nil {
		r.Error("decode wake leader req failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if cfg.LeaderId != r.s.opts.ConfigOptions.NodeId {
		r.Info("not leader, ignore wake leader", zap.String("channelId", cfg.ChannelId), zap.Uint8("channelType", cfg.ChannelType), zap.Uint64("leaderId", cfg.LeaderId))
		c.Write([]byte("not leader"))
		return
	}
	if err := r.s.channelServer.WakeLeaderIfNeed(cfg); err != nil {
		r.Error("wake leader failed", zap.Error(err), zap.String("channelId", cfg.ChannelId), zap.Uint8("channelType", cfg.ChannelType))
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}

func (r *rpcServer) handleChannelSwitchConfig(c *wkserver.Context) {
	cfg := wkdb.ChannelClusterConfig{}
	if err := cfg.Unmarshal(c.Body()); err != nil {
		r.Error("decode update channel config req failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	err := r.s.channelServer.SwitchConfig(cfg.ChannelId, cfg.ChannelType, cfg)
	if err != nil {
		r.Error("switch config failed", zap.Error(err), zap.String("channelId", cfg.ChannelId), zap.Uint8("channelType", cfg.ChannelType))
		c.WriteErr(err)
		return
	}

	c.WriteOk()
}

func (r *rpcServer) handleChannelLastLogInfo(c *wkserver.Context) {
	req := &channelReq{}
	if err := req.decode(c.Body()); err != nil {
		r.Error("decode channel last log info req failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	resp, err := r.s.getChannelLastLogInfo(req.channelId, req.channelType)
	if err != nil {
		r.Error("get channel last log info failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	data, err := resp.Marshal()
	if err != nil {
		r.Error("marshal channel last log info failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	c.Write(data)
}

func (s *Server) getChannelLastLogInfo(channelId string, channelType uint8) (*ChannelLastLogInfoResponse, error) {
	lastLogTerm, lastLogIndex, err := s.channelServer.LastLogIndexAndTerm(channelId, channelType)
	if err != nil {
		return nil, err
	}

	cfg, err := s.db.GetChannelClusterConfig(channelId, channelType)
	if err != nil && err != wkdb.ErrNotFound {
		return nil, err
	}

	resp := &ChannelLastLogInfoResponse{
		LogTerm:  lastLogTerm,
		LogIndex: lastLogIndex,
		Term:     cfg.Term,
	}
	return resp, nil
}

func (r *rpcServer) handleClusterJoin(c *wkserver.Context) {
	req := &ClusterJoinReq{}
	if err := req.Unmarshal(c.Body()); err != nil {
		r.Error("unmarshal ClusterJoinReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if !r.s.cfgServer.IsLeader() {
		resp, err := r.s.rpcClient.RequestClusterJoin(r.s.cfgServer.LeaderId(), req)
		if err != nil {
			r.Error("requestClusterJoin failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
		data, err := resp.Marshal()
		if err != nil {
			r.Error("marshal ClusterJoinResp failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
		c.Write(data)
		return
	}

	allowVote := false
	if req.Role == types.NodeRole_NodeRoleReplica {
		allowVote = true
	}

	resp := ClusterJoinResp{}

	nodeInfos := make([]*NodeInfo, 0, len(r.s.cfgServer.Nodes()))
	for _, node := range r.s.cfgServer.Nodes() {
		nodeInfos = append(nodeInfos, &NodeInfo{
			NodeId:     node.Id,
			ServerAddr: node.ClusterAddr,
		})
	}
	resp.Nodes = nodeInfos

	err := r.s.cfgServer.ProposeJoin(&types.Node{
		Id:          req.NodeId,
		ClusterAddr: req.ServerAddr,
		Join:        true,
		Online:      true,
		Role:        req.Role,
		AllowVote:   allowVote,
		CreatedAt:   time.Now().Unix(),
		Status:      types.NodeStatus_NodeStatusWillJoin,
	})
	if err != nil {
		r.Error("proposeJoin failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	result, err := resp.Marshal()
	if err != nil {
		r.Error("marshal ClusterJoinResp failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(result)
}
