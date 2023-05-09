package netpoll

type PollEvent int

const (
	PollEventUnknown PollEvent = 1 << iota // 未知事件
	PollEventRead                          // 读事件
	PollEventWrite                         // 写事件
	PollEventClose                         // 错误事件
)
