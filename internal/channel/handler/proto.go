package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// 范围 eventbus.ChannelEventMsgMin ~ options.ChannelEventMsgMax
type msgType uint32

const (
	// 转发用户事件
	msgForwardChannelEvent msgType = 3002
	//	 发送消息回执
	msgSendack msgType = 3003
)

func (m msgType) uint32() uint32 {
	return uint32(m)
}

type forwardChannelEventReq struct {
	fromNode    uint64
	channelId   string
	channelType uint8
	events      eventbus.EventBatch
}

func (f *forwardChannelEventReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(f.channelId)
	enc.WriteUint8(f.channelType)
	enc.WriteUint64(f.fromNode)
	if len(f.events) > 0 {
		data, err := f.events.Encode()
		if err != nil {
			return nil, err
		}
		enc.WriteBytes(data)
	}
	return enc.Bytes(), nil
}

func (f *forwardChannelEventReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	if f.channelId, err = dec.String(); err != nil {
		return err
	}
	if f.channelType, err = dec.Uint8(); err != nil {
		return err
	}
	if f.fromNode, err = dec.Uint64(); err != nil {
		return err
	}
	eventData, err := dec.BinaryAll()
	if err != nil {
		return err
	}
	if len(eventData) > 0 {
		err = f.events.Decode(eventData)
		if err != nil {
			return err
		}
	}
	return nil
}

type sendackReq struct {
	framer       uint8
	protoVersion uint8
	messageId    int64
	messageSeq   uint64
	clientSeq    uint64
	clientMsgNo  string
	reasonCode   uint8
	fromUid      string
	FromNode     uint64
	ConnId       int64
}

func (s *sendackReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint8(s.framer)
	enc.WriteUint8(s.protoVersion)
	enc.WriteInt64(s.messageId)
	enc.WriteUint64(s.messageSeq)
	enc.WriteUint64(s.clientSeq)
	enc.WriteString(s.clientMsgNo)
	enc.WriteUint8(s.reasonCode)
	enc.WriteString(s.fromUid)
	enc.WriteUint64(s.FromNode)
	enc.WriteInt64(s.ConnId)
	return enc.Bytes(), nil
}

func (s *sendackReq) decode(data []byte) error {

	dec := wkproto.NewDecoder(data)
	var err error
	if s.framer, err = dec.Uint8(); err != nil {
		return err
	}
	if s.protoVersion, err = dec.Uint8(); err != nil {
		return err
	}
	if s.messageId, err = dec.Int64(); err != nil {
		return err
	}

	if s.messageSeq, err = dec.Uint64(); err != nil {
		return err
	}

	if s.clientSeq, err = dec.Uint64(); err != nil {
		return err
	}
	if s.clientMsgNo, err = dec.String(); err != nil {
		return err
	}
	if s.reasonCode, err = dec.Uint8(); err != nil {
		return err
	}
	if s.fromUid, err = dec.String(); err != nil {
		return err
	}
	if s.FromNode, err = dec.Uint64(); err != nil {
		return err
	}
	if s.ConnId, err = dec.Int64(); err != nil {
		return err
	}
	return nil
}

type sendackBatchReq []*sendackReq

func (s sendackBatchReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(uint16(len(s)))
	for _, v := range s {
		enc.WriteUint8(v.framer)
		enc.WriteUint8(v.protoVersion)
		enc.WriteInt64(v.messageId)
		enc.WriteUint64(v.messageSeq)
		enc.WriteUint64(v.clientSeq)
		enc.WriteString(v.clientMsgNo)
		enc.WriteUint8(v.reasonCode)
		enc.WriteString(v.fromUid)
		enc.WriteUint64(v.FromNode)
		enc.WriteInt64(v.ConnId)
	}
	return enc.Bytes(), nil
}

func (s *sendackBatchReq) decode(data []byte) error {

	dec := wkproto.NewDecoder(data)
	var err error
	var size uint16
	if size, err = dec.Uint16(); err != nil {
		return err
	}
	for i := 0; i < int(size); i++ {
		var v sendackReq
		if v.framer, err = dec.Uint8(); err != nil {
			return err
		}
		if v.protoVersion, err = dec.Uint8(); err != nil {
			return err
		}
		if v.messageId, err = dec.Int64(); err != nil {
			return err
		}

		if v.messageSeq, err = dec.Uint64(); err != nil {
			return err
		}

		if v.clientSeq, err = dec.Uint64(); err != nil {
			return err
		}
		if v.clientMsgNo, err = dec.String(); err != nil {
			return err
		}
		if v.reasonCode, err = dec.Uint8(); err != nil {
			return err
		}
		if v.fromUid, err = dec.String(); err != nil {
			return err
		}
		if v.FromNode, err = dec.Uint64(); err != nil {
			return err
		}
		if v.ConnId, err = dec.Int64(); err != nil {
			return err
		}
		*s = append(*s, &v)
	}
	return nil
}
