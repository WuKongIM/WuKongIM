package raft

type Transport interface {
	// Send 发送事件
	Send(event Event)
}
