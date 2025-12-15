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
	dataTypeOther       byte = 0x04 // 其他
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
		StreamNo    [2]byte
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
		StreamNo    [2]byte
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
		StreamNo:    [2]byte{0x01, 0x0E},
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
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	Column          struct {
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
		PluginNo          [2]byte // 插件编号
	}
	Index struct {
		Uid [2]byte
	}
	SecondIndex struct {
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}
}{
	Id:              [2]byte{0x02, 0x01},
	Size:            2 + 2 + 8 + 2,     // tableId + dataType  + primaryKey + columnKey
	IndexSize:       2 + 2 + 2 + 8,     // tableId + dataType + indexName  + columnHash
	SecondIndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + secondIndexName + columnValue + primaryKey
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
		PluginNo          [2]byte
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
		PluginNo:          [2]byte{0x02, 0x0B},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x02, 0x01},
	},
	SecondIndex: struct {
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		CreatedAt: [2]byte{0x02, 0x01},
		UpdatedAt: [2]byte{0x02, 0x02},
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
	SecondIndex struct {
		Uid         [2]byte
		DeviceLevel [2]byte // 设备等级
		CreatedAt   [2]byte // 创建时间
		UpdatedAt   [2]byte // 更新时间
		DeviceFlag  [2]byte // 设备标识
	}
}{
	Id:              [2]byte{0x03, 0x01},
	Size:            2 + 2 + 8 + 2,     // tableId + dataType  + primaryKey + columnKey
	IndexSize:       2 + 2 + 2 + 8,     // tableId + dataType + indexName + columnValue
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
	SecondIndex: struct {
		Uid         [2]byte
		DeviceLevel [2]byte
		CreatedAt   [2]byte
		UpdatedAt   [2]byte
		DeviceFlag  [2]byte
	}{
		Uid:         [2]byte{0x03, 0x01},
		DeviceLevel: [2]byte{0x03, 0x02},
		CreatedAt:   [2]byte{0x03, 0x03},
		UpdatedAt:   [2]byte{0x03, 0x04},
		DeviceFlag:  [2]byte{0x03, 0x05},
	},
}

// ======================== Subscriber ========================

var TableSubscriber = struct {
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	Column          struct {
		Uid       [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}
	Index struct {
		Uid [2]byte
	}
	SecondIndex struct {
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}
}{
	Id:              [2]byte{0x04, 0x01},
	Size:            2 + 2 + 8 + 8 + 2,     // tableId + dataType  + channel hash + primaryKey + columnKey
	IndexSize:       2 + 2 + 2 + 8 + 8,     // tableId + dataType + indexName + channel hash + columnHash
	SecondIndexSize: 2 + 2 + 2 + 8 + 8 + 8, // tableId + dataType + secondIndexName + channel hash +  columnValue + primaryKey
	Column: struct {
		Uid       [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		Uid:       [2]byte{0x04, 0x01},
		CreatedAt: [2]byte{0x04, 0x02},
		UpdatedAt: [2]byte{0x04, 0x03},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x04, 0x01},
	},
	SecondIndex: struct {
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		CreatedAt: [2]byte{0x04, 0x01},
		UpdatedAt: [2]byte{0x04, 0x02},
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
	Size:      2 + 2 + 8 + 2,     // tableId + dataType  + primaryKey + columnHash
	IndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName  + primaryKey+ columnHash
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
		CreatedAt       [2]byte
		UpdatedAt       [2]byte
		SendBan         [2]byte
		AllowStranger   [2]byte
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
		CreatedAt       [2]byte
		UpdatedAt       [2]byte
		SendBan         [2]byte
		AllowStranger   [2]byte
	}
}{
	Id:              [2]byte{0x06, 0x01},
	Size:            2 + 2 + 8 + 2,     // tableId + dataType  + primaryKey + columnKey
	IndexSize:       2 + 2 + 2 + 8,     // tableId + dataType + indexName  + columnHash
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
		CreatedAt       [2]byte
		UpdatedAt       [2]byte
		SendBan         [2]byte
		AllowStranger   [2]byte
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
		CreatedAt:       [2]byte{0x06, 0x0A},
		UpdatedAt:       [2]byte{0x06, 0x0B},
		SendBan:         [2]byte{0x06, 0x0C},
		AllowStranger:   [2]byte{0x06, 0x0D},
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
		CreatedAt       [2]byte
		UpdatedAt       [2]byte
		SendBan         [2]byte
		AllowStranger   [2]byte
	}{
		Ban:             [2]byte{0x06, 0x01},
		Disband:         [2]byte{0x06, 0x02},
		SubscriberCount: [2]byte{0x06, 0x03},
		AllowlistCount:  [2]byte{0x06, 0x04},
		DenylistCount:   [2]byte{0x06, 0x05},
		CreatedAt:       [2]byte{0x06, 0x06},
		UpdatedAt:       [2]byte{0x06, 0x07},
		SendBan:         [2]byte{0x06, 0x08},
		AllowStranger:   [2]byte{0x06, 0x09},
	},
}

// ======================== Denylist ========================

var TableDenylist = struct {
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	Column          struct {
		Uid       [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}
	Index struct {
		Uid [2]byte
	}
	SecondIndex struct {
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}
}{
	Id:              [2]byte{0x07, 0x01},
	Size:            2 + 2 + 8 + 8 + 2,     // tableId + dataType  + channel hash + primaryKey + columnKey
	IndexSize:       2 + 2 + 2 + 8 + 8,     // tableId + dataType + indexName + channel hash + columnHash
	SecondIndexSize: 2 + 2 + 2 + 8 + 8 + 8, // tableId + dataType + secondIndexName + channel hash + columnValue + primaryKey
	Column: struct {
		Uid       [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		Uid:       [2]byte{0x07, 0x01},
		CreatedAt: [2]byte{0x07, 0x02},
		UpdatedAt: [2]byte{0x07, 0x03},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x07, 0x01},
	},
	SecondIndex: struct {
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		CreatedAt: [2]byte{0x07, 0x01},
		UpdatedAt: [2]byte{0x07, 0x02},
	},
}

// ======================== Allowlist ========================

var TableAllowlist = struct {
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	Column          struct {
		Uid       [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}
	Index struct {
		Uid [2]byte
	}
	SecondIndex struct {
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}
}{
	Id:              [2]byte{0x08, 0x01},
	Size:            2 + 2 + 8 + 8 + 2,     // tableId + dataType  + channel hash + primaryKey + columnKey
	IndexSize:       2 + 2 + 2 + 8 + 8,     // tableId + dataType + indexName + channel hash + columnHash
	SecondIndexSize: 2 + 2 + 2 + 8 + 8 + 8, // tableId + dataType + secondIndexName + channel hash + columnValue + primaryKey
	Column: struct {
		Uid       [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		Uid:       [2]byte{0x08, 0x01},
		CreatedAt: [2]byte{0x08, 0x02},
		UpdatedAt: [2]byte{0x08, 0x03},
	},
	Index: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x08, 0x01},
	},
	SecondIndex: struct {
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		CreatedAt: [2]byte{0x08, 0x01},
		UpdatedAt: [2]byte{0x08, 0x02},
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
		Type            [2]byte
		UnreadCount     [2]byte
		ReadedToMsgSeq  [2]byte
		CreatedAt       [2]byte
		UpdatedAt       [2]byte
		DeletedAtMsgSeq [2]byte
	}
	Index struct {
		Channel [2]byte
	}
	SecondIndex struct {
		Type      [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}
}{
	Id:              [2]byte{0x09, 0x01},
	Size:            2 + 2 + 8 + 8 + 2,     // tableId + dataType + uid hash  + primaryKey + columnKey
	IndexSize:       2 + 2 + 2 + 8 + 8,     // tableId + dataType   + indexName + primaryKey + columnHash
	SecondIndexSize: 2 + 2 + 8 + 2 + 8 + 8, // tableId + dataType + uid hash  + secondIndexName + columnValue + primaryKey
	Column: struct {
		Uid             [2]byte
		ChannelId       [2]byte
		ChannelType     [2]byte
		Type            [2]byte
		UnreadCount     [2]byte
		ReadedToMsgSeq  [2]byte
		CreatedAt       [2]byte
		UpdatedAt       [2]byte
		DeletedAtMsgSeq [2]byte
	}{
		Uid:             [2]byte{0x09, 0x01},
		ChannelId:       [2]byte{0x09, 0x02},
		ChannelType:     [2]byte{0x09, 0x03},
		Type:            [2]byte{0x09, 0x04},
		UnreadCount:     [2]byte{0x09, 0x05},
		ReadedToMsgSeq:  [2]byte{0x09, 0x06},
		CreatedAt:       [2]byte{0x09, 0x07},
		UpdatedAt:       [2]byte{0x09, 0x08},
		DeletedAtMsgSeq: [2]byte{0x09, 0x09},
	},
	Index: struct {
		Channel [2]byte
	}{
		Channel: [2]byte{0x09, 0x01},
	},
	SecondIndex: struct {
		Type      [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		Type:      [2]byte{0x09, 0x01},
		CreatedAt: [2]byte{0x09, 0x02},
		UpdatedAt: [2]byte{0x09, 0x03},
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
	Id              [2]byte
	Size            int
	IndexSize       int
	SecondIndexSize int
	OtherIndexSize  int
	Index           struct {
		Channel [2]byte
	}
	SecondIndex struct {
		LeaderId  [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}
	OtherIndex struct {
		LeaderId [2]byte
	}
	Column struct {
		ChannelId       [2]byte
		ChannelType     [2]byte
		ReplicaMaxCount [2]byte
		Replicas        [2]byte
		Learners        [2]byte
		LeaderId        [2]byte
		Term            [2]byte
		MigrateFrom     [2]byte
		MigrateTo       [2]byte
		Status          [2]byte
		ConfVersion     [2]byte
		Version         [2]byte
		CreatedAt       [2]byte
		UpdatedAt       [2]byte
	}
}{
	Id:              [2]byte{0x0B, 0x01},
	Size:            2 + 2 + 8 + 2,     // tableId + dataType  + primaryKey + columnKey
	IndexSize:       2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName + primaryKey + columnHash
	SecondIndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + secondIndexName + columnValue + primaryKey
	Index: struct {
		Channel [2]byte
	}{},
	SecondIndex: struct {
		LeaderId  [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		LeaderId:  [2]byte{0x0B, 0x01},
		CreatedAt: [2]byte{0x0B, 0x02},
		UpdatedAt: [2]byte{0x0B, 0x03},
	},
	OtherIndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + secondIndexName + primaryKey + columnValue
	OtherIndex: struct {
		LeaderId [2]byte
	}{
		LeaderId: [2]byte{0x0B, 0x01},
	},

	Column: struct {
		ChannelId       [2]byte
		ChannelType     [2]byte
		ReplicaMaxCount [2]byte
		Replicas        [2]byte
		Learners        [2]byte
		LeaderId        [2]byte
		Term            [2]byte
		MigrateFrom     [2]byte
		MigrateTo       [2]byte
		Status          [2]byte
		ConfVersion     [2]byte
		Version         [2]byte
		CreatedAt       [2]byte
		UpdatedAt       [2]byte
	}{
		ChannelId:       [2]byte{0x0B, 0x01},
		ChannelType:     [2]byte{0x0B, 0x02},
		ReplicaMaxCount: [2]byte{0x0B, 0x03},
		Replicas:        [2]byte{0x0B, 0x04},
		Learners:        [2]byte{0x0B, 0x05},
		LeaderId:        [2]byte{0x0B, 0x06},
		Term:            [2]byte{0x0B, 0x07},
		MigrateFrom:     [2]byte{0x0B, 0x08},
		MigrateTo:       [2]byte{0x0B, 0x09},
		Status:          [2]byte{0x0B, 0x0A},
		ConfVersion:     [2]byte{0x0B, 0x0B},
		Version:         [2]byte{0x0B, 0x0C},
		CreatedAt:       [2]byte{0x0B, 0x0D},
		UpdatedAt:       [2]byte{0x0B, 0x0E},
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

// ======================== total统计表 ========================

var TableTotal = struct {
	Id     [2]byte
	Size   int
	Column struct {
		User                 [2]byte
		Device               [2]byte
		Session              [2]byte
		Conversation         [2]byte
		Message              [2]byte
		Channel              [2]byte
		ChannelClusterConfig [2]byte
	}
}{
	Id:   [2]byte{0x0F, 0x01},
	Size: 2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	Column: struct {
		User                 [2]byte
		Device               [2]byte
		Session              [2]byte
		Conversation         [2]byte
		Message              [2]byte
		Channel              [2]byte
		ChannelClusterConfig [2]byte
	}{
		User:                 [2]byte{0x0F, 0x01},
		Device:               [2]byte{0x0F, 0x02},
		Session:              [2]byte{0x0F, 0x03},
		Conversation:         [2]byte{0x0F, 0x04},
		Message:              [2]byte{0x0F, 0x05},
		Channel:              [2]byte{0x0F, 0x06},
		ChannelClusterConfig: [2]byte{0x0F, 0x07},
	},
}

// ======================== system uid ========================

var TableSystemUid = struct {
	Id     [2]byte
	Size   int
	Column struct {
		Uid [2]byte
	}
}{
	Id:   [2]byte{0x10, 0x01},
	Size: 2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	Column: struct {
		Uid [2]byte
	}{
		Uid: [2]byte{0x10, 0x01},
	},
}

// ======================== stream ========================

var TableStream = struct {
	Id        [2]byte
	Size      int
	IndexSize int
	Index     struct {
		StreamNo [2]byte
	}
}{
	Id:        [2]byte{0x11, 0x01},
	IndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName + columnHash + seq
	Index: struct {
		StreamNo [2]byte
	}{
		StreamNo: [2]byte{0x11, 0x01},
	},
}

// ======================== streamMeta ========================

var TableStreamMeta = struct {
	Id     [2]byte
	Size   int
	Column struct {
		StreamNo [2]byte
	}
}{
	Id:   [2]byte{0x12, 0x01},
	Size: 2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	Column: struct {
		StreamNo [2]byte
	}{
		StreamNo: [2]byte{0x12, 0x01},
	},
}

// ======================== TableConversationLocalUser ========================

// 最近会话与本地用户关系表
var TableConversationLocalUser = struct {
	Id [2]byte
}{
	Id: [2]byte{0x13, 0x01},
}

// ======================== TableTester ========================

var TableTester = struct {
	Id     [2]byte
	Size   int
	Column struct {
		No        [2]byte // 测试机器编号
		Addr      [2]byte // 测试机器地址
		CreatedAt [2]byte // 创建时间
		UpdatedAt [2]byte // 更新时间
	}
}{
	Id:   [2]byte{0x14, 0x01},
	Size: 2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	Column: struct {
		No        [2]byte
		Addr      [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		No:        [2]byte{0x14, 0x01},
		Addr:      [2]byte{0x14, 0x02},
		CreatedAt: [2]byte{0x14, 0x03},
		UpdatedAt: [2]byte{0x14, 0x04},
	},
}

// ======================== TablePlugin ========================
// 插件表
var TablePlugin = struct {
	Id     [2]byte
	Size   int
	Column struct {
		No             [2]byte // 插件编号
		Name           [2]byte // 插件名称
		ConfigTemplate [2]byte // 插件配置模版
		CreatedAt      [2]byte // 创建时间
		UpdatedAt      [2]byte // 更新时间
		Status         [2]byte // 插件状态 0.未设置 1.启用 2.禁用
		Version        [2]byte // 插件版本
		Methods        [2]byte // 插件方法
		Priority       [2]byte // 插件优先级
		Config         [2]byte // 插件配置
	}
}{
	Id:   [2]byte{0x15, 0x01},
	Size: 2 + 2 + 8 + 2, // tableId + dataType  + primaryKey + columnKey
	Column: struct {
		No             [2]byte
		Name           [2]byte
		ConfigTemplate [2]byte
		CreatedAt      [2]byte
		UpdatedAt      [2]byte
		Status         [2]byte
		Version        [2]byte
		Methods        [2]byte
		Priority       [2]byte
		Config         [2]byte
	}{
		No:             [2]byte{0x15, 0x01},
		Name:           [2]byte{0x15, 0x02},
		ConfigTemplate: [2]byte{0x15, 0x03},
		CreatedAt:      [2]byte{0x15, 0x04},
		UpdatedAt:      [2]byte{0x15, 0x05},
		Status:         [2]byte{0x15, 0x06},
		Version:        [2]byte{0x15, 0x07},
		Methods:        [2]byte{0x15, 0x08},
		Priority:       [2]byte{0x15, 0x09},
		Config:         [2]byte{0x15, 0x0A},
	},
}

// ======================== TablePluginUser ========================

// 插件与用户关系表
var TablePluginUser = struct {
	Id              [2]byte
	Size            int
	SecondIndexSize int
	Column          struct {
		PluginNo  [2]byte // 插件编号
		Uid       [2]byte // 用户uid
		CreatedAt [2]byte // 创建时间
		UpdatedAt [2]byte // 更新时间
	}
	SecondIndex struct {
		Uid      [2]byte
		PluginNo [2]byte
	}
}{
	Id:              [2]byte{0x16, 0x01},
	Size:            2 + 2 + 8 + 2,     // tableId + dataType  + primaryKey + columnKey
	SecondIndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + secondIndexName + columnValue + primaryKey
	Column: struct {
		PluginNo  [2]byte
		Uid       [2]byte
		CreatedAt [2]byte
		UpdatedAt [2]byte
	}{
		PluginNo:  [2]byte{0x16, 0x01},
		Uid:       [2]byte{0x16, 0x02},
		CreatedAt: [2]byte{0x16, 0x03},
		UpdatedAt: [2]byte{0x16, 0x04},
	},
	SecondIndex: struct {
		Uid      [2]byte
		PluginNo [2]byte
	}{
		Uid:      [2]byte{0x16, 0x01},
		PluginNo: [2]byte{0x16, 0x02},
	},
}

// ======================== TableStreamV2 ========================
var TableStreamV2 = struct {
	Id        [2]byte
	Size      int
	IndexSize int
	Column    struct {
		ClientMsgNo [2]byte // 客户端消息唯一编号
		MessageId   [2]byte // 消息唯一id
		ChannelId   [2]byte // 频道id
		ChannelType [2]byte // 频道类型
		FromUid     [2]byte // 发送者uid
		End         [2]byte // 流是否正常结束
		EndReason   [2]byte // 流结束原因
		Payload     [2]byte // 流数据
		Error       [2]byte // 错误信息
	}
}{
	Id:        [2]byte{0x17, 0x01},
	Size:      2 + 2 + 8 + 2,     // tableId + dataType  + primaryKey + columnKey
	IndexSize: 2 + 2 + 2 + 8 + 8, // tableId + dataType + indexName + columnHash + seq
	Column: struct {
		ClientMsgNo [2]byte
		MessageId   [2]byte
		ChannelId   [2]byte
		ChannelType [2]byte
		FromUid     [2]byte
		End         [2]byte
		EndReason   [2]byte
		Payload     [2]byte
		Error       [2]byte
	}{
		ClientMsgNo: [2]byte{0x17, 0x01},
		MessageId:   [2]byte{0x17, 0x02},
		ChannelId:   [2]byte{0x17, 0x03},
		ChannelType: [2]byte{0x17, 0x04},
		FromUid:     [2]byte{0x17, 0x05},
		End:         [2]byte{0x17, 0x06},
		EndReason:   [2]byte{0x17, 0x07},
		Payload:     [2]byte{0x17, 0x08},
		Error:       [2]byte{0x17, 0x09},
	},
}
