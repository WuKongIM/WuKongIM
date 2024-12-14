package reactor

type UserActionType uint8

const (
	// 未知状态
	UserActionUnknown UserActionType = iota
	// 配置更新
	UserActionConfigUpdate
	// 选举
	UserActionElection
	// 加入
	UserActionJoin
	UserActionJoinResp
	// 认证
	UserActionAuthAdd
	// 收件箱
	UserActionInboundAdd
	UserActionInbound
	// 发件箱
	UserActionOutboundAdd
	UserActionOutboundForward
	UserActionOutboundForwardResp
	// 节点心跳请求 leader --> replica
	UserActionNodeHeartbeatReq
	// 节点心跳返回 replica --> leader
	UserActionNodeHeartbeatResp
	// write
	UserActionWrite
	// 关闭连接
	UserActionConnClose
	// 用户关闭
	UserActionUserClose
)

func (a UserActionType) String() string {
	switch a {
	case UserActionUnknown:
		return "UserActionUnknown"
	case UserActionElection:
		return "UserActionElection"
	case UserActionJoin:
		return "UserActionJoin"
	case UserActionJoinResp:
		return "UserActionJoinResp"
	case UserActionConfigUpdate:
		return "UserActionConfigUpdate"
	case UserActionAuthAdd:
		return "UserActionAuthAdd"
	case UserActionInboundAdd:
		return "UserActionInboundAdd"
	case UserActionInbound:
		return "UserActionInbound"
	case UserActionOutboundForward:
		return "UserActionOutboundForward"
	case UserActionOutboundForwardResp:
		return "UserActionOutboundForwardResp"
	case UserActionNodeHeartbeatReq:
		return "UserActionNodeHeartbeatReq"
	case UserActionNodeHeartbeatResp:
		return "UserActionNodeHeartbeatResp"
	case UserActionConnClose:
		return "UserActionConnClose"
	case UserActionUserClose:
		return "UserActionUserClose"
	case UserActionWrite:
		return "UserActionWrite"
	case UserActionOutboundAdd:
		return "UserActionOutboundAdd"
	default:
		return "UserUnknown"
	}
}

type UserAction struct {
	No          string // 唯一编号
	From        uint64 // 发送节点
	To          uint64 // 接收节点
	Uid         string
	Type        UserActionType
	Messages    UserMessageBatch
	Index       uint64
	LeaderId    uint64
	Cfg         UserConfig
	Conns       []*Conn
	Term        uint32 // 任期
	NodeVersion uint64 // 节点的数据版本
	Success     bool
	Role        Role
	Data        []byte
}

func (a UserAction) Size() uint64 {

	return 0
}
