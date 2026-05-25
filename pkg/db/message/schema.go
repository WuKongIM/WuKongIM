package message

import "github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"

const (
	// TableIDMessage is the channel message table ID.
	TableIDMessage uint32 = 1

	messageHeaderFamilyID  uint16 = 0
	messagePayloadFamilyID uint16 = 1

	messagePrimaryIndexID            uint16 = 1
	messageIndexIDMessageID          uint16 = 2
	messageIndexIDClientMsgNo        uint16 = 3
	messageIndexIDFromUIDClientMsgNo uint16 = 4

	messageSystemIDCheckpoint uint16 = 1
	messageSystemIDHistory    uint16 = 2
	messageSystemIDSnapshot   uint16 = 3
	messageSystemIDRetention  uint16 = 4
	messageSystemIDCursor     uint16 = 5

	messageColumnIDMessageSeq  uint16 = 1
	messageColumnIDMessageID   uint16 = 2
	messageColumnIDFramerFlags uint16 = 3
	messageColumnIDSetting     uint16 = 4
	messageColumnIDStreamFlag  uint16 = 5
	messageColumnIDMsgKey      uint16 = 6
	messageColumnIDExpire      uint16 = 7
	messageColumnIDClientSeq   uint16 = 8
	messageColumnIDClientMsgNo uint16 = 9
	messageColumnIDStreamNo    uint16 = 10
	messageColumnIDStreamID    uint16 = 11
	messageColumnIDTimestamp   uint16 = 12
	messageColumnIDChannelID   uint16 = 13
	messageColumnIDChannelType uint16 = 14
	messageColumnIDTopic       uint16 = 15
	messageColumnIDFromUID     uint16 = 16
	messageColumnIDPayloadHash uint16 = 17
	messageColumnIDPayloadSize uint16 = 18
	messageColumnIDPayload     uint16 = 19
)

// MessageTable describes the persisted channel message row schema.
var MessageTable = schema.Table{
	ID:   TableIDMessage,
	Name: "message",
	Columns: []schema.Column{
		{ID: messageColumnIDMessageSeq, Name: "message_seq", Type: schema.TypeUint64, Required: true},
		{ID: messageColumnIDMessageID, Name: "message_id", Type: schema.TypeUint64, Required: true},
		{ID: messageColumnIDFramerFlags, Name: "framer_flags", Type: schema.TypeUint8},
		{ID: messageColumnIDSetting, Name: "setting", Type: schema.TypeUint8},
		{ID: messageColumnIDStreamFlag, Name: "stream_flag", Type: schema.TypeUint8},
		{ID: messageColumnIDMsgKey, Name: "msg_key", Type: schema.TypeString},
		{ID: messageColumnIDExpire, Name: "expire", Type: schema.TypeUint64},
		{ID: messageColumnIDClientSeq, Name: "client_seq", Type: schema.TypeUint64},
		{ID: messageColumnIDClientMsgNo, Name: "client_msg_no", Type: schema.TypeString},
		{ID: messageColumnIDStreamNo, Name: "stream_no", Type: schema.TypeString},
		{ID: messageColumnIDStreamID, Name: "stream_id", Type: schema.TypeUint64},
		{ID: messageColumnIDTimestamp, Name: "timestamp", Type: schema.TypeInt64},
		{ID: messageColumnIDChannelID, Name: "channel_id", Type: schema.TypeString},
		{ID: messageColumnIDChannelType, Name: "channel_type", Type: schema.TypeUint8},
		{ID: messageColumnIDTopic, Name: "topic", Type: schema.TypeString},
		{ID: messageColumnIDFromUID, Name: "from_uid", Type: schema.TypeString},
		{ID: messageColumnIDPayloadHash, Name: "payload_hash", Type: schema.TypeUint64},
		{ID: messageColumnIDPayloadSize, Name: "payload_size", Type: schema.TypeUint64},
		{ID: messageColumnIDPayload, Name: "payload", Type: schema.TypeBytes},
	},
	Families: []schema.Family{
		{
			ID:   messageHeaderFamilyID,
			Name: "header",
			Columns: []uint16{
				messageColumnIDMessageID,
				messageColumnIDFramerFlags,
				messageColumnIDSetting,
				messageColumnIDStreamFlag,
				messageColumnIDMsgKey,
				messageColumnIDExpire,
				messageColumnIDClientSeq,
				messageColumnIDClientMsgNo,
				messageColumnIDStreamNo,
				messageColumnIDStreamID,
				messageColumnIDTimestamp,
				messageColumnIDChannelID,
				messageColumnIDChannelType,
				messageColumnIDTopic,
				messageColumnIDFromUID,
				messageColumnIDPayloadHash,
				messageColumnIDPayloadSize,
			},
		},
		{
			ID:      messagePayloadFamilyID,
			Name:    "payload",
			Columns: []uint16{messageColumnIDPayload},
		},
	},
	Primary: schema.Index{
		ID:      messagePrimaryIndexID,
		Name:    "pk_message",
		Unique:  true,
		Primary: true,
		Columns: []uint16{messageColumnIDMessageSeq},
	},
	Indexes: []schema.Index{
		{ID: messageIndexIDMessageID, Name: "uidx_message_id", Unique: true, Columns: []uint16{messageColumnIDMessageID}},
		{ID: messageIndexIDClientMsgNo, Name: "idx_client_msg_no", Columns: []uint16{messageColumnIDClientMsgNo, messageColumnIDMessageSeq}},
		{ID: messageIndexIDFromUIDClientMsgNo, Name: "uidx_from_uid_client_msg_no", Unique: true, Columns: []uint16{messageColumnIDFromUID, messageColumnIDClientMsgNo}},
	},
}
