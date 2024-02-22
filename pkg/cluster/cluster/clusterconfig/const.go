package clusterconfig

import "errors"

type EventType int

const (
	EventUnkown        EventType = iota
	EventPropose                 // 提议
	EventHup                     // 开始选举
	EventVote                    // 投票请求
	EventVoteResp                // 投票响应
	EventBeat                    // 领导节点beat
	EventHeartbeat               // 心跳请求
	EventHeartbeatResp           // 心跳响应
	// EventNotifySync              // 通知同步
	EventSync      // 同步请求
	EventSyncResp  // 同步响应
	EventApply     // 应用配置请求
	EventApplyResp // 应用配置响应
)

func (e EventType) String() string {
	switch e {
	case EventPropose:
		return "EventPropose"
	case EventHup:
		return "EventHup"
	case EventVote:
		return "EventVote"
	case EventVoteResp:
		return "EventVoteResp"
	case EventBeat:
		return "EventBeat"
	case EventHeartbeat:
		return "EventHeartbeat"
	case EventHeartbeatResp:
		return "EventHeartbeatResp"
	// case EventNotifySync:
	// return "EventNotifySync"
	case EventSync:
		return "EventSync"
	case EventSyncResp:
		return "EventSyncResp"
	case EventApply:
		return "EventApply"
	case EventApplyResp:
		return "EventApplyResp"
	default:
		return "EventUnkown"
	}
}

const (
	None uint64 = 0
)

type RoleType int

const (
	RoleFollower RoleType = iota
	RoleCandidate
	RoleLeader
	numStates
)

var (
	ErrStopped      = errors.New("clusterconfig: stopped")
	ErrNotLeader    = errors.New("clusterconfig: not leader")
	ErrSlotNotFound = errors.New("clusterconfig: slot not found")
)
