package store

// ColumnType describes the encoded value type stored for a column.
type ColumnType int

const (
	ColumnString ColumnType = iota + 1
	ColumnInt64
	ColumnUint64
	ColumnBytes
)

const (
	TableIDMessage uint32 = 1

	messagePrimaryFamilyID uint16 = 0
	messagePayloadFamilyID uint16 = 1

	messagePrimaryIndexID            uint16 = 1
	messageIndexIDMessageID          uint16 = 2
	messageIndexIDClientMsgNo        uint16 = 3
	messageIndexIDFromUIDClientMsgNo uint16 = 4

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
	messageColumnIDPayload     uint16 = 18
)

// ColumnDesc defines one logical column in a table schema.
type ColumnDesc struct {
	// ID is the stable encoded identifier stored in family payloads and indexes.
	ID uint16
	// Name is the human-readable schema name used by tests and diagnostics.
	Name string
	// Type describes how the column value is encoded on disk.
	Type ColumnType
	// Nullable reports whether empty values are allowed for this column.
	Nullable bool
}

// ColumnFamilyDesc groups encoded columns into one value family.
type ColumnFamilyDesc struct {
	// ID is the stable family identifier used in state keys.
	ID uint16
	// Name is the human-readable family name.
	Name string
	// ColumnIDs preserves the encoded column order inside this family payload.
	ColumnIDs []uint16
	// DefaultColumnID is the family's representative column for metadata and scans.
	DefaultColumnID uint16
}

// IndexDesc defines one primary or secondary index layout.
type IndexDesc struct {
	// ID is the stable identifier encoded into index keys.
	ID uint16
	// Name is the human-readable index name.
	Name string
	// Unique reports whether the index enforces unique keys.
	Unique bool
	// ColumnIDs preserves the indexed column order.
	ColumnIDs []uint16
	// Primary reports whether this index is the table primary key.
	Primary bool
}

// TableDesc defines the structured message table schema.
type TableDesc struct {
	// ID is the stable table identifier encoded into state/index keys.
	ID uint32
	// Name is the human-readable table name.
	Name string
	// Columns lists every logical column defined by the table.
	Columns []ColumnDesc
	// Families groups encoded column payloads by storage family.
	Families []ColumnFamilyDesc
	// PrimaryIndex describes the table primary key.
	PrimaryIndex IndexDesc
	// SecondaryIndexes lists all auxiliary lookup indexes.
	SecondaryIndexes []IndexDesc
}

// messageTableDesc is the canonical in-package schema for persisted message rows.
var messageTableDesc = TableDesc{
	ID:   TableIDMessage,
	Name: "message",
	Columns: []ColumnDesc{
		{ID: messageColumnIDMessageSeq, Name: "message_seq", Type: ColumnUint64},
		{ID: messageColumnIDMessageID, Name: "message_id", Type: ColumnUint64},
		{ID: messageColumnIDFramerFlags, Name: "framer_flags", Type: ColumnUint64},
		{ID: messageColumnIDSetting, Name: "setting", Type: ColumnUint64},
		{ID: messageColumnIDStreamFlag, Name: "stream_flag", Type: ColumnUint64},
		{ID: messageColumnIDMsgKey, Name: "msg_key", Type: ColumnString},
		{ID: messageColumnIDExpire, Name: "expire", Type: ColumnUint64},
		{ID: messageColumnIDClientSeq, Name: "client_seq", Type: ColumnUint64},
		{ID: messageColumnIDClientMsgNo, Name: "client_msg_no", Type: ColumnString, Nullable: true},
		{ID: messageColumnIDStreamNo, Name: "stream_no", Type: ColumnString, Nullable: true},
		{ID: messageColumnIDStreamID, Name: "stream_id", Type: ColumnUint64},
		{ID: messageColumnIDTimestamp, Name: "timestamp", Type: ColumnInt64},
		{ID: messageColumnIDChannelID, Name: "channel_id", Type: ColumnString},
		{ID: messageColumnIDChannelType, Name: "channel_type", Type: ColumnUint64},
		{ID: messageColumnIDTopic, Name: "topic", Type: ColumnString, Nullable: true},
		{ID: messageColumnIDFromUID, Name: "from_uid", Type: ColumnString, Nullable: true},
		{ID: messageColumnIDPayloadHash, Name: "payload_hash", Type: ColumnUint64},
		{ID: messageColumnIDPayload, Name: "payload", Type: ColumnBytes, Nullable: true},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:   messagePrimaryFamilyID,
			Name: "primary",
			ColumnIDs: []uint16{
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
			},
			DefaultColumnID: messageColumnIDMessageID,
		},
		{
			ID:              messagePayloadFamilyID,
			Name:            "payload",
			ColumnIDs:       []uint16{messageColumnIDPayload},
			DefaultColumnID: messageColumnIDPayload,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        messagePrimaryIndexID,
		Name:      "pk_message",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{messageColumnIDMessageSeq},
	},
	SecondaryIndexes: []IndexDesc{
		{
			ID:        messageIndexIDMessageID,
			Name:      "uidx_message_id",
			Unique:    true,
			ColumnIDs: []uint16{messageColumnIDMessageID},
		},
		{
			ID:        messageIndexIDClientMsgNo,
			Name:      "idx_client_msg_no",
			ColumnIDs: []uint16{messageColumnIDClientMsgNo, messageColumnIDMessageSeq},
		},
		{
			ID:        messageIndexIDFromUIDClientMsgNo,
			Name:      "uidx_from_uid_client_msg_no",
			Unique:    true,
			ColumnIDs: []uint16{messageColumnIDFromUID, messageColumnIDClientMsgNo},
		},
	},
}

// MessageTable returns a deep copy of the message table schema descriptor.
func MessageTable() TableDesc {
	return cloneTableDesc(messageTableDesc)
}

func canonicalMessageTable() *TableDesc {
	return &messageTableDesc
}

func cloneTableDesc(desc TableDesc) TableDesc {
	cloned := desc
	cloned.Columns = append([]ColumnDesc(nil), desc.Columns...)
	cloned.Families = make([]ColumnFamilyDesc, len(desc.Families))
	for i, family := range desc.Families {
		cloned.Families[i] = family
		cloned.Families[i].ColumnIDs = append([]uint16(nil), family.ColumnIDs...)
	}
	cloned.PrimaryIndex = cloneIndexDesc(desc.PrimaryIndex)
	cloned.SecondaryIndexes = make([]IndexDesc, len(desc.SecondaryIndexes))
	for i, index := range desc.SecondaryIndexes {
		cloned.SecondaryIndexes[i] = cloneIndexDesc(index)
	}
	return cloned
}

func cloneIndexDesc(index IndexDesc) IndexDesc {
	cloned := index
	cloned.ColumnIDs = append([]uint16(nil), index.ColumnIDs...)
	return cloned
}
