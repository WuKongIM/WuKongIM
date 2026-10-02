package message

import (
	"crypto/sha256"
	"encoding/binary"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	// TableIDMQTTReplay reserves a message-domain table, independent of meta IDs.
	TableIDMQTTReplay        uint32 = 2
	mqttReplayMaxBytes              = 16 << 20
	mqttReplayMaxRows               = 256
	mqttReplayContentVersion uint64 = 1
)

// MQTTReplayTable describes immutable, source-shared content and prefix counters.
var MQTTReplayTable = schema.Table{
	ID: TableIDMQTTReplay, Name: "mqtt_replay_message",
	Columns: []schema.Column{
		{ID: 1, Name: "source_generation", Type: schema.TypeString, Required: true},
		{ID: 2, Name: "source_position", Type: schema.TypeUint64, Required: true},
		{ID: 3, Name: "content_version", Type: schema.TypeUint64, Required: true},
		{ID: 4, Name: "message_id", Type: schema.TypeUint64, Required: true},
		{ID: 5, Name: "accounted_bytes", Type: schema.TypeUint64, Required: true},
		{ID: 6, Name: "total_bytes", Type: schema.TypeUint64, Required: true},
		{ID: 7, Name: "content_hash", Type: schema.TypeBytes, Required: true},
		{ID: 8, Name: "prefix_digest", Type: schema.TypeBytes, Required: true},
		{ID: 9, Name: "content", Type: schema.TypeBytes, Required: true},
		{ID: 10, Name: "total_stored_bytes", Type: schema.TypeUint64, Required: true},
	},
	Families: []schema.Family{{ID: 0, Name: "row", Columns: []uint16{4, 5, 6, 7, 8, 9, 10}}},
	Primary:  schema.Index{ID: 1, Name: "pk_mqtt_replay", Primary: true, Unique: true, Columns: []uint16{1, 2, 3}},
	Indexes:  []schema.Index{{ID: 2, Name: "idx_mqtt_replay_prefix", Unique: true, Columns: []uint16{1, 2}, Covering: []uint16{6, 8, 10}}},
}

// MQTTReplayRecord owns one original row envelope. It never enters the ordinary
// global MessageID index; consumers share the immutable source reference.
type MQTTReplayRecord struct {
	// Position is the original committed logical message sequence, not PacketID.
	Position uint64
	// ContentVersion identifies the immutable delivery representation in the key.
	ContentVersion uint64
	// MessageID remains the original business identity and is never reallocated.
	MessageID uint64
	// AccountedBytes counts original payload and publication metadata.
	AccountedBytes uint64
	// TotalBytes and TotalStoredBytes are source-prefix sums for bounded metering.
	TotalBytes       uint64
	TotalStoredBytes uint64
	// ContentHash binds source identity, position, version and canonical content.
	ContentHash [32]byte
	// Digest binds the preceding prefix and this complete source record.
	Digest [32]byte
	// Content uses the original message row codec and key checksum, with the
	// storage size hint canonicalized to the actual payload size.
	Content []byte
}

// MQTTReplayState records replica-local coverage, never a distributed receipt.
type MQTTReplayState struct {
	// Generation is the source log incarnation, independent of leader epochs.
	Generation string
	// StartAfter is immutable; later positions through Through are copied or
	// discharged by an independently committed materialized retirement baseline.
	StartAfter uint64
	Through    uint64
	// TotalBytes counts payload/properties; TotalStoredBytes counts row envelopes.
	TotalBytes       uint64
	TotalStoredBytes uint64
	// Digest identifies this local prefix, not a quorum or authorization proof.
	Digest [32]byte
}

func mqttReplayRowKey(key ChannelKey, generation string, position uint64) []byte {
	b := newMessageKey(key, 25+len(generation))
	b = append(b, byte(keycodec.SpaceRow))
	b = keycodec.AppendUint32(b, TableIDMQTTReplay)
	b = keycodec.AppendString(b, generation)
	b = keycodec.AppendUint64(b, position)
	b = keycodec.AppendUint64(b, mqttReplayContentVersion)
	return keycodec.AppendUint16(b, 0)
}

func mqttReplayStateKey(key ChannelKey) []byte {
	b := newMessageKey(key, 7)
	b = append(b, byte(keycodec.SpaceSystem))
	b = keycodec.AppendUint32(b, TableIDMQTTReplay)
	return keycodec.AppendUint16(b, 1)
}

func mqttReplayStateValid(s MQTTReplayState) bool {
	return (MQTTSourceState{Generation: s.Generation, Revision: 1}).valid() &&
		s.Through > s.StartAfter && s.TotalStoredBytes > 0 && s.TotalBytes <= s.TotalStoredBytes && s.Digest != [32]byte{}
}

func encodeMQTTReplayState(key ChannelKey, s MQTTReplayState) []byte {
	b := binary.BigEndian.AppendUint16(nil, uint16(len(s.Generation)))
	b = append(b, s.Generation...)
	for _, n := range []uint64{s.StartAfter, s.Through, s.TotalBytes, s.TotalStoredBytes} {
		b = binary.BigEndian.AppendUint64(b, n)
	}
	b = append(b, s.Digest[:]...)
	return rowcodec.Wrap(mqttReplayStateKey(key), 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, b)
}

func decodeMQTTReplayState(key ChannelKey, value []byte) (MQTTReplayState, error) {
	if len(value) > rowcodec.EnvelopeLen(66+128) {
		return MQTTReplayState{}, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(mqttReplayStateKey(key), value)
	if err != nil {
		return MQTTReplayState{}, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum || len(env.Payload) < 66 {
		return MQTTReplayState{}, dberrors.ErrCorruptValue
	}
	n := int(binary.BigEndian.Uint16(env.Payload))
	if n > 128 || len(env.Payload) != 66+n {
		return MQTTReplayState{}, dberrors.ErrCorruptValue
	}
	b := env.Payload[2+n:]
	s := MQTTReplayState{Generation: string(env.Payload[2 : 2+n]), StartAfter: binary.BigEndian.Uint64(b), Through: binary.BigEndian.Uint64(b[8:]), TotalBytes: binary.BigEndian.Uint64(b[16:]), TotalStoredBytes: binary.BigEndian.Uint64(b[24:])}
	copy(s.Digest[:], b[32:])
	if !mqttReplayStateValid(s) {
		return MQTTReplayState{}, dberrors.ErrCorruptValue
	}
	return s, nil
}

func mqttReplayContentHash(key []byte, content []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte("wukongim/mqtt-replay/content/v1\x00"))
	h.Write(key)
	h.Write(content)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func mqttReplayNextDigest(previous [32]byte, r MQTTReplayRecord) [32]byte {
	b := append([]byte("wukongim/mqtt-replay/prefix/v1\x00"), previous[:]...)
	b = append(b, r.ContentHash[:]...)
	for _, n := range []uint64{r.MessageID, r.AccountedBytes, r.TotalBytes, r.TotalStoredBytes} {
		b = binary.BigEndian.AppendUint64(b, n)
	}
	return sha256.Sum256(b)
}

func encodeMQTTReplayRecord(key ChannelKey, generation string, r MQTTReplayRecord) []byte {
	var w rowcodec.Writer
	// Constant ascending IDs cannot fail the Writer's only encoding precondition.
	_ = w.Uint64(4, r.MessageID)
	_ = w.Uint64(5, r.AccountedBytes)
	_ = w.Uint64(6, r.TotalBytes)
	_ = w.RawBytes(7, r.ContentHash[:])
	_ = w.RawBytes(8, r.Digest[:])
	_ = w.RawBytes(9, r.Content)
	_ = w.Uint64(10, r.TotalStoredBytes)
	return rowcodec.Wrap(mqttReplayRowKey(key, generation, r.Position), 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes())
}

func decodeMQTTReplayRecord(key ChannelKey, generation string, position uint64, value []byte) (MQTTReplayRecord, error) {
	var r MQTTReplayRecord
	if len(value) > mqttReplayMaxBytes+256 || position == 0 {
		return r, dberrors.ErrCorruptValue
	}
	rowKey := mqttReplayRowKey(key, generation, position)
	env, err := rowcodec.UnwrapBorrowed(rowKey, value)
	if err != nil {
		return r, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecColumns || env.Flags != rowcodec.FlagChecksum {
		return r, dberrors.ErrCorruptValue
	}
	r.Position, r.ContentVersion = position, mqttReplayContentVersion
	s := rowcodec.NewBorrowedScanner(env.Payload)
	var seen uint16
	var last uint16
	for s.Next() {
		id := s.ColumnID()
		if id <= last {
			return r, dberrors.ErrCorruptValue
		}
		last = id
		switch id {
		case 4:
			r.MessageID, err = s.Uint64()
		case 5:
			r.AccountedBytes, err = s.Uint64()
		case 6:
			r.TotalBytes, err = s.Uint64()
		case 7, 8:
			var b []byte
			b, err = s.BorrowedBytes()
			if err == nil && len(b) != 32 {
				err = dberrors.ErrCorruptValue
			}
			if id == 7 {
				copy(r.ContentHash[:], b)
			} else {
				copy(r.Digest[:], b)
			}
		case 9:
			r.Content, err = s.Bytes()
		case 10:
			r.TotalStoredBytes, err = s.Uint64()
		}
		if err != nil {
			return r, err
		}
		if id >= 4 && id <= 10 {
			seen |= 1 << (id - 4)
		}
	}
	if s.Err() != nil {
		return r, s.Err()
	}
	if seen != 127 || len(r.Content) == 0 || len(r.Content) > mqttReplayMaxBytes || r.MessageID == 0 || r.TotalBytes < r.AccountedBytes || r.TotalStoredBytes < uint64(len(r.Content)) || r.TotalBytes > r.TotalStoredBytes || r.Digest == [32]byte{} || r.ContentHash != mqttReplayContentHash(rowKey, r.Content) {
		return r, dberrors.ErrCorruptValue
	}
	row, err := mqttReplayOriginalRow(key, position, r.Content)
	if err != nil {
		return r, err
	}
	if row.MessageID != r.MessageID || row.PayloadSize != uint64(len(row.Payload)) || r.AccountedBytes != uint64(len(row.Payload)+len(row.PublicationMetadata)) {
		return r, dberrors.ErrCorruptValue
	}
	return r, nil
}

func mqttReplayOriginalRow(key ChannelKey, position uint64, content []byte) (messageRow, error) {
	row := messageRow{MessageSeq: position}
	if err := decodeMessageHeader(encodeMessageRowKey(key, position, 0), content, &row); err != nil {
		return row, err
	}
	if err := validateMaterializedMessageRow(row); err != nil {
		return row, err
	}
	return row, nil
}

// extendMQTTReplayState verifies contiguous coverage, checked counters and the
// exact hash chain before publishing the next replica-local prefix.
func extendMQTTReplayState(s MQTTReplayState, r MQTTReplayRecord) (MQTTReplayState, error) {
	if s.Through == math.MaxUint64 || r.Position != s.Through+1 || math.MaxUint64-s.TotalBytes < r.AccountedBytes || math.MaxUint64-s.TotalStoredBytes < uint64(len(r.Content)) || r.TotalBytes != s.TotalBytes+r.AccountedBytes || r.TotalStoredBytes != s.TotalStoredBytes+uint64(len(r.Content)) || r.Digest != mqttReplayNextDigest(s.Digest, r) {
		return s, dberrors.ErrCorruptState
	}
	s.Through, s.TotalBytes, s.TotalStoredBytes, s.Digest = r.Position, r.TotalBytes, r.TotalStoredBytes, r.Digest
	return s, nil
}
