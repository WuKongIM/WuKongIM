package key

// 表结构
// ---------------------
// | tableID  | dataType | primaryKey | columnKey |
// | 2 byte   | 1 byte   | 8 字节 	   | 2 字节		|
// ---------------------

// 数据类型
var (
	dataTypeTable       byte = 0x01 // 表
	dataTypeIndex       byte = 0x02 // 索引
	dataTypeSecondIndex byte = 0x03 // 二级索引
	dataTypeOther       byte = 0x03 // 其他
)

// ======================== Message ========================
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

// ======================== User ========================

var TableUser = struct {
	Id        [2]byte
	Size      int
	IndexSize int
	Column    struct {
		Uid         [2]byte
		Token       [2]byte
		DeviceFlag  [2]byte
		DeviceLevel [2]byte
	}
	Index struct {
		Uid [2]byte
	}
}{
	Id:        [2]byte{0x02, 0x01},
	Size:      2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8, // tableId + dataType + indexName + columnHash
	Column: struct {
		Uid         [2]byte
		Token       [2]byte
		DeviceFlag  [2]byte
		DeviceLevel [2]byte
	}{
		Uid:         [2]byte{0x02, 0x01},
		Token:       [2]byte{0x02, 0x02},
		DeviceFlag:  [2]byte{0x02, 0x03},
		DeviceLevel: [2]byte{0x02, 0x04},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x02, 0x01},
	},
}

// ======================== Subscriber ========================

var TableSubscriber = struct {
	Id        [2]byte
	Size      int
	IndexSize int
	Column    struct {
		Uid [2]byte
	}
	Index struct {
		Uid [2]byte
	}
}{
	Id:        [2]byte{0x03, 0x01},
	Size:      2 + 2 + 8 + 8 + 2, // tableId + dataType  + channel hash + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName + channel hash + columnHash
	Column: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x03, 0x01},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x03, 0x01},
	},
}

// ======================== ChannelInfo ========================

var TableChannelInfo = struct {
	Id        [2]byte
	Size      int
	IndexSize int
	Column    struct {
		ChannelId   [2]byte
		ChannelType [2]byte
		Ban         [2]byte
		Large       [2]byte
	}
	Index struct {
		Channel [2]byte
	}
}{
	Id:        [2]byte{0x04, 0x01},
	Size:      2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8, // tableId + dataType + indexName + columnHash
	Column: struct {
		ChannelId   [2]byte
		ChannelType [2]byte
		Ban         [2]byte
		Large       [2]byte
	}{
		ChannelId:   [2]byte{0x04, 0x01},
		ChannelType: [2]byte{0x04, 0x02},
		Ban:         [2]byte{0x04, 0x03},
		Large:       [2]byte{0x04, 0x04},
	},
	Index: struct {
		Channel [2]byte
	}{
		Channel: [2]byte{0x04, 0x01},
	},
}

// ======================== Denylist ========================

var TableDenylist = struct {
	Id        [2]byte
	Size      int
	IndexSize int
	Column    struct {
		Uid [2]byte
	}
	Index struct {
		Uid [2]byte
	}
}{
	Id:        [2]byte{0x05, 0x01},
	Size:      2 + 2 + 8 + 8 + 2, // tableId + dataType  + channel hash + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName + channel hash + columnHash
	Column: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x05, 0x01},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x05, 0x01},
	},
}

// ======================== Allowlist ========================

var TableAllowlist = struct {
	Id        [2]byte
	Size      int
	IndexSize int
	Column    struct {
		Uid [2]byte
	}
	Index struct {
		Uid [2]byte
	}
}{
	Id:        [2]byte{0x06, 0x01},
	Size:      2 + 2 + 8 + 8 + 2, // tableId + dataType  + channel hash + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName + channel hash + columnHash
	Column: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x06, 0x01},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x06, 0x01},
	},
}

// ======================== Conversation ========================

var TableConversation = struct {
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	Column          struct {
		Uid             [2]byte
		ChannelId       [2]byte
		ChannelType     [2]byte
		UnreadCount     [2]byte
		Timestamp       [2]byte
		LastMsgSeq      [2]byte
		LastMsgClientNo [2]byte
		LastMsgId       [2]byte
		Version         [2]byte
	}
	Index struct {
		Channel [2]byte
	}
	SecondIndex struct {
		Timestamp [2]byte
	}
}{
	Id:              [2]byte{0x07, 0x01},
	Size:            2 + 2 + 8 + 8 + 2,     // tableId + dataType + uid hash  + primaryKey + columnKey
	IndexSize:       2 + 2 + 8 + 2 + 8,     // tableId + dataType + uid hash  + indexName + columnHash
	SecondIndexSize: 2 + 2 + 8 + 2 + 8 + 8, // tableId + dataType + uid hash  + secondIndexName + columnValue + primaryKey
	Column: struct {
		Uid             [2]byte
		ChannelId       [2]byte
		ChannelType     [2]byte
		UnreadCount     [2]byte
		Timestamp       [2]byte
		LastMsgSeq      [2]byte
		LastMsgClientNo [2]byte
		LastMsgId       [2]byte
		Version         [2]byte
	}{
		Uid:             [2]byte{0x07, 0x01},
		ChannelId:       [2]byte{0x07, 0x02},
		ChannelType:     [2]byte{0x07, 0x03},
		UnreadCount:     [2]byte{0x07, 0x04},
		Timestamp:       [2]byte{0x07, 0x05},
		LastMsgSeq:      [2]byte{0x07, 0x06},
		LastMsgClientNo: [2]byte{0x07, 0x07},
		LastMsgId:       [2]byte{0x07, 0x08},
		Version:         [2]byte{0x07, 0x09},
	},
	Index: struct {
		Channel [2]byte
	}{
		Channel: [2]byte{0x07, 0x01},
	},
	SecondIndex: struct {
		Timestamp [2]byte
	}{
		Timestamp: [2]byte{0x07, 0x01},
	},
}
