package server

import (
	"fmt"

	"github.com/sasha-s/go-deadlock"
)

type userHandler struct {
	uid     string
	actions []*UserAction
	conns   []*connContext

	msgQueue *userMsgQueue

	mu deadlock.RWMutex
}

func newUserHandler(uid string) *userHandler {

	return &userHandler{
		uid:      uid,
		msgQueue: newUserMsgQueue(fmt.Sprintf("user:%s", uid)),
	}
}

func (u *userHandler) hasReady() bool {
	if u.msgQueue.hasNextMessages() {
		return true
	}
	return len(u.actions) > 0
}

func (u *userHandler) ready() userReady {
	messages := u.msgQueue.nextMessages()

	if len(messages) > 0 {
		u.actions = append(u.actions, &UserAction{
			ActionType: UserActionProcess,
			Messages:   messages,
		})
	}

	actions := u.actions
	u.actions = nil
	u.msgQueue.acceptInProgress()
	return userReady{
		actions: actions,
	}
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

type userReady struct {
	actions []*UserAction
}

type UserAction struct {
	ActionType UserActionType
	Messages   []*ReactorUserMessage
	Index      uint64
}
