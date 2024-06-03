package key

// 表结构
// ---------------------
// | tableID  | dataType | primaryKey | columnKey |
// | 2 byte   | 1 byte   | 8 字节 	   | 2 字节		|
// ---------------------

// 数据类型
var (
	dataTypeTable       byte = 0x01 // 表
	dataTypeIndex       byte = 0x02 // 唯一索引 key结构一般是：  (tableId + dataType + indexName + columnHash) 值一般为primaryKey
	dataTypeSecondIndex byte = 0x03 // 非唯一二级索引 key结构一般是： (tableId + dataType + uid hash + secondIndexName + columnValue + primaryKey) 值一般为空
	dataTypeOther       byte = 0x03 // 其他
)

// ======================== Message ========================
// ---------------------
// | tableID  | dataType	| channel hash | messageSeq   | columnKey |
// | 2 byte   | 1 byte   	| 8 字节 	   	|  8 字节	   | 2 字节		|
// ---------------------

var TableMessage = struct {
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	Column          struct {
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
	Index struct {
		MessageId [2]byte
	}
	SecondIndex struct {
		FromUid     [2]byte
		ClientMsgNo [2]byte
		Timestamp   [2]byte
		Channel     [2]byte
	}
}{
	Id:              [2]byte{0x01, 0x01},
	Size:            2 + 2 + 8 + 8 + 2,  // tableId + dataType + channel hash + messageSeq + columnKey
	IndexSize:       2 + 2 + 2 + 8,      // tableId + dataType + indexName + columnHash
	SecondIndexSize: 2 + 2 + 2 + 8 + 16, // tableId + dataType + secondIndexName + columnValue + primaryKey
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
	Index: struct {
		MessageId [2]byte
	}{
		MessageId: [2]byte{0x01, 0x01},
	},
	SecondIndex: struct {
		FromUid     [2]byte
		ClientMsgNo [2]byte
		Timestamp   [2]byte
		Channel     [2]byte
	}{
		FromUid:     [2]byte{0x01, 0x01},
		ClientMsgNo: [2]byte{0x01, 0x02},
		Timestamp:   [2]byte{0x01, 0x03},
		Channel:     [2]byte{0x01, 0x04},
	},
}

// ======================== User ========================

var TableUser = struct {
	Id        [2]byte
	Size      int
	IndexSize int
	Column    struct {
		Uid               [2]byte // 用户uid
		DeviceCount       [2]byte // 设备数量
		OnlineDeviceCount [2]byte // 在线设备数量
		ConnCount         [2]byte // 连接数量
		SendMsgCount      [2]byte // 发送消息数量
		RecvMsgCount      [2]byte // 接受消息数量
		SendMsgBytes      [2]byte // 发送消息字节数量
		RecvMsgBytes      [2]byte // 接受消息字节数量
		CreatedAt         [2]byte // 创建时间
		UpdatedAt         [2]byte // 更新时间
	}
	Index struct {
		Uid [2]byte
	}
}{
	Id:        [2]byte{0x02, 0x01},
	Size:      2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8, // tableId + dataType + indexName + columnHash
	Column: struct {
		Uid               [2]byte
		DeviceCount       [2]byte // 设备数量
		OnlineDeviceCount [2]byte // 在线设备数量
		ConnCount         [2]byte // 连接数量
		SendMsgCount      [2]byte // 发送消息数量
		RecvMsgCount      [2]byte // 接受消息数量
		SendMsgBytes      [2]byte // 发送消息字节数量
		RecvMsgBytes      [2]byte // 接受消息字节数量
		CreatedAt         [2]byte
		UpdatedAt         [2]byte
	}{
		Uid:               [2]byte{0x02, 0x01},
		DeviceCount:       [2]byte{0x02, 0x02},
		OnlineDeviceCount: [2]byte{0x02, 0x03},
		ConnCount:         [2]byte{0x02, 0x04},
		SendMsgCount:      [2]byte{0x02, 0x05},
		RecvMsgCount:      [2]byte{0x02, 0x06},
		SendMsgBytes:      [2]byte{0x02, 0x07},
		RecvMsgBytes:      [2]byte{0x02, 0x08},
		CreatedAt:         [2]byte{0x02, 0x09},
		UpdatedAt:         [2]byte{0x02, 0x0A},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x02, 0x01},
	},
}

// ======================== Device ========================

var TableDevice = struct {
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	Column          struct {
		Uid         [2]byte // 用户uid
		Token       [2]byte // 设备Token
		DeviceFlag  [2]byte // 设备标识
		DeviceLevel [2]byte // 设备等级
		CreatedAt   [2]byte // 创建时间
		UpdatedAt   [2]byte // 更新时间
	}
	Index struct {
		Device [2]byte
	}
	SecondIndex struct {
		Uid         [2]byte
		DeviceLevel [2]byte // 设备等级
		CreatedAt   [2]byte // 创建时间
		UpdatedAt   [2]byte // 更新时间
	}
}{
	Id:              [2]byte{0x03, 0x01},
	Size:            2 + 2 + 8 + 2,     // tableId + dataType  + primaryKey + columnKey
	IndexSize:       2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName + uid hash + deviceFlag
	SecondIndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + secondIndexName + columnValue + primaryKey
	Column: struct {
		Uid         [2]byte
		Token       [2]byte
		DeviceFlag  [2]byte
		DeviceLevel [2]byte
		CreatedAt   [2]byte
		UpdatedAt   [2]byte
	}{
		Uid:         [2]byte{0x03, 0x01},
		Token:       [2]byte{0x03, 0x02},
		DeviceFlag:  [2]byte{0x03, 0x03},
		DeviceLevel: [2]byte{0x03, 0x04},
		CreatedAt:   [2]byte{0x03, 0x05},
		UpdatedAt:   [2]byte{0x03, 0x06},
	},
	Index: struct {
		Device [2]byte
	}{
		Device: [2]byte{0x03, 0x01},
	},
	SecondIndex: struct {
		Uid         [2]byte
		DeviceLevel [2]byte
		CreatedAt   [2]byte
		UpdatedAt   [2]byte
	}{
		Uid:         [2]byte{0x03, 0x01},
		DeviceLevel: [2]byte{0x03, 0x02},
		CreatedAt:   [2]byte{0x03, 0x03},
		UpdatedAt:   [2]byte{0x03, 0x04},
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
	Id:        [2]byte{0x04, 0x01},
	Size:      2 + 2 + 8 + 8 + 2, // tableId + dataType  + channel hash + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName + channel hash + columnHash
	Column: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x04, 0x01},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x04, 0x01},
	},
}

// ======================== Subscriber Channel Relation ========================

var TableSubscriberChannelRelation = struct {
	Id        [2]byte
	Size      int
	IndexSize int
	Column    struct {
		Channel [2]byte
	}
	Index struct{}
}{
	Id:        [2]byte{0x05, 0x01},
	Size:      2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8, // tableId + dataType + indexName + columnHash
	Column: struct {
		Channel [2]byte
	}{
		Channel: [2]byte{0x05, 0x01},
	},
}

// ======================== ChannelInfo ========================

var TableChannelInfo = struct {
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	Column          struct {
		Id              [2]byte
		ChannelId       [2]byte
		ChannelType     [2]byte
		Ban             [2]byte
		Large           [2]byte
		Disband         [2]byte
		SubscriberCount [2]byte // 订阅者数量
		AllowlistCount  [2]byte // 白名单数量
		DenylistCount   [2]byte // 黑名单数量
	}
	Index struct {
		Channel [2]byte
	}
	SecondIndex struct {
		Ban             [2]byte
		Disband         [2]byte
		SubscriberCount [2]byte
		AllowlistCount  [2]byte
		DenylistCount   [2]byte
	}
}{
	Id:              [2]byte{0x06, 0x01},
	Size:            2 + 2 + 8 + 2,     // tableId + dataType  + primaryKey + columnKey
	IndexSize:       2 + 2 + 2 + 8,     // tableId + dataType + indexName + columnHash
	SecondIndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + secondIndexName + columnValue + primaryKey
	Column: struct {
		Id              [2]byte
		ChannelId       [2]byte
		ChannelType     [2]byte
		Ban             [2]byte
		Large           [2]byte
		Disband         [2]byte
		SubscriberCount [2]byte
		AllowlistCount  [2]byte
		DenylistCount   [2]byte
	}{
		Id:              [2]byte{0x06, 0x01},
		ChannelId:       [2]byte{0x06, 0x02},
		ChannelType:     [2]byte{0x06, 0x03},
		Ban:             [2]byte{0x06, 0x04},
		Large:           [2]byte{0x06, 0x05},
		Disband:         [2]byte{0x06, 0x06},
		SubscriberCount: [2]byte{0x06, 0x07},
		AllowlistCount:  [2]byte{0x06, 0x08},
		DenylistCount:   [2]byte{0x06, 0x09},
	},
	Index: struct {
		Channel [2]byte
	}{
		Channel: [2]byte{0x06, 0x01},
	},
	SecondIndex: struct {
		Ban             [2]byte
		Disband         [2]byte
		SubscriberCount [2]byte
		AllowlistCount  [2]byte
		DenylistCount   [2]byte
	}{
		Ban:             [2]byte{0x06, 0x01},
		Disband:         [2]byte{0x06, 0x02},
		SubscriberCount: [2]byte{0x06, 0x03},
		AllowlistCount:  [2]byte{0x06, 0x04},
		DenylistCount:   [2]byte{0x06, 0x05},
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
	Id:        [2]byte{0x07, 0x01},
	Size:      2 + 2 + 8 + 8 + 2, // tableId + dataType  + channel hash + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName + channel hash + columnHash
	Column: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x07, 0x01},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x07, 0x01},
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
	Id:        [2]byte{0x08, 0x01},
	Size:      2 + 2 + 8 + 8 + 2, // tableId + dataType  + channel hash + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName + channel hash + columnHash
	Column: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x08, 0x01},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x08, 0x01},
	},
}

// ======================== Conversation ========================

var TableConversation = struct {
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	Column          struct {
		Uid            [2]byte
		SessionId      [2]byte
		UnreadCount    [2]byte
		ReadedToMsgSeq [2]byte
	}
	Index struct {
		SessionId [2]byte
	}
}{
	Id:              [2]byte{0x09, 0x01},
	Size:            2 + 2 + 8 + 8 + 2,     // tableId + dataType + uid hash  + primaryKey + columnKey
	IndexSize:       2 + 2 + 8 + 2 + 8,     // tableId + dataType + uid hash  + indexName + columnHash
	SecondIndexSize: 2 + 2 + 8 + 2 + 8 + 8, // tableId + dataType + uid hash  + secondIndexName + columnValue + primaryKey
	Column: struct {
		Uid            [2]byte
		SessionId      [2]byte
		UnreadCount    [2]byte
		ReadedToMsgSeq [2]byte
	}{
		Uid:            [2]byte{0x09, 0x01},
		SessionId:      [2]byte{0x09, 0x02},
		UnreadCount:    [2]byte{0x09, 0x03},
		ReadedToMsgSeq: [2]byte{0x09, 0x04},
	},
	Index: struct {
		SessionId [2]byte
	}{
		SessionId: [2]byte{0x09, 0x01},
	},
}

// ======================== MessageNotifyQueue ========================

var TableMessageNotifyQueue = struct {
	Id   [2]byte
	Size int
}{
	Id:   [2]byte{0x0A, 0x01},
	Size: 2 + 2 + 8, // tableId + dataType  + messageId
}

// ======================== ChannelClusterConfig ========================

var TableChannelClusterConfig = struct {
	Id        [2]byte
	Size      int
	IndexSize int
	Index     struct {
		Channel [2]byte
	}
	Column struct {
		ChannelId        [2]byte
		ChannelType      [2]byte
		ReplicaMaxCount  [2]byte
		Replicas         [2]byte
		Learners         [2]byte
		LeaderId         [2]byte
		Term             [2]byte
		MigrateFrom      [2]byte
		MigrateTo        [2]byte
		LeaderTransferTo [2]byte
		Status           [2]byte
		ConfVersion      [2]byte
		Version          [2]byte
	}
}{
	Id:        [2]byte{0x0B, 0x01},
	Size:      2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8, // tableId + dataType + indexName + columnHash
	Index: struct {
		Channel [2]byte
	}{},
	Column: struct {
		ChannelId        [2]byte
		ChannelType      [2]byte
		ReplicaMaxCount  [2]byte
		Replicas         [2]byte
		Learners         [2]byte
		LeaderId         [2]byte
		Term             [2]byte
		MigrateFrom      [2]byte
		MigrateTo        [2]byte
		LeaderTransferTo [2]byte
		Status           [2]byte
		ConfVersion      [2]byte
		Version          [2]byte
	}{
		ChannelId:        [2]byte{0x0B, 0x01},
		ChannelType:      [2]byte{0x0B, 0x02},
		ReplicaMaxCount:  [2]byte{0x0B, 0x03},
		Replicas:         [2]byte{0x0B, 0x04},
		Learners:         [2]byte{0x0B, 0x05},
		LeaderId:         [2]byte{0x0B, 0x06},
		Term:             [2]byte{0x0B, 0x07},
		MigrateFrom:      [2]byte{0x0B, 0x08},
		MigrateTo:        [2]byte{0x0B, 0x09},
		LeaderTransferTo: [2]byte{0x0B, 0x0A},
		Status:           [2]byte{0x0B, 0x0B},
		ConfVersion:      [2]byte{0x0B, 0x0C},
		Version:          [2]byte{0x0B, 0x0D},
	},
}

// ======================== LeaderTermSequence ========================

var TableLeaderTermSequence = struct {
	Id   [2]byte
	Size int
}{
	Id:   [2]byte{0x0C, 0x01},
	Size: 2 + 2 + 8 + 4, // tableId + dataType  + shardNo hash + term
}

// ======================== ChannelCommon ========================

var TableChannelCommon = struct {
	Id     [2]byte
	Size   int
	Column struct {
		AppliedIndex [2]byte
	}
}{
	Id:   [2]byte{0x0D, 0x01},
	Size: 2 + 2 + 8 + 2, // tableId + dataType  + channel hash + columnKey
	Column: struct {
		AppliedIndex [2]byte
	}{
		AppliedIndex: [2]byte{0x0D, 0x01},
	},
}

// ======================== session ========================

var TableSession = struct {
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	Column          struct {
		Uid         [2]byte
		ChannelId   [2]byte
		ChannelType [2]byte
		CreatedAt   [2]byte
		UpdatedAt   [2]byte
	}
	Index struct {
		Channel [2]byte
	}
	SecondIndex struct {
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}
}{
	Id:              [2]byte{0x0E, 0x01},
	Size:            2 + 2 + 8 + 8 + 2,     // tableId + dataType + uid hash + primaryKey + columnKey
	IndexSize:       2 + 2 + 8 + 2 + 8,     // tableId + dataType + uid hash + indexName + columnHash
	SecondIndexSize: 2 + 2 + 8 + 2 + 8 + 8, // tableId + dataType + uid hash + secondIndexName + columnValue + primaryKey
	Column: struct {
		Uid         [2]byte
		ChannelId   [2]byte
		ChannelType [2]byte
		CreatedAt   [2]byte
		UpdatedAt   [2]byte
	}{
		Uid:         [2]byte{0x0E, 0x01},
		ChannelId:   [2]byte{0x0E, 0x02},
		ChannelType: [2]byte{0x0E, 0x03},
		CreatedAt:   [2]byte{0x0E, 0x04},
		UpdatedAt:   [2]byte{0x0E, 0x05},
	},
	Index: struct {
		Channel [2]byte
	}{
		Channel: [2]byte{0x0E, 0x01},
	},
	SecondIndex: struct {
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		CreatedAt: [2]byte{0x0E, 0x04},
		UpdatedAt: [2]byte{0x0E, 0x05},
	},
}

// ======================== total统计表 ========================

var TableTotal = struct {
	Id     [2]byte
	Size   int
	Column struct {
		User         [2]byte
		Device       [2]byte
		Session      [2]byte
		Conversation [2]byte
		Message      [2]byte
		Channel      [2]byte
	}
}{
	Id:   [2]byte{0x0F, 0x01},
	Size: 2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	Column: struct {
		User         [2]byte
		Device       [2]byte
		Session      [2]byte
		Conversation [2]byte
		Message      [2]byte
		Channel      [2]byte
	}{
		User:         [2]byte{0x0F, 0x01},
		Device:       [2]byte{0x0F, 0x02},
		Session:      [2]byte{0x0F, 0x03},
		Conversation: [2]byte{0x0F, 0x04},
		Message:      [2]byte{0x0F, 0x05},
		Channel:      [2]byte{0x0F, 0x06},
	},
}
