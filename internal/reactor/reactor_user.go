package reactor

import wkproto "github.com/WuKongIM/WuKongIMGoProto"

type IUser interface {
	// Start 开始
	Start() error
	// Stop 停止
	Stop()
	// WakeIfNeed 根据需要唤醒用户（如果用户在就不需要唤醒）
	WakeIfNeed(uid string)
	// AddAction 添加用户行为
	AddAction(a UserAction) bool
}

type UserPlus struct {
	user IUser
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
func (u *UserPlus) AddAuth(conn Conn, connectPacket *wkproto.ConnectPacket) {
	u.user.AddAction(UserAction{
		Type: UserActionAuthAdd,
		Uid:  connectPacket.UID,
		Messages: []UserMessage{
			&defaultUserMessage{
				conn:  conn,
				frame: connectPacket,
			},
		},
	})
}

// AddMessages 添加消息
func (u *UserPlus) AddMessages(uid string, msgs []UserMessage) {
	u.user.AddAction(UserAction{
		Type:     UserActionInboundAdd,
		Uid:      uid,
		Messages: msgs,
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
func (u *UserPlus) Kick(conn Conn, reasonCode wkproto.ReasonCode, reason string) {
	u.user.AddAction(UserAction{
		Type: UserActionOutboundAdd,
		Uid:  conn.Uid(),
		Messages: []UserMessage{
			&defaultUserMessage{
				conn: conn,
				frame: &wkproto.DisconnectPacket{
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

// ========================================== conn ==========================================

// ConnsByDeviceFlag 根据设备标识获取连接
func (u *UserPlus) ConnsByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) []Conn {
	return nil
}

// ConnsByUid 根据用户uid获取连接
func (u *UserPlus) ConnsByUid(uid string) []Conn {
	return nil
}

// LocalConnById 获取本地连接
func (u *UserPlus) LocalConnById(uid string, id int64) Conn {
	return nil
}

// ConnById 获取连接
func (u *UserPlus) ConnById(uid string, fromNode uint64, id int64) Conn {
	return nil
}

// CloseConn 关闭连接
func (u *UserPlus) CloseConn(conn Conn) {

}

// ConnWrite 连接写包
func (u *UserPlus) ConnWrite(conn Conn, frame wkproto.Frame) {
	u.user.AddAction(UserAction{
		Type: UserActionOutboundAdd,
		Uid:  conn.Uid(),
		Messages: []UserMessage{
			&defaultUserMessage{
				conn:  conn,
				frame: frame,
			},
		},
	})
}

func (u *UserPlus) ConnWriteBytes(conn Conn, bytes []byte) {

}

// AllConnCount 所有连接数量
func (u *UserPlus) AllConnCount() int {
	return 0
}
