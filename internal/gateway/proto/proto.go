package proto

import (
	"bytes"
	"errors"
	"hash/crc32"

	"github.com/WuKongIM/WuKongIM/internal/pb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

const (
	crcLen     = 4
	dataLenLen = 4
)

var (
	ErrCRC = errors.New("crc error")
)

var (
	Start = []byte{0x73, 0x77}
	End   = []byte{0x69, 0x78}
)

type Proto struct {
}

func New() *Proto {
	return &Proto{}
}

func (p *Proto) Encode(block *Block) ([]byte, error) {

	if block.Conn == nil {
		return nil, errors.New("conn is nil")
	}

	blockEncoder := wkproto.NewEncoder()
	defer blockEncoder.End()

	blockEncoder.WriteUint64(block.Seq)
	connData, _ := block.Conn.Marshal()
	blockEncoder.WriteBinary(connData)
	blockEncoder.WriteBytes(block.Data)

	data := blockEncoder.Bytes()

	encoder := wkproto.NewEncoder()
	defer encoder.End()
	crc := crc32.ChecksumIEEE(data)
	encoder.WriteBytes(Start)
	encoder.WriteUint32(uint32(len(data) + crcLen + len(End)))
	encoder.WriteUint32(crc)
	encoder.WriteBytes(data)
	encoder.WriteBytes(End)
	return encoder.Bytes(), nil
}

func (p *Proto) Decode(packetData []byte) (*Block, int, error) {

	if len(packetData) <= minLen() {
		return nil, 0, nil
	}

	// ---------- 正确性检查 ----------

	if packetData[0] != Start[0] {
		return nil, 1, nil
	}
	if packetData[1] != Start[1] {
		return nil, len(Start), nil
	}

	dLen := uint32((int32(packetData[2]) << 24) | (int32(packetData[3]) << 16) | (int32(packetData[4]) << 8) | int32(packetData[5]))

	if dLen > uint32(len(packetData)-len(Start)-dataLenLen) { // 数据不够
		return nil, 0, nil
	}
	end := packetData[len(Start)+dataLenLen+int(dLen)-len(End) : len(Start)+dataLenLen+int(dLen)]
	if !bytes.Equal(end, End) {
		return nil, len(Start), nil
	}

	// ---------- 解码 ----------

	decoder := wkproto.NewDecoder(packetData)

	_, err := decoder.Bytes(len(Start))
	if err != nil {
		return nil, 0, err
	}
	packLen, err := decoder.Uint32() // length
	if err != nil {
		return nil, 0, err
	}

	crc, err := decoder.Uint32() // crc
	if err != nil {
		return nil, 0, err
	}

	data, err := decoder.Bytes(int(packLen) - crcLen - len(End))
	if err != nil {
		return nil, 0, err
	}
	if crc != crc32.ChecksumIEEE(data) {
		return nil, 0, ErrCRC
	}

	blockDecoder := wkproto.NewDecoder(data)

	seq, err := blockDecoder.Uint64()
	if err != nil {
		return nil, 0, err
	}
	connData, err := blockDecoder.Binary()
	if err != nil {
		return nil, 0, err
	}
	conn := &pb.Conn{}
	err = conn.Unmarshal(connData)
	if err != nil {
		return nil, 0, err
	}
	result, err := blockDecoder.BinaryAll()
	if err != nil {
		return nil, 0, err
	}

	return &Block{
		Seq:  seq,
		Conn: conn,
		Data: result,
	}, minLen() + len(data), nil
}

func minLen() int {
	return crcLen + dataLenLen + len(Start) + len(End)
}

type Block struct {
	Seq  uint64
	Conn *pb.Conn
	Data []byte
}
