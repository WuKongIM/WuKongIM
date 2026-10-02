package meta

import (
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// Column, family and index IDs are permanent durable identifiers.
var mqttSourceBindingTable = registerMetaTable(TableSpec[MQTTSourceBinding]{
	ID: TableIDMQTTSourceBinding, Name: "mqtt_source_binding",
	Columns: []schema.Column{
		{ID: 1, Name: "owner_kind", Type: schema.TypeUint8, Required: true},
		{ID: 2, Name: "owner_id", Type: schema.TypeString, Required: true},
		{ID: 3, Name: "owner_generation", Type: schema.TypeString, Required: true},
		{ID: 4, Name: "broker_namespace", Type: schema.TypeString, Required: true},
		{ID: 5, Name: "client_id", Type: schema.TypeString, Required: true},
		{ID: 6, Name: "session_generation", Type: schema.TypeUint64, Required: true},
		{ID: 7, Name: "subscription_generation", Type: schema.TypeUint64, Required: true},
		{ID: 8, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: 9, Name: "topic", Type: schema.TypeString, Required: true},
		{ID: 10, Name: "revision", Type: schema.TypeUint64, Required: true},
		{ID: 11, Name: "intent_revision", Type: schema.TypeUint64, Required: true},
		{ID: 12, Name: "progress_revision", Type: schema.TypeUint64, Required: true},
		{ID: 13, Name: "authorization_version", Type: schema.TypeUint64, Required: true},
		{ID: 14, Name: "operation_id", Type: schema.TypeString, Required: true},
		{ID: 15, Name: "stage", Type: schema.TypeUint8, Required: true},
		{ID: 16, Name: "boundary_known", Type: schema.TypeUint8, Required: true},
		{ID: 17, Name: "start_after", Type: schema.TypeUint64, Required: true},
		{ID: 18, Name: "completed_through", Type: schema.TypeUint64, Required: true},
		{ID: 19, Name: "end_known", Type: schema.TypeUint8, Required: true},
		{ID: 20, Name: "end_through", Type: schema.TypeUint64, Required: true},
		{ID: 21, Name: "release_reason", Type: schema.TypeUint8, Required: true},
		{ID: 22, Name: "discovery_after_channel_id", Type: schema.TypeString, Required: true},
		{ID: 23, Name: "discovery_after_channel_type", Type: schema.TypeUint8, Required: true},
		{ID: 24, Name: "discovery_done", Type: schema.TypeUint8, Required: true},
		{ID: 25, Name: "recovery_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 26, Name: "updated_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 27, Name: "protection_revision", Type: schema.TypeUint64, Required: true},
		{ID: 28, Name: "retention_floor", Type: schema.TypeUint64},
		{ID: 29, Name: "drain_version", Type: schema.TypeUint8},
		{ID: 30, Name: "drain_after_source_id", Type: schema.TypeString},
		{ID: 31, Name: "drain_after_source_generation", Type: schema.TypeString},
		{ID: 32, Name: "drain_done", Type: schema.TypeUint8},
	},
	Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 29, 30, 31, 32}}},
	Primary:  PrimarySpec[MQTTSourceBinding]{IndexID: 1, Name: "pk_mqtt_source_binding", Columns: []uint16{1, 2, 3, 4, 5, 6, 7}, Layout: KeyLayout{KeyUint8, KeyString, KeyString, KeyString, KeyString, KeyUint64, KeyUint64}, Key: func(r MQTTSourceBinding) KeyParts { return mqttSourceBindingPrimaryKey(r.Key) }},
	Indexes: []IndexSpec[MQTTSourceBinding]{
		{ID: 2, Name: "idx_mqtt_source_binding_candidate", Columns: []uint16{1, 2, 3, 4, 5, 6, 7}, Layout: KeyLayout{KeyUint8, KeyString, KeyString, KeyString, KeyString, KeyUint64, KeyUint64}, CorruptIndexKeyIsError: true, Key: func(r MQTTSourceBinding) (KeyParts, bool) {
			return mqttSourceBindingPrimaryKey(r.Key), r.Stage == MQTTBindingPreparing || r.Stage == MQTTBindingActive
		}},
		{ID: 3, Name: "idx_mqtt_source_binding_recovery", Columns: []uint16{25, 1, 2, 3, 4, 5, 6, 7}, Layout: KeyLayout{KeyInt64Ordered, KeyUint8, KeyString, KeyString, KeyString, KeyString, KeyUint64, KeyUint64}, CorruptIndexKeyIsError: true, Key: func(r MQTTSourceBinding) (KeyParts, bool) {
			// Removed rows stay indexed only while scheduled for tombstone retirement.
			return append(KeyParts{Int64Ordered(r.RecoveryAtMS)}, mqttSourceBindingPrimaryKey(r.Key)...), r.Stage != MQTTBindingRemoved || r.RecoveryAtMS > 0
		}},
		{ID: 4, Name: "idx_mqtt_source_binding_retention", Columns: []uint16{1, 2, 3, 28, 4, 5, 6, 7}, Layout: KeyLayout{KeyUint8, KeyString, KeyString, KeyUint64, KeyString, KeyString, KeyUint64, KeyUint64}, CorruptIndexKeyIsError: true, Key: func(r MQTTSourceBinding) (KeyParts, bool) {
			return mqttSourceBindingRetentionParts(r.Key, r.CompletedThrough), r.Key.Owner.Kind == MQTTBindingChannel && r.Stage != MQTTBindingRemoved
		}},
	},
	Validate:           ValidateMQTTSourceBinding,
	EncodeValueWithKey: encodeMQTTSourceBindingRow,
	DecodeValueWithKey: decodeMQTTSourceBindingRow,
})

// MQTTSourceBindingTable exposes source projections to schema and snapshot tools.
var MQTTSourceBindingTable = mqttSourceBindingTable.Schema()

func encodeMQTTSourceBindingRow(key []byte, r MQTTSourceBinding) ([]byte, error) {
	if err := ValidateMQTTSourceBinding(r); err != nil {
		return nil, err
	}
	var w rowcodec.Writer
	_ = w.String(8, r.UID)
	_ = w.String(9, r.Topic)
	_ = w.Uint64(10, r.Revision)
	_ = w.Uint64(11, r.IntentRevision)
	_ = w.Uint64(12, r.ProgressRevision)
	_ = w.Uint64(13, r.AuthorizationVersion)
	_ = w.String(14, r.OperationID)
	_ = w.Uint8(15, uint8(r.Stage))
	var b16 uint8
	if r.BoundaryKnown {
		b16 = 1
	}
	_ = w.Uint8(16, b16)
	_ = w.Uint64(17, r.StartAfter)
	_ = w.Uint64(18, r.CompletedThrough)
	var b19 uint8
	if r.EndKnown {
		b19 = 1
	}
	_ = w.Uint8(19, b19)
	_ = w.Uint64(20, r.EndThrough)
	_ = w.Uint8(21, uint8(r.ReleaseReason))
	_ = w.String(22, r.DiscoveryAfterChannelID)
	_ = w.Uint8(23, r.DiscoveryAfterChannelType)
	var b24 uint8
	if r.DiscoveryDone {
		b24 = 1
	}
	_ = w.Uint8(24, b24)
	_ = w.Int64(25, r.RecoveryAtMS)
	_ = w.Int64(26, r.UpdatedAtMS)
	_ = w.Uint64(27, r.ProtectionRevision)
	if r.DrainVersion != 0 {
		_ = w.Uint8(29, r.DrainVersion)
		_ = w.String(30, r.DrainAfterSourceID)
		_ = w.String(31, r.DrainAfterSourceGeneration)
		var done uint8
		if r.DrainDone {
			done = 1
		}
		_ = w.Uint8(32, done)
	}
	return rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes()), nil
}

func decodeMQTTSourceBindingRow(key []byte, pk KeyParts, value []byte) (MQTTSourceBinding, error) {
	var r MQTTSourceBinding
	if len(value) > 16<<10 || len(pk) != 7 {
		return r, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return r, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecColumns || env.Flags != rowcodec.FlagChecksum {
		return r, dberrors.ErrCorruptValue
	}
	// Reject noncanonical physical UID keys instead of accepting two encodings
	// for the same logical binding during snapshot/inspection decoding.
	if pk[0].U8 == uint8(MQTTBindingUID) && pk[2].S != "\x00" {
		return r, dberrors.ErrCorruptValue
	}
	r.Key = mqttSourceBindingKeyFromParts(pk)
	s := rowcodec.NewBorrowedScanner(env.Payload)
	var seen uint32
	var drainSeen uint8
	var last uint16
	for s.Next() {
		id := s.ColumnID()
		if id <= last {
			return MQTTSourceBinding{}, dberrors.ErrCorruptValue
		}
		last = id
		if id >= 8 && id <= 27 {
			seen |= 1 << (id - 8)
		}
		if id >= 29 && id <= 32 {
			drainSeen |= 1 << (id - 29)
		}
		switch id {
		case 8:
			r.UID, err = s.String()
		case 9:
			r.Topic, err = s.String()
		case 10:
			r.Revision, err = s.Uint64()
		case 11:
			r.IntentRevision, err = s.Uint64()
		case 12:
			r.ProgressRevision, err = s.Uint64()
		case 13:
			r.AuthorizationVersion, err = s.Uint64()
		case 14:
			r.OperationID, err = s.String()
		case 15:
			var n uint8
			n, err = s.Uint8()
			r.Stage = MQTTBindingStage(n)
		case 16:
			var n uint8
			n, err = s.Uint8()
			if n > 1 {
				return MQTTSourceBinding{}, dberrors.ErrCorruptValue
			}
			r.BoundaryKnown = n == 1
		case 17:
			r.StartAfter, err = s.Uint64()
		case 18:
			r.CompletedThrough, err = s.Uint64()
		case 19:
			var n uint8
			n, err = s.Uint8()
			if n > 1 {
				return MQTTSourceBinding{}, dberrors.ErrCorruptValue
			}
			r.EndKnown = n == 1
		case 20:
			r.EndThrough, err = s.Uint64()
		case 21:
			var n uint8
			n, err = s.Uint8()
			r.ReleaseReason = MQTTBindingReleaseReason(n)
		case 22:
			r.DiscoveryAfterChannelID, err = s.String()
		case 23:
			r.DiscoveryAfterChannelType, err = s.Uint8()
		case 24:
			var n uint8
			n, err = s.Uint8()
			if n > 1 {
				return MQTTSourceBinding{}, dberrors.ErrCorruptValue
			}
			r.DiscoveryDone = n == 1
		case 25:
			r.RecoveryAtMS, err = s.Int64()
		case 26:
			r.UpdatedAtMS, err = s.Int64()
		case 27:
			r.ProtectionRevision, err = s.Uint64()
		case 29:
			r.DrainVersion, err = s.Uint8()
		case 30:
			r.DrainAfterSourceID, err = s.String()
		case 31:
			r.DrainAfterSourceGeneration, err = s.String()
		case 32:
			var n uint8
			n, err = s.Uint8()
			if n > 1 {
				return MQTTSourceBinding{}, dberrors.ErrCorruptValue
			}
			r.DrainDone = n == 1
		}
		if err != nil {
			return MQTTSourceBinding{}, err
		}
	}
	if s.Err() != nil || seen != (1<<20)-1 || drainSeen != 0 && (drainSeen != 15 || r.DrainVersion != 1) || ValidateMQTTSourceBinding(r) != nil {
		return MQTTSourceBinding{}, dberrors.ErrCorruptValue
	}
	return r, nil
}

func inspectMQTTSourceBindingRow(r MQTTSourceBinding) InspectRow {
	return InspectRow{
		"owner_kind":                    uint8(r.Key.Owner.Kind),
		"owner_id":                      r.Key.Owner.ID,
		"owner_generation":              r.Key.Owner.Generation,
		"broker_namespace":              r.Key.Namespace,
		"client_id":                     r.Key.ClientID,
		"session_generation":            r.Key.SessionGeneration,
		"subscription_generation":       r.Key.SubscriptionGeneration,
		"uid":                           r.UID,
		"topic":                         r.Topic,
		"revision":                      r.Revision,
		"intent_revision":               r.IntentRevision,
		"progress_revision":             r.ProgressRevision,
		"authorization_version":         r.AuthorizationVersion,
		"operation_id":                  r.OperationID,
		"stage":                         uint8(r.Stage),
		"boundary_known":                r.BoundaryKnown,
		"start_after":                   r.StartAfter,
		"completed_through":             r.CompletedThrough,
		"end_known":                     r.EndKnown,
		"end_through":                   r.EndThrough,
		"release_reason":                uint8(r.ReleaseReason),
		"discovery_after_channel_id":    r.DiscoveryAfterChannelID,
		"discovery_after_channel_type":  r.DiscoveryAfterChannelType,
		"discovery_done":                r.DiscoveryDone,
		"recovery_at_ms":                r.RecoveryAtMS,
		"updated_at_ms":                 r.UpdatedAtMS,
		"protection_revision":           r.ProtectionRevision,
		"retention_floor":               r.CompletedThrough,
		"drain_version":                 r.DrainVersion,
		"drain_after_source_id":         r.DrainAfterSourceID,
		"drain_after_source_generation": r.DrainAfterSourceGeneration,
		"drain_done":                    r.DrainDone,
	}
}
