package meta

import (
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// MessageUpdate is a channel-owned durable message-edit record.
type MessageUpdate struct {
	// PendingAfterUID is the subscriber cursor for this exact version; only pending rows use it.
	PendingAfterUID string `json:"pending_after_uid"`
	// ChannelID is the canonical channel identity and Hash Slot routing key.
	ChannelID string `json:"channel_id"`
	// ChannelType distinguishes channel kinds sharing an ID.
	ChannelType int64 `json:"channel_type"`
	// MessageID identifies the immutable original message.
	MessageID uint64 `json:"message_id"`
	// MessageSeq is the original ordering/retention position, never an edit cursor.
	MessageSeq uint64 `json:"message_seq"`
	// Version is the monotonically increasing replacement version for one message.
	Version uint64 `json:"version"`
	// UpdateSeq is the channel-wide edit position, independent of MessageSeq.
	UpdateSeq uint64 `json:"update_seq"`
	// Payload is the complete opaque replacement; pending rows always leave it empty.
	Payload []byte `json:"payload"`
	// UpdatedAtMS is server wall time for presentation, never a consistency cursor.
	UpdatedAtMS int64 `json:"updated_at_ms"`
	// Pending marks body-free notification rows; latest-payload rows leave it zero.
	Pending uint8 `json:"pending"`
}

var messageUpdateSpec = TableSpec[MessageUpdate]{
	ID: TableIDMessageUpdate, Name: "message_update",
	Columns: []schema.Column{
		{ID: 1, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: 2, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: 3, Name: "message_id", Type: schema.TypeUint64, Required: true},
		{ID: 4, Name: "message_seq", Type: schema.TypeUint64, Required: false},
		{ID: 5, Name: "version", Type: schema.TypeUint64, Required: false},
		{ID: 6, Name: "update_seq", Type: schema.TypeUint64, Required: false},
		{ID: 7, Name: "payload", Type: schema.TypeBytes, Required: false},
		{ID: 8, Name: "updated_at_ms", Type: schema.TypeInt64, Required: false},
		{ID: 9, Name: "pending", Type: schema.TypeUint8, Required: false},
		{ID: 10, Name: "pending_after_uid", Type: schema.TypeString},
	},
	Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{4, 5, 6, 7, 8, 9, 10}}},
	Primary: PrimarySpec[MessageUpdate]{IndexID: 1, Name: "pk_message_update", Columns: []uint16{1, 2, 3}, Layout: KeyLayout{KeyString, KeyInt64Ordered, KeyUint64}, Key: func(r MessageUpdate) KeyParts {
		return KeyParts{String(r.ChannelID), Int64Ordered(r.ChannelType), Uint64(r.MessageID)}
	}},
	Indexes: []IndexSpec[MessageUpdate]{
		{ID: 2, Name: "idx_message_update_sequence", Columns: []uint16{1, 2, 6}, Layout: KeyLayout{KeyString, KeyInt64Ordered, KeyUint64}, CorruptIndexKeyIsError: true, Key: func(r MessageUpdate) (KeyParts, bool) {
			return KeyParts{String(r.ChannelID), Int64Ordered(r.ChannelType), Uint64(r.UpdateSeq)}, true
		}},
		{ID: 3, Name: "idx_message_update_pending", Columns: []uint16{1, 2, 3}, Layout: KeyLayout{KeyString, KeyInt64Ordered, KeyUint64}, CorruptIndexKeyIsError: true, Key: func(r MessageUpdate) (KeyParts, bool) {
			return KeyParts{String(r.ChannelID), Int64Ordered(r.ChannelType), Uint64(r.MessageID)}, r.Pending != 0
		}},
		{ID: 4, Name: "idx_message_update_retention", Columns: []uint16{1, 2, 4}, Layout: KeyLayout{KeyString, KeyInt64Ordered, KeyUint64}, CorruptIndexKeyIsError: true, Key: func(r MessageUpdate) (KeyParts, bool) {
			return KeyParts{String(r.ChannelID), Int64Ordered(r.ChannelType), Uint64(r.MessageSeq)}, true
		}},
	},
	Validate: func(r MessageUpdate) error {
		return validateChannelKey(ChannelKey{ChannelID: r.ChannelID, ChannelType: r.ChannelType})
	},
	EncodeValue: func(r MessageUpdate) ([]byte, error) {
		var value []byte
		value = appendValueUint64(value, r.MessageSeq)
		value = appendValueUint64(value, r.Version)
		value = appendValueUint64(value, r.UpdateSeq)
		value = appendValueBytes(value, r.Payload)
		value = appendValueInt64(value, r.UpdatedAtMS)
		value = append(value, r.Pending)
		value = appendValueString(value, r.PendingAfterUID)
		return value, nil
	},
	DecodeValue: func(pk KeyParts, value []byte) (MessageUpdate, error) {
		r := MessageUpdate{ChannelID: pk[0].S, ChannelType: pk[1].I64, MessageID: pk[2].U64}
		var err error
		r.MessageSeq, value, err = readValueUint64(value)
		if err != nil {
			return r, err
		}
		r.Version, value, err = readValueUint64(value)
		if err != nil {
			return r, err
		}
		r.UpdateSeq, value, err = readValueUint64(value)
		if err != nil {
			return r, err
		}
		r.Payload, value, err = readValueBytes(value)
		if err != nil {
			return r, err
		}
		r.UpdatedAtMS, value, err = readValueInt64(value)
		if err != nil {
			return r, err
		}
		if len(value) < 1 {
			return r, dberrors.ErrCorruptValue
		}
		r.Pending = value[0]
		value = value[1:]
		r.PendingAfterUID, value, err = readValueString(value)
		if err != nil {
			return r, err
		}
		if len(value) != 0 {
			return r, dberrors.ErrCorruptValue
		}
		return r, nil
	},
}

var messageUpdateTable = registerMetaTable(func() TableSpec[MessageUpdate] {
	spec := messageUpdateSpec
	spec.Indexes = []IndexSpec[MessageUpdate]{messageUpdateSpec.Indexes[0], messageUpdateSpec.Indexes[2]}
	return spec
}())

// MessageUpdateHead is a channel-owned durable message-edit record.
type MessageUpdateHead struct {
	// ReplicaSet records the exact Slot replica IDs that passed edit-protocol activation.
	ReplicaSet string `json:"replica_set"`
	// ChannelID is the canonical channel identity and Hash Slot routing key.
	ChannelID string `json:"channel_id"`
	// ChannelType distinguishes channel kinds sharing an ID.
	ChannelType int64 `json:"channel_type"`
	// Generation is an opaque incarnation that changes after channel recreation.
	Generation string `json:"generation"`
	// UpdateSeq is the channel-wide edit position, independent of MessageSeq.
	UpdateSeq uint64 `json:"update_seq"`
}

var messageUpdateHeadTable = registerMetaTable(TableSpec[MessageUpdateHead]{
	ID: TableIDMessageUpdateHead, Name: "message_update_head",
	Columns: []schema.Column{
		{ID: 1, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: 2, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: 3, Name: "generation", Type: schema.TypeString, Required: false},
		{ID: 4, Name: "update_seq", Type: schema.TypeUint64, Required: false},
		{ID: 5, Name: "replica_set", Type: schema.TypeString},
	},
	Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{3, 4, 5}}},
	Primary:  PrimarySpec[MessageUpdateHead]{IndexID: 1, Name: "pk_message_update_head", Columns: []uint16{1, 2}, Layout: KeyLayout{KeyString, KeyInt64Ordered}, Key: func(r MessageUpdateHead) KeyParts { return KeyParts{String(r.ChannelID), Int64Ordered(r.ChannelType)} }},
	Validate: func(r MessageUpdateHead) error {
		return validateChannelKey(ChannelKey{ChannelID: r.ChannelID, ChannelType: r.ChannelType})
	},
	EncodeValue: func(r MessageUpdateHead) ([]byte, error) {
		var value []byte
		value = appendValueString(value, r.Generation)
		value = appendValueUint64(value, r.UpdateSeq)
		value = appendValueString(value, r.ReplicaSet)
		return value, nil
	},
	DecodeValue: func(pk KeyParts, value []byte) (MessageUpdateHead, error) {
		r := MessageUpdateHead{ChannelID: pk[0].S, ChannelType: pk[1].I64}
		var err error
		r.Generation, value, err = readValueString(value)
		if err != nil {
			return r, err
		}
		r.UpdateSeq, value, err = readValueUint64(value)
		if err != nil {
			return r, err
		}
		r.ReplicaSet, value, err = readValueString(value)
		if err != nil {
			return r, err
		}
		if len(value) != 0 {
			return r, dberrors.ErrCorruptValue
		}
		return r, nil
	},
})

// MessageUpdateRequest is a channel-owned durable message-edit record.
type MessageUpdateRequest struct {
	// ChannelID is the canonical channel identity and Hash Slot routing key.
	ChannelID string `json:"channel_id"`
	// ChannelType distinguishes channel kinds sharing an ID.
	ChannelType int64 `json:"channel_type"`
	// MessageID identifies the immutable original message.
	MessageID uint64 `json:"message_id"`
	// RequestID scopes idempotent retries to this target message.
	RequestID string `json:"request_id"`
	// Digest hashes expected version and payload, excluding generated time and route guards.
	Digest string `json:"digest"`
	// MessageSeq is the original ordering/retention position, never an edit cursor.
	MessageSeq uint64 `json:"message_seq"`
	// Version is the monotonically increasing replacement version for one message.
	Version uint64 `json:"version"`
	// UpdateSeq is the channel-wide edit position, independent of MessageSeq.
	UpdateSeq uint64 `json:"update_seq"`
	// UpdatedAtMS is server wall time for presentation, never a consistency cursor.
	UpdatedAtMS int64 `json:"updated_at_ms"`
}

var messageUpdateRequestTable = registerMetaTable(TableSpec[MessageUpdateRequest]{
	ID: TableIDMessageUpdateRequest, Name: "message_update_request",
	Columns: []schema.Column{
		{ID: 1, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: 2, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: 3, Name: "message_id", Type: schema.TypeUint64, Required: true},
		{ID: 4, Name: "request_id", Type: schema.TypeString, Required: true},
		{ID: 5, Name: "digest", Type: schema.TypeString, Required: false},
		{ID: 6, Name: "message_seq", Type: schema.TypeUint64, Required: false},
		{ID: 7, Name: "version", Type: schema.TypeUint64, Required: false},
		{ID: 8, Name: "update_seq", Type: schema.TypeUint64, Required: false},
		{ID: 9, Name: "updated_at_ms", Type: schema.TypeInt64, Required: false},
	},
	Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{5, 6, 7, 8, 9}}},
	Primary: PrimarySpec[MessageUpdateRequest]{IndexID: 1, Name: "pk_message_update_request", Columns: []uint16{1, 2, 3, 4}, Layout: KeyLayout{KeyString, KeyInt64Ordered, KeyUint64, KeyString}, Key: func(r MessageUpdateRequest) KeyParts {
		return KeyParts{String(r.ChannelID), Int64Ordered(r.ChannelType), Uint64(r.MessageID), String(r.RequestID)}
	}},
	Validate: func(r MessageUpdateRequest) error {
		return validateChannelKey(ChannelKey{ChannelID: r.ChannelID, ChannelType: r.ChannelType})
	},
	EncodeValue: func(r MessageUpdateRequest) ([]byte, error) {
		var value []byte
		value = appendValueString(value, r.Digest)
		value = appendValueUint64(value, r.MessageSeq)
		value = appendValueUint64(value, r.Version)
		value = appendValueUint64(value, r.UpdateSeq)
		value = appendValueInt64(value, r.UpdatedAtMS)
		return value, nil
	},
	DecodeValue: func(pk KeyParts, value []byte) (MessageUpdateRequest, error) {
		r := MessageUpdateRequest{ChannelID: pk[0].S, ChannelType: pk[1].I64, MessageID: pk[2].U64, RequestID: pk[3].S}
		var err error
		r.Digest, value, err = readValueString(value)
		if err != nil {
			return r, err
		}
		r.MessageSeq, value, err = readValueUint64(value)
		if err != nil {
			return r, err
		}
		r.Version, value, err = readValueUint64(value)
		if err != nil {
			return r, err
		}
		r.UpdateSeq, value, err = readValueUint64(value)
		if err != nil {
			return r, err
		}
		r.UpdatedAtMS, value, err = readValueInt64(value)
		if err != nil {
			return r, err
		}
		if len(value) != 0 {
			return r, dberrors.ErrCorruptValue
		}
		return r, nil
	},
})

// messageUpdatePendingTable shares target identity and version encoding, but
// always stores an empty Payload. Progress writes never rewrite message bodies.
var messageUpdatePendingTable = registerMetaTable(func() TableSpec[MessageUpdate] {
	spec := messageUpdateSpec
	spec.ID = TableIDMessageUpdatePending
	spec.Name = "message_update_pending"
	spec.Primary.Name = "pk_message_update_pending"
	spec.Indexes = []IndexSpec[MessageUpdate]{messageUpdateSpec.Indexes[1]}
	return spec
}())
