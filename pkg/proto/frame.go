package proto

type Frame interface {
	Decode(d Decoder, remainingLen uint32, protoVersion uint8) error
	Encode(e Encoder) error
}

type Framer struct {
	FrameType       FrameType
	RemainingLength uint32 // 控制报文总长度等于固定报头的长度加上剩余长度
	ProtocolName    string // 协议名称
	ProtocolLevel   int    // 协议版本号
	FrameSize       int64  // frame的总大小
}
