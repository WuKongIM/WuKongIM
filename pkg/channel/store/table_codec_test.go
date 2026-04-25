package store

import (
	"encoding/binary"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

const expectedMessageFamilyCodecVersion byte = 1

func TestMessageTableCatalogDeclaresPrimaryPayloadAndIndexes(t *testing.T) {
	table := MessageTable()
	require.Equal(t, "message", table.Name)
	require.Equal(t, []uint16{messageColumnIDMessageSeq}, table.PrimaryIndex.ColumnIDs)
	require.Len(t, table.Families, 2)
	require.Equal(t, "primary", table.Families[0].Name)
	require.Equal(t, "payload", table.Families[1].Name)
	require.Len(t, table.SecondaryIndexes, 3)
	require.Equal(t, "uidx_message_id", table.SecondaryIndexes[0].Name)
	require.Equal(t, []uint16{messageColumnIDMessageID}, table.SecondaryIndexes[0].ColumnIDs)
	require.Equal(t, "idx_client_msg_no", table.SecondaryIndexes[1].Name)
	require.Equal(t, []uint16{messageColumnIDClientMsgNo, messageColumnIDMessageSeq}, table.SecondaryIndexes[1].ColumnIDs)
	require.Equal(t, "uidx_from_uid_client_msg_no", table.SecondaryIndexes[2].Name)
	require.Equal(t, []uint16{messageColumnIDFromUID, messageColumnIDClientMsgNo}, table.SecondaryIndexes[2].ColumnIDs)
}

func TestEncodeMessageFamiliesUsesVersionedColumnLengthWireFormat(t *testing.T) {
	row := messageRow{
		MessageSeq:  9,
		MessageID:   42,
		FramerFlags: 3,
		ClientMsgNo: "c-1",
		FromUID:     "u1",
		ChannelID:   "room",
		ChannelType: 1,
		Payload:     []byte("hello"),
		PayloadHash: 123,
	}

	primary, payload, err := encodeMessageFamilies(row)
	require.NoError(t, err)
	require.Equal(t, expectedMessageFamilyCodecVersion, primary[0])
	require.Equal(t, expectedMessageFamilyCodecVersion, payload[0])

	columnID, encodedLen, value, next := decodeTestFamilyField(t, primary, 1)
	require.Equal(t, uint64(messageColumnIDMessageID), columnID)
	require.Equal(t, uint64(len(encodeFamilyUintBytes(row.MessageID))), encodedLen)
	require.Equal(t, encodeFamilyUintBytes(row.MessageID), value)

	columnID, encodedLen, value, _ = decodeTestFamilyField(t, primary, next)
	require.Equal(t, uint64(messageColumnIDFramerFlags), columnID)
	require.Equal(t, uint64(len(encodeFamilyUintBytes(uint64(row.FramerFlags)))), encodedLen)
	require.Equal(t, encodeFamilyUintBytes(uint64(row.FramerFlags)), value)
}

func TestDecodeMessageFamiliesSkipsUnknownColumns(t *testing.T) {
	row := messageRow{
		MessageSeq:  9,
		MessageID:   42,
		ClientMsgNo: "c-1",
		FromUID:     "u1",
		ChannelID:   "room",
		ChannelType: 1,
		Payload:     []byte("hello"),
		PayloadHash: 123,
	}

	primary, payload, err := encodeMessageFamilies(row)
	require.NoError(t, err)

	primary = appendTestBytesColumn(primary, messageColumnIDPayloadHash+1, []byte("future"))

	decoded, err := decodeMessageFamilies(row.MessageSeq, primary, payload)
	require.NoError(t, err)
	require.Equal(t, row.MessageID, decoded.MessageID)
	require.Equal(t, row.ClientMsgNo, decoded.ClientMsgNo)
	require.Equal(t, row.FromUID, decoded.FromUID)
	require.Equal(t, row.Payload, decoded.Payload)
}

func TestTableStateKeyRoundTripUsesExpectedLayout(t *testing.T) {
	channelKey := channel.ChannelKey("room:1")
	key := encodeTableStateKey(channelKey, TableIDMessage, 42, messagePayloadFamilyID)

	want := append([]byte{keyspaceTableState, byte(len(channelKey))}, []byte(channelKey)...)
	want = binary.BigEndian.AppendUint32(want, TableIDMessage)
	want = binary.BigEndian.AppendUint64(want, 42)
	want = binary.BigEndian.AppendUint16(want, messagePayloadFamilyID)
	require.Equal(t, want, key)

	primaryKey, familyID, err := decodeTableStateKey(key, channelKey, TableIDMessage)
	require.NoError(t, err)
	require.Equal(t, uint64(42), primaryKey)
	require.Equal(t, uint16(messagePayloadFamilyID), familyID)
}

func TestDecodeTableStateKeyRejectsCorruptInputs(t *testing.T) {
	channelKey := channel.ChannelKey("room:1")
	key := encodeTableStateKey(channelKey, TableIDMessage, 42, messagePayloadFamilyID)

	_, _, err := decodeTableStateKey(key[:len(key)-1], channelKey, TableIDMessage)
	require.ErrorIs(t, err, channel.ErrCorruptValue)

	_, _, err = decodeTableStateKey(key, channel.ChannelKey("other"), TableIDMessage)
	require.ErrorIs(t, err, channel.ErrCorruptValue)
}

func TestTableIndexAndSystemKeyUseExpectedLayout(t *testing.T) {
	channelKey := channel.ChannelKey("room:1")

	indexPrefix := encodeTableIndexPrefix(channelKey, TableIDMessage, messageIndexIDMessageID)
	wantIndex := append([]byte{keyspaceTableIndex, byte(len(channelKey))}, []byte(channelKey)...)
	wantIndex = binary.BigEndian.AppendUint32(wantIndex, TableIDMessage)
	wantIndex = binary.BigEndian.AppendUint16(wantIndex, messageIndexIDMessageID)
	require.Equal(t, wantIndex, indexPrefix)

	systemKey := encodeTableSystemKey(channelKey, TableIDMessage, 9)
	wantSystem := append([]byte{keyspaceTableSystem, byte(len(channelKey))}, []byte(channelKey)...)
	wantSystem = binary.BigEndian.AppendUint32(wantSystem, TableIDMessage)
	wantSystem = binary.BigEndian.AppendUint16(wantSystem, 9)
	require.Equal(t, wantSystem, systemKey)
}

func TestMessageIDIndexValueRoundTripAndRejectsCorrupt(t *testing.T) {
	value := encodeMessageIDIndexValue(42)
	require.Len(t, value, 8)

	decoded, err := decodeMessageIDIndexValue(value)
	require.NoError(t, err)
	require.Equal(t, uint64(42), decoded)

	_, err = decodeMessageIDIndexValue(value[:7])
	require.ErrorIs(t, err, channel.ErrCorruptValue)
}

func TestIdempotencyIndexValueRoundTripAndRejectsCorrupt(t *testing.T) {
	row := messageRow{
		MessageSeq: 9,
		MessageID:  42,
		Payload:    []byte("hello"),
	}

	value, err := encodeIdempotencyIndexValue(row)
	require.NoError(t, err)
	require.Len(t, value, 24)

	decoded, err := decodeIdempotencyIndexValue(value)
	require.NoError(t, err)
	require.Equal(t, row.MessageSeq, decoded.MessageSeq)
	require.Equal(t, row.MessageID, decoded.MessageID)
	require.Equal(t, hashMessagePayload(row.Payload), decoded.PayloadHash)

	_, err = decodeIdempotencyIndexValue(value[:23])
	require.ErrorIs(t, err, channel.ErrCorruptValue)
}

func TestMessageFamilyEncodingFollowsCatalogFamilyColumns(t *testing.T) {
	table := MessageTable()
	table.Families[0].ColumnIDs = []uint16{
		messageColumnIDClientMsgNo,
		messageColumnIDMessageID,
		messageColumnIDTopic,
		messageColumnIDPayloadHash,
	}
	table.Families[1].ColumnIDs = []uint16{
		messageColumnIDPayload,
	}

	row := messageRow{
		MessageSeq:  9,
		MessageID:   42,
		ClientMsgNo: "c-1",
		Topic:       "topic",
		Payload:     []byte("hello"),
		PayloadHash: 123,
	}

	primary, payload, err := encodeMessageFamiliesWithDesc(row, table)
	require.NoError(t, err)
	require.Equal(t, table.Families[0].ColumnIDs, decodeTestFamilyColumnIDs(t, primary))
	require.Equal(t, table.Families[1].ColumnIDs, decodeTestFamilyColumnIDs(t, payload))

	decoded, err := decodeMessageFamiliesWithDesc(table, row.MessageSeq, primary, payload)
	require.NoError(t, err)
	require.Equal(t, row.MessageID, decoded.MessageID)
	require.Equal(t, row.ClientMsgNo, decoded.ClientMsgNo)
	require.Equal(t, row.Topic, decoded.Topic)
	require.Equal(t, row.PayloadHash, decoded.PayloadHash)
	require.Equal(t, row.Payload, decoded.Payload)
}

func TestMessageTableReturnsDeepCopy(t *testing.T) {
	first := MessageTable()
	first.Families[0].ColumnIDs[0] = messageColumnIDPayload

	second := MessageTable()
	require.Equal(t, messageColumnIDMessageID, second.Families[0].ColumnIDs[0])
}

func appendTestBytesColumn(dst []byte, columnID uint16, value []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(columnID))
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func decodeTestFamilyField(t *testing.T, payload []byte, offset int) (uint64, uint64, []byte, int) {
	t.Helper()

	columnID, n := binary.Uvarint(payload[offset:])
	require.Positive(t, n)
	offset += n

	length, n := binary.Uvarint(payload[offset:])
	require.Positive(t, n)
	offset += n
	require.GreaterOrEqual(t, len(payload[offset:]), int(length))

	value := append([]byte(nil), payload[offset:offset+int(length)]...)
	return columnID, length, value, offset + int(length)
}

func decodeTestFamilyColumnIDs(t *testing.T, payload []byte) []uint16 {
	t.Helper()

	require.NotEmpty(t, payload)
	require.Equal(t, expectedMessageFamilyCodecVersion, payload[0])

	offset := 1
	var ids []uint16
	for offset < len(payload) {
		columnID, _, _, next := decodeTestFamilyField(t, payload, offset)
		ids = append(ids, uint16(columnID))
		offset = next
	}
	return ids
}
