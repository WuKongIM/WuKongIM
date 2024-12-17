package process

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type msgType uint32

// 范围 options.ReactorDiffuseMsgTypeMin ~ options.ReactorDiffuseMsgTypeMax
const (
	// 消息发件箱请求
	msgOutboundReq msgType = 4001
)

func (m msgType) uint32() uint32 {
	return uint32(m)
}

func (m msgType) String() string {
	switch m {
	case msgOutboundReq:
		return "msgOutboundReq"
	default:
		return fmt.Sprintf("unknown diffuse msgType %d", m)
	}
}

type outboundReq struct {
	fromNode uint64
	messages reactor.ChannelMessageBatch
}

func (o *outboundReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteUint64(o.fromNode)
	data, err := o.messages.Encode()
	if err != nil {
		return nil, err
	}
	enc.WriteBytes(data)
	return enc.Bytes(), nil
}

func (o *outboundReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if o.fromNode, err = dec.Uint64(); err != nil {
		return err
	}
	msgData, err := dec.BinaryAll()
	if err != nil {
		return err
	}
	return o.messages.Decode(msgData)
}
