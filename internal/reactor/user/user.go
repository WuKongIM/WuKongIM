package reactor

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type User struct {
	no            string // 唯一编号
	uid           string
	conns         *conns
	authReady     *ready //认证
	role          reactor.Role
	stepFnc       func(a reactor.UserAction)
	tickFnc       func()
	heartbeatTick int
	idleTick      int // 领导空闲时间
	actions       []reactor.UserAction
	cfg           reactor.UserConfig
	joined        bool // 是否已成功加入集群
	wklog.Log
	inbound  *ready         // 收件箱
	outbound *outboundReady // 发送箱
}

func NewUser(no, uid string) *User {

	prefix := fmt.Sprintf("user[%s]", uid)
	return &User{
		no:        no,
		uid:       uid,
		authReady: newReady(prefix),
		conns:     &conns{},
		Log:       wklog.NewWKLog(prefix),
		inbound:   newReady(prefix),
		outbound:  newOutboundReady(prefix),
	}
}

// ==================================== ready ====================================

func (u *User) hasReady() bool {
	if u.needElection() {
		return true
	}

	if u.authReady.has() {
		return true
	}

	if u.inbound.has() {
		return true
	}

	if u.outbound.has() {
		return true
	}

	if len(u.actions) > 0 {
		return true
	}

	return false
}

func (u *User) ready() []reactor.UserAction {
	if u.electioned() {
		// ---------- auth ----------
		if u.authReady.has() {
			msgs := u.authReady.sliceAndTruncate()
			u.actions = append(u.actions, reactor.UserAction{
				No:       u.no,
				From:     options.NodeId,
				To:       reactor.LocalNode,
				Type:     reactor.UserActionAuth,
				Messages: msgs,
			})
		}

		// ---------- inbound ----------
		if u.inbound.has() {
			msgs := u.inbound.sliceAndTruncate()
			u.actions = append(u.actions, reactor.UserAction{
				No:       u.no,
				From:     options.NodeId,
				To:       reactor.LocalNode,
				Type:     reactor.UserActionInbound,
				Messages: msgs,
			})
		}

		// ---------- outbound ----------
		if u.outbound.has() {
			actions := u.outbound.ready()
			if len(actions) > 0 {
				u.actions = append(u.actions, actions...)
			}
		}
	}
	actions := u.actions
	u.actions = u.actions[:0]
	return actions
}

// ==================================== step ====================================

func (u *User) step(action reactor.UserAction) {
	u.idleTick = 0
	switch action.Type {
	case reactor.UserActionConfigUpdate:
		fmt.Println("UserActionConfigUpdate....")
		u.handleConfigUpdate(action.Cfg)
	case reactor.UserActionAuthAdd:
		for _, msg := range action.Messages {
			if msg.Conn() == nil {
				u.Warn("add auth failed, msg conn not exist", zap.String("uid", action.Uid))
				return
			}
			conn := u.conns.connByConnId(msg.Conn().FromNode(), msg.Conn().ConnId())
			if conn != nil {
				u.Warn("add auth failed, conn exist", zap.String("uid", action.Uid))
				return
			}
			u.conns.add(msg.Conn())
			u.authReady.append(msg)
		}
	case reactor.UserActionAuthResp: // 认证处理返回
		for _, conn := range action.Conns {
			conn.SetAuth(action.Success)
			u.conns.updateConn(conn.ConnId(), conn.FromNode(), conn)
		}
	case reactor.UserActionInboundAdd: // 收件箱
		for _, msg := range action.Messages {
			u.inbound.append(msg)
		}
	case reactor.UserActionOutboundAdd: // 发送箱
		for _, msg := range action.Messages {
			u.outbound.append(msg)
		}
	case reactor.UserActionOutboundForwardResp: // 转发返回
		u.outbound.updateFollowerIndex(action.From, action.Index)
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
		u.heartbeatTick = 0
		localConns := u.conns.connsByNodeId(options.NodeId)
		var closeConns []reactor.Conn
		for _, localConn := range localConns {
			if !localConn.IsAuth() {
				continue
			}
			exist := false
			for _, conn := range action.Conns {
				if localConn.ConnId() == conn.ConnId() {
					exist = true
					break
				}
			}
			if !exist {
				if closeConns == nil {
					closeConns = make([]reactor.Conn, 0, len(localConns))
				}
				closeConns = append(closeConns, localConn)
			}
		}
		u.sendHeartbeatResp(action.From)
		if len(closeConns) > 0 {
			u.sendConnClose(closeConns)
		}
	// 领导返回加入结果
	case reactor.UserActionJoinResp:
		u.joined = action.Success
	}
}

func (u *User) stepLeader(action reactor.UserAction) {
	switch action.Type {
	// 副本收到心跳回应
	case reactor.UserActionNodeHeartbeatResp:
		u.outbound.updateFollowerHeartbeat(action.From)
	// 副本请求加入
	case reactor.UserActionJoin:
		u.outbound.addNewFollower(action.From)
		u.sendJoinResp(action.From)
	}
}

// ==================================== tick ====================================

func (u *User) tick() {

	u.authReady.tick()
	u.inbound.tick()
	u.outbound.tick()

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

	if len(u.outbound.followers) == 0 && u.idleTick >= options.LeaderIdleTimeoutTick {
		u.idleTick = 0
		u.sendUserClose()
		return
	}

	if u.heartbeatTick >= options.NodeHeartbeatTick {
		u.heartbeatTick = 0
		u.sendHeartbeatReq()
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
	for _, follower := range u.outbound.followers {
		conns := u.conns.connsByNodeId(follower.nodeId)
		u.actions = append(u.actions, reactor.UserAction{
			No:          u.no,
			From:        options.NodeId,
			To:          follower.nodeId,
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
		NodeVersion: options.NodeVersion(),
	})
}

func (u *User) sendConnClose(conns []reactor.Conn) {
	u.actions = append(u.actions, reactor.UserAction{
		No:    u.no,
		From:  options.NodeId,
		To:    reactor.LocalNode,
		Type:  reactor.UserActionConnClose,
		Conns: conns,
	})
}

func (u *User) sendUserClose() {
	u.actions = append(u.actions, reactor.UserAction{
		No:   u.no,
		From: options.NodeId,
		To:   reactor.LocalNode,
		Type: reactor.UserActionUserClose,
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

}

func (u *User) becomeFollower() {
	u.role = reactor.RoleFollower
	u.stepFnc = u.stepFollower
	u.tickFnc = u.tickFollower

	u.Info("become follower")
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
	u.authReady.reset()
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
