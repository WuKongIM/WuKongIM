package proto

import (
	"errors"
	"hash/crc32"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

const (
	crcLen     = 4
	dataLenLen = 4
)

var (
	ErrCRC = errors.New("crc error")
)

type Proto struct {
}

func New() *Proto {
	return &Proto{}
}

func (p *Proto) Encode(block *Block) ([]byte, error) {

	blockEncoder := wkproto.NewEncoder()
	defer blockEncoder.End()

	blockEncoder.WriteUint64(block.Seq)
	blockEncoder.WriteInt64(block.ConnID)
	blockEncoder.WriteString(block.UID)
	blockEncoder.WriteUint8(block.DeviceFlag.ToUint8())
	blockEncoder.WriteBytes(block.Data)

	data := blockEncoder.Bytes()

	encoder := wkproto.NewEncoder()
	defer encoder.End()
	crc := crc32.ChecksumIEEE(data)
	encoder.WriteUint32(crc)
	encoder.WriteUint32(uint32(len(data)))
	encoder.WriteBytes(data)
	return encoder.Bytes(), nil
}

func (p *Proto) Decode(packetData []byte) (*Block, int, error) {

	if len(packetData) <= minLen() {
		return nil, 0, nil
	}

	decoder := wkproto.NewDecoder(packetData)

	crc, err := decoder.Uint32() // crc
	if err != nil {
		return nil, 0, err
	}
	dataLen, err := decoder.Uint32() // length
	if err != nil {
		return nil, 0, err
	}
	if dataLen > uint32(len(packetData)-minLen()) {
		return nil, 0, nil
	}
	data, err := decoder.Bytes(int(dataLen))
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
	connID, err := blockDecoder.Int64()
	if err != nil {
		return nil, 0, err
	}
	uid, err := blockDecoder.String()
	if err != nil {
		return nil, 0, err
	}
	deviceFlag, err := blockDecoder.Uint8()
	if err != nil {
		return nil, 0, err
	}
	result, err := blockDecoder.BinaryAll()
	if err != nil {
		return nil, 0, err
	}

	return &Block{
		Seq:        seq,
		ConnID:     connID,
		UID:        uid,
		DeviceFlag: wkproto.DeviceFlag(deviceFlag),
		Data:       result,
	}, minLen() + len(data), nil
}

func minLen() int {
	return crcLen + dataLenLen
}

type Block struct {
	Seq        uint64
	ConnID     int64
	UID        string
	DeviceFlag wkproto.DeviceFlag
	Data       []byte
}
