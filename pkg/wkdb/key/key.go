package key

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
)

var MinColumnKey = [2]byte{0x00, 0x00}
var MaxColumnKey = [2]byte{0xff, 0xff}

// ---------------------- Message ----------------------
func NewMessageColumnKey(channelId string, channelType uint8, messageSeq uint64, columnName [2]byte) []byte {
	key := make([]byte, TableMessage.Size)
	channelHash := channelToNum(channelId, channelType)
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

func NewMessageColumnKeyWithPrimary(primary [16]byte, columnName [2]byte) []byte {
	key := make([]byte, TableMessage.Size)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	copy(key[4:], primary[:])
	key[20] = columnName[0]
	key[21] = columnName[1]
	return key
}

func NewMessagePrimaryKey(channelId string, channelType uint8, messageSeq uint64) []byte {
	key := make([]byte, 20)
	channelHash := channelToNum(channelId, channelType)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], messageSeq)
	return key
}

func NewMessageLowKey() []byte {
	key := make([]byte, 12)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], 0)
	binary.BigEndian.PutUint64(key[12:], 0)
	return key
}

func NewMessageHighKey() []byte {
	key := make([]byte, 12)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], math.MaxUint64)
	binary.BigEndian.PutUint64(key[12:], math.MaxUint64)
	return key

}

func NewMessageSearchLowKeWith(channelId string, channelType uint8, messageSeq uint64) []byte {
	key := make([]byte, 20)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	if strings.TrimSpace(channelId) != "" && channelType != 0 {
		channelHash := channelToNum(channelId, channelType)
		binary.BigEndian.PutUint64(key[4:], channelHash)
	} else {
		binary.BigEndian.PutUint64(key[4:], 0)
	}
	binary.BigEndian.PutUint64(key[12:], messageSeq)
	return key
}

func NewMessageSearchHighKeWith(channelId string, channelType uint8, messageSeq uint64) []byte {
	key := make([]byte, 20)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	if strings.TrimSpace(channelId) != "" && channelType != 0 {
		channelHash := channelToNum(channelId, channelType)
		binary.BigEndian.PutUint64(key[4:], channelHash)
	} else {
		binary.BigEndian.PutUint64(key[4:], math.MaxUint64)
	}
	binary.BigEndian.PutUint64(key[12:], messageSeq)
	return key
}

func NewChannelLastMessageSeqKey(channelId string, channelType uint8) []byte {
	key := make([]byte, 12)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeOther
	key[3] = 0
	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[4:], channelHash)
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

func channelToNum(channelId string, channelType uint8) uint64 {
	h := fnv.New64a()
	h.Write([]byte(ChannelKey(channelId, channelType)))
	return h.Sum64()
}

func ChannelToNum(channelId string, channelType uint8) uint64 {
	return channelToNum(channelId, channelType)
}

func ChannelKey(channelId string, channelType uint8) string {
	var b strings.Builder
	b.WriteString(channelId)
	b.WriteByte('-')
	b.WriteString(strconv.FormatInt(int64(channelType), 10))
	return b.String()
}

func HashWithString(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

func NewMessageIndexMessageIdKey(messageId uint64) []byte {
	key := make([]byte, TableMessage.IndexSize)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableMessage.Index.MessageId[0]
	key[5] = TableMessage.Index.MessageId[1]
	binary.BigEndian.PutUint64(key[6:], messageId)
	return key

}

func NewMessageSecondIndexFromUidKey(uid string, primaryKey [16]byte) []byte {
	key := make([]byte, TableMessage.SecondIndexSize)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	key[4] = TableMessage.SecondIndex.FromUid[0]
	key[5] = TableMessage.SecondIndex.FromUid[1]
	binary.BigEndian.PutUint64(key[6:], HashWithString(uid))
	copy(key[14:], primaryKey[:])
	return key
}

func NewMessageSecondIndexClientMsgNoKey(clientMsgNo string, primaryKey [16]byte) []byte {
	key := make([]byte, TableMessage.SecondIndexSize)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	key[4] = TableMessage.SecondIndex.ClientMsgNo[0]
	key[5] = TableMessage.SecondIndex.ClientMsgNo[1]
	binary.BigEndian.PutUint64(key[6:], HashWithString(clientMsgNo))
	copy(key[14:], primaryKey[:])
	return key

}

func NewMessageIndexTimestampKey(timestamp uint64, primaryKey [16]byte) []byte {
	key := make([]byte, TableMessage.SecondIndexSize)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableMessage.SecondIndex.Timestamp[0]
	key[5] = TableMessage.SecondIndex.Timestamp[1]
	binary.BigEndian.PutUint64(key[6:], timestamp)
	copy(key[14:], primaryKey[:])
	return key

}

func ParseMessageSecondIndexKey(key []byte) (primaryKey [16]byte, err error) {
	if len(key) != TableMessage.SecondIndexSize {
		return [16]byte{}, fmt.Errorf("message: invalid index key length, keyLen: %d", len(key))
	}
	copy(primaryKey[:], key[14:])
	return
}

// ---------------------- User ----------------------
func NewUserColumnKey(id uint64, columnName [2]byte) []byte {
	key := make([]byte, TableUser.Size)
	key[0] = TableUser.Id[0]
	key[1] = TableUser.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], id)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

// NewUserIndexUidKey 创建一个uid唯一索引key
func NewUserIndexKey(indexName [2]byte, columnValue uint64) []byte {
	key := make([]byte, TableUser.IndexSize)
	key[0] = TableUser.Id[0]
	key[1] = TableUser.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]

	binary.BigEndian.PutUint64(key[6:], columnValue)
	return key
}

func NewUserSecondIndexKey(indexName [2]byte, columnValue uint64, id uint64) []byte {
	key := make([]byte, TableUser.SecondIndexSize)
	key[0] = TableUser.Id[0]
	key[1] = TableUser.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]
	binary.BigEndian.PutUint64(key[6:], columnValue)
	binary.BigEndian.PutUint64(key[14:], id)
	return key
}

func ParseUserSecondIndexKey(key []byte) (columnValue uint64, id uint64, err error) {
	if len(key) != TableUser.SecondIndexSize {
		err = fmt.Errorf("user: second index invalid key length, keyLen: %d", len(key))
		return
	}
	columnValue = binary.BigEndian.Uint64(key[6:])
	id = binary.BigEndian.Uint64(key[14:])
	return
}

func ParseUserColumnKey(key []byte) (id uint64, columnName [2]byte, err error) {
	if len(key) != TableUser.Size {
		err = fmt.Errorf("user: invalid key length, keyLen: %d", len(key))
		return
	}
	id = binary.BigEndian.Uint64(key[4:])
	columnName[0] = key[12]
	columnName[1] = key[13]
	return
}

// ---------------------- Device ----------------------

func NewDeviceColumnKey(id uint64, columnName [2]byte) []byte {
	key := make([]byte, TableDevice.Size)
	key[0] = TableDevice.Id[0]
	key[1] = TableDevice.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], id)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key

}

func NewDeviceIndexKey(indexName [2]byte, columnValue uint64) []byte {
	key := make([]byte, TableDevice.IndexSize)
	key[0] = TableDevice.Id[0]
	key[1] = TableDevice.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]
	binary.BigEndian.PutUint64(key[6:], columnValue)
	return key
}

func NewDeviceSecondIndexKey(indexName [2]byte, columnValue uint64, id uint64) []byte {
	key := make([]byte, TableDevice.SecondIndexSize)
	key[0] = TableDevice.Id[0]
	key[1] = TableDevice.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]
	binary.BigEndian.PutUint64(key[6:], columnValue)
	binary.BigEndian.PutUint64(key[14:], id)

	return key
}

func ParseDeviceColumnKey(key []byte) (id uint64, columnName [2]byte, err error) {
	if len(key) != TableDevice.Size {
		err = fmt.Errorf("device: invalid key length, keyLen: %d", len(key))
		return
	}
	id = binary.BigEndian.Uint64(key[4:])
	columnName[0] = key[12]
	columnName[1] = key[13]
	return
}

func ParseDeviceSecondIndexKey(key []byte) (columnValue uint64, id uint64, err error) {
	if len(key) != TableDevice.SecondIndexSize {
		err = fmt.Errorf("device: second index invalid key length, keyLen: %d", len(key))
		return
	}
	columnValue = binary.BigEndian.Uint64(key[6:])
	id = binary.BigEndian.Uint64(key[14:])
	return
}

// ---------------------- Subscriber ----------------------

func NewSubscriberColumnKey(channelId string, channelType uint8, id uint64, columnName [2]byte) []byte {
	key := make([]byte, TableSubscriber.Size)
	channelHash := channelToNum(channelId, channelType)
	key[0] = TableSubscriber.Id[0]
	key[1] = TableSubscriber.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], id)
	key[20] = columnName[0]
	key[21] = columnName[1]
	return key
}

// NewSubscriberIndexKey 创建一个唯一索引的 key
func NewSubscriberIndexKey(channelId string, channelType uint8, indexName [2]byte, primaryKey uint64) []byte {
	key := make([]byte, TableSubscriber.IndexSize)
	key[0] = TableSubscriber.Id[0]
	key[1] = TableSubscriber.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[0]

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], primaryKey)
	return key
}

func NewSubscriberSecondIndexKey(channelId string, channelType uint8, indexName [2]byte, columnValue uint64, id uint64) []byte {
	key := make([]byte, TableSubscriber.SecondIndexSize)
	key[0] = TableSubscriber.Id[0]
	key[1] = TableSubscriber.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], columnValue)
	binary.BigEndian.PutUint64(key[22:], id)
	return key
}

func ParseSubscriberSecondIndexKey(key []byte) (columnValue uint64, id uint64, err error) {
	if len(key) != TableSubscriber.SecondIndexSize {
		err = fmt.Errorf("subscriber: second index invalid key length, keyLen: %d", len(key))
		return
	}
	columnValue = binary.BigEndian.Uint64(key[14:])
	id = binary.BigEndian.Uint64(key[22:])
	return
}

func ParseSubscriberColumnKey(key []byte) (id uint64, columnName [2]byte, err error) {
	if len(key) != TableSubscriber.Size {
		err = fmt.Errorf("subscriber: invalid key length, keyLen: %d", len(key))
		return
	}
	id = binary.BigEndian.Uint64(key[12:])
	columnName[0] = key[20]
	columnName[1] = key[21]
	return
}

// ---------------------- Subscriber Channel Relation ----------------------

func NewSubscriberChannelRelationColumnKey(id uint64, columnName [2]byte) []byte {
	key := make([]byte, TableSubscriberChannelRelation.Size)
	key[0] = TableSubscriberChannelRelation.Id[0]
	key[1] = TableSubscriberChannelRelation.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], id)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

// ---------------------- ChannelInfo ----------------------

func NewChannelInfoColumnKey(id uint64, columnName [2]byte) []byte {
	key := make([]byte, TableChannelInfo.Size)
	key[0] = TableChannelInfo.Id[0]
	key[1] = TableChannelInfo.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], id)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

// NewChannelInfoIndexKey 创建一个channelId,channelType 的
func NewChannelInfoIndexKey(indexName [2]byte, columnValue uint64) []byte {
	key := make([]byte, TableChannelInfo.IndexSize)
	key[0] = TableChannelInfo.Id[0]
	key[1] = TableChannelInfo.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]
	binary.BigEndian.PutUint64(key[6:], columnValue)
	return key
}

func NewChannelInfoSecondIndexKey(indexName [2]byte, columnValue uint64, id uint64) []byte {
	key := make([]byte, TableChannelInfo.SecondIndexSize)
	key[0] = TableChannelInfo.Id[0]
	key[1] = TableChannelInfo.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]
	binary.BigEndian.PutUint64(key[6:], columnValue)
	binary.BigEndian.PutUint64(key[14:], id)

	return key
}

func ParseChannelInfoSecondIndexKey(key []byte) (columnValue uint64, id uint64, err error) {
	if len(key) != TableChannelInfo.SecondIndexSize {
		err = fmt.Errorf("channelInfo: second index invalid key length, keyLen: %d", len(key))
		return
	}
	columnValue = binary.BigEndian.Uint64(key[6:])
	id = binary.BigEndian.Uint64(key[14:])
	return

}

func ParseChannelInfoColumnKey(key []byte) (id uint64, columnName [2]byte, err error) {
	if len(key) != TableChannelInfo.Size {
		err = fmt.Errorf("channelInfo: invalid key length, keyLen: %d", len(key))
		return
	}
	id = binary.BigEndian.Uint64(key[4:])
	columnName[0] = key[12]
	columnName[1] = key[13]
	return
}

// ---------------------- Denylist ----------------------

func NewDenylistColumnKey(channelId string, channelType uint8, id uint64, columnName [2]byte) []byte {
	key := make([]byte, TableDenylist.Size)
	channelHash := channelToNum(channelId, channelType)
	key[0] = TableDenylist.Id[0]
	key[1] = TableDenylist.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], id)
	key[20] = columnName[0]
	key[21] = columnName[1]
	return key
}

func NewDenylistPrimaryKey(channelId string, channelType uint8, id uint64) []byte {
	key := make([]byte, 20)

	key[0] = TableDenylist.Id[0]
	key[1] = TableDenylist.Id[1]
	key[2] = dataTypeTable
	key[3] = 0

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], id)
	return key
}

func NewDenylistIndexKey(channelId string, channelType uint8, indexName [2]byte, primaryKey uint64) []byte {
	key := make([]byte, TableDenylist.IndexSize)
	key[0] = TableDenylist.Id[0]
	key[1] = TableDenylist.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], primaryKey)
	return key
}

func NewDenylistSecondIndexKey(channelId string, channelType uint8, indexName [2]byte, columnValue uint64, id uint64) []byte {
	key := make([]byte, TableDenylist.SecondIndexSize)
	key[0] = TableDenylist.Id[0]
	key[1] = TableDenylist.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], columnValue)
	binary.BigEndian.PutUint64(key[22:], id)

	return key
}

func ParseDenylistColumnKey(key []byte) (id uint64, columnName [2]byte, err error) {
	if len(key) != TableDenylist.Size {
		err = fmt.Errorf("denylist: invalid key length, keyLen: %d", len(key))
		return
	}
	id = binary.BigEndian.Uint64(key[12:])
	columnName[0] = key[20]
	columnName[1] = key[21]
	return
}

// ---------------------- Allowlist ----------------------

func NewAllowlistColumnKey(channelId string, channelType uint8, id uint64, columnName [2]byte) []byte {
	key := make([]byte, TableDenylist.Size)
	channelHash := channelToNum(channelId, channelType)
	key[0] = TableAllowlist.Id[0]
	key[1] = TableAllowlist.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], id)
	key[20] = columnName[0]
	key[21] = columnName[1]
	return key
}

func NewAllowlistPrimaryKey(channelId string, channelType uint8, id uint64) []byte {
	key := make([]byte, 20)

	key[0] = TableAllowlist.Id[0]
	key[1] = TableAllowlist.Id[1]
	key[2] = dataTypeTable
	key[3] = 0

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], id)
	return key
}

// NewAllowlistIndexKey 创建一个唯一索引的 key
func NewAllowlistIndexKey(channelId string, channelType uint8, indexName [2]byte, primaryKey uint64) []byte {
	key := make([]byte, TableAllowlist.IndexSize)
	key[0] = TableAllowlist.Id[0]
	key[1] = TableAllowlist.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[0]

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], primaryKey)
	return key
}

func NewAllowlistSecondIndexKey(channelId string, channelType uint8, indexName [2]byte, columnValue uint64, id uint64) []byte {
	key := make([]byte, TableAllowlist.SecondIndexSize)
	key[0] = TableAllowlist.Id[0]
	key[1] = TableAllowlist.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], columnValue)
	binary.BigEndian.PutUint64(key[22:], id)

	return key
}

// NewAllowlistIndexUidLowKey 创建一个uid唯一索引的low key
func NewAllowlistIndexUidLowKey(channelId string, channelType uint8) []byte {
	key := make([]byte, TableAllowlist.IndexSize)
	key[0] = TableAllowlist.Id[0]
	key[1] = TableAllowlist.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableAllowlist.Index.Uid[0]
	key[5] = TableAllowlist.Index.Uid[1]

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], 0)
	return key
}

// NewAllowlistIndexUidHighKey 创建一个uid唯一索引的high key
func NewAllowlistIndexUidHighKey(channelId string, channelType uint8) []byte {
	key := make([]byte, TableAllowlist.IndexSize)
	key[0] = TableAllowlist.Id[0]
	key[1] = TableAllowlist.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableAllowlist.Index.Uid[0]
	key[5] = TableAllowlist.Index.Uid[1]

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], math.MaxUint64)
	return key

}

func ParseAllowlistColumnKey(key []byte) (id uint64, columnName [2]byte, err error) {
	if len(key) != TableAllowlist.Size {
		err = fmt.Errorf("denylist: invalid key length, keyLen: %d", len(key))
		return
	}
	id = binary.BigEndian.Uint64(key[12:])
	columnName[0] = key[20]
	columnName[1] = key[21]
	return
}

// ---------------------- Conversation ----------------------

func NewConversationColumnKey(uid string, primaryKey uint64, columnName [2]byte) []byte {
	key := make([]byte, TableConversation.Size)
	key[0] = TableConversation.Id[0]
	key[1] = TableConversation.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], HashWithString(uid))
	binary.BigEndian.PutUint64(key[12:], primaryKey)
	key[20] = columnName[0]
	key[21] = columnName[1]
	return key
}

func NewConversationPrimaryKey(uid string, primaryKey uint64) []byte {
	key := make([]byte, 20)
	key[0] = TableConversation.Id[0]
	key[1] = TableConversation.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], HashWithString(uid))
	binary.BigEndian.PutUint64(key[12:], primaryKey)
	return key
}

func NewConversationUidHashKey(uidHash uint64) []byte {
	key := make([]byte, 12)
	key[0] = TableConversation.Id[0]
	key[1] = TableConversation.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], uidHash)
	return key
}

// NewConversationIndexChannelKey 创建一个channel唯一索引的 key
func NewConversationIndexChannelKey(uid string, channelId string, channelType uint8) []byte {
	key := make([]byte, TableConversation.IndexSize)
	key[0] = TableConversation.Id[0]
	key[1] = TableConversation.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0

	key[4] = TableConversation.Index.Channel[0]
	key[5] = TableConversation.Index.Channel[1]

	binary.BigEndian.PutUint64(key[6:], HashWithString(uid))

	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[14:], channelHash)

	return key
}

func NewConversationSecondIndexKey(uid string, indexName [2]byte, indexValue uint64, primaryKey uint64) []byte {
	key := make([]byte, TableConversation.SecondIndexSize)
	key[0] = TableConversation.Id[0]
	key[1] = TableConversation.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], HashWithString(uid))
	key[12] = indexName[0]
	key[13] = indexName[1]
	binary.BigEndian.PutUint64(key[14:], indexValue)
	binary.BigEndian.PutUint64(key[22:], primaryKey)

	return key
}

func ParseConversationSecondIndexKey(key []byte) (primaryKey uint64, columnName [2]byte, columnValue uint64, err error) {
	if len(key) != TableConversation.SecondIndexSize {
		err = fmt.Errorf("conversation: second index invalid key length, keyLen: %d", len(key))
		return
	}
	columnName[0] = key[12]
	columnName[1] = key[13]
	columnValue = binary.BigEndian.Uint64(key[14:])
	primaryKey = binary.BigEndian.Uint64(key[22:])

	return
}

func ParseConversationColumnKey(key []byte) (primaryKey uint64, columnName [2]byte, err error) {
	if len(key) != TableConversation.Size {
		err = fmt.Errorf("conversation: invalid key length, keyLen: %d", len(key))
		return
	}
	primaryKey = binary.BigEndian.Uint64(key[12:])
	columnName[0] = key[20]
	columnName[1] = key[21]
	return
}

// ---------------------- MessageNotifyQueue ----------------------

func NewMessageNotifyQueueKey(messageId uint64) []byte {
	key := make([]byte, TableMessageNotifyQueue.Size)
	key[0] = TableMessageNotifyQueue.Id[0]
	key[1] = TableMessageNotifyQueue.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], messageId)
	return key
}

// ---------------------- ChannelClusterConfig ----------------------

func NewChannelClusterConfigColumnKey(primaryKey uint64, columnName [2]byte) []byte {
	key := make([]byte, TableChannelClusterConfig.Size)
	key[0] = TableChannelClusterConfig.Id[0]
	key[1] = TableChannelClusterConfig.Id[1]
	key[2] = dataTypeTable
	key[3] = 0

	binary.BigEndian.PutUint64(key[4:], primaryKey)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

// NewChannelClusterConfigIndexKey 创建一个channelId,channelType 的索引
func NewChannelClusterConfigIndexKey(channelId string, channelType uint8) []byte {
	key := make([]byte, TableChannelInfo.IndexSize)
	key[0] = TableChannelClusterConfig.Id[0]
	key[1] = TableChannelClusterConfig.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableChannelClusterConfig.Index.Channel[0]
	key[5] = TableChannelClusterConfig.Index.Channel[1]
	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	return key
}

func NewChannelClusterConfigSecondIndexKey(indexName [2]byte, columnValue uint64, primaryKey uint64) []byte {
	key := make([]byte, TableChannelClusterConfig.SecondIndexSize)
	key[0] = TableChannelClusterConfig.Id[0]
	key[1] = TableChannelClusterConfig.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]
	binary.BigEndian.PutUint64(key[6:], columnValue)
	binary.BigEndian.PutUint64(key[14:], primaryKey)

	return key

}

func ParseChannelClusterConfigColumnKey(key []byte) (primaryKey uint64, columnName [2]byte, err error) {
	if len(key) != TableChannelClusterConfig.Size {
		err = fmt.Errorf("channelClusterConfig: invalid key length, keyLen: %d", len(key))
		return
	}
	primaryKey = binary.BigEndian.Uint64(key[4:])
	columnName[0] = key[12]
	columnName[1] = key[13]
	return
}

func ParseChannelClusterConfigSecondIndexKey(key []byte) (columnValue uint64, id uint64, err error) {
	if len(key) != TableChannelClusterConfig.SecondIndexSize {
		err = fmt.Errorf("channelClusterConfig: second index invalid key length, keyLen: %d", len(key))
		return
	}
	columnValue = binary.BigEndian.Uint64(key[6:])
	id = binary.BigEndian.Uint64(key[14:])
	return

}

// ---------------------- LeaderTermSequence ----------------------

func NewLeaderTermSequenceTermKey(shardNo string, term uint32) []byte {
	key := make([]byte, TableLeaderTermSequence.Size)
	key[0] = TableLeaderTermSequence.Id[0]
	key[1] = TableLeaderTermSequence.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], HashWithString(shardNo))
	binary.BigEndian.PutUint32(key[12:], term)
	return key
}

func ParseLeaderTermSequenceTermKey(key []byte) (term uint32, err error) {
	if len(key) != TableLeaderTermSequence.Size {
		err = fmt.Errorf("leaderTermSequence: invalid key length, keyLen: %d", len(key))
		return
	}
	term = binary.BigEndian.Uint32(key[12:])
	return
}

// ---------------------- ChannelCommon ----------------------

func NewChannelCommonColumnKey(channelId string, channelType uint8, columnName [2]byte) []byte {
	key := make([]byte, TableChannelCommon.Size)
	channelHash := channelToNum(channelId, channelType)
	key[0] = TableChannelCommon.Id[0]
	key[1] = TableChannelCommon.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], channelHash)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

// ---------------------- total ----------------------

func NewTotalColumnKey(columnName [2]byte) []byte {
	key := make([]byte, TableTotal.Size)
	key[0] = TableTotal.Id[0]
	key[1] = TableTotal.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], 0)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

// ---------------------- system uid ----------------------

func NewSystemUidColumnKey(id uint64, columnName [2]byte) []byte {
	key := make([]byte, TableSystemUid.Size)
	key[0] = TableSystemUid.Id[0]
	key[1] = TableSystemUid.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], id)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

// ---------------------- stream ----------------------

func NewStreamIndexKey(streamNo string, seq uint64) []byte {
	key := make([]byte, TableStream.IndexSize)
	key[0] = TableStream.Id[0]
	key[1] = TableStream.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableStream.Index.StreamNo[0]
	key[5] = TableStream.Index.StreamNo[1]
	binary.BigEndian.PutUint64(key[6:], HashWithString(streamNo))
	binary.BigEndian.PutUint64(key[14:], seq)
	return key
}

func NewStreamMetaKey(streamNo string) []byte {
	key := make([]byte, 12)
	key[0] = TableStreamMeta.Id[0]
	key[1] = TableStreamMeta.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], HashWithString(streamNo))
	return key
}

// ---------------------- ConversationLocalUser ----------------------

func NewConversationLocalUserKey(channelId string, channelType uint8, uid string) []byte {

	uidBytes := []byte(uid)

	key := make([]byte, 12+len(uidBytes))
	key[0] = TableConversationLocalUser.Id[0]
	key[1] = TableConversationLocalUser.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[4:], channelHash)
	copy(key[12:], uidBytes)

	return key
}

func NewConversationLocalUserLowKey(channelId string, channelType uint8) []byte {
	key := make([]byte, 13)
	key[0] = TableConversationLocalUser.Id[0]
	key[1] = TableConversationLocalUser.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[4:], channelHash)
	key[12] = 0x00
	return key
}

func NewConversationLocalUserHighKey(channelId string, channelType uint8) []byte {
	key := make([]byte, 13)
	key[0] = TableConversationLocalUser.Id[0]
	key[1] = TableConversationLocalUser.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	channelHash := channelToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[4:], channelHash)
	key[12] = 0xff
	return key
}

func ParseConversationLocalUserKey(key []byte) (uid string, err error) {
	if len(key) < 12 {
		err = fmt.Errorf("conversationLocalUser: invalid key length, keyLen: %d", len(key))
		return
	}

	uid = string(key[12:])
	return uid, nil
}

// ---------------------- Tester ----------------------

func NewTesterColumnKey(id uint64, columnName [2]byte) []byte {
	key := make([]byte, TableTester.Size)
	key[0] = TableTester.Id[0]
	key[1] = TableTester.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], id)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

func ParseTesterColumnKey(key []byte) (id uint64, columnName [2]byte, err error) {
	if len(key) != TableTester.Size {
		err = fmt.Errorf("tester: invalid key length, keyLen: %d", len(key))
		return
	}
	id = binary.BigEndian.Uint64(key[4:])
	columnName[0] = key[12]
	columnName[1] = key[13]
	return
}

// ---------------------- Plugin ----------------------

func NewPluginColumnKey(id uint64, columnName [2]byte) []byte {
	key := make([]byte, TablePlugin.Size)
	key[0] = TablePlugin.Id[0]
	key[1] = TablePlugin.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], id)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

func ParsePluginColumnKey(key []byte) (id uint64, columnName [2]byte, err error) {
	if len(key) != TablePlugin.Size {
		err = fmt.Errorf("plugin: invalid key length, keyLen: %d", len(key))
		return
	}
	id = binary.BigEndian.Uint64(key[4:])
	columnName[0] = key[12]
	columnName[1] = key[13]
	return
}

// ---------------------- PluginUser ----------------------

func NewPluginUserColumnKey(pluginId uint64, columnName [2]byte) []byte {
	key := make([]byte, TablePluginUser.Size)
	key[0] = TablePluginUser.Id[0]
	key[1] = TablePluginUser.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], pluginId)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

func ParsePluginUserColumnKey(key []byte) (pluginId uint64, columnName [2]byte, err error) {
	if len(key) != TablePluginUser.Size {
		err = fmt.Errorf("pluginUser: invalid key length, keyLen: %d", len(key))
		return
	}
	pluginId = binary.BigEndian.Uint64(key[4:])
	columnName[0] = key[12]
	columnName[1] = key[13]
	return
}

func NewPluginUserSecondIndexKey(indexName [2]byte, columnValue uint64, id uint64) []byte {
	key := make([]byte, TablePluginUser.SecondIndexSize)
	key[0] = TablePluginUser.Id[0]
	key[1] = TablePluginUser.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = indexName[0]
	key[5] = indexName[1]
	binary.BigEndian.PutUint64(key[6:], columnValue)
	binary.BigEndian.PutUint64(key[14:], id)

	return key
}

func ParsePluginUserSecondIndexKey(key []byte) (columnValue uint64, id uint64, err error) {
	if len(key) != TablePluginUser.SecondIndexSize {
		err = fmt.Errorf("PluginUser: second index invalid key length, keyLen: %d", len(key))
		return
	}
	columnValue = binary.BigEndian.Uint64(key[6:])
	id = binary.BigEndian.Uint64(key[14:])
	return

}

// ---------------------- TableStreamV2 ----------------------

func NewStreamV2ColumnKey(clientMsgNo string, columnName [2]byte) []byte {
	key := make([]byte, TableStreamV2.Size)
	key[0] = TableStreamV2.Id[0]
	key[1] = TableStreamV2.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], HashWithString(clientMsgNo))
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

func ParseStreamV2ColumnKey(key []byte) (messageId int64, columnName [2]byte, err error) {
	if len(key) != TableStreamV2.Size {
		err = fmt.Errorf("streamV2: invalid key length, keyLen: %d", len(key))
		return
	}
	messageId = int64(binary.BigEndian.Uint64(key[4:]))
	columnName[0] = key[12]
	columnName[1] = key[13]
	return
}
