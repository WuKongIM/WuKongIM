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
)

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
