package key

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
)

// 表结构
// ---------------------
// | tableID  | dataType | primaryKey | columnKey |
// | 2 byte   | 1 byte   | 8 字节 	   | 2 字节		|
// ---------------------

// 数据类型
var (
	dataTypeTable byte = 0x01 // 表
	dataTypeIndex byte = 0x02 // 索引
)

// 消息表
// ---------------------
// | tableID  | dataType	| channel hash | messageSeq   | columnKey |
// | 2 byte   | 1 byte   	| 8 字节 	   	|  8 字节	   | 2 字节		|
// ---------------------

var TableMessage = struct {
	Id     [2]byte
	Size   int
	Column struct {
		Header      [2]byte
		Setting     [2]byte
		Expire      [2]byte
		MessageId   [2]byte
		MessageSeq  [2]byte
		ClientMsgNo [2]byte
		Timestamp   [2]byte
		ChannelId   [2]byte
		ChannelType [2]byte
		Topic       [2]byte
		FromUid     [2]byte
		Payload     [2]byte
		Term        [2]byte
	}
}{
	Id:   [2]byte{0x01, 0x01},
	Size: 2 + 2 + 8 + 8 + 2, // tableId + dataType + channel hash + messageSeq + columnKey
	Column: struct {
		Header      [2]byte
		Setting     [2]byte
		Expire      [2]byte
		MessageId   [2]byte
		MessageSeq  [2]byte
		ClientMsgNo [2]byte
		Timestamp   [2]byte
		ChannelId   [2]byte
		ChannelType [2]byte
		Topic       [2]byte
		FromUid     [2]byte
		Payload     [2]byte
		Term        [2]byte
	}{
		Header:      [2]byte{0x01, 0x01},
		Setting:     [2]byte{0x01, 0x02},
		Expire:      [2]byte{0x01, 0x03},
		MessageId:   [2]byte{0x01, 0x04},
		MessageSeq:  [2]byte{0x01, 0x05},
		ClientMsgNo: [2]byte{0x01, 0x06},
		Timestamp:   [2]byte{0x01, 0x07},
		ChannelId:   [2]byte{0x01, 0x08},
		ChannelType: [2]byte{0x01, 0x09},
		Topic:       [2]byte{0x01, 0x0A},
		FromUid:     [2]byte{0x01, 0x0B},
		Payload:     [2]byte{0x01, 0x0C},
		Term:        [2]byte{0x01, 0x0D},
	},
}

// 表大小
const ()

func NewMessageColumnKey(channelId string, channelType uint8, messageSeq uint64, columnName [2]byte) []byte {
	key := make([]byte, TableMessage.Size)
	channelHash := channelIdToNum(channelId, channelType)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], messageSeq)
	key[20] = columnName[0]
	key[21] = columnName[1]
	return key
}

func NewMessagePrimaryKey(channelId string, channelType uint8, messageSeq uint64) []byte {
	key := make([]byte, 20)
	channelHash := channelIdToNum(channelId, channelType)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], messageSeq)
	return key
}

func ParseMessageColumnKey(key []byte) (messageSeq uint64, columnName [2]byte, err error) {
	if len(key) != TableMessage.Size {
		err = fmt.Errorf("message: invalid key length, keyLen: %d", len(key))
		return
	}
	messageSeq = binary.BigEndian.Uint64(key[12:])
	columnName[0] = key[20]
	columnName[1] = key[21]
	return
}

func channelIdToNum(channelId string, channelType uint8) uint64 {
	h := fnv.New64a()
	h.Write([]byte(ChannelKey(channelId, channelType)))
	return h.Sum64()
}

func ChannelKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%d-%s", channelType, channelID)
}
