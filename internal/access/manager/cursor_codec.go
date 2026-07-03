package manager

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strconv"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

const managerCursorVersion byte = 1

var (
	messageCursorMagic         = [...]byte{'W', 'K', 'M', 'C'}
	businessChannelCursorMagic = [...]byte{'W', 'K', 'C', 'L'}
	channelRuntimeCursorMagic  = [...]byte{'W', 'K', 'R', 'M'}
	userListCursorMagic        = [...]byte{'W', 'K', 'U', 'L'}
)

func encodeMessageCursor(cursor managementusecase.MessageListCursor) (string, error) {
	if cursor == (managementusecase.MessageListCursor{}) {
		return "", nil
	}
	var data [len(messageCursorMagic) + 1 + 8]byte
	copy(data[:], messageCursorMagic[:])
	data[len(messageCursorMagic)] = managerCursorVersion
	binary.BigEndian.PutUint64(data[len(messageCursorMagic)+1:], cursor.BeforeSeq)
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}

func decodeMessageCursor(raw string) (managementusecase.MessageListCursor, error) {
	if raw == "" {
		return managementusecase.MessageListCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return managementusecase.MessageListCursor{}, err
	}
	if hasCursorMagic(payload, messageCursorMagic) {
		return decodeMessageCursorBinary(payload)
	}
	return decodeLegacyMessageCursorJSON(payload)
}

type messageCursorPayload struct {
	Version   int    `json:"v"`
	BeforeSeq uint64 `json:"before_seq"`
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

func encodeUserListCursor(cursor managementusecase.UserListCursor) (string, error) {
	if cursor == (managementusecase.UserListCursor{}) {
		return "", nil
	}
	data := make([]byte, 0, len(userListCursorMagic)+1+4+4+binary.MaxVarintLen64+len(cursor.UID))
	data = append(data, userListCursorMagic[:]...)
	data = append(data, managerCursorVersion)
	data = binary.BigEndian.AppendUint32(data, cursor.SlotID)
	data = binary.BigEndian.AppendUint32(data, cursor.KeywordHash)
	data = appendCursorString(data, cursor.UID)
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeUserListCursor(raw string) (managementusecase.UserListCursor, error) {
	if raw == "" {
		return managementusecase.UserListCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return managementusecase.UserListCursor{}, err
	}
	return decodeUserListCursorBinary(payload)
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
	data := make([]byte, 0, len(businessChannelCursorMagic)+1+4+8+8+4+binary.MaxVarintLen64+len(cursor.ChannelID))
	data = append(data, businessChannelCursorMagic[:]...)
	data = append(data, managerCursorVersion)
	data = binary.BigEndian.AppendUint32(data, cursor.SlotID)
	data = binary.BigEndian.AppendUint64(data, uint64(cursor.ChannelType))
	data = binary.BigEndian.AppendUint64(data, uint64(cursor.TypeFilter))
	data = binary.BigEndian.AppendUint32(data, cursor.KeywordHash)
	data = appendCursorString(data, cursor.ChannelID)
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeBusinessChannelCursor(raw string) (managementusecase.ChannelListCursor, error) {
	if raw == "" {
		return managementusecase.ChannelListCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return managementusecase.ChannelListCursor{}, err
	}
	return decodeBusinessChannelCursorBinary(payload)
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

func encodeChannelRuntimeMetaCursor(cursor managementusecase.ChannelRuntimeMetaListCursor) (string, error) {
	if cursor == (managementusecase.ChannelRuntimeMetaListCursor{}) {
		return "", nil
	}
	data := make([]byte, 0, len(channelRuntimeCursorMagic)+1+4+8+binary.MaxVarintLen64+len(cursor.ChannelID))
	data = append(data, channelRuntimeCursorMagic[:]...)
	data = append(data, managerCursorVersion)
	data = binary.BigEndian.AppendUint32(data, cursor.SlotID)
	data = binary.BigEndian.AppendUint64(data, uint64(cursor.ChannelType))
	data = appendCursorString(data, cursor.ChannelID)
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeChannelRuntimeMetaCursor(raw string) (managementusecase.ChannelRuntimeMetaListCursor, error) {
	if raw == "" {
		return managementusecase.ChannelRuntimeMetaListCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return managementusecase.ChannelRuntimeMetaListCursor{}, err
	}
	return decodeChannelRuntimeMetaCursorBinary(payload)
}

func decodeChannelRuntimeMetaCursorBinary(payload []byte) (managementusecase.ChannelRuntimeMetaListCursor, error) {
	if !hasCursorMagic(payload, channelRuntimeCursorMagic) || len(payload) < len(channelRuntimeCursorMagic)+1+4+8 || payload[len(channelRuntimeCursorMagic)] != managerCursorVersion {
		return managementusecase.ChannelRuntimeMetaListCursor{}, strconv.ErrSyntax
	}
	rest := payload[len(channelRuntimeCursorMagic)+1:]
	cursor := managementusecase.ChannelRuntimeMetaListCursor{
		SlotID:      binary.BigEndian.Uint32(rest[:4]),
		ChannelType: int64(binary.BigEndian.Uint64(rest[4:12])),
	}
	rest = rest[12:]
	channelID, rest, err := readCursorString(rest)
	if err != nil {
		return managementusecase.ChannelRuntimeMetaListCursor{}, err
	}
	if len(rest) != 0 || cursor.SlotID == 0 || channelID == "" || cursor.ChannelType <= 0 {
		return managementusecase.ChannelRuntimeMetaListCursor{}, strconv.ErrSyntax
	}
	cursor.ChannelID = channelID
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
