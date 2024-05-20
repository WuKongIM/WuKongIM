package server

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/sasha-s/go-deadlock"
)

type userHandler struct {
	uid     string
	actions []*UserAction
	conns   []*connContext

	sendPing  bool          // 正在发送ping
	pingQueue *userMsgQueue // ping消息队列

	sendRecvacking bool          // 正在发送ack
	recvackQueue   *userMsgQueue // 接收ack的队列

	recvMsging   bool          // 正在收消息
	recvMsgQueue *userMsgQueue // 接收消息的队列

	forwardRecvacking bool // 转发recvack中

	leaderId uint64 // 用户所在的领导节点

	status userStatus // 用户状态
	role   userRole   // 用户角色

	stepFnc func(*UserAction) error

	sub *userReactorSub

	mu deadlock.RWMutex

	wklog.Log
}

func newUserHandler(uid string, sub *userReactorSub) *userHandler {

	return &userHandler{
		uid:          uid,
		pingQueue:    newUserMsgQueue(fmt.Sprintf("user:ping:%s", uid)),
		recvackQueue: newUserMsgQueue(fmt.Sprintf("user:recvack:%s", uid)),
		recvMsgQueue: newUserMsgQueue(fmt.Sprintf("user:recv:%s", uid)),
		Log:          wklog.NewWKLog(fmt.Sprintf("userHandler[%s]", uid)),
		sub:          sub,
	}
}

func (u *userHandler) hasReady() bool {
	if !u.isInitialized() {
		return u.status != userStatusInitializing
	}

	if u.role == userRoleLeader {
		if u.hasPing() {
			return true
		}
		if u.hasRecvack() {
			return true
		}

		if u.hasRecvMsg() {
			return true
		}
	} else {
		if u.hasForwardRecvack() {
			return true
		}
	}

	return len(u.actions) > 0
}

func (u *userHandler) ready() userReady {
	if !u.isInitialized() {
		if u.status == userStatusInitializing {
			return userReady{}
		}
		u.status = userStatusInitializing
		u.actions = append(u.actions, &UserAction{ActionType: UserActionInit})
	} else {
		if u.role == userRoleLeader {
			// 发送ping
			if u.hasPing() {
				u.sendPing = true
				msgs := u.pingQueue.sliceWithSize(u.pingQueue.processingIndex+1, u.pingQueue.lastIndex+1, 0)
				u.actions = append(u.actions, &UserAction{ActionType: UserActionPing, Messages: msgs})
			}

			// 发送recvack
			if u.hasRecvack() {
				u.sendRecvacking = true
				msgs := u.recvackQueue.sliceWithSize(u.recvackQueue.processingIndex+1, u.recvackQueue.lastIndex+1, 0)
				u.actions = append(u.actions, &UserAction{ActionType: UserActionRecvack, Messages: msgs})
			}

			// 接受消息
			if u.hasRecvMsg() {
				u.recvMsging = true
				msgs := u.recvMsgQueue.sliceWithSize(u.recvMsgQueue.processingIndex+1, u.recvMsgQueue.lastIndex+1, 0)
				u.actions = append(u.actions, &UserAction{ActionType: UserActionRecv, Messages: msgs})
			}
		} else {
			if u.hasForwardRecvack() {
				u.forwardRecvacking = true
				msgs := u.recvackQueue.sliceWithSize(u.recvackQueue.processingIndex+1, u.recvackQueue.lastIndex+1, 0)
				u.actions = append(u.actions, &UserAction{ActionType: UserActionForwardRecvack, Messages: msgs})
			}
		}

	}

	actions := u.actions
	u.actions = nil
	return userReady{
		actions: actions,
	}
}

func (u *userHandler) hasRecvack() bool {
	if u.sendRecvacking {
		return false
	}
	return u.recvackQueue.processingIndex < u.recvackQueue.lastIndex
}

func (u *userHandler) hasForwardRecvack() bool {
	if u.forwardRecvacking {
		return false
	}
	return u.recvackQueue.processingIndex < u.recvackQueue.lastIndex
}

func (u *userHandler) hasPing() bool {
	if u.sendPing {
		return false
	}
	return u.pingQueue.processingIndex < u.pingQueue.lastIndex
}

func (u *userHandler) hasRecvMsg() bool {
	if u.recvMsging {
		return false
	}
	return u.recvMsgQueue.processingIndex < u.recvMsgQueue.lastIndex

}

func (u *userHandler) addConnIfNotExist(conn *connContext) {
	u.mu.Lock()
	defer u.mu.Unlock()

	exist := false
	for _, c := range u.conns {
		if c.deviceId == conn.deviceId {
			exist = true
			break
		}
	}
	if !exist {
		u.conns = append(u.conns, conn)
	}
}

// 是否已初始化
func (u *userHandler) isInitialized() bool {

	return u.status == userStatusInitialized
}

func (u *userHandler) becomeLeader() {
	u.reset()
	u.leaderId = 0
	u.role = userRoleLeader
	u.stepFnc = u.stepLeader
	u.Info("become logic leader")
}

func (u *userHandler) becomeProxy(leaderId uint64) {
	u.reset()
	u.leaderId = leaderId
	u.role = userRoleProxy
	u.stepFnc = u.stepProxy

	u.Info("become logic proxy")
}

func (u *userHandler) reset() {

	u.pingQueue.processingIndex = 0
	u.recvackQueue.processingIndex = 0
	u.recvMsgQueue.processingIndex = 0

	u.sendPing = false
	u.sendRecvacking = false
	u.recvMsging = false
	u.forwardRecvacking = false

	// 将ping和recvack队列里的数据清空掉，因为这些数据已无意义
	// 但是recvMsgQueue里的数据还是有意义的，因为这些数据是需要投递给用户的
	if u.pingQueue.hasNextMessages() {
		u.pingQueue.truncateTo(u.pingQueue.lastIndex)
	}

	if u.recvackQueue.hasNextMessages() {
		u.recvackQueue.truncateTo(u.recvackQueue.lastIndex)
	}

}

func (u *userHandler) tick() {

}

func (u *userHandler) getConn(deviceId string) *connContext {
	u.mu.RLock()
	defer u.mu.RUnlock()
	for _, c := range u.conns {
		if c.deviceId == deviceId {
			return c
		}
	}
	return nil
}

func (u *userHandler) getConnById(id int64) *connContext {
	u.mu.RLock()
	defer u.mu.RUnlock()
	for _, c := range u.conns {
		if c.id == id {
			return c
		}
	}
	return nil
}

func (u *userHandler) getConns() []*connContext {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.conns
}

func (u *userHandler) getConnCount() int {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return len(u.conns)
}

func (u *userHandler) removeConn(deviceId string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for i, c := range u.conns {
		if c.deviceId == deviceId {
			u.conns = append(u.conns[:i], u.conns[i+1:]...)
			return
		}
	}
}

func (u *userHandler) removeConnById(id int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for i, c := range u.conns {
		if c.id == id {
			u.conns = append(u.conns[:i], u.conns[i+1:]...)
			return
		}
	}
}

// 用户是否存在主设备在线
func (u *userHandler) hasMasterDevice() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	for _, c := range u.conns {
		if c.deviceLevel == wkproto.DeviceLevelMaster {
			return true
		}
	}
	return false

}

type userReady struct {
	actions []*UserAction
}

type UserAction struct {
	ActionType UserActionType
	Messages   []*ReactorUserMessage
	LeaderId   uint64 // 用户所在的领导节点
	Index      uint64
	Reason     Reason
}
