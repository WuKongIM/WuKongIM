package store

import (
	"encoding/binary"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const (
	keyspaceTableState  byte = 0x15
	keyspaceTableIndex  byte = 0x16
	keyspaceTableSystem byte = 0x17
)

const messageFamilyCodecVersion byte = 1

// messageIndexHit stores the row pointer returned by a secondary message index.
type messageIndexHit struct {
	MessageSeq  uint64
	MessageID   uint64
	PayloadHash uint64
}

func encodeKeyspacePrefix(keyspace byte, channelKey channel.ChannelKey) []byte {
	key := make([]byte, 0, 2+len(channelKey))
	key = append(key, keyspace)
	key = binary.AppendUvarint(key, uint64(len(channelKey)))
	key = append(key, channelKey...)
	return key
}

func encodeTableStatePrefix(channelKey channel.ChannelKey, tableID uint32) []byte {
	key := encodeKeyspacePrefix(keyspaceTableState, channelKey)
	return binary.BigEndian.AppendUint32(key, tableID)
}

func encodeTableStateKey(channelKey channel.ChannelKey, tableID uint32, primaryKey uint64, familyID uint16) []byte {
	key := encodeTableStatePrefix(channelKey, tableID)
	key = binary.BigEndian.AppendUint64(key, primaryKey)
	return binary.BigEndian.AppendUint16(key, familyID)
}

func decodeTableStateKey(key []byte, channelKey channel.ChannelKey, tableID uint32) (uint64, uint16, error) {
	prefix := encodeTableStatePrefix(channelKey, tableID)
	if len(key) != len(prefix)+10 {
		return 0, 0, channel.ErrCorruptValue
	}
	if string(key[:len(prefix)]) != string(prefix) {
		return 0, 0, channel.ErrCorruptValue
	}
	rest := key[len(prefix):]
	return binary.BigEndian.Uint64(rest[:8]), binary.BigEndian.Uint16(rest[8:10]), nil
}

func encodeTableIndexPrefix(channelKey channel.ChannelKey, tableID uint32, indexID uint16) []byte {
	key := encodeKeyspacePrefix(keyspaceTableIndex, channelKey)
	key = binary.BigEndian.AppendUint32(key, tableID)
	return binary.BigEndian.AppendUint16(key, indexID)
}

func encodeTableSystemPrefix(channelKey channel.ChannelKey, tableID uint32, systemID uint16) []byte {
	key := encodeKeyspacePrefix(keyspaceTableSystem, channelKey)
	key = binary.BigEndian.AppendUint32(key, tableID)
	return binary.BigEndian.AppendUint16(key, systemID)
}

func encodeTableSystemKey(channelKey channel.ChannelKey, tableID uint32, systemID uint16) []byte {
	return encodeTableSystemPrefix(channelKey, tableID, systemID)
}

func encodeMessageFamilies(row messageRow) ([]byte, []byte, error) {
	return encodeMessageFamiliesWithDesc(row, *canonicalMessageTable())
}

func encodeMessageFamiliesWithDesc(row messageRow, table TableDesc) ([]byte, []byte, error) {
	if err := row.validate(); err != nil {
		return nil, nil, err
	}

	payloadHash := row.PayloadHash
	if payloadHash == 0 {
		payloadHash = hashMessagePayload(row.Payload)
	}

	families, err := encodeMessageFamilyPayloads(table, row, payloadHash)
	if err != nil {
		return nil, nil, err
	}
	return families[0], families[1], nil
}

func decodeMessageFamilies(messageSeq uint64, primary []byte, payload []byte) (messageRow, error) {
	return decodeMessageFamiliesWithDesc(*canonicalMessageTable(), messageSeq, primary, payload)
}

func decodeMessageFamiliesWithDesc(table TableDesc, messageSeq uint64, primary []byte, payload []byte) (messageRow, error) {
	row := messageRow{MessageSeq: messageSeq}
	if len(table.Families) != 2 {
		return messageRow{}, channel.ErrInvalidArgument
	}
	if err := decodeMessageFamilyInto(&row, table.Families[0], primary); err != nil {
		return messageRow{}, err
	}
	if err := decodeMessageFamilyInto(&row, table.Families[1], payload); err != nil {
		return messageRow{}, err
	}
	if row.MessageID == 0 {
		return messageRow{}, channel.ErrCorruptValue
	}
	return row, nil
}

func encodeMessageFamilyPayloads(table TableDesc, row messageRow, payloadHash uint64) ([][]byte, error) {
	if len(table.Families) != 2 {
		return nil, channel.ErrInvalidArgument
	}
	families := make([][]byte, 0, len(table.Families))
	for _, family := range table.Families {
		encoded := []byte{messageFamilyCodecVersion}
		for _, columnID := range family.ColumnIDs {
			value, err := encodeMessageFamilyColumnValue(row, payloadHash, columnID)
			if err != nil {
				return nil, err
			}
			encoded = appendFamilyColumn(encoded, columnID, value)
		}
		families = append(families, encoded)
	}
	return families, nil
}

func encodeMessageFamilyColumnValue(row messageRow, payloadHash uint64, columnID uint16) ([]byte, error) {
	switch columnID {
	case messageColumnIDMessageID:
		return encodeFamilyUintBytes(row.MessageID), nil
	case messageColumnIDFramerFlags:
		return encodeFamilyUintBytes(uint64(row.FramerFlags)), nil
	case messageColumnIDSetting:
		return encodeFamilyUintBytes(uint64(row.Setting)), nil
	case messageColumnIDStreamFlag:
		return encodeFamilyUintBytes(uint64(row.StreamFlag)), nil
	case messageColumnIDMsgKey:
		return []byte(row.MsgKey), nil
	case messageColumnIDExpire:
		return encodeFamilyUintBytes(uint64(row.Expire)), nil
	case messageColumnIDClientSeq:
		return encodeFamilyUintBytes(row.ClientSeq), nil
	case messageColumnIDClientMsgNo:
		return []byte(row.ClientMsgNo), nil
	case messageColumnIDStreamNo:
		return []byte(row.StreamNo), nil
	case messageColumnIDStreamID:
		return encodeFamilyUintBytes(row.StreamID), nil
	case messageColumnIDTimestamp:
		return encodeFamilyIntBytes(int64(row.Timestamp)), nil
	case messageColumnIDChannelID:
		return []byte(row.ChannelID), nil
	case messageColumnIDChannelType:
		return encodeFamilyUintBytes(uint64(row.ChannelType)), nil
	case messageColumnIDTopic:
		return []byte(row.Topic), nil
	case messageColumnIDFromUID:
		return []byte(row.FromUID), nil
	case messageColumnIDPayloadHash:
		return encodeFamilyUintBytes(payloadHash), nil
	case messageColumnIDPayload:
		return row.Payload, nil
	default:
		return nil, channel.ErrInvalidArgument
	}
}

func decodeMessageFamilyInto(row *messageRow, family ColumnFamilyDesc, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	if payload[0] != messageFamilyCodecVersion {
		return channel.ErrCorruptValue
	}
	payload = payload[1:]

	for len(payload) > 0 {
		columnID, n := binary.Uvarint(payload)
		if n <= 0 {
			return channel.ErrCorruptValue
		}
		payload = payload[n:]

		length, n := binary.Uvarint(payload)
		if n <= 0 {
			return channel.ErrCorruptValue
		}
		payload = payload[n:]
		if uint64(len(payload)) < length {
			return channel.ErrCorruptValue
		}

		value := payload[:length]
		payload = payload[length:]

		if !familyHasColumn(family, uint16(columnID)) {
			continue
		}

		switch uint16(columnID) {
		case messageColumnIDMessageID:
			decoded, err := decodeFamilyUintBytes(value)
			if err != nil {
				return err
			}
			row.MessageID = decoded
		case messageColumnIDFramerFlags:
			decoded, err := decodeFamilyUintBytes(value)
			if err != nil {
				return err
			}
			if decoded > math.MaxUint8 {
				return channel.ErrCorruptValue
			}
			row.FramerFlags = uint8(decoded)
		case messageColumnIDSetting:
			decoded, err := decodeFamilyUintBytes(value)
			if err != nil {
				return err
			}
			if decoded > math.MaxUint8 {
				return channel.ErrCorruptValue
			}
			row.Setting = uint8(decoded)
		case messageColumnIDStreamFlag:
			decoded, err := decodeFamilyUintBytes(value)
			if err != nil {
				return err
			}
			if decoded > math.MaxUint8 {
				return channel.ErrCorruptValue
			}
			row.StreamFlag = uint8(decoded)
		case messageColumnIDMsgKey:
			row.MsgKey = string(value)
		case messageColumnIDExpire:
			decoded, err := decodeFamilyUintBytes(value)
			if err != nil {
				return err
			}
			if decoded > math.MaxUint32 {
				return channel.ErrCorruptValue
			}
			row.Expire = uint32(decoded)
		case messageColumnIDClientSeq:
			decoded, err := decodeFamilyUintBytes(value)
			if err != nil {
				return err
			}
			row.ClientSeq = decoded
		case messageColumnIDClientMsgNo:
			row.ClientMsgNo = string(value)
		case messageColumnIDStreamNo:
			row.StreamNo = string(value)
		case messageColumnIDStreamID:
			decoded, err := decodeFamilyUintBytes(value)
			if err != nil {
				return err
			}
			row.StreamID = decoded
		case messageColumnIDTimestamp:
			decoded, err := decodeFamilyIntBytes(value)
			if err != nil {
				return err
			}
			if decoded < math.MinInt32 || decoded > math.MaxInt32 {
				return channel.ErrCorruptValue
			}
			row.Timestamp = int32(decoded)
		case messageColumnIDChannelID:
			row.ChannelID = string(value)
		case messageColumnIDChannelType:
			decoded, err := decodeFamilyUintBytes(value)
			if err != nil {
				return err
			}
			if decoded > math.MaxUint8 {
				return channel.ErrCorruptValue
			}
			row.ChannelType = uint8(decoded)
		case messageColumnIDTopic:
			row.Topic = string(value)
		case messageColumnIDFromUID:
			row.FromUID = string(value)
		case messageColumnIDPayloadHash:
			decoded, err := decodeFamilyUintBytes(value)
			if err != nil {
				return err
			}
			row.PayloadHash = decoded
		case messageColumnIDPayload:
			row.Payload = append([]byte(nil), value...)
		default:
			// Skip unknown columns for forward compatibility.
		}
	}
	return nil
}

func familyHasColumn(family ColumnFamilyDesc, columnID uint16) bool {
	for _, candidate := range family.ColumnIDs {
		if candidate == columnID {
			return true
		}
	}
	return false
}

func encodeMessageIDIndexValue(messageSeq uint64) []byte {
	return binary.BigEndian.AppendUint64(nil, messageSeq)
}

func decodeMessageIDIndexValue(value []byte) (uint64, error) {
	if len(value) != 8 {
		return 0, channel.ErrCorruptValue
	}
	return binary.BigEndian.Uint64(value), nil
}

func encodeIdempotencyIndexValue(row messageRow) ([]byte, error) {
	if err := row.validate(); err != nil {
		return nil, err
	}
	payloadHash := row.PayloadHash
	if payloadHash == 0 {
		payloadHash = hashMessagePayload(row.Payload)
	}
	value := make([]byte, 0, 24)
	value = binary.BigEndian.AppendUint64(value, row.MessageSeq)
	value = binary.BigEndian.AppendUint64(value, row.MessageID)
	value = binary.BigEndian.AppendUint64(value, payloadHash)
	return value, nil
}

func decodeIdempotencyIndexValue(value []byte) (messageIndexHit, error) {
	if len(value) != 24 {
		return messageIndexHit{}, channel.ErrCorruptValue
	}
	return messageIndexHit{
		MessageSeq:  binary.BigEndian.Uint64(value[0:8]),
		MessageID:   binary.BigEndian.Uint64(value[8:16]),
		PayloadHash: binary.BigEndian.Uint64(value[16:24]),
	}, nil
}

func keyUpperBound(prefix []byte) []byte {
	if len(prefix) == 0 {
		return nil
	}
	upper := append([]byte(nil), prefix...)
	for i := len(upper) - 1; i >= 0; i-- {
		if upper[i] == 0xff {
			continue
		}
		upper[i]++
		return upper[:i+1]
	}
	return nil
}

func appendKeyString(dst []byte, value string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	dst = append(dst, value...)
	return dst
}

func decodeKeyString(src []byte) (string, []byte, error) {
	length, n := binary.Uvarint(src)
	if n <= 0 {
		return "", nil, channel.ErrCorruptValue
	}
	src = src[n:]
	if uint64(len(src)) < length {
		return "", nil, channel.ErrCorruptValue
	}
	return string(src[:length]), src[length:], nil
}

func appendFamilyColumn(dst []byte, columnID uint16, value []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(columnID))
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func encodeFamilyUintBytes(value uint64) []byte {
	return binary.AppendUvarint(nil, value)
}

func decodeFamilyUintBytes(value []byte) (uint64, error) {
	decoded, n := binary.Uvarint(value)
	if n <= 0 || n != len(value) {
		return 0, channel.ErrCorruptValue
	}
	return decoded, nil
}

func encodeFamilyIntBytes(value int64) []byte {
	return binary.AppendUvarint(nil, encodeZigZagInt64(value))
}

func decodeFamilyIntBytes(value []byte) (int64, error) {
	decoded, err := decodeFamilyUintBytes(value)
	if err != nil {
		return 0, err
	}
	return decodeZigZagInt64(decoded), nil
}

func encodeZigZagInt64(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

func decodeZigZagInt64(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}
