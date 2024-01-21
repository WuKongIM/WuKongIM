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
	SetTerm(term uint64)
	GetTerm() uint64
}

func EncodeMessage(messageSeq uint32, term uint64, data []byte) []byte {
	p := new(bytes.Buffer)
	_ = binary.Write(p, Encoding, MagicNumber)
	_ = binary.Write(p, Encoding, MessageVersion)
	_ = binary.Write(p, Encoding, uint32(len(data)))
	_ = binary.Write(p, Encoding, uint64(messageSeq)) // offsetSize = 8
	_ = binary.Write(p, Encoding, term)               // term = 8
	_ = binary.Write(p, Encoding, data)
	_ = binary.Write(p, Encoding, EndMagicNumber)
	return p.Bytes()
}

func DecodeMessage(msg []byte) (uint32, uint64, []byte, error) {
	offset := 0
	magicNum := msg[offset : len(MagicNumber)+offset]
	if !bytes.Equal(magicNum, MagicNumber[:]) {
		return 0, 0, nil, fmt.Errorf("start magicNumber不正确 expect:%s actual:%s", string(MagicNumber[:]), string(magicNum))
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

	// term
	term := Encoding.Uint64(msg[offset : offset+TermIndexSize])
	offset += TermIndexSize

	// data
	data := msg[offset : offset+int(dataLen)]
	offset += int(dataLen)

	// end magic
	endMagicNum := msg[offset : len(EndMagicNumber)+offset]
	if !bytes.Equal(endMagicNum, EndMagicNumber[:]) {
		return 0, 0, nil, fmt.Errorf("end magicNumber不正确 expect:%s actual:%s", string(EndMagicNumber[:]), string(endMagicNum))
	}
	return messageSeq, term, data, nil
}
