package server

import (
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/sasha-s/go-deadlock"
	"github.com/valyala/fastrand"
	"go.uber.org/zap"
)

type userHandler struct {
	uniqueNo string // 唯一标识，所有step只处理相同标识的消息
	uid      string
	actions  []UserAction

	conns []*connContext

	connNodeIds []uint64 // 连接涉及到的节点id集合

	pingState *replica.ReadyState // 正在发送ping
	pingQueue *userMsgQueue       // ping消息队列

	// 认证
	authState *replica.ReadyState // 正在认证
	authQueue *userMsgQueue       // 认证消息队列

	// 接收ack
	recvackState *replica.ReadyState // 发送ack
	recvackQueue *userMsgQueue       // 接收ack的队列

	// 收消息
	recvMsgState *replica.ReadyState
	recvMsgQueue *userMsgQueue

	leaderId uint64 // 用户所在的领导节点

	status userStatus // 用户状态
	role   userRole   // 用户角色

	stepFnc func(UserAction) error
	tickFnc func()

	sub *userReactorSub

	mu deadlock.RWMutex

	nodePingTick        int            // 节点ping计时
	nodePongTimeoutTick map[uint64]int // 节点pong超时计时

	initState *replica.ReadyState // 初始化状态
	// authTick int // auth tick 计时
	// sendRecvackTick int // 发送recvack计时
	// recvMsgTick int // 接收消息计时

	checkLeaderTick            int // 定时检查正确的领导节点
	maxCheckLeaderIntervalTick int
	maxNodePingTick            int
	maxNodePongTimeoutTick     int

	wklog.Log

	opts *Options
}

func newUserHandler(uid string, sub *userReactorSub) *userHandler {

	opts := sub.r.s.opts

	// 以避免系统中因定时器、周期性任务或请求间隔完全一致而导致的同步问题（例如拥堵或资源竞争）。
	p := float64(fastrand.Uint32()) / (1 << 32)
	checkLeaderJitter := p * float64(opts.Reactor.User.CheckLeaderIntervalTick)
	maxCheckLeaderIntervalTick := opts.Reactor.User.CheckLeaderIntervalTick + int(checkLeaderJitter)

	p = float64(fastrand.Uint32()) / (1 << 32)
	nodePingTickJitter := p * float64(opts.Reactor.User.NodePingTick)

	maxNodePingTick := opts.Reactor.User.NodePingTick + int(nodePingTickJitter)

	p = float64(fastrand.Uint32()) / (1 << 32)
	nodePongTimeoutJitter := p * float64(opts.Reactor.User.NodePongTimeoutTick)

	maxNodePongTimeoutTick := opts.Reactor.User.NodePongTimeoutTick + int(nodePongTimeoutJitter)

	retryTickCount := 20
	u := &userHandler{
		uid:                        uid,
		authQueue:                  newUserMsgQueue(fmt.Sprintf("user:auth:%s", uid)),
		pingQueue:                  newUserMsgQueue(fmt.Sprintf("user:ping:%s", uid)),
		recvackQueue:               newUserMsgQueue(fmt.Sprintf("user:recvack:%s", uid)),
		recvMsgQueue:               newUserMsgQueue(fmt.Sprintf("user:recv:%s", uid)),
		Log:                        wklog.NewWKLog(fmt.Sprintf("userHandler[%d][%s]", sub.r.s.opts.Cluster.NodeId, uid)),
		sub:                        sub,
		nodePongTimeoutTick:        make(map[uint64]int),
		uniqueNo:                   wkutil.GenUUID(),
		opts:                       opts,
		initState:                  replica.NewReadyState(retryTickCount),
		pingState:                  replica.NewReadyState(retryTickCount),
		authState:                  replica.NewReadyState(retryTickCount),
		recvackState:               replica.NewReadyState(retryTickCount),
		recvMsgState:               replica.NewReadyState(retryTickCount),
		maxCheckLeaderIntervalTick: maxCheckLeaderIntervalTick,
		maxNodePingTick:            maxNodePingTick,
		maxNodePongTimeoutTick:     maxNodePongTimeoutTick,
	}

	return u
}

func (u *userHandler) hasReady() bool {
	if u.isUninitialized() {

		return true
	}
	if u.hasAuth() {
		return true
	}
	if u.hasRecvack() {
		return true
	}
	if u.hasRecvMsg() {
		return true
	}
	if u.role == userRoleLeader {
		if u.hasPing() {
			return true
		}
	}

	return len(u.actions) > 0
}

func (u *userHandler) ready() userReady {
	if u.isUninitialized() {
		if !u.initState.IsProcessing() {
			u.initState.StartProcessing()
			u.actions = append(u.actions, UserAction{UniqueNo: u.uniqueNo, ActionType: UserActionInit})
		}

	} else {
		if u.role == userRoleLeader {
			// 连接认证
			if u.hasAuth() {
				u.authState.StartProcessing()
				msgs := u.authQueue.sliceWithSize(u.authQueue.processingIndex+1, u.authQueue.lastIndex+1, 0)
				u.actions = append(u.actions, UserAction{UniqueNo: u.uniqueNo, ActionType: UserActionAuth, Messages: msgs})
				u.Debug("send auth...")
			}
			// 发送ping
			if u.hasPing() {
				u.pingState.StartProcessing()
				msgs := u.pingQueue.sliceWithSize(u.pingQueue.processingIndex+1, u.pingQueue.lastIndex+1, 0)
				u.actions = append(u.actions, UserAction{UniqueNo: u.uniqueNo, ActionType: UserActionPing, Messages: msgs})
				// u.Info("send ping...")
			}

			// 发送recvack
			if u.hasRecvack() {
				u.recvackState.StartProcessing()
				msgs := u.recvackQueue.sliceWithSize(u.recvackQueue.processingIndex+1, u.recvackQueue.lastIndex+1, 0)
				u.actions = append(u.actions, UserAction{UniqueNo: u.uniqueNo, ActionType: UserActionRecvack, Messages: msgs})
				// u.Info("send recvack...")
			}

		} else {
			// 转发用户action
			if u.hasRecvack() {
				u.recvackState.StartProcessing()
				msgs := u.recvackQueue.sliceWithSize(u.recvackQueue.processingIndex+1, u.recvackQueue.lastIndex+1, 0)
				u.actions = append(u.actions, UserAction{
					UniqueNo:   u.uniqueNo,
					ActionType: UserActionForward,
					Uid:        u.uid,
					LeaderId:   u.leaderId,
					Forward: &UserAction{
						ActionType: UserActionSend,
						Uid:        u.uid,
						Messages:   msgs,
					}})
				// u.Info("forward recvack...")
			}
			// 转发连接认证消息
			if u.hasAuth() {
				u.authState.StartProcessing()
				msgs := u.authQueue.sliceWithSize(u.authQueue.processingIndex+1, u.authQueue.lastIndex+1, 0)
				u.actions = append(u.actions, UserAction{
					UniqueNo:   u.uniqueNo,
					ActionType: UserActionForward,
					Uid:        u.uid,
					LeaderId:   u.leaderId,
					Forward: &UserAction{
						ActionType: UserActionConnect,
						Uid:        u.uid,
						Messages:   msgs,
					},
				})
				u.Debug("forward auth...")
			}

		}
		// 接受消息
		if u.hasRecvMsg() {
			u.recvMsgState.StartProcessing()
			msgs := u.recvMsgQueue.sliceWithSize(u.recvMsgQueue.processingIndex+1, u.recvMsgQueue.lastIndex+1, 0)
			u.actions = append(u.actions, UserAction{UniqueNo: u.uniqueNo, ActionType: UserActionRecv, Messages: msgs})
			// u.Info("recv msg...", zap.Int("msgCount", len(msgs)))
		}

	}

	actions := u.actions
	u.actions = nil
	return userReady{
		actions: actions,
	}
}

func (u *userHandler) hasRecvack() bool {
	if u.recvackState.IsProcessing() {
		return false
	}

	return u.recvackQueue.processingIndex < u.recvackQueue.lastIndex
}

func (u *userHandler) hasAuth() bool {
	if u.authState.IsProcessing() {
		return false
	}

	return u.authQueue.processingIndex < u.authQueue.lastIndex
}

func (u *userHandler) hasPing() bool {
	if u.pingState.IsProcessing() {
		return false
	}
	return u.pingQueue.processingIndex < u.pingQueue.lastIndex
}

func (u *userHandler) hasRecvMsg() bool {
	if u.recvMsgState.IsProcessing() {
		return false
	}

	return u.recvMsgQueue.processingIndex < u.recvMsgQueue.lastIndex

}

// 是否未初始化
func (u *userHandler) isUninitialized() bool {

	return u.status == userStatusUninitialized
}

func (u *userHandler) becomeLeader() {
	u.reset()
	u.leaderId = 0
	u.role = userRoleLeader
	u.stepFnc = u.stepLeader
	u.tickFnc = u.tickLeader
	u.Debug("become logic leader")
}

func (u *userHandler) becomeProxy(leaderId uint64) {
	u.reset()
	u.leaderId = leaderId
	u.role = userRoleProxy
	u.stepFnc = u.stepProxy
	u.tickFnc = u.tickProxy

	u.Debug("become logic proxy", zap.Uint64("leaderId", u.leaderId))
}

func (u *userHandler) reset() {

	// 将ping和recvack队列里的数据清空掉，因为这些数据已无意义
	// 但是recvMsgQueue里的数据还是有意义的，因为这些数据是需要投递给用户的
	// if u.pingQueue.hasNextMessages() {
	// 	u.pingQueue.truncateTo(u.pingQueue.lastIndex)
	// }

	// if u.recvackQueue.hasNextMessages() {
	// 	u.recvackQueue.truncateTo(u.recvackQueue.lastIndex)
	// }

	u.pingQueue.resetIndex()
	u.recvackQueue.resetIndex()
	u.recvMsgQueue.resetIndex()
	u.authQueue.resetIndex()

	u.initState.Reset()
	u.pingState.Reset()
	u.recvackState.Reset()
	u.recvMsgState.Reset()
	u.authState.Reset()

	// u.authTick = 0

	u.nodePingTick = 0
	u.nodePongTimeoutTick = make(map[uint64]int)
}

func (u *userHandler) tick() {

	u.initState.Tick()

	u.authState.Tick()

	u.pingState.Tick()

	u.recvackState.Tick()

	u.recvMsgState.Tick()

	// if u.authing && u.authTick > u.sub.r.s.opts.Reactor.User.ProcessIntervalTick {
	// 	u.authing = false
	// 	u.authTick = 0
	// }
	// if u.sendRecvacking && u.sendRecvackTick > u.sub.r.s.opts.Reactor.User.ProcessIntervalTick {
	// 	u.sendRecvacking = false
	// 	u.sendRecvackTick = 0
	// }
	// if u.recvMsging && u.recvMsgTick > u.sub.r.s.opts.Reactor.User.ProcessIntervalTick {
	// 	u.recvMsging = false
	// 	u.recvMsgTick = 0
	// }

	// 定时校验领导的正确性
	u.checkLeaderTick++
	if u.checkLeaderTick >= u.maxCheckLeaderIntervalTick {
		u.checkLeaderTick = 0
		u.actions = append(u.actions, UserAction{UniqueNo: u.uniqueNo, ActionType: UserActionCheckLeader, Uid: u.uid})
	}

	if u.tickFnc != nil {
		u.tickFnc()
	}

}

func (u *userHandler) tickProxy() {
	u.nodePingTick++
	if u.nodePingTick >= u.maxNodePingTick { // 与领导失去联系，主动断开连接
		u.Debug("nodePingTick timeout", zap.String("uid", u.uid), zap.Int("nodePingTick", u.nodePingTick))
		u.nodePingTick = 0
		u.actions = append(u.actions, UserAction{UniqueNo: u.uniqueNo, ActionType: UserActionClose, Uid: u.uid})
	}
}

func (u *userHandler) tickLeader() {
	u.nodePingTick++

	for _, proxyNodeId := range u.connNodeIds {
		u.nodePongTimeoutTick[proxyNodeId]++
	}

	if u.nodePingTick >= u.maxNodePingTick/3 { // 领导发送ping
		u.nodePingTick = 0
		var messages []ReactorUserMessage
		// TODO: u.conns可能存在竞锁问题
		if len(u.conns) > 0 {
			for _, c := range u.conns {
				if c.isRealConn {
					continue
				}
				messages = append(messages, ReactorUserMessage{
					FromNodeId: c.realNodeId,
					DeviceId:   c.deviceId,
					ConnId:     c.proxyConnId,
				})
			}
			if len(messages) > 0 {
				u.actions = append(u.actions, UserAction{ActionType: UserActionNodePing, Uid: u.uid, Messages: messages})
			}

		} else {
			// 没有任何连接了，可以关闭了
			u.Debug("no conn, close user", zap.String("uid", u.uid))
			u.actions = append(u.actions, UserAction{UniqueNo: u.uniqueNo, ActionType: UserActionClose, Uid: u.uid})
		}
	}

	// 检查代理节点是否超时
	for _, proxyNodeId := range u.connNodeIds {
		if u.nodePongTimeoutTick[proxyNodeId] >= u.maxNodePingTick {
			u.Info("user node pong timeout", zap.String("uid", u.uid), zap.Uint64("proxyNodeId", proxyNodeId))
			u.actions = append(u.actions, UserAction{ActionType: UserActionProxyNodeTimeout, Uid: u.uid, Messages: []ReactorUserMessage{
				{FromNodeId: proxyNodeId},
			}})
		}
	}
}

func (u *userHandler) keepActivity() {
	if u.role == userRoleProxy {
		u.nodePingTick = 0
	}
}

func (u *userHandler) addConnIfNotExist(conn *connContext) {
	u.mu.Lock()
	defer u.mu.Unlock()

	exist := false
	for _, c := range u.conns {
		if c.deviceId == conn.deviceId && c.realNodeId == conn.realNodeId && c.connId == conn.connId {
			exist = true
			break
		}
	}
	if !exist {
		u.Debug("add conn", zap.Int64("connId", conn.connId), zap.String("uid", u.uid), zap.String("deviceId", conn.deviceId))
		u.conns = append(u.conns, conn)
	}

	u.resetConnNodeIds()

	u.keepActivity() // 有新连接进来了  继续保活
}

func (u *userHandler) getConnByDeviceId(deviceId string) []*connContext {
	if strings.TrimSpace(deviceId) == "" {
		return nil
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	conns := make([]*connContext, 0, len(u.conns))
	for _, c := range u.conns {
		if c.deviceId == deviceId {
			conns = append(conns, c)
		}
	}
	return conns
}

func (u *userHandler) getConnById(id int64) *connContext {
	u.mu.RLock()
	defer u.mu.RUnlock()
	for _, c := range u.conns {
		if c.connId == id {
			return c
		}
	}
	return nil
}

// func (u *userHandler) getConnByProxyConnId(nodeId uint64, proxyConnId int64) *connContext {
// 	u.mu.RLock()
// 	defer u.mu.RUnlock()
// 	for _, c := range u.conns {
// 		if c.realNodeId == nodeId && c.proxyConnId == proxyConnId {
// 			return c
// 		}
// 	}
// 	return nil
// }

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

// func (u *userHandler) removeConn(deviceId string) {
// 	u.mu.Lock()
// 	defer u.mu.Unlock()
// 	for i, c := range u.conns {
// 		if c.deviceId == deviceId {
// 			u.conns = append(u.conns[:i], u.conns[i+1:]...)
// 			break
// 		}
// 	}
// 	u.resetConnNodeIds()
// }

func (u *userHandler) removeConnById(id int64) *connContext {
	var existConn *connContext
	for i, c := range u.conns {
		if c.connId == id {
			existConn = c
			u.conns = append(u.conns[:i], u.conns[i+1:]...)
			break
		}
	}
	u.resetConnNodeIds()

	return existConn

}

// 移除指定节点id的所有连接
func (u *userHandler) removeConnsByNodeId(nodeId uint64) []*connContext {
	u.mu.Lock()
	defer u.mu.Unlock()

	newConns := make([]*connContext, 0, len(u.conns))
	removeConns := make([]*connContext, 0, len(u.conns))
	for i := 0; i < len(u.conns); i++ {
		if u.conns[i].realNodeId != nodeId {
			newConns = append(newConns, u.conns[i])
		} else {
			removeConns = append(removeConns, u.conns[i])
		}
	}
	u.conns = newConns
	u.resetConnNodeIds()
	return removeConns
}

func (u *userHandler) resetConnNodeIds() {
	u.connNodeIds = u.connNodeIds[:0]
	for _, conn := range u.conns {
		if conn.realNodeId == 0 {
			continue
		}
		var exist = false
		for _, connNodeId := range u.connNodeIds {

			if connNodeId == conn.realNodeId {
				exist = true
				break
			}
		}
		if !exist {
			u.connNodeIds = append(u.connNodeIds, conn.realNodeId)
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
	actions []UserAction
}

type UserAction struct {
	UniqueNo   string
	ActionType UserActionType
	Uid        string
	Messages   []ReactorUserMessage
	LeaderId   uint64 // 用户所在的领导节点
	Index      uint64
	Reason     Reason

	Forward *UserAction
}

func (u *UserAction) MarshalWithEncoder(enc *wkproto.Encoder) error {
	enc.WriteUint8(uint8(u.ActionType))
	enc.WriteString(u.Uid)
	enc.WriteInt32(int32(len(u.Messages)))
	for _, m := range u.Messages {
		err := m.MarshalWithEncoder(enc)
		if err != nil {
			return err
		}
	}
	return nil
}

func (u *UserAction) UnmarshalWithDecoder(dec *wkproto.Decoder) error {
	var err error
	var t uint8

	// type
	if t, err = dec.Uint8(); err != nil {
		return err
	}
	u.ActionType = UserActionType(t)

	// uid
	if u.Uid, err = dec.String(); err != nil {
		return err
	}

	// messages
	var msgCount int32
	if msgCount, err = dec.Int32(); err != nil {
		return err
	}
	for i := 0; i < int(msgCount); i++ {
		m := ReactorUserMessage{}
		if err = m.UnmarshalWithDecoder(dec); err != nil {
			return err
		}
		u.Messages = append(u.Messages, m)
	}
	return nil
}

type UserActionSet []UserAction

func (u UserActionSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteInt32(int32(len(u)))
	for _, a := range u {
		if err := a.MarshalWithEncoder(enc); err != nil {
			return nil, err
		}
	}
	return enc.Bytes(), nil
}

func (u *UserActionSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var count int32
	if count, err = dec.Int32(); err != nil {
		return err
	}
	for i := 0; i < int(count); i++ {
		a := UserAction{}
		if err = a.UnmarshalWithDecoder(dec); err != nil {
			return err
		}
		*u = append(*u, a)
	}
	return nil
}
