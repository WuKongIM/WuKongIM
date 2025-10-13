package types

import "fmt"

// Event Event
type Event struct {
	Event string      `json:"event"` // 事件标示
	Data  interface{} `json:"data"`  // 事件数据
}

func (e *Event) String() string {
	return fmt.Sprintf("Event:%s Data:%v", e.Event, e.Data)
}

type MessageOfflineNotify struct {
	MessageResp
	ToUids          []string `json:"to_uids"`
	Compress        string   `json:"compress,omitempty"`         // 压缩ToUIDs 如果为空 表示不压缩 为gzip则采用gzip压缩
	CompresssToUids []byte   `json:"compress_to_uids,omitempty"` // 已压缩的to_uids
	SourceId        int64    `json:"source_id,omitempty"`        // 来源节点ID
}

const (
	// EventMsgOffline 离线消息
	EventMsgOffline = "msg.offline"
	// EventMsgNotify 消息通知（将所有消息通知到第三方程序）
	EventMsgNotify = "msg.notify"
	// EventOnlineStatus 用户在线状态
	EventOnlineStatus = "user.onlinestatus"
	// EventMsgStream 流消息
	EventMsgStream = "msg.stream"
)
