package reactor

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type IUser interface {
	// Start 开始
	Start() error
	// Stop 停止
	Stop()
	// WakeIfNeed 根据需要唤醒用户（如果用户在就不需要唤醒）
	WakeIfNeed(uid string)
	// AddAction 添加用户行为
	AddAction(a UserAction) bool
	// CloseConn 关闭连接
	CloseConn(conn *Conn)
	// Advance 推进，让用户立即执行下一个动作
	Advance(uid string)
	// Exist 用户是否存在
	Exist(uid string) bool
	// 查询连接信息
	ConnsByUid(uid string) []*Conn
	ConnCountByUid(uid string) int
	ConnsByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) []*Conn
	ConnCountByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) int
	ConnById(uid string, fromNode uint64, id int64) *Conn
	LocalConnById(uid string, id int64) *Conn

	// 更新连接
	UpdateConn(conn *Conn)
}

type UserPlus struct {
	user IUser
	wklog.Log
}

func newUserPlus(user IUser) *UserPlus {
	return &UserPlus{
		user: user,
		Log:  wklog.NewWKLog("UserPlus"),
	}
}

// WakeIfNeed 根据需要唤醒用户（如果用户在就不需要唤醒）
func (u *UserPlus) WakeIfNeed(uid string) {
	u.user.WakeIfNeed(uid)
}

// UpdateConfig 更新配置
func (u *UserPlus) UpdateConfig(uid string, cfg UserConfig) {
	u.user.AddAction(UserAction{
		Type: UserActionConfigUpdate,
		Uid:  uid,
		Cfg:  cfg,
	})
}

// AddAuth 添加认证
func (u *UserPlus) AddAuth(conn *Conn, connectPacket *wkproto.ConnectPacket) {
	u.user.AddAction(UserAction{
		Type: UserActionAuthAdd,
		Uid:  connectPacket.UID,
		Messages: []*UserMessage{
			{
				Conn:  conn,
				Frame: connectPacket,
			},
		},
	})
	u.user.Advance(connectPacket.UID)
}

// Join 副本加入到领导
func (u *UserPlus) Join(uid string, nodeId uint64) {
	u.user.AddAction(UserAction{
		Type:    UserActionJoin,
		Uid:     uid,
		From:    nodeId,
		Success: true,
	})
	u.user.Advance(uid)
}

// JoinResp 加入返回
func (u *UserPlus) JoinResp(uid string) {
	u.user.AddAction(UserAction{
		Type:    UserActionJoinResp,
		Uid:     uid,
		Success: true,
	})
	u.user.Advance(uid)
}

// Exist 用户是否存在
func (u *UserPlus) Exist(uid string) bool {
	return u.user.Exist(uid)
}

// Kick 踢掉连接
func (u *UserPlus) Kick(conn *Conn, reasonCode wkproto.ReasonCode, reason string) {
	u.user.AddAction(UserAction{
		Type: UserActionOutboundAdd,
		Uid:  conn.Uid,
		Messages: []*UserMessage{
			{
				Conn: conn,
				Frame: &wkproto.DisconnectPacket{
					ReasonCode: reasonCode,
					Reason:     reason,
				},
			},
		},
	})
}

func (u *UserPlus) AllUserCount() int {
	return 0
}

// HeartbeatReq 心跳请求，follower节点执行 领导节点发送给follower节点
func (u *UserPlus) HeartbeatReq(uid string, fromNode uint64, connIds []int64) {
	conns := make([]*Conn, 0, len(connIds))
	for _, connId := range connIds {
		conn := u.LocalConnById(uid, connId)
		if conn != nil {
			conns = append(conns, conn)
		}
	}
	u.user.AddAction(UserAction{
		Uid:   uid,
		Type:  UserActionNodeHeartbeatReq,
		From:  fromNode,
		Conns: conns,
	})
}

// HeartbeatResp 心跳响应,leader节点执行 副本节点响应领导节点的心跳
func (u *UserPlus) HeartbeatResp(uid string, fromNode uint64, connIds []int64) {
	conns := make([]*Conn, 0, len(connIds))
	for _, connId := range connIds {
		conn := u.ConnById(uid, fromNode, connId)
		if conn != nil {
			conns = append(conns, conn)
		}
	}
	u.user.AddAction(UserAction{
		Uid:   uid,
		Type:  UserActionNodeHeartbeatResp,
		From:  fromNode,
		Conns: conns,
	})
}

// ========================================== message ==========================================

// AddMessages 添加消息到收件箱
func (u *UserPlus) AddMessages(uid string, msgs []*UserMessage) {
	u.user.AddAction(UserAction{
		Type:     UserActionInboundAdd,
		Uid:      uid,
		Messages: msgs,
	})
	u.user.Advance(uid)
}

// AddMessage 添加消息到收件箱
func (u *UserPlus) AddMessage(uid string, msg *UserMessage) {
	u.user.AddAction(UserAction{
		Type: UserActionInboundAdd,
		Uid:  uid,
		Messages: []*UserMessage{
			msg,
		},
	})
	u.user.Advance(uid)
}

// AddMessageToOutbound 添加消息到发件箱
func (u *UserPlus) AddMessageToOutbound(uid string, msg *UserMessage) {
	u.user.AddAction(UserAction{
		Type:     UserActionOutboundAdd,
		Uid:      uid,
		Messages: []*UserMessage{msg},
	})
	u.user.Advance(uid)
}

// ========================================== conn ==========================================

// ConnsByDeviceFlag 根据设备标识获取连接
func (u *UserPlus) ConnsByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) []*Conn {
	return u.user.ConnsByDeviceFlag(uid, deviceFlag)
}

func (u *UserPlus) ConnCountByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) int {
	return u.user.ConnCountByDeviceFlag(uid, deviceFlag)
}

// ConnsByUid 根据用户uid获取连接
func (u *UserPlus) ConnsByUid(uid string) []*Conn {
	return u.user.ConnsByUid(uid)
}

func (u *UserPlus) ConnCountByUid(uid string) int {
	return u.user.ConnCountByUid(uid)
}

// LocalConnById 获取本地连接
func (u *UserPlus) LocalConnById(uid string, id int64) *Conn {
	return u.user.LocalConnById(uid, id)
}

// ConnById 获取连接
func (u *UserPlus) ConnById(uid string, fromNode uint64, id int64) *Conn {
	return u.user.ConnById(uid, fromNode, id)
}

// CloseConn 关闭连接
func (u *UserPlus) CloseConn(conn *Conn) {

	fmt.Println("CloseConn---->", conn.Uid)
	u.user.AddAction(UserAction{
		Type:  UserActionConnClose,
		Uid:   conn.Uid,
		Conns: []*Conn{conn},
	})
}

// UpdateConn 更新连接
func (u *UserPlus) UpdateConn(conn *Conn) {
	u.user.UpdateConn(conn)
}

// ConnWrite 连接写包
func (u *UserPlus) ConnWrite(conn *Conn, frame wkproto.Frame) {

	data, err := Proto.EncodeFrame(frame, conn.ProtoVersion)
	if err != nil {
		u.Error("encode failed", zap.Error(err))
		return
	}
	u.ConnWriteBytes(conn, data)
}

func (u *UserPlus) ConnWriteBytes(conn *Conn, bytes []byte) {
	u.user.AddAction(UserAction{
		Type: UserActionWrite,
		Uid:  conn.Uid,
		Messages: []*UserMessage{
			{
				Conn:      conn,
				WriteData: bytes,
			},
		},
	})
	u.user.Advance(conn.Uid)
}

// AllConnCount 所有连接数量
func (u *UserPlus) AllConnCount() int {
	return 0
}
