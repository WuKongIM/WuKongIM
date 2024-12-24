package reactor

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
)

type Channel struct {
	no            string
	key           string
	channelId     string
	channelType   uint8
	heartbeatTick int
	idleTick      int
	actions       []reactor.ChannelAction
	role          reactor.Role
	stepFnc       func(a reactor.ChannelAction)
	tickFnc       func()
	// 节点收件箱
	// 来接收其他节点投递到当前节点的消息，此收件箱的数据会进入process
	inbound *ready
	// 节点发送箱
	// (节点直接投递数据用，如果当前节点是领导，转发给追随者，如果是追随者，转发给领导)
	outbound *outboundReady
	joined   bool                  // 是否已成功加入集群
	cfg      reactor.ChannelConfig // 频道配置
	wklog.Log
	advanceFnc func()
}

func NewChannel(channelId string, channelType uint8, advance func()) *Channel {
	key := wkutil.ChannelToKey(channelId, channelType)
	prefix := fmt.Sprintf("channel[%s]", key)
	ch := &Channel{
		key:         key,
		channelId:   channelId,
		channelType: channelType,
		no:          wkutil.GenUUID(),
		inbound:     newReady(prefix),
		Log:         wklog.NewWKLog(prefix),
		advanceFnc:  advance,
	}
	ch.outbound = newOutboundReady(prefix, ch)
	ch.sendElection() // 初始化时就开始选举，提早触发，这样不用等tick
	return ch
}

// ==================================== ready ====================================

func (c *Channel) hasReady() bool {
	if len(c.actions) > 0 {
		return true
	}

	if c.needElection() {
		return true
	}

	if c.inbound.has() {
		return true
	}

	if c.outbound.has() {
		return true
	}

	return false
}

func (c *Channel) ready() []reactor.ChannelAction {
	if c.allowReady() {
		// ---------- inbound ----------
		if c.inbound.has() {
			msgs := c.inbound.sliceAndTruncate(options.MaxBatchBytes)
			c.actions = append(c.actions, reactor.ChannelAction{
				No:            c.no,
				FakeChannelId: c.channelId,
				ChannelType:   c.channelType,
				From:          options.NodeId,
				To:            reactor.LocalNode,
				Type:          reactor.ChannelActionInbound,
				Messages:      msgs,
				Role:          c.role,
			})
		}
		// ---------- outbound ----------
		if c.outbound.has() {
			actions := c.outbound.ready()
			if len(actions) > 0 {
				c.actions = append(c.actions, actions...)
			}
		}
	}
	actions := c.actions
	c.actions = c.actions[:0]
	return actions
}

func (c *Channel) allowReady() bool {

	if c.electioned() {
		if c.role == reactor.RoleLeader {
			return true
		}
		if c.role == reactor.RoleFollower && c.joined {
			return true
		}
	}
	return false
}

// ==================================== step ====================================

func (c *Channel) step(a reactor.ChannelAction) {
	if a.Type != reactor.ChannelActionHeartbeatReq && a.Type != reactor.ChannelActionHeartbeatResp {
		c.idleTick = 0
	}
	switch a.Type {
	case reactor.ChannelActionConfigUpdate:
		c.handleConfigUpdate(a.Cfg)
	case reactor.ChannelActionInboundAdd: // 收件箱
		for _, msg := range a.Messages {
			c.inbound.append(msg)
		}
	case reactor.ChannelActionOutboundAdd: // 发送箱
		for _, msg := range a.Messages {
			c.outbound.append(msg)
		}
	case reactor.ChannelActionClose:
		c.sendChannelClose()
	default:
		if c.stepFnc != nil {
			c.stepFnc(a)
		}
	}
}

func (c *Channel) stepLeader(a reactor.ChannelAction) {

	switch a.Type {
	case reactor.ChannelActionHeartbeatResp:
		c.outbound.updateReplicaHeartbeat(a.From)
	case reactor.ChannelActionJoin:
		c.outbound.addNewReplica(a.From)
		c.sendJoinResp(a.From)
		c.advance()
	}
}

func (c *Channel) stepFollower(a reactor.ChannelAction) {
	switch a.Type {
	case reactor.ChannelActionHeartbeatReq:
		c.outbound.updateReplicaHeartbeat(a.From)
		c.heartbeatTick = 0
		c.sendHeartbeatResp(a.From)
	case reactor.ChannelActionJoinResp:
		if a.From == c.cfg.LeaderId {
			c.joined = true
			c.advance()
		}
	}
}

// ==================================== tick ====================================

func (c *Channel) tick() {
	c.inbound.tick()

	if c.needElection() {
		c.sendElection()
	}

	if c.tickFnc != nil {
		c.tickFnc()
	}
}

func (c *Channel) tickLeader() {

	c.outbound.tick()

	c.heartbeatTick++
	c.idleTick++

	if c.idleTick >= options.LeaderIdleTimeoutTick {
		c.idleTick = 0
		c.sendChannelClose()
		return
	}

	if c.heartbeatTick >= options.NodeHeartbeatTick {
		c.heartbeatTick = 0
		c.sendHeartbeatReq()
	}
}

func (c *Channel) tickFollower() {
	c.heartbeatTick++

	if c.heartbeatTick >= options.NodeHeartbeatTimeoutTick {
		c.heartbeatTick = 0
		// 如果领导心跳超时，但是还有消息需要发送，则发送join请求来唤醒领导
		if c.outbound.queue.len() > 0 {
			c.sendJoin()
		} else {
			c.sendChannelClose()
		}
	}
	if c.needJoin() {
		c.sendJoin()
	}
}

// ==================================== send ====================================

func (c *Channel) sendElection() {
	c.actions = append(c.actions, reactor.ChannelAction{
		Type:          reactor.ChannelActionElection,
		FakeChannelId: c.channelId,
		ChannelType:   c.channelType,
	})
}

func (c *Channel) sendJoin() {
	c.actions = append(c.actions, reactor.ChannelAction{
		No:            c.no,
		Type:          reactor.ChannelActionJoin,
		FakeChannelId: c.channelId,
		ChannelType:   c.channelType,
		From:          options.NodeId,
		To:            c.cfg.LeaderId,
	})
}

func (c *Channel) sendJoinResp(to uint64) {
	c.actions = append(c.actions, reactor.ChannelAction{
		No:            c.no,
		Type:          reactor.ChannelActionJoinResp,
		FakeChannelId: c.channelId,
		ChannelType:   c.channelType,
		From:          options.NodeId,
		To:            to,
	})
}

func (c *Channel) sendChannelClose() {
	c.actions = append(c.actions, reactor.ChannelAction{
		No:            c.no,
		Type:          reactor.ChannelActionClose,
		FakeChannelId: c.channelId,
		ChannelType:   c.channelType,
		From:          options.NodeId,
		To:            c.cfg.LeaderId,
	})
}

func (c *Channel) sendHeartbeatReq() {
	for _, replica := range c.outbound.replicas {
		c.actions = append(c.actions, reactor.ChannelAction{
			No:            c.no,
			From:          options.NodeId,
			To:            replica.nodeId,
			Type:          reactor.ChannelActionHeartbeatReq,
			FakeChannelId: c.channelId,
			ChannelType:   c.channelType,
		})
	}

}

func (c *Channel) sendHeartbeatResp(to uint64) {
	c.actions = append(c.actions, reactor.ChannelAction{
		No:            c.no,
		From:          options.NodeId,
		To:            to,
		FakeChannelId: c.channelId,
		ChannelType:   c.channelType,
		Type:          reactor.ChannelActionHeartbeatResp,
	})
}

// ==================================== become ====================================

func (c *Channel) becomeLeader() {
	c.role = reactor.RoleLeader
	c.stepFnc = c.stepLeader
	c.tickFnc = c.tickLeader

	c.Info("become leader")
	c.advance()

}

func (c *Channel) becomeFollower() {
	c.role = reactor.RoleFollower
	c.stepFnc = c.stepFollower
	c.tickFnc = c.tickFollower

	// 如果是追随者，需要添加领导到副本列表
	c.outbound.addNewReplica(c.cfg.LeaderId)

	// c.Info("become follower", zap.Uint64("leaderId", c.cfg.LeaderId))

	c.sendJoin()

	c.advance()
}

func (c *Channel) becomeUnknown() {
	c.role = reactor.RoleUnknown
	c.stepFnc = nil
	c.tickFnc = nil

	c.Info("become unknown")

}

// ==================================== other ====================================

func (c *Channel) needJoin() bool {

	return !c.joined
}

// func (c *Channel) isLeader() bool {
// 	return c.role == reactor.RoleLeader
// }

func (c *Channel) needElection() bool {
	return c.role == reactor.RoleUnknown
}

func (c *Channel) electioned() bool {
	return c.role != reactor.RoleUnknown
}

func (c *Channel) reset() {
	c.joined = false
	c.role = reactor.RoleUnknown
	c.stepFnc = nil
	c.tickFnc = nil
	c.heartbeatTick = 0
	c.idleTick = 0
	c.actions = c.actions[:0]
	c.cfg = reactor.ChannelConfig{}
	c.inbound.reset()
	c.outbound.reset()
}

func (c *Channel) handleConfigUpdate(cfg reactor.ChannelConfig) {

	if c.cfg.LeaderId == cfg.LeaderId {
		return
	}

	oldRole := c.role
	// 角色非初始状态需要reset
	if oldRole != reactor.RoleUnknown {
		c.reset()
	}
	c.cfg = cfg
	if c.cfg.LeaderId == 0 {
		c.becomeUnknown()
	} else if c.cfg.LeaderId == options.NodeId {
		c.becomeLeader()
	} else {
		c.becomeFollower()
	}

}

func (c *Channel) advance() {
	if c.advanceFnc != nil {
		c.advanceFnc()
	}
}
