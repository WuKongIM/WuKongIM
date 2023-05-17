package wkstore

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

type Message interface {
	GetMessageID() int64
	SetSeq(seq uint32)
	GetSeq() uint32
	Encode() []byte
	Decode(msg []byte) error
}

func EncodeMessage(messageSeq uint32, data []byte) []byte {
	p := new(bytes.Buffer)
	binary.Write(p, Encoding, MagicNumber)
	binary.Write(p, Encoding, MessageVersion)
	binary.Write(p, Encoding, uint32(len(data)))
	binary.Write(p, Encoding, int64(messageSeq)) // offsetSize = 8
	binary.Write(p, Encoding, int64(0))          // appliIndexSize = 8
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
	messageSeq := uint32(Encoding.Uint64(msg[offset : offset+MessageSeqSize]))
	offset += MessageSeqSize

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
