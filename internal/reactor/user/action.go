package reactor

type ActionType uint8

const (
	// 未知状态
	ActionUnknown ActionType = iota
	// 连接请求
	ActionConnectReq
	// 连接返回
	ActionConnectResp
	// 收到发送消息
	ActionOnSend
	// ping
	ActionPing
	// 接收消息
	ActionRecv
)

func (a ActionType) String() string {
	switch a {
	case ActionUnknown:
		return "ActionUnknown"
	case ActionConnectReq:
		return "ActionConnectReq"
	case ActionConnectResp:
		return "ActionConnectResp"
	case ActionOnSend:
		return "ActionOnSend"
	case ActionPing:
		return "ActionPing"
	case ActionRecv:
		return "ActionRecv"
	default:
		return "Unknown"
	}
}

type Action struct {
	No       string // 唯一编号
	Uid      string
	Type     ActionType
	Messages []Message
	Index    uint64
	LeaderId uint64
}

func (a Action) Size() uint64 {

	return 0
}
