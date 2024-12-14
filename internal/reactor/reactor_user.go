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
}

// Join 副本加入到领导
func (u *UserPlus) Join(uid string, nodeId uint64) {
	u.user.AddAction(UserAction{
		Type:    UserActionAuthAdd,
		Uid:     uid,
		From:    nodeId,
		Success: true,
	})
}

// Role 获取用户角色
func (u *UserPlus) Role(uid string) Role {
	return 0
}

// Exist 用户是否存在
func (u *UserPlus) Exist(uid string) bool {
	return false
}

// LeaderId 获取领导ID
func (u *UserPlus) LeaderId(uid string) uint64 {
	return 0
}

// Kick 踢掉连接
func (u *UserPlus) Kick(conn *Conn, reasonCode wkproto.ReasonCode, reason string) {
	u.user.AddAction(UserAction{
		Type: UserActionOutboundAdd,
		Uid:  conn.Uid,
		Messages: []*UserMessage{
			&UserMessage{
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

// ========================================== message ==========================================

// AddMessages 添加消息到收件箱
func (u *UserPlus) AddMessages(uid string, msgs []*UserMessage) {
	u.user.AddAction(UserAction{
		Type:     UserActionInboundAdd,
		Uid:      uid,
		Messages: msgs,
	})
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
}

// AddMessageToOutbound 添加消息到发件箱
func (u *UserPlus) AddMessageToOutbound(uid string, msg *UserMessage) {
	u.user.AddAction(UserAction{
		Type:     UserActionOutboundAdd,
		Uid:      uid,
		Messages: []*UserMessage{msg},
	})
}

// ========================================== conn ==========================================

// ConnsByDeviceFlag 根据设备标识获取连接
func (u *UserPlus) ConnsByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) []*Conn {
	return nil
}

func (u *UserPlus) ConnCountByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) int {
	return 0
}

// ConnsByUid 根据用户uid获取连接
func (u *UserPlus) ConnsByUid(uid string) []*Conn {
	return nil
}

func (u *UserPlus) ConnCountByUid(uid string) int {
	return 0
}

// LocalConnById 获取本地连接
func (u *UserPlus) LocalConnById(uid string, id int64) *Conn {
	return nil
}

// ConnById 获取连接
func (u *UserPlus) ConnById(uid string, fromNode uint64, id int64) *Conn {
	return nil
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
}

// AllConnCount 所有连接数量
func (u *UserPlus) AllConnCount() int {
	return 0
}
