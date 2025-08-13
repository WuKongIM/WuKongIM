package types

// ag-ui事件 https://github.com/ag-ui-protocol/ag-ui
type AGUIEvent string

const (
	// 文本消息相关事件
	// 文本消息开始
	AGUIEventTextMessageStart AGUIEvent = "___TextMessageStart"
	// 文本消息内容
	AGUIEventTextMessageContent AGUIEvent = "___TextMessageContent"
	// 文本消息结束
	AGUIEventTextMessageEnd AGUIEvent = "___TextMessageEnd"

	// 工具调用相关事件
	// 工具调用开始
	AGUIEventToolCallStart AGUIEvent = "___ToolCallStart"
	// 工具调用参数
	AGUIEventToolCallArgs AGUIEvent = "___ToolCallArgs"
	// 工具调用结束
	AGUIEventToolCallEnd AGUIEvent = "___ToolCallEnd"
	// 工具调用结果
	AGUIEventToolCallResult AGUIEvent = "___ToolCallResult"
)

func (a AGUIEvent) String() string {
	return string(a)
}
