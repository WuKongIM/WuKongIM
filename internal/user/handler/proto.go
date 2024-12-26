package handler

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// 范围 eventbus.UserEventMsgMin ~ options.UserEventMsgMax
type msgType uint32

const (
	// 转发用户事件
	msgForwardUserEvent msgType = 2001
)

type forwardUserEventReq struct {
	fromNode uint64
	uid      string
	events   eventbus.EventBatch
}

func (f *forwardUserEventReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(f.uid)
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

func (f *forwardUserEventReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	if f.uid, err = dec.String(); err != nil {
		return err
	}
	if f.fromNode, err = dec.Uint64(); err != nil {
		return err
	}
	eventData, err := dec.BinaryAll()
	if err != nil {
		fmt.Println("BinaryAll failed", err)
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
