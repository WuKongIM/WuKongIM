package process

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type msgType uint32

// 范围 options.ReactorPushMsgTypeMin ~ options.ReactorPushMsgTypeMax
const (
	// 消息发件箱请求
	msgOutboundReq msgType = 5001
)

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

type tagReq struct {
	tagKey string
	nodeId uint64
}

func (t *tagReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(t.tagKey)
	enc.WriteUint64(t.nodeId)
	return enc.Bytes(), nil
}

func (t *tagReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if t.tagKey, err = dec.String(); err != nil {
		return err
	}
	if t.nodeId, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type tagResp struct {
	tagKey string
	uids   []string
}

func (t *tagResp) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(t.tagKey)
	enc.WriteUint32(uint32(len(t.uids)))
	for _, uid := range t.uids {
		enc.WriteString(uid)
	}
	return enc.Bytes(), nil
}

func (t *tagResp) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	tagKey, err := dec.String()
	if err != nil {
		return err
	}
	t.tagKey = tagKey
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	t.uids = make([]string, 0, count)
	for i := 0; i < int(count); i++ {
		var uid string
		if uid, err = dec.String(); err != nil {
			return err
		}
		t.uids = append(t.uids, uid)
	}
	return nil
}
