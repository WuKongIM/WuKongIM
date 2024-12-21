package reactor

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type User struct {
	no            string // 唯一编号
	uid           string
	conns         *conns
	role          reactor.Role
	stepFnc       func(a reactor.UserAction)
	tickFnc       func()
	heartbeatTick int
	idleTick      int // 领导空闲时间
	actions       []reactor.UserAction
	cfg           reactor.UserConfig
	joined        bool // 是否已成功加入集群
	wklog.Log
	// 节点收件箱
	// 来接收其他节点投递到当前节点的消息，此收件箱的数据会进入process
	inbound *ready
	// 节点发送箱
	// (节点直接投递数据用，如果当前节点是领导，转发给追随者，如果是追随者，转发给领导)
	outbound *outboundReady
	// 客户端发送箱
	// 将数据投递给自己直连的客户端
	clientOutbound *ready
	nodeVersion    uint64
	advance        func()
}

func NewUser(no, uid string, advance func()) *User {

	prefix := fmt.Sprintf("user[%s]", uid)
	u := &User{
		no:             no,
		uid:            uid,
		conns:          &conns{},
		Log:            wklog.NewWKLog(prefix),
		inbound:        newReady(prefix),
		clientOutbound: newReady(prefix),
		nodeVersion:    options.NodeVersion(),
		advance:        advance,
	}
	u.outbound = newOutboundReady(prefix, u)
	return u
}

// ==================================== ready ====================================

func (u *User) hasReady() bool {

	if len(u.actions) > 0 {
		return true
	}

	if u.needElection() {
		return true
	}

	if u.inbound.has() {
		return true
	}

	if u.outbound.has() {
		return true
	}

	if u.clientOutbound.has() {
		return true
	}

	return false
}

func (u *User) ready() []reactor.UserAction {
	if u.allowReady() {
		// ---------- inbound ----------
		if u.inbound.has() {
			msgs := u.inbound.sliceAndTruncate(options.MaxBatchBytes)
			u.actions = append(u.actions, reactor.UserAction{
				No:       u.no,
				Uid:      u.uid,
				From:     options.NodeId,
				To:       reactor.LocalNode,
				Type:     reactor.UserActionInbound,
				Messages: msgs,
				Role:     u.role,
			})
		}

		// ---------- outbound ----------
		if u.outbound.has() {
			actions := u.outbound.ready()
			if len(actions) > 0 {
				u.actions = append(u.actions, actions...)
			}
		}

		// ---------- clientOutbound ----------
		if u.clientOutbound.has() {
			msgs := u.clientOutbound.sliceAndTruncate(options.MaxBatchBytes)
			u.actions = append(u.actions, reactor.UserAction{
				No:       u.no,
				Uid:      u.uid,
				From:     options.NodeId,
				To:       reactor.LocalNode,
				Type:     reactor.UserActionWrite,
				Messages: msgs,
			})
		}
	}
	actions := u.actions
	u.actions = u.actions[:0]
	return actions
}

func (u *User) allowReady() bool {

	if u.electioned() {
		if u.role == reactor.RoleLeader {
			return true
		}
		if u.role == reactor.RoleFollower && u.joined {
			return true
		}
	}
	return false
}

// ==================================== step ====================================

func (u *User) step(action reactor.UserAction) {

	// fmt.Println("step---->", action.Type.String(), action.Uid)
	u.idleTick = 0
	switch action.Type {
	case reactor.UserActionConfigUpdate:
		u.handleConfigUpdate(action.Cfg)
	case reactor.UserActionInboundAdd: // 收件箱
		for _, msg := range action.Messages {
			if msg.Frame != nil && msg.Conn != nil {
				if msg.Frame.GetFrameType() == wkproto.CONNECT {
					conn := u.conns.connByConnId(msg.Conn.FromNode, msg.Conn.ConnId)
					if conn == nil {
						u.outbound.keepaliveAll() // 这里包活下，防止连接进来了，但是用户没了
						u.conns.add(msg.Conn)
					}
				}
			}
			u.inbound.append(msg)
		}
	case reactor.UserActionOutboundAdd: // 发送箱
		for _, msg := range action.Messages {
			u.outbound.append(msg)
		}
	case reactor.UserActionConnClose: // 关闭连接
		for _, conn := range action.Conns {
			u.conns.remove(conn)
		}
		u.sendConnClose(action.Conns)
	case reactor.UserActionWrite: // 写数据
		for _, msg := range action.Messages {
			u.clientOutbound.append(msg)
		}

	default:
		if u.stepFnc != nil {
			u.stepFnc(action)
		}
	}
}

func (u *User) stepFollower(action reactor.UserAction) {
	switch action.Type {
	// 收到来自领导的心跳
	case reactor.UserActionNodeHeartbeatReq:
		u.outbound.keepalive(action.From)
		u.heartbeatTick = 0
		localConns := u.conns.connsByNodeId(options.NodeId)
		var closeConns []*reactor.Conn
		for _, localConn := range localConns {
			if !localConn.Auth {
				continue
			}
			exist := false
			for _, conn := range action.Conns {
				if localConn.ConnId == conn.ConnId {
					exist = true
					break
				}
			}
			if !exist {
				if closeConns == nil {
					closeConns = make([]*reactor.Conn, 0, len(localConns))
				}
				u.conns.remove(localConn)
				closeConns = append(closeConns, localConn)
			}
		}
		// 只有当前有连接的时候才回应领导的心跳
		// 这样领导没收到follower的心跳回应
		// 领导就会将follower踢出，follower就可以安全退出
		if len(localConns) > 0 {
			u.sendHeartbeatResp(action.From)
		}

		if len(closeConns) > 0 {
			u.sendConnClose(closeConns)
		}
	// 领导返回加入结果
	case reactor.UserActionJoinResp:
		u.joined = action.Success
		if u.advance != nil {
			u.advance()
		}
	}
}

func (u *User) stepLeader(action reactor.UserAction) {
	switch action.Type {
	//领导收到其他领导的心跳
	// （这种情况出现了脑裂的情况，选比较nodeVersion，大的当选，如果nodeVersion一样，则都进入选举状态，都一样的情况这种说明系统出问题了）
	case reactor.UserActionNodeHeartbeatReq:
		u.Warn("出现多个领导节点", zap.Uint64("currentNodeVersion", options.NodeVersion()), zap.Uint64("otherNodeVersion", action.NodeVersion))
		if action.NodeVersion > options.NodeVersion() {
			u.handleConfigUpdate(reactor.UserConfig{
				LeaderId: action.From,
			})
		} else if action.NodeVersion == options.NodeVersion() {
			u.handleConfigUpdate(reactor.UserConfig{
				LeaderId: 0, // 成为未选举状态
			})
		}
	// 副本收到心跳回应
	case reactor.UserActionNodeHeartbeatResp:
		nodeConns := u.conns.connsByNodeId(action.From)

		for _, nodeConn := range nodeConns {
			exist := false
			for _, conn := range action.Conns {
				if nodeConn.ConnId == conn.ConnId {
					exist = true
					break
				}
			}
			if !exist {
				// fmt.Println("leader: remove conn...", nodeConn.Uid, nodeConn.FromNode, nodeConn.ConnId)
				u.conns.remove(nodeConn)
			}
		}
		u.outbound.keepalive(action.From)
	// 副本请求加入
	case reactor.UserActionJoin:
		u.outbound.addNewReplica(action.From)
		u.sendJoinResp(action.From)
	}
}

// ==================================== tick ====================================

func (u *User) tick() {

	u.inbound.tick()
	u.outbound.tick()
	u.clientOutbound.tick()

	if u.needElection() {
		u.sendElection()
	}

	if u.tickFnc != nil {
		u.tickFnc()
	}
}

func (u *User) tickLeader() {
	u.heartbeatTick++
	u.idleTick++

	if u.conns.len() == 0 && len(u.outbound.replicas) == 0 && u.idleTick >= options.LeaderIdleTimeoutTick {
		u.idleTick = 0
		u.sendUserClose()
		return
	}

	if u.conns.len() > 0 && u.heartbeatTick >= options.NodeHeartbeatTick {
		u.heartbeatTick = 0
		u.sendHeartbeatReq()
	}

	if u.nodeVersion < options.NodeVersion() {
		u.Info("node version update, close user", zap.Uint64("old", u.nodeVersion), zap.Uint64("new", options.NodeVersion()))
		u.sendUserClose()
	}
}

func (u *User) tickFollower() {
	u.heartbeatTick++

	if u.heartbeatTick >= options.NodeHeartbeatTimeoutTick {
		u.heartbeatTick = 0
		u.sendUserClose()
	}
	if u.needJoin() {
		u.sendJoin()
	}
}

// ==================================== send ====================================

func (u *User) sendHeartbeatReq() {
	for _, replica := range u.outbound.replicas {
		conns := u.conns.connsByNodeId(replica.nodeId)
		if len(conns) == 0 { // 如果没有连接，不发送心跳
			continue
		}
		u.actions = append(u.actions, reactor.UserAction{
			No:          u.no,
			From:        options.NodeId,
			To:          replica.nodeId,
			Uid:         u.uid,
			Type:        reactor.UserActionNodeHeartbeatReq,
			Conns:       conns,
			NodeVersion: options.NodeVersion(),
		})
	}

}

func (u *User) sendHeartbeatResp(to uint64) {
	u.actions = append(u.actions, reactor.UserAction{
		No:          u.no,
		From:        options.NodeId,
		To:          to,
		Uid:         u.uid,
		Type:        reactor.UserActionNodeHeartbeatResp,
		Conns:       u.conns.allConns(),
		NodeVersion: options.NodeVersion(),
	})
}

func (u *User) sendConnClose(conns []*reactor.Conn) {
	u.actions = append(u.actions, reactor.UserAction{
		No:    u.no,
		Uid:   u.uid,
		From:  options.NodeId,
		To:    reactor.LocalNode,
		Type:  reactor.UserActionConnClose,
		Conns: conns,
	})
}

func (u *User) sendUserClose() {
	if u.role == reactor.RoleLeader {
		trace.GlobalTrace.Metrics.App().OnlineUserCountAdd(-1)
	}
	u.actions = append(u.actions, reactor.UserAction{
		No:    u.no,
		Uid:   u.uid,
		From:  options.NodeId,
		To:    reactor.LocalNode,
		Type:  reactor.UserActionUserClose,
		Conns: u.conns.connsByNodeId(options.NodeId),
	})
}

func (u *User) sendElection() {
	u.actions = append(u.actions, reactor.UserAction{
		Type: reactor.UserActionElection,
		Uid:  u.uid,
	})
}

func (u *User) sendJoin() {
	u.actions = append(u.actions, reactor.UserAction{
		No:   u.no,
		Type: reactor.UserActionJoin,
		Uid:  u.uid,
		From: options.NodeId,
		To:   u.cfg.LeaderId,
	})
}

func (u *User) sendJoinResp(to uint64) {
	u.actions = append(u.actions, reactor.UserAction{
		No:   u.no,
		Type: reactor.UserActionJoinResp,
		Uid:  u.uid,
		From: options.NodeId,
		To:   to,
	})
}

// ==================================== become ====================================

func (u *User) becomeLeader() {
	u.role = reactor.RoleLeader
	u.stepFnc = u.stepLeader
	u.tickFnc = u.tickLeader

	u.Info("become leader")
	if u.advance != nil {
		u.advance()
	}
	trace.GlobalTrace.Metrics.App().OnlineUserCountAdd(1)

}

func (u *User) becomeFollower() {
	u.role = reactor.RoleFollower
	u.stepFnc = u.stepFollower
	u.tickFnc = u.tickFollower

	// 如果是追随者，需要添加领导到副本列表
	u.outbound.addNewReplica(u.cfg.LeaderId)

	u.Info("become follower")

	if u.advance != nil {
		u.advance()
	}

}

func (u *User) becomeUnknown() {
	u.role = reactor.RoleUnknown
	u.stepFnc = nil
	u.tickFnc = nil

	u.Info("become unknown")
}

// ==================================== other ====================================

func (u *User) needJoin() bool {

	return !u.joined
}

func (u *User) isLeader() bool {
	return u.role == reactor.RoleLeader
}

func (u *User) needElection() bool {
	return u.role == reactor.RoleUnknown
}

func (u *User) electioned() bool {
	return u.role != reactor.RoleUnknown
}

func (u *User) reset() {
	u.joined = false
	u.role = reactor.RoleUnknown
	u.stepFnc = nil
	u.tickFnc = nil
	u.heartbeatTick = 0
	u.actions = u.actions[:0]
	u.cfg = reactor.UserConfig{}
	u.inbound.reset()
	u.outbound.reset()
	u.clientOutbound.reset()
	u.conns.reset()
}

func (u *User) handleConfigUpdate(cfg reactor.UserConfig) {

	if u.cfg.LeaderId == cfg.LeaderId {
		return
	}

	oldRole := u.role
	// 角色非初始状态需要reset
	if oldRole != reactor.RoleUnknown {
		u.reset()
	}
	u.cfg = cfg
	if u.cfg.LeaderId == 0 {
		u.becomeUnknown()
	} else if u.cfg.LeaderId == options.NodeId {
		u.becomeLeader()
	} else {
		u.becomeFollower()
	}

}
