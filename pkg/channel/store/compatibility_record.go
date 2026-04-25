package store

import (
	"bytes"
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
	if err := row.validate(); err != nil {
		return channel.Record{}, err
	}

	payloadHash := row.PayloadHash
	if payloadHash == 0 {
		payloadHash = hashMessagePayload(row.Payload)
	}

	var buf bytes.Buffer
	if err := buf.WriteByte(channel.DurableMessageCodecVersion); err != nil {
		return channel.Record{}, err
	}
	if err := binary.Write(&buf, binary.BigEndian, row.MessageID); err != nil {
		return channel.Record{}, err
	}
	if err := buf.WriteByte(row.FramerFlags); err != nil {
		return channel.Record{}, err
	}
	if err := buf.WriteByte(row.Setting); err != nil {
		return channel.Record{}, err
	}
	if err := buf.WriteByte(row.StreamFlag); err != nil {
		return channel.Record{}, err
	}
	if err := buf.WriteByte(row.ChannelType); err != nil {
		return channel.Record{}, err
	}
	if err := binary.Write(&buf, binary.BigEndian, row.Expire); err != nil {
		return channel.Record{}, err
	}
	if err := binary.Write(&buf, binary.BigEndian, row.ClientSeq); err != nil {
		return channel.Record{}, err
	}
	if err := binary.Write(&buf, binary.BigEndian, row.StreamID); err != nil {
		return channel.Record{}, err
	}
	if err := binary.Write(&buf, binary.BigEndian, row.Timestamp); err != nil {
		return channel.Record{}, err
	}
	if err := binary.Write(&buf, binary.BigEndian, payloadHash); err != nil {
		return channel.Record{}, err
	}
	if err := writeCompatibilityRecordString(&buf, row.MsgKey); err != nil {
		return channel.Record{}, err
	}
	if err := writeCompatibilityRecordString(&buf, row.ClientMsgNo); err != nil {
		return channel.Record{}, err
	}
	if err := writeCompatibilityRecordString(&buf, row.StreamNo); err != nil {
		return channel.Record{}, err
	}
	if err := writeCompatibilityRecordString(&buf, row.ChannelID); err != nil {
		return channel.Record{}, err
	}
	if err := writeCompatibilityRecordString(&buf, row.Topic); err != nil {
		return channel.Record{}, err
	}
	if err := writeCompatibilityRecordString(&buf, row.FromUID); err != nil {
		return channel.Record{}, err
	}
	if err := writeCompatibilityRecordBytes(&buf, row.Payload); err != nil {
		return channel.Record{}, err
	}

	payload := buf.Bytes()
	return channel.Record{Payload: payload, SizeBytes: len(payload)}, nil
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
		row, err := decodeCompatibilityRecordPayload(record.Payload)
		if err != nil {
			return nil, err
		}
		row.MessageSeq = startSeq + uint64(i)
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

func writeCompatibilityRecordString(buf *bytes.Buffer, value string) error {
	return writeCompatibilityRecordBytes(buf, []byte(value))
}

func writeCompatibilityRecordBytes(buf *bytes.Buffer, value []byte) error {
	if len(value) > math.MaxUint32 {
		return channel.ErrInvalidArgument
	}
	if err := binary.Write(buf, binary.BigEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := buf.Write(value)
	return err
}
