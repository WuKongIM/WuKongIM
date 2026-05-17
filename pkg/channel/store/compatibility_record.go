package store

import (
	"encoding/binary"
	"io"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

// decodeCompatibilityRecordPayload decodes one compatibility payload into a structured row.
func decodeCompatibilityRecordPayload(payload []byte) (messageRow, error) {
	if len(payload) < channel.DurableMessageHeaderSize {
		return messageRow{}, io.ErrUnexpectedEOF
	}
	if payload[0] != channel.DurableMessageCodecVersion {
		return messageRow{}, channel.ErrCorruptValue
	}

	row := messageRow{
		MessageID:   binary.BigEndian.Uint64(payload[1:9]),
		FramerFlags: payload[9],
		Setting:     payload[10],
		StreamFlag:  payload[11],
		ChannelType: payload[12],
		Expire:      binary.BigEndian.Uint32(payload[13:17]),
		ClientSeq:   binary.BigEndian.Uint64(payload[17:25]),
		StreamID:    binary.BigEndian.Uint64(payload[25:33]),
		Timestamp:   int32(binary.BigEndian.Uint32(payload[33:37])),
		PayloadHash: binary.BigEndian.Uint64(payload[37:45]),
	}
	if row.MessageID == 0 {
		return messageRow{}, channel.ErrCorruptValue
	}

	pos := channel.DurableMessageHeaderSize
	msgKey, nextPos, err := readCompatibilitySizedBytesView(payload, pos)
	if err != nil {
		return messageRow{}, err
	}
	clientMsgNo, nextPos, err := readCompatibilitySizedBytesView(payload, nextPos)
	if err != nil {
		return messageRow{}, err
	}
	streamNo, nextPos, err := readCompatibilitySizedBytesView(payload, nextPos)
	if err != nil {
		return messageRow{}, err
	}
	channelID, nextPos, err := readCompatibilitySizedBytesView(payload, nextPos)
	if err != nil {
		return messageRow{}, err
	}
	topic, nextPos, err := readCompatibilitySizedBytesView(payload, nextPos)
	if err != nil {
		return messageRow{}, err
	}
	fromUID, nextPos, err := readCompatibilitySizedBytesView(payload, nextPos)
	if err != nil {
		return messageRow{}, err
	}
	body, _, err := readCompatibilitySizedBytesView(payload, nextPos)
	if err != nil {
		return messageRow{}, err
	}

	row.MsgKey = string(msgKey)
	row.ClientMsgNo = string(clientMsgNo)
	row.StreamNo = string(streamNo)
	row.ChannelID = string(channelID)
	row.Topic = string(topic)
	row.FromUID = string(fromUID)
	row.Payload = append([]byte(nil), body...)
	return row, nil
}

// encodeCompatibilityRecord encodes one structured row into the compatibility record layout.
func encodeCompatibilityRecord(row messageRow) (channel.Record, error) {
	payloadHash := row.PayloadHash
	if payloadHash == 0 {
		payloadHash = hashMessagePayload(row.Payload)
	}

	size, err := compatibilityRecordPayloadSize(row)
	if err != nil {
		return channel.Record{}, err
	}

	payload := make([]byte, 0, size)
	payload = append(payload, channel.DurableMessageCodecVersion)
	payload = binary.BigEndian.AppendUint64(payload, row.MessageID)
	payload = append(payload, row.FramerFlags, row.Setting, row.StreamFlag, row.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, row.Expire)
	payload = binary.BigEndian.AppendUint64(payload, row.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, row.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(row.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, payloadHash)
	payload = appendCompatibilityRecordString(payload, row.MsgKey)
	payload = appendCompatibilityRecordString(payload, row.ClientMsgNo)
	payload = appendCompatibilityRecordString(payload, row.StreamNo)
	payload = appendCompatibilityRecordString(payload, row.ChannelID)
	payload = appendCompatibilityRecordString(payload, row.Topic)
	payload = appendCompatibilityRecordString(payload, row.FromUID)
	payload = appendCompatibilityRecordBytes(payload, row.Payload)
	return channel.Record{Payload: payload, SizeBytes: len(payload)}, nil
}

// compatibilityRecordPayloadSize returns the encoded compatibility payload size without materializing it.
func compatibilityRecordPayloadSize(row messageRow) (int, error) {
	if err := row.validate(); err != nil {
		return 0, err
	}

	size := channel.DurableMessageHeaderSize
	fieldSizes := [...]int{
		len(row.MsgKey),
		len(row.ClientMsgNo),
		len(row.StreamNo),
		len(row.ChannelID),
		len(row.Topic),
		len(row.FromUID),
		len(row.Payload),
	}
	for _, fieldSize := range fieldSizes {
		if fieldSize > math.MaxUint32 {
			return 0, channel.ErrInvalidArgument
		}
		size += 4 + fieldSize
	}
	return size, nil
}

// toCompatibilityRecord encodes the structured row into the compatibility record layout.
func (r messageRow) toCompatibilityRecord() (channel.Record, error) {
	return encodeCompatibilityRecord(r)
}

// structuredRowsFromCompatibilityRecords decodes compatibility records and stamps contiguous message sequences.
func structuredRowsFromCompatibilityRecords(startSeq uint64, records []channel.Record) ([]messageRow, error) {
	if len(records) == 0 {
		return nil, nil
	}
	if startSeq == 0 {
		return nil, channel.ErrInvalidArgument
	}

	rows := make([]messageRow, 0, len(records))
	for i, record := range records {
		expectedSeq := startSeq + uint64(i)
		if record.Index != 0 && record.Index != expectedSeq {
			return nil, channel.ErrCorruptState
		}

		row, err := decodeCompatibilityRecordPayload(record.Payload)
		if err != nil {
			return nil, err
		}
		if record.ID != 0 && record.ID != row.MessageID {
			return nil, channel.ErrCorruptState
		}
		row.MessageSeq = expectedSeq
		rows = append(rows, row)
	}
	return rows, nil
}

// compatibilityRecordsFromRows materializes structured rows as compatibility records.
func compatibilityRecordsFromRows(rows []messageRow) ([]channel.Record, error) {
	records := make([]channel.Record, 0, len(rows))
	for _, row := range rows {
		record, err := encodeCompatibilityRecord(row)
		if err != nil {
			return nil, err
		}
		record.ID = row.MessageID
		record.Index = row.MessageSeq
		records = append(records, record)
	}
	return records, nil
}

// logRecordsFromStructuredRows materializes structured rows as offset-addressable log records.
func logRecordsFromStructuredRows(rows []messageRow) ([]LogRecord, error) {
	records := make([]LogRecord, 0, len(rows))
	for _, row := range rows {
		record, err := encodeCompatibilityRecord(row)
		if err != nil {
			return nil, err
		}
		records = append(records, LogRecord{Offset: row.MessageSeq - 1, Payload: record.Payload})
	}
	return records, nil
}

func readCompatibilitySizedBytesView(payload []byte, pos int) ([]byte, int, error) {
	if len(payload)-pos < 4 {
		return nil, pos, io.ErrUnexpectedEOF
	}
	size := int(binary.BigEndian.Uint32(payload[pos : pos+4]))
	pos += 4
	if len(payload)-pos < size {
		return nil, pos, io.ErrUnexpectedEOF
	}
	return payload[pos : pos+size], pos + size, nil
}

func appendCompatibilityRecordString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendCompatibilityRecordBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}
