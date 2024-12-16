package process

import (
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type allowSendReq struct {
	From string // 发送者
	To   string // 接收者
}

func (a *allowSendReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if a.From, err = dec.String(); err != nil {
		return err
	}
	if a.To, err = dec.String(); err != nil {
		return err
	}
	return nil
}

func (a *allowSendReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(a.From)
	enc.WriteString(a.To)
	return enc.Bytes(), nil
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
