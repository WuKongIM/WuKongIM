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
	channelHash := channelIdToNum(channelId, channelType)
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
		channelHash := channelIdToNum(channelId, channelType)
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
		channelHash := channelIdToNum(channelId, channelType)
		binary.BigEndian.PutUint64(key[4:], channelHash)
	} else {
		binary.BigEndian.PutUint64(key[4:], math.MaxUint64)
	}
	binary.BigEndian.PutUint64(key[12:], messageSeq)
	return key
}

func NewChannelLastMessageSeqKey(channelId string, channelType uint8) []byte {
	key := make([]byte, 12)
	channelHash := channelIdToNum(channelId, channelType)
	key[0] = TableMessage.Id[0]
	key[1] = TableMessage.Id[1]
	key[2] = dataTypeOther
	key[3] = 0
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

func channelIdToNum(channelId string, channelType uint8) uint64 {
	h := fnv.New64a()
	h.Write([]byte(ChannelKey(channelId, channelType)))
	return h.Sum64()
}

func ChannelIdToNum(channelId string, channelType uint8) uint64 {
	h := fnv.New64a()
	h.Write([]byte(ChannelKey(channelId, channelType)))
	return h.Sum64()
}

func ChannelKey(channelID string, channelType uint8) string {
	var b strings.Builder
	b.WriteString(channelID)
	b.WriteByte('-')
	b.WriteString(strconv.FormatInt(int64(channelType), 10))
	return b.String()
}

func hashWithString(s string) uint64 {
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
	binary.BigEndian.PutUint64(key[6:], hashWithString(uid))
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
	binary.BigEndian.PutUint64(key[6:], hashWithString(clientMsgNo))
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
	if len(key) != TableMessage.IndexSize {
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
func NewUserIndexUidKey(uid string) []byte {
	key := make([]byte, TableUser.IndexSize)
	key[0] = TableUser.Id[0]
	key[1] = TableUser.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableUser.Index.Uid[0]
	key[5] = TableUser.Index.Uid[1]

	uidHash := hashWithString(uid)
	binary.BigEndian.PutUint64(key[6:], uidHash)
	return key
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

func NewDeviceIndexUidAndDeviceFlagKey(uid string, deviceFlag uint64) []byte {
	key := make([]byte, TableDevice.IndexSize)
	key[0] = TableDevice.Id[0]
	key[1] = TableDevice.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableDevice.Index.Device[0]
	key[5] = TableDevice.Index.Device[1]
	uidHash := hashWithString(uid)
	binary.BigEndian.PutUint64(key[6:], uidHash)
	binary.BigEndian.PutUint64(key[14:], deviceFlag)

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

// ---------------------- Subscriber ----------------------

func NewSubscriberColumnKey(channelId string, channelType uint8, id uint64, columnName [2]byte) []byte {
	key := make([]byte, TableSubscriber.Size)
	channelHash := channelIdToNum(channelId, channelType)
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

func NewSubscriberPrimaryKey(channelId string, channelType uint8, id uint64) []byte {
	key := make([]byte, 20)

	key[0] = TableSubscriber.Id[0]
	key[1] = TableSubscriber.Id[1]
	key[2] = dataTypeTable
	key[3] = 0

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], id)
	return key
}

// NewSubscriberIndexUidKey 创建一个uid唯一索引的 key
func NewSubscriberIndexUidKey(channelId string, channelType uint8, uid string) []byte {
	key := make([]byte, TableSubscriber.IndexSize)
	key[0] = TableSubscriber.Id[0]
	key[1] = TableSubscriber.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableSubscriber.Index.Uid[0]
	key[5] = TableSubscriber.Index.Uid[1]

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], hashWithString(uid))
	return key
}

// NewSubscriberIndexUidLowKey 创建一个uid唯一索引的low key
func NewSubscriberIndexUidLowKey(channelId string, channelType uint8) []byte {
	key := make([]byte, TableSubscriber.IndexSize)
	key[0] = TableSubscriber.Id[0]
	key[1] = TableSubscriber.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableSubscriber.Index.Uid[0]
	key[5] = TableSubscriber.Index.Uid[1]

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], 0)
	return key
}

// NewSubscriberIndexUidHighKey 创建一个uid唯一索引的high key
func NewSubscriberIndexUidHighKey(channelId string, channelType uint8) []byte {
	key := make([]byte, TableSubscriber.IndexSize)
	key[0] = TableSubscriber.Id[0]
	key[1] = TableSubscriber.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableSubscriber.Index.Uid[0]
	key[5] = TableSubscriber.Index.Uid[1]

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], math.MaxUint64)
	return key

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
func NewChannelInfoIndexKey(channelId string, channelType uint8) []byte {
	key := make([]byte, TableChannelInfo.IndexSize)
	key[0] = TableChannelInfo.Id[0]
	key[1] = TableChannelInfo.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableChannelInfo.Index.Channel[0]
	key[5] = TableChannelInfo.Index.Channel[1]
	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
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
	channelHash := channelIdToNum(channelId, channelType)
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

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], id)
	return key
}

// NewDenylistIndexUidKey 创建一个uid唯一索引的 key
func NewDenylistIndexUidKey(channelId string, channelType uint8, uid string) []byte {
	key := make([]byte, TableDenylist.IndexSize)
	key[0] = TableDenylist.Id[0]
	key[1] = TableDenylist.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableDenylist.Index.Uid[0]
	key[5] = TableDenylist.Index.Uid[1]

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], hashWithString(uid))
	return key
}

// NewDenylistIndexUidLowKey 创建一个uid唯一索引的low key
func NewDenylistIndexUidLowKey(channelId string, channelType uint8) []byte {
	key := make([]byte, TableDenylist.IndexSize)
	key[0] = TableDenylist.Id[0]
	key[1] = TableDenylist.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableDenylist.Index.Uid[0]
	key[5] = TableDenylist.Index.Uid[1]

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], 0)
	return key
}

// NewDenylistIndexUidHighKey 创建一个uid唯一索引的high key
func NewDenylistIndexUidHighKey(channelId string, channelType uint8) []byte {
	key := make([]byte, TableDenylist.IndexSize)
	key[0] = TableDenylist.Id[0]
	key[1] = TableDenylist.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableDenylist.Index.Uid[0]
	key[5] = TableDenylist.Index.Uid[1]

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], math.MaxUint64)
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
	channelHash := channelIdToNum(channelId, channelType)
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

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[4:], channelHash)
	binary.BigEndian.PutUint64(key[12:], id)
	return key
}

// NewAllowlistIndexUidKey 创建一个uid唯一索引的 key
func NewAllowlistIndexUidKey(channelId string, channelType uint8, uid string) []byte {
	key := make([]byte, TableAllowlist.IndexSize)
	key[0] = TableAllowlist.Id[0]
	key[1] = TableAllowlist.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	key[4] = TableAllowlist.Index.Uid[0]
	key[5] = TableAllowlist.Index.Uid[1]

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
	binary.BigEndian.PutUint64(key[14:], hashWithString(uid))
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

	channelHash := channelIdToNum(channelId, channelType)
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

	channelHash := channelIdToNum(channelId, channelType)
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
	binary.BigEndian.PutUint64(key[4:], hashWithString(uid))
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
	binary.BigEndian.PutUint64(key[4:], hashWithString(uid))
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
func NewConversationIndexSessionIdKey(uid string, sessionId uint64) []byte {
	key := make([]byte, TableConversation.IndexSize)
	key[0] = TableConversation.Id[0]
	key[1] = TableConversation.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], hashWithString(uid))

	key[12] = TableConversation.Index.SessionId[0]
	key[13] = TableConversation.Index.SessionId[1]

	binary.BigEndian.PutUint64(key[14:], sessionId)

	return key
}

func ParseConversationSecondIndexTimestampKey(key []byte) (timestamp uint64, primaryKey uint64, err error) {
	if len(key) != TableConversation.SecondIndexSize {
		err = fmt.Errorf("conversation: second index invalid key length, keyLen: %d", len(key))
		return
	}

	timestamp = binary.BigEndian.Uint64(key[14:])
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
	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[6:], channelHash)
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

// ---------------------- LeaderTermSequence ----------------------

func NewLeaderTermSequenceTermKey(shardNo string, term uint32) []byte {
	key := make([]byte, TableLeaderTermSequence.Size)
	key[0] = TableLeaderTermSequence.Id[0]
	key[1] = TableLeaderTermSequence.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], hashWithString(shardNo))
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
	channelHash := channelIdToNum(channelId, channelType)
	key[0] = TableChannelCommon.Id[0]
	key[1] = TableChannelCommon.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], channelHash)
	key[12] = columnName[0]
	key[13] = columnName[1]
	return key
}

// ---------------------- Session ----------------------

func NewSessionColumnKey(uid string, primaryKey uint64, columnName [2]byte) []byte {
	key := make([]byte, TableSession.Size)
	key[0] = TableSession.Id[0]
	key[1] = TableSession.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], hashWithString(uid))
	binary.BigEndian.PutUint64(key[12:], primaryKey)
	key[20] = columnName[0]
	key[21] = columnName[1]
	return key
}

func NewSessionUidHashKey(uidHash uint64) []byte {
	key := make([]byte, 12)
	key[0] = TableSession.Id[0]
	key[1] = TableSession.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], uidHash)
	return key

}

func NewSessionPrimaryKey(uid string, primaryKey uint64) []byte {
	key := make([]byte, 20)
	key[0] = TableSession.Id[0]
	key[1] = TableSession.Id[1]
	key[2] = dataTypeTable
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], hashWithString(uid))
	binary.BigEndian.PutUint64(key[12:], primaryKey)
	return key
}

func NewSessionChannelIndexKey(uid string, channelId string, channelType uint8) []byte {
	key := make([]byte, TableSession.IndexSize)
	key[0] = TableSession.Id[0]
	key[1] = TableSession.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], hashWithString(uid))

	key[12] = TableSession.Index.Channel[0]
	key[13] = TableSession.Index.Channel[1]

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[14:], channelHash)
	return key
}

func NewSessionSecondIndexKey(uid string, columnName [2]byte, columnValue uint64, primaryKey uint64) []byte {
	key := make([]byte, TableSession.SecondIndexSize)
	key[0] = TableSession.Id[0]
	key[1] = TableSession.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], hashWithString(uid))
	key[12] = columnName[0]
	key[13] = columnName[1]
	binary.BigEndian.PutUint64(key[14:], columnValue)
	binary.BigEndian.PutUint64(key[22:], primaryKey)

	return key

}

func ParseSessionSecondIndexKey(key []byte) (primaryKey uint64, columnName [2]byte, columnValue uint64, err error) {
	if len(key) != TableSession.SecondIndexSize {
		err = fmt.Errorf("session: second index invalid key length, keyLen: %d", len(key))
		return
	}
	columnName[0] = key[12]
	columnName[1] = key[13]
	columnValue = binary.BigEndian.Uint64(key[14:])
	primaryKey = binary.BigEndian.Uint64(key[22:])

	return
}

func ParseSessionColumnKey(key []byte) (primaryKey uint64, columnName [2]byte, err error) {
	if len(key) != TableSession.Size {
		err = fmt.Errorf("session: invalid key length, keyLen: %d", len(key))
		return
	}
	primaryKey = binary.BigEndian.Uint64(key[12:])
	columnName[0] = key[20]
	columnName[1] = key[21]
	return
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
