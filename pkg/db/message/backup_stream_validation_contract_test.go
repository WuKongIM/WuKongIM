package message

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

type rawBackupTestChannel struct {
	key        ChannelKey
	id         ChannelID
	checkpoint Checkpoint
	system     []backupRawEntry
	rows       []messageRow
}

func TestBackupStreamParserAcceptsCanonicalEmptySnapshot(t *testing.T) {
	body := encodeRawBackupTestStream(t, 27, nil, nil)
	boundaryCalls := 0
	recordCalls := 0
	stats, err := ReplayBackupSnapshotReader(context.Background(), bytes.NewReader(body), int64(len(body)),
		func(BackupSnapshotBoundary) error { boundaryCalls++; return nil },
		func(BackupSnapshotRecord) error { recordCalls++; return nil },
	)
	if err != nil {
		t.Fatalf("ReplayBackupSnapshotReader(): %v", err)
	}
	if stats != (BackupSnapshotStats{HashSlot: 27}) || boundaryCalls != 0 || recordCalls != 0 {
		t.Fatalf("empty replay = stats %+v boundary calls %d record calls %d", stats, boundaryCalls, recordCalls)
	}
}

func TestBackupStreamChecksumAndEnvelopeValidationFailClosed(t *testing.T) {
	valid := encodeRawBackupTestStream(t, 1, nil, nil)
	badChecksum := append([]byte(nil), valid...)
	badChecksum[len(badChecksum)-1] ^= 0xff
	badMagic := encodeRawBackupTestStream(t, 1, nil, func(payload *bytes.Buffer) {
		bytes := payload.Bytes()
		bytes[0] ^= 0xff
	})
	badVersion := encodeRawBackupTestStream(t, 1, nil, func(payload *bytes.Buffer) {
		binary.BigEndian.PutUint16(payload.Bytes()[4:6], messageBackupSnapshotVersion+1)
	})
	tooManyChannels := encodeRawBackupTestStreamWithCount(t, 1, maxMessageBackupStreamChannels+1, nil)
	extraPayload := encodeRawBackupTestStreamWithCount(t, 1, 0, func(payload *bytes.Buffer) {
		payload.WriteByte(0x01)
	})

	tests := []struct {
		name string
		body []byte
		size int64
		want error
	}{
		{name: "nil source", body: nil, size: 0, want: dberrors.ErrCorruptValue},
		{name: "short source", body: []byte("short"), size: 5, want: dberrors.ErrCorruptValue},
		{name: "declared size mismatch", body: valid, size: int64(len(valid) + 1), want: dberrors.ErrCorruptValue},
		{name: "checksum mismatch", body: badChecksum, size: int64(len(badChecksum)), want: dberrors.ErrChecksumMismatch},
		{name: "bad magic", body: badMagic, size: int64(len(badMagic)), want: dberrors.ErrCorruptValue},
		{name: "bad version", body: badVersion, size: int64(len(badVersion)), want: dberrors.ErrCorruptValue},
		{name: "channel count bound", body: tooManyChannels, size: int64(len(tooManyChannels)), want: dberrors.ErrCorruptValue},
		{name: "trailing payload", body: extraPayload, size: int64(len(extraPayload)), want: dberrors.ErrCorruptValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var reader *bytes.Reader
			if test.body != nil {
				reader = bytes.NewReader(test.body)
			}
			_, err := ReplayBackupSnapshotReader(context.Background(), reader, test.size,
				func(BackupSnapshotBoundary) error { return nil },
				func(BackupSnapshotRecord) error { return nil },
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("ReplayBackupSnapshotReader() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestBackupStreamChannelAndRowValidationFailClosed(t *testing.T) {
	validRow := messageRow{
		MessageSeq: 1, MessageID: 101, ChannelID: "room", ChannelType: 1,
		FromUID: "u1", ClientMsgNo: "c1", Payload: []byte("one"),
	}
	channelWith := func(key ChannelKey, id ChannelID, checkpoint Checkpoint, rows ...messageRow) rawBackupTestChannel {
		return rawBackupTestChannel{key: key, id: id, checkpoint: checkpoint, rows: rows}
	}
	wrongIdentity := validRow
	wrongIdentity.ChannelID = "other"
	zeroSeq := validRow
	zeroSeq.MessageSeq = 0
	aheadSeq := validRow
	aheadSeq.MessageSeq = 2
	oversizedField := encodeRawBackupTestStreamWithCount(t, 1, 1, func(payload *bytes.Buffer) {
		_ = writeBackupUvarint(payload, maxMessageBackupStreamFieldBytes+1)
	})

	tests := []struct {
		name     string
		channels []rawBackupTestChannel
		body     []byte
		want     error
	}{
		{
			name:     "empty channel key",
			channels: []rawBackupTestChannel{channelWith("", ChannelID{ID: "room", Type: 1}, Checkpoint{})},
			want:     dberrors.ErrCorruptValue,
		},
		{
			name:     "empty channel id",
			channels: []rawBackupTestChannel{channelWith("room:1", ChannelID{Type: 1}, Checkpoint{})},
			want:     dberrors.ErrCorruptValue,
		},
		{
			name: "duplicate channel key",
			channels: []rawBackupTestChannel{
				channelWith("room:1", ChannelID{ID: "room", Type: 1}, Checkpoint{}),
				channelWith("room:1", ChannelID{ID: "room", Type: 1}, Checkpoint{}),
			},
			want: dberrors.ErrCorruptValue,
		},
		{
			name:     "invalid checkpoint boundary",
			channels: []rawBackupTestChannel{channelWith("room:1", ChannelID{ID: "room", Type: 1}, Checkpoint{LogStartOffset: 2, HW: 1})},
			want:     dberrors.ErrCorruptState,
		},
		{
			name: "system key outside channel namespace",
			channels: []rawBackupTestChannel{{
				key: "room:1", id: ChannelID{ID: "room", Type: 1}, checkpoint: Checkpoint{},
				system: []backupRawEntry{{Key: []byte("foreign"), Value: []byte("value")}},
			}},
			want: dberrors.ErrCorruptValue,
		},
		{
			name:     "zero message sequence",
			channels: []rawBackupTestChannel{channelWith("room:1", ChannelID{ID: "room", Type: 1}, Checkpoint{HW: 1}, zeroSeq)},
			want:     dberrors.ErrCorruptValue,
		},
		{
			name:     "message sequence beyond committed cut",
			channels: []rawBackupTestChannel{channelWith("room:1", ChannelID{ID: "room", Type: 1}, Checkpoint{HW: 1}, aheadSeq)},
			want:     dberrors.ErrCorruptValue,
		},
		{
			name:     "message identity disagrees with channel header",
			channels: []rawBackupTestChannel{channelWith("room:1", ChannelID{ID: "room", Type: 1}, Checkpoint{HW: 1}, wrongIdentity)},
			want:     dberrors.ErrCorruptState,
		},
		{name: "oversized key field", body: oversizedField, want: dberrors.ErrCorruptValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			if body == nil {
				body = encodeRawBackupTestStream(t, 1, test.channels, nil)
			}
			_, err := ReplayBackupSnapshotReader(context.Background(), bytes.NewReader(body), int64(len(body)),
				func(BackupSnapshotBoundary) error { return nil },
				func(BackupSnapshotRecord) error { return nil },
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("ReplayBackupSnapshotReader() error = %v, want %v", err, test.want)
			}
		})
	}
}

func encodeRawBackupTestStream(t *testing.T, slot uint16, channels []rawBackupTestChannel, mutate func(*bytes.Buffer)) []byte {
	t.Helper()
	return encodeRawBackupTestStreamWithCount(t, slot, uint32(len(channels)), func(payload *bytes.Buffer) {
		for _, channel := range channels {
			writeRawBackupTestChannel(t, payload, channel)
		}
		if mutate != nil {
			mutate(payload)
		}
	})
}

func encodeRawBackupTestStreamWithCount(t *testing.T, slot uint16, channelCount uint32, writePayload func(*bytes.Buffer)) []byte {
	t.Helper()
	var payload bytes.Buffer
	payload.Write(messageBackupSnapshotMagic[:])
	if err := binary.Write(&payload, binary.BigEndian, messageBackupSnapshotVersion); err != nil {
		t.Fatalf("write version: %v", err)
	}
	if err := binary.Write(&payload, binary.BigEndian, slot); err != nil {
		t.Fatalf("write slot: %v", err)
	}
	if err := binary.Write(&payload, binary.BigEndian, channelCount); err != nil {
		t.Fatalf("write channel count: %v", err)
	}
	if writePayload != nil {
		writePayload(&payload)
	}
	checksum := crc32.ChecksumIEEE(payload.Bytes())
	if err := binary.Write(&payload, binary.BigEndian, checksum); err != nil {
		t.Fatalf("write checksum: %v", err)
	}
	return payload.Bytes()
}

func writeRawBackupTestChannel(t *testing.T, writer *bytes.Buffer, channel rawBackupTestChannel) {
	t.Helper()
	if err := writeBackupString(writer, string(channel.key)); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := writeBackupString(writer, channel.id.ID); err != nil {
		t.Fatalf("write id: %v", err)
	}
	if err := writer.WriteByte(channel.id.Type); err != nil {
		t.Fatalf("write type: %v", err)
	}
	if _, err := writer.Write(encodeCheckpoint(channel.checkpoint)); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}
	if err := writeBackupUvarint(writer, uint64(len(channel.system))); err != nil {
		t.Fatalf("write system count: %v", err)
	}
	for _, entry := range channel.system {
		if err := writeBackupBytes(writer, entry.Key); err != nil {
			t.Fatalf("write system key: %v", err)
		}
		if err := writeBackupBytes(writer, entry.Value); err != nil {
			t.Fatalf("write system value: %v", err)
		}
	}
	if err := writeBackupUvarint(writer, uint64(len(channel.rows))); err != nil {
		t.Fatalf("write message count: %v", err)
	}
	for _, row := range channel.rows {
		headerKey := encodeMessageRowKey(channel.key, row.MessageSeq, messageHeaderFamilyID)
		header, err := encodeMessageHeader(headerKey, row)
		if err != nil {
			t.Fatalf("encode header: %v", err)
		}
		payloadKey := encodeMessageRowKey(channel.key, row.MessageSeq, messagePayloadFamilyID)
		payload, err := encodeMessagePayload(payloadKey, row)
		if err != nil {
			t.Fatalf("encode payload: %v", err)
		}
		if err := binary.Write(writer, binary.BigEndian, row.MessageSeq); err != nil {
			t.Fatalf("write sequence: %v", err)
		}
		if err := writeBackupBytes(writer, header); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if err := writeBackupBytes(writer, payload); err != nil {
			t.Fatalf("write payload: %v", err)
		}
	}
}
