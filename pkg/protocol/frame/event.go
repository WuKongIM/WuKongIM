package frame

import "fmt"

type EventPacket struct {
	Framer
	// 事件ID(可以为空)
	Id string
	// 事件类型
	Type string
	// 事件时间戳
	Timestamp int64
	// 事件数据
	Data []byte
}

func (e *EventPacket) GetFrameType() FrameType {
	return EVENT
}

func (e *EventPacket) String() string {
	return fmt.Sprintf("Id:%s Type:%s Timestamp:%d Data:%s", e.Id, e.Type, e.Timestamp, string(e.Data))
}
