package api

type eventReq struct {
	ClientMsgNo string     `json:"client_msg_no"` // 客户端消息编号（这里的clientMsgNo为必填，并且不允许重复，建议使用uuid）
	ChannelId   string     `json:"channel_id"`    // 频道ID
	ChannelType uint8      `json:"channel_type"`  // 频道类型
	FromUid     string     `json:"from_uid"`      // 发送者UID
	Event       eventModel `json:"event"`         // 事件
}

type eventModel struct {
	Id        string `json:"id,omitempty"`        // 事件ID
	Type      string `json:"type"`                // 事件类型
	Timestamp int64  `json:"timestamp,omitempty"` // 事件时间戳
	Data      string `json:"data,omitempty"`      // 事件数据
}
