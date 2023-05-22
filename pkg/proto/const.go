package proto

// FrameType 包类型
type FrameType uint8

// 包类型
const (
	UNKNOWN    FrameType = iota // 保留位
	CONNECT                     // 客户端请求连接到服务器(c2s)
	CONNACK                     // 服务端收到连接请求后确认的报文(s2c)
	PUB                         // 发送消息(c2s)
	PUBACK                      // 收到消息确认的报文(s2c)
	RECV                        // 收取消息(s2c)
	RECVACK                     // 收取消息确认(c2s)
	PING                        //ping请求
	PONG                        // 对ping请求的相应
	DISCONNECT                  // 请求断开连接
)
