package wkdb

import wkproto "github.com/WuKongIM/WuKongIMGoProto"

var (
	proto = wkproto.New()
)

type Message struct {
	wkproto.RecvPacket
	Term uint64 // raft term
}

func (m *Message) Unmarshal(data []byte) error {
	f, size, err := proto.DecodeFrame(data, wkproto.LatestVersion)
	if err != nil {
		return err
	}
	rcv := f.(*wkproto.RecvPacket)
	m.RecvPacket = *rcv

	dec := wkproto.NewDecoder(data[size:])
	if m.Term, err = dec.Uint64(); err != nil {
		return err
	}

	return nil
}

func (m *Message) Marshal() ([]byte, error) {
	data, err := proto.EncodeFrame(&m.RecvPacket, wkproto.LatestVersion)
	if err != nil {
		return nil, err
	}

	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteBytes(data)
	enc.WriteUint64(m.Term)
	return enc.Bytes(), nil
}
