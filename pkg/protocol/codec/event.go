package codec

import "github.com/WuKongIM/WuKongIM/pkg/protocol/frame"

func decodeEvent(f frame.Frame, data []byte, _ uint8) (frame.Frame, error) {
	dec := NewDecoder(data)
	eventPacket := &frame.EventPacket{}
	eventPacket.Framer = f.(frame.Framer)
	var err error
	if eventPacket.Id, err = dec.String(); err != nil {
		return nil, err
	}

	if eventPacket.Type, err = dec.String(); err != nil {
		return nil, err
	}
	if eventPacket.Timestamp, err = dec.Int64(); err != nil {
		return nil, err
	}
	if eventPacket.Data, err = dec.BinaryAll(); err != nil {
		return nil, err
	}

	return eventPacket, nil
}

func encodeEvent(eventPacket *frame.EventPacket, enc *Encoder, _ uint8) error {
	enc.WriteString(eventPacket.Id)
	enc.WriteString(eventPacket.Type)
	enc.WriteInt64(eventPacket.Timestamp)
	enc.WriteBytes(eventPacket.Data)
	return nil
}

func encodeEventSize(eventPacket *frame.EventPacket, _ uint8) int {
	size := 0
	size += len(eventPacket.Id) + frame.StringFixLenByteSize
	size += len(eventPacket.Type) + frame.StringFixLenByteSize
	size += frame.BigTimestampByteSize
	size += len(eventPacket.Data)
	return size
}
