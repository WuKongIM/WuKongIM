package meta

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

const (
	recordKindPrimary        byte = 0x01
	recordKindSecondaryIndex byte = 0x02
	wrappedValueTag          byte = 0x0A
	valueTypeInt             byte = 0x03
	valueTypeUint            byte = 0x04
	valueTypeBytes           byte = 0x06

	keyspaceState byte = 0x10
	keyspaceIndex byte = 0x11
	keyspaceMeta  byte = 0x12
	keyspaceRaft  byte = 0x20
)

func encodeStatePrefix(hashSlot uint16, tableID uint32) []byte {
	key := make([]byte, 0, 1+2+4)
	key = append(key, keyspaceState)
	key = binary.BigEndian.AppendUint16(key, hashSlot)
	key = binary.BigEndian.AppendUint32(key, tableID)
	return key
}

func encodeIndexPrefix(hashSlot uint16, tableID uint32, indexID uint16) []byte {
	key := make([]byte, 0, 1+2+4+2)
	key = append(key, keyspaceIndex)
	key = binary.BigEndian.AppendUint16(key, hashSlot)
	key = binary.BigEndian.AppendUint32(key, tableID)
	key = binary.BigEndian.AppendUint16(key, indexID)
	return key
}

func encodeMetaPrefix(hashSlot uint16) []byte {
	key := make([]byte, 0, 1+2)
	key = append(key, keyspaceMeta)
	key = binary.BigEndian.AppendUint16(key, hashSlot)
	return key
}

func encodeUserPrimaryKey(hashSlot uint16, uid string, familyID uint16) []byte {
	key := make([]byte, 0, 32)
	key = encodeStatePrefix(hashSlot, UserTable.ID)
	key = appendKeyString(key, uid)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}

func encodeChannelPrimaryKey(hashSlot uint16, channelID string, channelType int64, familyID uint16) []byte {
	key := make([]byte, 0, 48)
	key = encodeStatePrefix(hashSlot, ChannelTable.ID)
	key = appendKeyString(key, channelID)
	key = appendKeyInt64Ordered(key, channelType)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}

func encodeChannelIDIndexKey(hashSlot uint16, channelID string, channelType int64) []byte {
	key := encodeChannelIDIndexPrefix(hashSlot, channelID)
	key = appendKeyInt64Ordered(key, channelType)
	return key
}

func encodeChannelIDIndexPrefix(hashSlot uint16, channelID string) []byte {
	key := make([]byte, 0, 40)
	key = encodeIndexPrefix(hashSlot, ChannelTable.ID, channelIndexIDChannelID)
	key = appendKeyString(key, channelID)
	return key
}

func encodeChannelRuntimeMetaPrimaryKey(hashSlot uint16, channelID string, channelType int64, familyID uint16) []byte {
	key := make([]byte, 0, 64)
	key = encodeStatePrefix(hashSlot, ChannelRuntimeMetaTable.ID)
	key = appendKeyString(key, channelID)
	key = appendKeyInt64Ordered(key, channelType)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}

func encodeDevicePrimaryKey(hashSlot uint16, uid string, deviceFlag int64, familyID uint16) []byte {
	key := make([]byte, 0, 48)
	key = encodeStatePrefix(hashSlot, DeviceTable.ID)
	key = appendKeyString(key, uid)
	key = appendKeyInt64Ordered(key, deviceFlag)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}

func encodeUserFamilyValue(token string, deviceFlag, deviceLevel int64, key []byte) []byte {
	payload := make([]byte, 0, 32)
	payload = appendBytesValue(payload, userColumnIDToken, 0, token)
	payload = appendIntValue(payload, userColumnIDDeviceFlag, userColumnIDToken, deviceFlag)
	payload = appendIntValue(payload, userColumnIDDeviceLevel, userColumnIDDeviceFlag, deviceLevel)
	return wrapFamilyValue(key, payload)
}

func decodeUserFamilyValue(key, value []byte) (string, int64, int64, error) {
	_, payload, err := decodeWrappedValue(key, value)
	if err != nil {
		return "", 0, 0, err
	}

	var (
		token       string
		deviceFlag  int64
		deviceLevel int64
		colID       uint16
		haveToken   bool
		haveFlag    bool
		haveLevel   bool
	)

	for len(payload) > 0 {
		tag := payload[0]
		payload = payload[1:]

		delta := uint16(tag >> 4)
		valueType := tag & 0x0f
		if delta == 0 {
			return "", 0, 0, fmt.Errorf("%w: zero column delta", ErrCorruptValue)
		}
		colID += delta

		switch valueType {
		case valueTypeBytes:
			length, n := binary.Uvarint(payload)
			if n <= 0 {
				return "", 0, 0, fmt.Errorf("metadb: invalid bytes length")
			}
			payload = payload[n:]
			if uint64(len(payload)) < length {
				return "", 0, 0, fmt.Errorf("metadb: bytes payload truncated")
			}
			raw := payload[:length]
			payload = payload[length:]

			switch colID {
			case userColumnIDToken:
				token = string(raw)
				haveToken = true
			case userColumnIDDeviceFlag, userColumnIDDeviceLevel:
				return "", 0, 0, fmt.Errorf("%w: invalid int column %d", ErrCorruptValue, colID)
			}
		case valueTypeInt:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return "", 0, 0, fmt.Errorf("metadb: invalid int payload")
			}
			payload = payload[n:]

			switch colID {
			case userColumnIDToken:
				return "", 0, 0, fmt.Errorf("%w: invalid string column %d", ErrCorruptValue, colID)
			case userColumnIDDeviceFlag:
				deviceFlag = decodeZigZagInt64(raw)
				haveFlag = true
			case userColumnIDDeviceLevel:
				deviceLevel = decodeZigZagInt64(raw)
				haveLevel = true
			}
		default:
			return "", 0, 0, fmt.Errorf("metadb: unsupported value type %d", valueType)
		}
	}

	if !haveToken {
		return "", 0, 0, fmt.Errorf("%w: missing string column %d", ErrCorruptValue, userColumnIDToken)
	}
	if !haveFlag {
		return "", 0, 0, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, userColumnIDDeviceFlag)
	}
	if !haveLevel {
		return "", 0, 0, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, userColumnIDDeviceLevel)
	}
	return token, deviceFlag, deviceLevel, nil
}

func encodeDeviceFamilyValue(token string, deviceLevel int64, key []byte) []byte {
	payload := make([]byte, 0, 24)
	payload = appendBytesValue(payload, deviceColumnIDToken, 0, token)
	payload = appendIntValue(payload, deviceColumnIDDeviceLevel, deviceColumnIDToken, deviceLevel)
	return wrapFamilyValue(key, payload)
}

func decodeDeviceFamilyValue(key, value []byte) (string, int64, error) {
	_, payload, err := decodeWrappedValue(key, value)
	if err != nil {
		return "", 0, err
	}

	var (
		token       string
		deviceLevel int64
		colID       uint16
		haveToken   bool
		haveLevel   bool
	)

	for len(payload) > 0 {
		tag := payload[0]
		payload = payload[1:]

		delta := uint16(tag >> 4)
		valueType := tag & 0x0f
		if delta == 0 {
			return "", 0, fmt.Errorf("%w: zero column delta", ErrCorruptValue)
		}
		colID += delta

		switch valueType {
		case valueTypeBytes:
			length, n := binary.Uvarint(payload)
			if n <= 0 {
				return "", 0, fmt.Errorf("metadb: invalid bytes length")
			}
			payload = payload[n:]
			if uint64(len(payload)) < length {
				return "", 0, fmt.Errorf("metadb: bytes payload truncated")
			}
			raw := payload[:length]
			payload = payload[length:]

			switch colID {
			case deviceColumnIDToken:
				token = string(raw)
				haveToken = true
			case deviceColumnIDDeviceLevel:
				return "", 0, fmt.Errorf("%w: invalid int column %d", ErrCorruptValue, colID)
			}
		case valueTypeInt:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return "", 0, fmt.Errorf("metadb: invalid int payload")
			}
			payload = payload[n:]

			switch colID {
			case deviceColumnIDToken:
				return "", 0, fmt.Errorf("%w: invalid string column %d", ErrCorruptValue, colID)
			case deviceColumnIDDeviceLevel:
				deviceLevel = decodeZigZagInt64(raw)
				haveLevel = true
			}
		default:
			return "", 0, fmt.Errorf("metadb: unsupported value type %d", valueType)
		}
	}

	if !haveToken {
		return "", 0, fmt.Errorf("%w: missing string column %d", ErrCorruptValue, deviceColumnIDToken)
	}
	if !haveLevel {
		return "", 0, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, deviceColumnIDDeviceLevel)
	}
	return token, deviceLevel, nil
}

func encodeChannelFamilyValue(ban int64, key []byte) []byte {
	payload := make([]byte, 0, 8)
	payload = appendIntValue(payload, channelColumnIDBan, 0, ban)
	return wrapFamilyValue(key, payload)
}

func encodeChannelIndexValue(ban int64) []byte {
	return binary.AppendUvarint(make([]byte, 0, binary.MaxVarintLen64), encodeZigZagInt64(ban))
}

func decodeChannelFamilyValue(key, value []byte) (int64, error) {
	_, payload, err := decodeWrappedValue(key, value)
	if err != nil {
		return 0, err
	}

	var (
		ban     int64
		colID   uint16
		haveBan bool
	)

	for len(payload) > 0 {
		tag := payload[0]
		payload = payload[1:]

		delta := uint16(tag >> 4)
		valueType := tag & 0x0f
		if delta == 0 {
			return 0, fmt.Errorf("%w: zero column delta", ErrCorruptValue)
		}
		colID += delta

		switch valueType {
		case valueTypeInt:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return 0, fmt.Errorf("metadb: invalid int payload")
			}
			payload = payload[n:]

			switch colID {
			case channelColumnIDBan:
				ban = decodeZigZagInt64(raw)
				haveBan = true
			}
		case valueTypeBytes:
			if colID == channelColumnIDBan {
				return 0, fmt.Errorf("%w: invalid int column %d", ErrCorruptValue, colID)
			}
			length, n := binary.Uvarint(payload)
			if n <= 0 {
				return 0, fmt.Errorf("metadb: invalid bytes length")
			}
			payload = payload[n:]
			if uint64(len(payload)) < length {
				return 0, fmt.Errorf("metadb: bytes payload truncated")
			}
			payload = payload[length:]
		default:
			return 0, fmt.Errorf("metadb: unsupported value type %d", valueType)
		}
	}

	if !haveBan {
		return 0, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, channelColumnIDBan)
	}
	return ban, nil
}

func decodeChannelIndexValue(key, value []byte) (int64, error) {
	if len(value) == 0 {
		return 0, ErrCorruptValue
	}
	if len(value) >= 5 {
		if decoded, err := decodeChannelFamilyValue(key, value); err == nil {
			return decoded, nil
		}
	}
	raw, n := binary.Uvarint(value)
	if n <= 0 || n != len(value) {
		return 0, ErrCorruptValue
	}
	return decodeZigZagInt64(raw), nil
}

func encodeChannelRuntimeMetaFamilyValue(meta ChannelRuntimeMeta, key []byte) []byte {
	payload := make([]byte, 0, 128)
	payload = appendUint64Value(payload, channelRuntimeMetaColumnIDChannelEpoch, 0, meta.ChannelEpoch)
	payload = appendUint64Value(payload, channelRuntimeMetaColumnIDLeaderEpoch, channelRuntimeMetaColumnIDChannelEpoch, meta.LeaderEpoch)
	payload = appendRawBytesValue(payload, channelRuntimeMetaColumnIDReplicas, channelRuntimeMetaColumnIDLeaderEpoch, encodeUint64Slice(meta.Replicas))
	payload = appendRawBytesValue(payload, channelRuntimeMetaColumnIDISR, channelRuntimeMetaColumnIDReplicas, encodeUint64Slice(meta.ISR))
	payload = appendUint64Value(payload, channelRuntimeMetaColumnIDLeader, channelRuntimeMetaColumnIDISR, meta.Leader)
	payload = appendIntValue(payload, channelRuntimeMetaColumnIDMinISR, channelRuntimeMetaColumnIDLeader, meta.MinISR)
	payload = appendUint64Value(payload, channelRuntimeMetaColumnIDStatus, channelRuntimeMetaColumnIDMinISR, uint64(meta.Status))
	payload = appendUint64Value(payload, channelRuntimeMetaColumnIDFeatures, channelRuntimeMetaColumnIDStatus, meta.Features)
	payload = appendIntValue(payload, channelRuntimeMetaColumnIDLeaseUntilMS, channelRuntimeMetaColumnIDFeatures, meta.LeaseUntilMS)
	return wrapFamilyValue(key, payload)
}

func decodeChannelRuntimeMetaFamilyValue(key, value []byte) (ChannelRuntimeMeta, error) {
	_, payload, err := decodeWrappedValue(key, value)
	if err != nil {
		return ChannelRuntimeMeta{}, err
	}

	var (
		meta            ChannelRuntimeMeta
		colID           uint16
		haveEpoch       bool
		haveLeaderEpoch bool
		haveReplicas    bool
		haveISR         bool
		haveLeader      bool
		haveMinISR      bool
		haveStatus      bool
		haveFeatures    bool
		haveLeaseUntil  bool
	)

	for len(payload) > 0 {
		tag := payload[0]
		payload = payload[1:]

		delta := uint16(tag >> 4)
		valueType := tag & 0x0f
		if delta == 0 {
			return ChannelRuntimeMeta{}, fmt.Errorf("%w: zero column delta", ErrCorruptValue)
		}
		colID += delta

		switch valueType {
		case valueTypeBytes:
			length, n := binary.Uvarint(payload)
			if n <= 0 {
				return ChannelRuntimeMeta{}, fmt.Errorf("metadb: invalid bytes length")
			}
			payload = payload[n:]
			if uint64(len(payload)) < length {
				return ChannelRuntimeMeta{}, fmt.Errorf("metadb: bytes payload truncated")
			}
			raw := append([]byte(nil), payload[:length]...)
			payload = payload[length:]

			switch colID {
			case channelRuntimeMetaColumnIDReplicas:
				meta.Replicas, err = decodeUint64Slice(raw)
				if err != nil {
					return ChannelRuntimeMeta{}, err
				}
				haveReplicas = true
			case channelRuntimeMetaColumnIDISR:
				meta.ISR, err = decodeUint64Slice(raw)
				if err != nil {
					return ChannelRuntimeMeta{}, err
				}
				haveISR = true
			default:
				return ChannelRuntimeMeta{}, fmt.Errorf("%w: invalid bytes column %d", ErrCorruptValue, colID)
			}
		case valueTypeInt:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return ChannelRuntimeMeta{}, fmt.Errorf("metadb: invalid int payload")
			}
			payload = payload[n:]

			switch colID {
			case channelRuntimeMetaColumnIDMinISR:
				meta.MinISR = decodeZigZagInt64(raw)
				haveMinISR = true
			case channelRuntimeMetaColumnIDLeaseUntilMS:
				meta.LeaseUntilMS = decodeZigZagInt64(raw)
				haveLeaseUntil = true
			default:
				return ChannelRuntimeMeta{}, fmt.Errorf("%w: invalid int column %d", ErrCorruptValue, colID)
			}
		case valueTypeUint:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return ChannelRuntimeMeta{}, fmt.Errorf("metadb: invalid uint payload")
			}
			payload = payload[n:]

			switch colID {
			case channelRuntimeMetaColumnIDChannelEpoch:
				meta.ChannelEpoch = raw
				haveEpoch = true
			case channelRuntimeMetaColumnIDLeaderEpoch:
				meta.LeaderEpoch = raw
				haveLeaderEpoch = true
			case channelRuntimeMetaColumnIDLeader:
				meta.Leader = raw
				haveLeader = true
			case channelRuntimeMetaColumnIDStatus:
				if raw > uint64(^uint8(0)) {
					return ChannelRuntimeMeta{}, fmt.Errorf("%w: invalid status %d", ErrCorruptValue, raw)
				}
				meta.Status = uint8(raw)
				haveStatus = true
			case channelRuntimeMetaColumnIDFeatures:
				meta.Features = raw
				haveFeatures = true
			default:
				return ChannelRuntimeMeta{}, fmt.Errorf("%w: invalid uint column %d", ErrCorruptValue, colID)
			}
		default:
			return ChannelRuntimeMeta{}, fmt.Errorf("metadb: unsupported value type %d", valueType)
		}
	}

	if !haveEpoch {
		return ChannelRuntimeMeta{}, fmt.Errorf("%w: missing uint column %d", ErrCorruptValue, channelRuntimeMetaColumnIDChannelEpoch)
	}
	if !haveLeaderEpoch {
		return ChannelRuntimeMeta{}, fmt.Errorf("%w: missing uint column %d", ErrCorruptValue, channelRuntimeMetaColumnIDLeaderEpoch)
	}
	if !haveReplicas {
		return ChannelRuntimeMeta{}, fmt.Errorf("%w: missing bytes column %d", ErrCorruptValue, channelRuntimeMetaColumnIDReplicas)
	}
	if !haveISR {
		return ChannelRuntimeMeta{}, fmt.Errorf("%w: missing bytes column %d", ErrCorruptValue, channelRuntimeMetaColumnIDISR)
	}
	if !haveLeader {
		return ChannelRuntimeMeta{}, fmt.Errorf("%w: missing uint column %d", ErrCorruptValue, channelRuntimeMetaColumnIDLeader)
	}
	if !haveMinISR {
		return ChannelRuntimeMeta{}, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, channelRuntimeMetaColumnIDMinISR)
	}
	if !haveStatus {
		return ChannelRuntimeMeta{}, fmt.Errorf("%w: missing uint column %d", ErrCorruptValue, channelRuntimeMetaColumnIDStatus)
	}
	if !haveFeatures {
		return ChannelRuntimeMeta{}, fmt.Errorf("%w: missing uint column %d", ErrCorruptValue, channelRuntimeMetaColumnIDFeatures)
	}
	if !haveLeaseUntil {
		return ChannelRuntimeMeta{}, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, channelRuntimeMetaColumnIDLeaseUntilMS)
	}
	return normalizeChannelRuntimeMeta(meta), nil
}

func wrapFamilyValue(key, payload []byte) []byte {
	value := make([]byte, 5+len(payload))
	value[4] = wrappedValueTag
	copy(value[5:], payload)

	sum := crc32.Update(0, crc32.IEEETable, key)
	sum = crc32.Update(sum, crc32.IEEETable, value[4:])
	if sum == 0 {
		sum = 1
	}
	binary.BigEndian.PutUint32(value[:4], sum)
	return value
}

func decodeWrappedValue(key, value []byte) (byte, []byte, error) {
	if len(value) < 5 {
		return 0, nil, fmt.Errorf("metadb: wrapped value too short")
	}
	want := binary.BigEndian.Uint32(value[:4])
	body := value[4:]
	got := crc32.Update(0, crc32.IEEETable, key)
	got = crc32.Update(got, crc32.IEEETable, body)
	if got == 0 {
		got = 1
	}
	if got != want {
		return 0, nil, ErrChecksumMismatch
	}
	if body[0] != wrappedValueTag {
		return 0, nil, fmt.Errorf("%w: unexpected value tag %x", ErrCorruptValue, body[0])
	}
	return body[0], body[1:], nil
}

func decodeFamilyPayload(payload []byte) (map[uint16]any, error) {
	result := make(map[uint16]any)
	var colID uint16

	for len(payload) > 0 {
		tag := payload[0]
		payload = payload[1:]

		delta := uint16(tag >> 4)
		valueType := tag & 0x0f
		if delta == 0 {
			return nil, fmt.Errorf("%w: zero column delta", ErrCorruptValue)
		}
		colID += delta

		switch valueType {
		case valueTypeBytes:
			length, n := binary.Uvarint(payload)
			if n <= 0 {
				return nil, fmt.Errorf("metadb: invalid bytes length")
			}
			payload = payload[n:]
			if uint64(len(payload)) < length {
				return nil, fmt.Errorf("metadb: bytes payload truncated")
			}
			result[colID] = string(payload[:length])
			payload = payload[length:]
		case valueTypeInt:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return nil, fmt.Errorf("metadb: invalid int payload")
			}
			payload = payload[n:]
			result[colID] = decodeZigZagInt64(raw)
		default:
			return nil, fmt.Errorf("metadb: unsupported value type %d", valueType)
		}
	}

	return result, nil
}

func requireStringColumn(cols map[uint16]any, columnID uint16) (string, error) {
	value, ok := cols[columnID]
	if !ok {
		return "", fmt.Errorf("%w: missing string column %d", ErrCorruptValue, columnID)
	}
	typed, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: invalid string column %d", ErrCorruptValue, columnID)
	}
	return typed, nil
}

func requireInt64Column(cols map[uint16]any, columnID uint16) (int64, error) {
	value, ok := cols[columnID]
	if !ok {
		return 0, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, columnID)
	}
	typed, ok := value.(int64)
	if !ok {
		return 0, fmt.Errorf("%w: invalid int column %d", ErrCorruptValue, columnID)
	}
	return typed, nil
}

func appendBytesValue(dst []byte, columnID, prevColumnID uint16, value string) []byte {
	delta := columnID - prevColumnID
	dst = append(dst, encodeValueTag(delta, valueTypeBytes))
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	dst = append(dst, value...)
	return dst
}

func appendRawBytesValue(dst []byte, columnID, prevColumnID uint16, value []byte) []byte {
	delta := columnID - prevColumnID
	dst = append(dst, encodeValueTag(delta, valueTypeBytes))
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	dst = append(dst, value...)
	return dst
}

func appendIntValue(dst []byte, columnID, prevColumnID uint16, value int64) []byte {
	delta := columnID - prevColumnID
	dst = append(dst, encodeValueTag(delta, valueTypeInt))
	dst = binary.AppendUvarint(dst, encodeZigZagInt64(value))
	return dst
}

func appendUint64Value(dst []byte, columnID, prevColumnID uint16, value uint64) []byte {
	delta := columnID - prevColumnID
	dst = append(dst, encodeValueTag(delta, valueTypeUint))
	dst = binary.AppendUvarint(dst, value)
	return dst
}

func encodeValueTag(delta uint16, typ byte) byte {
	return byte(delta<<4) | typ
}

func appendTableIndexPrefix(dst []byte, kind byte, tableID uint32, indexID uint16) []byte {
	dst = append(dst, kind)
	dst = binary.BigEndian.AppendUint32(dst, tableID)
	dst = binary.BigEndian.AppendUint16(dst, indexID)
	return dst
}

func appendKeyString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(value)))
	dst = append(dst, value...)
	return dst
}

func appendKeyInt64Ordered(dst []byte, value int64) []byte {
	u := uint64(value) ^ 0x8000000000000000
	return binary.BigEndian.AppendUint64(dst, u)
}

func decodeOrderedInt64(src []byte) (int64, []byte, error) {
	if len(src) < 8 {
		return 0, nil, fmt.Errorf("metadb: ordered int64 too short")
	}
	u := binary.BigEndian.Uint64(src[:8]) ^ 0x8000000000000000
	return int64(u), src[8:], nil
}

func decodeKeyString(src []byte) (string, []byte, error) {
	if len(src) < 2 {
		return "", nil, fmt.Errorf("metadb: key string too short")
	}
	n := binary.BigEndian.Uint16(src[:2])
	src = src[2:]
	if len(src) < int(n) {
		return "", nil, fmt.Errorf("metadb: key string truncated")
	}
	return string(src[:n]), src[n:], nil
}

func encodeZigZagInt64(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

func decodeZigZagInt64(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

func encodeUint64Slice(values []uint64) []byte {
	if len(values) == 0 {
		return nil
	}
	buf := make([]byte, 8*len(values))
	for i, value := range values {
		binary.BigEndian.PutUint64(buf[i*8:], value)
	}
	return buf
}

func decodeUint64Slice(data []byte) ([]uint64, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if len(data)%8 != 0 {
		return nil, fmt.Errorf("%w: malformed uint64 slice", ErrCorruptValue)
	}
	values := make([]uint64, len(data)/8)
	for i := range values {
		values[i] = binary.BigEndian.Uint64(data[i*8:])
	}
	return values, nil
}
