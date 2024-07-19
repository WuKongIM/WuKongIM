package gossip

import "fmt"

type ServerRole uint8

const (
	// 未知状态
	ServerRoleUnknown ServerRole = iota
	// 从节点
	ServerRoleSlave
	// 主节点
	ServerRoleMaster
)

type NodeStateType int

func (t NodeStateType) metricsString() string {
	switch t {
	case StateAlive:
		return "alive"
	case StateDead:
		return "dead"
	case StateSuspect:
		return "suspect"
	case StateLeft:
		return "left"
	default:
		return fmt.Sprintf("unhandled-value-%d", t)
	}
}

const (
	StateAlive NodeStateType = iota
	StateSuspect
	StateDead
	StateLeft
)
