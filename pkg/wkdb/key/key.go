package key

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
)

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

func ChannelKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%d-%s", channelType, channelID)
}

func hashWithString(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
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

// NewConversationIndexChannelKey 创建一个channel唯一索引的 key
func NewConversationIndexChannelKey(uid string, channelId string, channelType uint8) []byte {
	key := make([]byte, TableConversation.IndexSize)
	key[0] = TableConversation.Id[0]
	key[1] = TableConversation.Id[1]
	key[2] = dataTypeIndex
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], hashWithString(uid))

	key[12] = TableConversation.Index.Channel[0]
	key[13] = TableConversation.Index.Channel[1]

	channelHash := channelIdToNum(channelId, channelType)
	binary.BigEndian.PutUint64(key[14:], channelHash)

	return key
}

func NewConversationSecondIndexTimestampKey(uid string, timestamp uint64, primaryKey uint64) []byte {
	key := make([]byte, TableConversation.SecondIndexSize)
	key[0] = TableConversation.Id[0]
	key[1] = TableConversation.Id[1]
	key[2] = dataTypeSecondIndex
	key[3] = 0
	binary.BigEndian.PutUint64(key[4:], hashWithString(uid))

	key[12] = TableConversation.SecondIndex.Timestamp[0]
	key[13] = TableConversation.SecondIndex.Timestamp[1]

	binary.BigEndian.PutUint64(key[14:], timestamp)
	binary.BigEndian.PutUint64(key[22:], primaryKey)

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
