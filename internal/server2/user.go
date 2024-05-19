package server

import (
	"fmt"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/sasha-s/go-deadlock"
)

type userHandler struct {
	uid     string
	actions []*UserAction
	conns   []*connContext

	processMsgQueue *userMsgQueue // 处理消息的队列

	mu deadlock.RWMutex
}

func newUserHandler(uid string) *userHandler {

	return &userHandler{
		uid:             uid,
		processMsgQueue: newUserMsgQueue(fmt.Sprintf("user:process:%s", uid)),
	}
}

func (u *userHandler) hasReady() bool {
	if u.processMsgQueue.hasNextMessages() {
		return true
	}
	return len(u.actions) > 0
}

func (u *userHandler) ready() userReady {

	// 处理消息
	proccessMsgs := u.processMsgQueue.nextMessages()
	if len(proccessMsgs) > 0 {
		u.actions = append(u.actions, &UserAction{
			ActionType: UserActionProcess,
			Messages:   proccessMsgs,
		})
	}

	actions := u.actions
	u.actions = nil
	u.processMsgQueue.acceptInProgress()
	return userReady{
		actions: actions,
	}
}

func (u *userHandler) addConnIfNotExist(conn *connContext) {
	u.mu.Lock()
	defer u.mu.Unlock()

	fmt.Println("addConnIfNotExist--->", conn.id)

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

func (u *userHandler) acceptReady(rd userReady) {

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
	Index      uint64
}
