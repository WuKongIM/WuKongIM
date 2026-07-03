package manager

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strconv"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
)

const managerCursorVersion byte = 1

var (
	messageCursorMagic            = [...]byte{'W', 'K', 'M', 'C'}
	channelRuntimeMetaCursorMagic = [...]byte{'W', 'K', 'R', 'M'}
	userListCursorMagic           = [...]byte{'W', 'K', 'U', 'L'}
	businessChannelCursorMagic    = [...]byte{'W', 'K', 'C', 'L'}
	businessChannelMemberMagic    = [...]byte{'W', 'K', 'C', 'M'}
	distributedTaskCursorMagic    = [...]byte{'W', 'K', 'D', 'T'}
)

func encodeMessageCursorBinary(cursor managementusecase.MessageListCursor) string {
	var data [len(messageCursorMagic) + 1 + 8]byte
	copy(data[:], messageCursorMagic[:])
	data[len(messageCursorMagic)] = managerCursorVersion
	binary.BigEndian.PutUint64(data[len(messageCursorMagic)+1:], cursor.BeforeSeq)
	return encodeCursorBase64(data[:])
}

func decodeMessageCursorRaw(raw string) (managementusecase.MessageListCursor, error) {
	if raw == "" {
		return managementusecase.MessageListCursor{}, nil
	}
	var stack [64]byte
	decodedLen := base64.RawURLEncoding.DecodedLen(len(raw))
	if decodedLen > len(stack) {
		payload, err := decodeCursorBase64Heap(raw, decodedLen)
		if err != nil {
			return managementusecase.MessageListCursor{}, err
		}
		return decodeMessageCursorPayload(payload)
	}
	n, err := decodeRawURLBase64String(stack[:decodedLen], raw)
	if err != nil {
		return managementusecase.MessageListCursor{}, err
	}
	payload := stack[:n]
	if hasCursorMagic(payload, messageCursorMagic) {
		return decodeMessageCursorBinary(payload)
	}
	return decodeLegacyMessageCursorJSON(append([]byte(nil), payload...))
}

func decodeMessageCursorPayload(payload []byte) (managementusecase.MessageListCursor, error) {
	if hasCursorMagic(payload, messageCursorMagic) {
		return decodeMessageCursorBinary(payload)
	}
	return decodeLegacyMessageCursorJSON(payload)
}

func encodeDistributedTaskCursor(offset int) string {
	if offset <= 0 {
		return ""
	}
	var data [len(distributedTaskCursorMagic) + 1 + 4]byte
	copy(data[:], distributedTaskCursorMagic[:])
	data[len(distributedTaskCursorMagic)] = managerCursorVersion
	binary.BigEndian.PutUint32(data[len(distributedTaskCursorMagic)+1:], uint32(offset))
	return encodeCursorBase64(data[:])
}

func decodeDistributedTaskCursor(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	var stack [64]byte
	decodedLen := base64.RawURLEncoding.DecodedLen(len(raw))
	if decodedLen > len(stack) {
		payload, err := decodeCursorBase64Heap(raw, decodedLen)
		if err != nil {
			return 0, err
		}
		return decodeDistributedTaskCursorPayload(payload)
	}
	n, err := decodeRawURLBase64String(stack[:decodedLen], raw)
	if err != nil {
		return 0, err
	}
	return decodeDistributedTaskCursorPayload(stack[:n])
}

func decodeDistributedTaskCursorPayload(payload []byte) (int, error) {
	if len(payload) != len(distributedTaskCursorMagic)+1+4 || !hasCursorMagic(payload, distributedTaskCursorMagic) || payload[len(distributedTaskCursorMagic)] != managerCursorVersion {
		return 0, strconv.ErrSyntax
	}
	offset := binary.BigEndian.Uint32(payload[len(distributedTaskCursorMagic)+1:])
	if offset == 0 {
		return 0, strconv.ErrSyntax
	}
	return int(offset), nil
}

func decodeMessageCursorBinary(payload []byte) (managementusecase.MessageListCursor, error) {
	if len(payload) != len(messageCursorMagic)+1+8 || payload[len(messageCursorMagic)] != managerCursorVersion {
		return managementusecase.MessageListCursor{}, strconv.ErrSyntax
	}
	beforeSeq := binary.BigEndian.Uint64(payload[len(messageCursorMagic)+1:])
	if beforeSeq == 0 {
		return managementusecase.MessageListCursor{}, strconv.ErrSyntax
	}
	return managementusecase.MessageListCursor{BeforeSeq: beforeSeq}, nil
}

func decodeLegacyMessageCursorJSON(payload []byte) (managementusecase.MessageListCursor, error) {
	var body messageCursorPayload
	if err := json.Unmarshal(payload, &body); err != nil {
		return managementusecase.MessageListCursor{}, err
	}
	if body.Version != 1 || body.BeforeSeq == 0 {
		return managementusecase.MessageListCursor{}, strconv.ErrSyntax
	}
	return managementusecase.MessageListCursor{BeforeSeq: body.BeforeSeq}, nil
}

func encodeChannelRuntimeMetaCursorBinary(cursor managementusecase.ChannelRuntimeMetaListCursor) string {
	var stack [256]byte
	data := stack[:0]
	if len(channelRuntimeMetaCursorMagic)+1+4+8+binary.MaxVarintLen64+len(cursor.ChannelID) > len(stack) {
		data = make([]byte, 0, len(channelRuntimeMetaCursorMagic)+1+4+8+binary.MaxVarintLen64+len(cursor.ChannelID))
	}
	data = append(data, channelRuntimeMetaCursorMagic[:]...)
	data = append(data, managerCursorVersion)
	data = binary.BigEndian.AppendUint32(data, cursor.SlotID)
	data = binary.BigEndian.AppendUint64(data, uint64(cursor.ChannelType))
	data = appendCursorString(data, cursor.ChannelID)
	return encodeCursorBase64(data)
}

func decodeChannelRuntimeMetaCursorRaw(raw string) (managementusecase.ChannelRuntimeMetaListCursor, error) {
	if raw == "" {
		return managementusecase.ChannelRuntimeMetaListCursor{}, nil
	}
	var stack [512]byte
	decodedLen := base64.RawURLEncoding.DecodedLen(len(raw))
	if decodedLen > len(stack) {
		payload, err := decodeCursorBase64Heap(raw, decodedLen)
		if err != nil {
			return managementusecase.ChannelRuntimeMetaListCursor{}, err
		}
		return decodeChannelRuntimeMetaCursorPayload(payload)
	}
	n, err := decodeRawURLBase64String(stack[:decodedLen], raw)
	if err != nil {
		return managementusecase.ChannelRuntimeMetaListCursor{}, err
	}
	payload := stack[:n]
	if hasCursorMagic(payload, channelRuntimeMetaCursorMagic) {
		return decodeChannelRuntimeMetaCursorBinary(payload)
	}
	return decodeLegacyChannelRuntimeMetaCursorJSON(append([]byte(nil), payload...))
}

func decodeChannelRuntimeMetaCursorPayload(payload []byte) (managementusecase.ChannelRuntimeMetaListCursor, error) {
	if hasCursorMagic(payload, channelRuntimeMetaCursorMagic) {
		return decodeChannelRuntimeMetaCursorBinary(payload)
	}
	return decodeLegacyChannelRuntimeMetaCursorJSON(payload)
}

func decodeChannelRuntimeMetaCursorBinary(payload []byte) (managementusecase.ChannelRuntimeMetaListCursor, error) {
	if len(payload) < len(channelRuntimeMetaCursorMagic)+1+4+8 || payload[len(channelRuntimeMetaCursorMagic)] != managerCursorVersion {
		return managementusecase.ChannelRuntimeMetaListCursor{}, strconv.ErrSyntax
	}
	rest := payload[len(channelRuntimeMetaCursorMagic)+1:]
	cursor := managementusecase.ChannelRuntimeMetaListCursor{
		SlotID:      binary.BigEndian.Uint32(rest[:4]),
		ChannelType: int64(binary.BigEndian.Uint64(rest[4:12])),
	}
	rest = rest[12:]
	channelID, rest, err := readCursorString(rest)
	if err != nil {
		return managementusecase.ChannelRuntimeMetaListCursor{}, err
	}
	if len(rest) != 0 {
		return managementusecase.ChannelRuntimeMetaListCursor{}, strconv.ErrSyntax
	}
	cursor.ChannelID = channelID
	if err := validateChannelRuntimeMetaListCursor(cursor); err != nil {
		return managementusecase.ChannelRuntimeMetaListCursor{}, err
	}
	return cursor, nil
}

func decodeLegacyChannelRuntimeMetaCursorJSON(payload []byte) (managementusecase.ChannelRuntimeMetaListCursor, error) {
	var body channelRuntimeMetaCursorPayload
	if err := json.Unmarshal(payload, &body); err != nil {
		return managementusecase.ChannelRuntimeMetaListCursor{}, err
	}
	if err := validateChannelRuntimeMetaCursorPayload(body); err != nil {
		return managementusecase.ChannelRuntimeMetaListCursor{}, err
	}
	return managementusecase.ChannelRuntimeMetaListCursor{
		SlotID:      body.SlotID,
		ChannelID:   body.ChannelID,
		ChannelType: body.ChannelType,
	}, nil
}

func validateChannelRuntimeMetaListCursor(cursor managementusecase.ChannelRuntimeMetaListCursor) error {
	return validateChannelRuntimeMetaCursorPayload(channelRuntimeMetaCursorPayload{
		Version:     1,
		SlotID:      cursor.SlotID,
		ChannelID:   cursor.ChannelID,
		ChannelType: cursor.ChannelType,
	})
}

func encodeUserListCursor(cursor managementusecase.UserListCursor) (string, error) {
	if cursor == (managementusecase.UserListCursor{}) {
		return "", nil
	}
	var stack [256]byte
	data := stack[:0]
	if len(userListCursorMagic)+1+4+4+binary.MaxVarintLen64+len(cursor.UID) > len(stack) {
		data = make([]byte, 0, len(userListCursorMagic)+1+4+4+binary.MaxVarintLen64+len(cursor.UID))
	}
	data = append(data, userListCursorMagic[:]...)
	data = append(data, managerCursorVersion)
	data = binary.BigEndian.AppendUint32(data, cursor.SlotID)
	data = binary.BigEndian.AppendUint32(data, cursor.KeywordHash)
	data = appendCursorString(data, cursor.UID)
	return encodeCursorBase64(data), nil
}

func decodeUserListCursor(raw string) (managementusecase.UserListCursor, error) {
	if raw == "" {
		return managementusecase.UserListCursor{}, nil
	}
	var stack [512]byte
	decodedLen := base64.RawURLEncoding.DecodedLen(len(raw))
	if decodedLen > len(stack) {
		payload, err := decodeCursorBase64Heap(raw, decodedLen)
		if err != nil {
			return managementusecase.UserListCursor{}, err
		}
		return decodeUserListCursorBinary(payload)
	}
	n, err := decodeRawURLBase64String(stack[:decodedLen], raw)
	if err != nil {
		return managementusecase.UserListCursor{}, err
	}
	return decodeUserListCursorBinary(stack[:n])
}

func decodeUserListCursorBinary(payload []byte) (managementusecase.UserListCursor, error) {
	if !hasCursorMagic(payload, userListCursorMagic) || len(payload) < len(userListCursorMagic)+1+4+4 || payload[len(userListCursorMagic)] != managerCursorVersion {
		return managementusecase.UserListCursor{}, strconv.ErrSyntax
	}
	rest := payload[len(userListCursorMagic)+1:]
	cursor := managementusecase.UserListCursor{
		SlotID:      binary.BigEndian.Uint32(rest[:4]),
		KeywordHash: binary.BigEndian.Uint32(rest[4:8]),
	}
	rest = rest[8:]
	uid, rest, err := readCursorString(rest)
	if err != nil {
		return managementusecase.UserListCursor{}, err
	}
	if len(rest) != 0 || cursor.SlotID == 0 || uid == "" {
		return managementusecase.UserListCursor{}, strconv.ErrSyntax
	}
	cursor.UID = uid
	return cursor, nil
}

func encodeBusinessChannelCursor(cursor managementusecase.ChannelListCursor) (string, error) {
	if cursor == (managementusecase.ChannelListCursor{}) {
		return "", nil
	}
	var stack [256]byte
	data := stack[:0]
	if len(businessChannelCursorMagic)+1+4+8+8+4+binary.MaxVarintLen64+len(cursor.ChannelID) > len(stack) {
		data = make([]byte, 0, len(businessChannelCursorMagic)+1+4+8+8+4+binary.MaxVarintLen64+len(cursor.ChannelID))
	}
	data = append(data, businessChannelCursorMagic[:]...)
	data = append(data, managerCursorVersion)
	data = binary.BigEndian.AppendUint32(data, cursor.SlotID)
	data = binary.BigEndian.AppendUint64(data, uint64(cursor.ChannelType))
	data = binary.BigEndian.AppendUint64(data, uint64(cursor.TypeFilter))
	data = binary.BigEndian.AppendUint32(data, cursor.KeywordHash)
	data = appendCursorString(data, cursor.ChannelID)
	return encodeCursorBase64(data), nil
}

func decodeBusinessChannelCursor(raw string) (managementusecase.ChannelListCursor, error) {
	if raw == "" {
		return managementusecase.ChannelListCursor{}, nil
	}
	var stack [512]byte
	decodedLen := base64.RawURLEncoding.DecodedLen(len(raw))
	if decodedLen > len(stack) {
		payload, err := decodeCursorBase64Heap(raw, decodedLen)
		if err != nil {
			return managementusecase.ChannelListCursor{}, err
		}
		return decodeBusinessChannelCursorBinary(payload)
	}
	n, err := decodeRawURLBase64String(stack[:decodedLen], raw)
	if err != nil {
		return managementusecase.ChannelListCursor{}, err
	}
	return decodeBusinessChannelCursorBinary(stack[:n])
}

func decodeBusinessChannelCursorBinary(payload []byte) (managementusecase.ChannelListCursor, error) {
	if !hasCursorMagic(payload, businessChannelCursorMagic) || len(payload) < len(businessChannelCursorMagic)+1+4+8+8+4 || payload[len(businessChannelCursorMagic)] != managerCursorVersion {
		return managementusecase.ChannelListCursor{}, strconv.ErrSyntax
	}
	rest := payload[len(businessChannelCursorMagic)+1:]
	cursor := managementusecase.ChannelListCursor{
		SlotID:      binary.BigEndian.Uint32(rest[:4]),
		ChannelType: int64(binary.BigEndian.Uint64(rest[4:12])),
		TypeFilter:  int64(binary.BigEndian.Uint64(rest[12:20])),
		KeywordHash: binary.BigEndian.Uint32(rest[20:24]),
	}
	rest = rest[24:]
	channelID, rest, err := readCursorString(rest)
	if err != nil {
		return managementusecase.ChannelListCursor{}, err
	}
	if len(rest) != 0 || cursor.SlotID == 0 || channelID == "" || cursor.ChannelType <= 0 {
		return managementusecase.ChannelListCursor{}, strconv.ErrSyntax
	}
	cursor.ChannelID = channelID
	return cursor, nil
}

func encodeBusinessChannelMemberCursor(cursor managementusecase.ChannelMemberCursor) (string, error) {
	if cursor == (managementusecase.ChannelMemberCursor{}) {
		return "", nil
	}
	var stack [256]byte
	data := stack[:0]
	if len(businessChannelMemberMagic)+1+4+8+1+binary.MaxVarintLen64+len(cursor.UID) > len(stack) {
		data = make([]byte, 0, len(businessChannelMemberMagic)+1+4+8+1+binary.MaxVarintLen64+len(cursor.UID))
	}
	data = append(data, businessChannelMemberMagic[:]...)
	data = append(data, managerCursorVersion)
	data = binary.BigEndian.AppendUint32(data, cursor.ChannelIDHash)
	data = binary.BigEndian.AppendUint64(data, uint64(cursor.ChannelType))
	data = append(data, cursor.ListKind)
	data = appendCursorString(data, cursor.UID)
	return encodeCursorBase64(data), nil
}

func decodeBusinessChannelMemberCursor(raw string) (managementusecase.ChannelMemberCursor, error) {
	if raw == "" {
		return managementusecase.ChannelMemberCursor{}, nil
	}
	var stack [512]byte
	decodedLen := base64.RawURLEncoding.DecodedLen(len(raw))
	if decodedLen > len(stack) {
		payload, err := decodeCursorBase64Heap(raw, decodedLen)
		if err != nil {
			return managementusecase.ChannelMemberCursor{}, err
		}
		return decodeBusinessChannelMemberCursorBinary(payload)
	}
	n, err := decodeRawURLBase64String(stack[:decodedLen], raw)
	if err != nil {
		return managementusecase.ChannelMemberCursor{}, err
	}
	return decodeBusinessChannelMemberCursorBinary(stack[:n])
}

func decodeBusinessChannelMemberCursorBinary(payload []byte) (managementusecase.ChannelMemberCursor, error) {
	if !hasCursorMagic(payload, businessChannelMemberMagic) || len(payload) < len(businessChannelMemberMagic)+1+4+8+1 || payload[len(businessChannelMemberMagic)] != managerCursorVersion {
		return managementusecase.ChannelMemberCursor{}, strconv.ErrSyntax
	}
	rest := payload[len(businessChannelMemberMagic)+1:]
	cursor := managementusecase.ChannelMemberCursor{
		ChannelIDHash: binary.BigEndian.Uint32(rest[:4]),
		ChannelType:   int64(binary.BigEndian.Uint64(rest[4:12])),
		ListKind:      rest[12],
	}
	rest = rest[13:]
	uid, rest, err := readCursorString(rest)
	if err != nil {
		return managementusecase.ChannelMemberCursor{}, err
	}
	if len(rest) != 0 || cursor.ChannelType <= 0 || cursor.ListKind == 0 || uid == "" {
		return managementusecase.ChannelMemberCursor{}, strconv.ErrSyntax
	}
	cursor.UID = uid
	return cursor, nil
}

func appendCursorString(dst []byte, value string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func readCursorString(src []byte) (string, []byte, error) {
	length, n := binary.Uvarint(src)
	if n <= 0 {
		return "", nil, strconv.ErrSyntax
	}
	rest := src[n:]
	if length > uint64(len(rest)) {
		return "", nil, strconv.ErrSyntax
	}
	value := string(rest[:int(length)])
	return value, rest[int(length):], nil
}

func encodeCursorBase64(data []byte) string {
	encodedLen := base64.RawURLEncoding.EncodedLen(len(data))
	var stack [512]byte
	dst := stack[:encodedLen]
	if encodedLen > len(stack) {
		dst = make([]byte, encodedLen)
	}
	base64.RawURLEncoding.Encode(dst, data)
	return string(dst)
}

func decodeCursorBase64Heap(raw string, decodedLen int) ([]byte, error) {
	dst := make([]byte, decodedLen)
	n, err := decodeRawURLBase64String(dst, raw)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

func decodeRawURLBase64String(dst []byte, raw string) (int, error) {
	if len(raw)%4 == 1 {
		return 0, base64.CorruptInputError(len(raw) - 1)
	}

	written := 0
	i := 0
	for len(raw)-i >= 4 {
		a, ok := rawURLBase64Value(raw[i])
		if !ok {
			return 0, base64.CorruptInputError(i)
		}
		b, ok := rawURLBase64Value(raw[i+1])
		if !ok {
			return 0, base64.CorruptInputError(i + 1)
		}
		c, ok := rawURLBase64Value(raw[i+2])
		if !ok {
			return 0, base64.CorruptInputError(i + 2)
		}
		d, ok := rawURLBase64Value(raw[i+3])
		if !ok {
			return 0, base64.CorruptInputError(i + 3)
		}
		dst[written] = byte(a<<2 | b>>4)
		dst[written+1] = byte(b<<4 | c>>2)
		dst[written+2] = byte(c<<6 | d)
		written += 3
		i += 4
	}

	switch len(raw) - i {
	case 0:
	case 2:
		a, ok := rawURLBase64Value(raw[i])
		if !ok {
			return 0, base64.CorruptInputError(i)
		}
		b, ok := rawURLBase64Value(raw[i+1])
		if !ok {
			return 0, base64.CorruptInputError(i + 1)
		}
		dst[written] = byte(a<<2 | b>>4)
		written++
	case 3:
		a, ok := rawURLBase64Value(raw[i])
		if !ok {
			return 0, base64.CorruptInputError(i)
		}
		b, ok := rawURLBase64Value(raw[i+1])
		if !ok {
			return 0, base64.CorruptInputError(i + 1)
		}
		c, ok := rawURLBase64Value(raw[i+2])
		if !ok {
			return 0, base64.CorruptInputError(i + 2)
		}
		dst[written] = byte(a<<2 | b>>4)
		dst[written+1] = byte(b<<4 | c>>2)
		written += 2
	default:
		return 0, base64.CorruptInputError(i)
	}
	return written, nil
}

func rawURLBase64Value(c byte) (byte, bool) {
	switch {
	case c >= 'A' && c <= 'Z':
		return c - 'A', true
	case c >= 'a' && c <= 'z':
		return c - 'a' + 26, true
	case c >= '0' && c <= '9':
		return c - '0' + 52, true
	case c == '-':
		return 62, true
	case c == '_':
		return 63, true
	default:
		return 0, false
	}
}

func hasCursorMagic(data []byte, magic [4]byte) bool {
	if len(data) < len(magic) {
		return false
	}
	for i, value := range magic {
		if data[i] != value {
			return false
		}
	}
	return true
}
