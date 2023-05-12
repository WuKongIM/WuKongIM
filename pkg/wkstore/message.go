package wkstore

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
)

type Message interface {
	SetSeq(seq uint32)
	GetSeq() uint32
	Encode() []byte
	Decode(msg []byte) error
}

type DefaultMessage struct {
	wkproto.RecvPacket
	Version uint8
}

func (d *DefaultMessage) SetSeq(seq uint32) {
	d.MessageSeq = seq
}
func (d *DefaultMessage) GetSeq() uint32 {
	return d.MessageSeq
}

func (d *DefaultMessage) Encode() ([]byte, error) {

	data := MarshalMessage(d)

	p := new(bytes.Buffer)
	binary.Write(p, Encoding, MagicNumber)
	binary.Write(p, Encoding, MessageVersion)
	binary.Write(p, Encoding, uint32(len(data)))
	binary.Write(p, Encoding, d.MessageSeq) // offsetSize = 8
	binary.Write(p, Encoding, int64(0))
	binary.Write(p, Encoding, data)
	binary.Write(p, Encoding, EndMagicNumber)

	return p.Bytes(), nil
}

func EncodeMessage(messageSeq uint32, data []byte) []byte {
	p := new(bytes.Buffer)
	binary.Write(p, Encoding, MagicNumber)
	binary.Write(p, Encoding, MessageVersion)
	binary.Write(p, Encoding, uint32(len(data)))
	binary.Write(p, Encoding, messageSeq) // offsetSize = 8
	binary.Write(p, Encoding, int64(0))
	binary.Write(p, Encoding, data)
	binary.Write(p, Encoding, EndMagicNumber)
	return p.Bytes()
}

func DecodeMessage(msg []byte) (uint32, []byte, error) {
	offset := 0
	magicNum := msg[offset : len(MagicNumber)+offset]
	if !bytes.Equal(magicNum, MagicNumber[:]) {
		return 0, nil, fmt.Errorf("start magicNumber不正确 expect:%s actual:%s", string(MagicNumber[:]), string(magicNum))
	}
	offset += len(MagicNumber)

	// version
	_ = msg[offset]
	offset += len(MessageVersion)

	// dataLen
	dataLen := Encoding.Uint32(msg[offset : MessageDataLenSize+offset])
	offset += MessageDataLenSize

	// seq
	messageSeq := uint32(Encoding.Uint64(msg[offset : offset+OffsetSize]))
	offset += OffsetSize

	// applindex
	_ = Encoding.Uint64(msg[offset : offset+AppliIndexSize])
	offset += AppliIndexSize

	// data
	data := msg[offset : offset+int(dataLen)]
	offset += int(dataLen)

	// end magic
	endMagicNum := msg[offset : len(EndMagicNumber)+offset]
	if !bytes.Equal(endMagicNum, EndMagicNumber[:]) {
		return 0, nil, fmt.Errorf("end magicNumber不正确 expect:%s actual:%s", string(EndMagicNumber[:]), string(endMagicNum))
	}
	return messageSeq, data, nil
}

func (d *DefaultMessage) Decode(msg []byte) error {
	offset := 0
	magicNum := msg[offset : len(MagicNumber)+offset]
	if !bytes.Equal(magicNum, MagicNumber[:]) {
		return fmt.Errorf("start magicNumber不正确 expect:%s actual:%s", string(MagicNumber[:]), string(magicNum))
	}
	offset += len(MagicNumber)

	// version
	_ = msg[offset]
	offset += len(MessageVersion)

	// dataLen
	dataLen := Encoding.Uint32(msg[offset : MessageDataLenSize+offset])
	offset += MessageDataLenSize

	// seq
	d.MessageSeq = uint32(Encoding.Uint64(msg[offset : offset+OffsetSize]))
	offset += OffsetSize

	// applindex
	_ = Encoding.Uint64(msg[offset : offset+AppliIndexSize])
	offset += AppliIndexSize

	// data
	data := msg[offset : offset+int(dataLen)]
	offset += int(dataLen)

	err := UnmarshalMessage(data, d)
	if err != nil {
		return err
	}

	// end magic
	endMagicNum := msg[offset : len(EndMagicNumber)+offset]
	if !bytes.Equal(endMagicNum, EndMagicNumber[:]) {
		return fmt.Errorf("end magicNumber不正确 expect:%s actual:%s", string(EndMagicNumber[:]), string(endMagicNum))
	}
	return nil
}

// MarshalMessage MarshalMessage
func MarshalMessage(m *DefaultMessage) []byte {
	enc := wkproto.NewEncoder()
	enc.WriteByte(wkproto.ToFixHeaderUint8(&m.RecvPacket))
	enc.WriteUint8(m.Version)
	enc.WriteByte(m.Setting.Uint8())
	enc.WriteInt64(m.MessageID)
	enc.WriteUint32(m.MessageSeq)
	enc.WriteString(m.ClientMsgNo)
	enc.WriteInt32(m.Timestamp)
	enc.WriteString(m.FromUID)
	enc.WriteString(m.ChannelID)
	enc.WriteUint8(m.ChannelType)
	enc.WriteBytes(m.Payload)
	return enc.Bytes()
}

// UnmarshalMessage UnmarshalMessage
func UnmarshalMessage(data []byte, m *DefaultMessage) error {
	dec := wkproto.NewDecoder(data)

	// header
	var err error
	var header uint8
	if header, err = dec.Uint8(); err != nil {
		return err
	}
	recvPacket := wkproto.RecvPacket{}
	framer := wkproto.FramerFromUint8(header)
	if m.Version, err = dec.Uint8(); err != nil {
		return err
	}
	recvPacket.Framer = framer

	// setting
	var setting uint8
	if setting, err = dec.Uint8(); err != nil {
		return err
	}
	m.RecvPacket = recvPacket

	m.Setting = wkproto.Setting(setting)

	// messageID
	if m.MessageID, err = dec.Int64(); err != nil {
		return err
	}

	// MessageSeq
	if m.MessageSeq, err = dec.Uint32(); err != nil {
		return err
	}
	// ClientMsgNo
	if m.ClientMsgNo, err = dec.String(); err != nil {
		return err
	}
	// Timestamp
	if m.Timestamp, err = dec.Int32(); err != nil {
		return err
	}

	// FromUID
	if m.FromUID, err = dec.String(); err != nil {
		return err
	}
	// if m.QueueUID, err = dec.String(); err != nil {
	// 	return err
	// }

	// ChannelID
	if m.ChannelID, err = dec.String(); err != nil {
		return err
	}

	// ChannelType
	if m.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}

	// Payload
	if m.Payload, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}
