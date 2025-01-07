package types

type EventType uint8

const (
	Unknown EventType = iota

	// RaftEvent raft事件
	RaftEvent
	// ConfigChange 配置变更
	ConfigChange
	// NodeAdd 节点添加
	NodeAdd
)

type Event struct {
	// Type 事件类型
	Type EventType

	Config *Config

	Index uint64
}
