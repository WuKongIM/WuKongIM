package cluster

type IMonitor interface {
	// RequestGoroutine 请求协程数
	RequestGoroutine() int64
	// MessageGoroutine 消息处理协程数
	MessageGoroutine() int64
	// InboundFlightMessageCount 入站飞行消息数
	InboundFlightMessageCount() int64
	// OutboundFlightMessageCount 出站飞行消息数
	OutboundFlightMessageCount() int64
	// InboundFlightMessageBytes 入站飞行消息字节数
	InboundFlightMessageBytes() int64
	// OutboundFlightMessageBytes 出站飞行消息字节数
	OutboundFlightMessageBytes() int64
}
