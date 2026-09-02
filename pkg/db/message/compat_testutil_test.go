package message

import (
	"encoding/binary"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func openCompatEngine(t *testing.T) *Engine {
	t.Helper()
	eng, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() {
		if err := eng.Close(); err != nil {
			t.Fatalf("Engine.Close(): %v", err)
		}
	})
	return eng
}

func mustForChannel(t testing.TB, eng *Engine, key channel.ChannelKey, id channel.ChannelID) *ChannelStore {
	t.Helper()
	store, err := eng.ForChannel(key, id)
	if err != nil {
		t.Fatalf("ForChannel(%q): %v", key, err)
	}
	return store
}

func deletePhysicalTestKey(t *testing.T, db *Engine, key []byte) {
	t.Helper()
	batch := db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Delete(key); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
}

func setPhysicalTestValue(t *testing.T, db *Engine, key, value []byte) {
	t.Helper()
	batch := db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(key, value); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
}

func encodeCompatTestMessage(t *testing.T, msg channel.Message) []byte {
	t.Helper()
	payload := make([]byte, 0, channel.DurableMessageHeaderSize+64)
	payload = append(payload, channel.DurableMessageCodecVersion)
	payload = binary.BigEndian.AppendUint64(payload, msg.MessageID)
	payload = append(payload, 0, byte(msg.Setting), byte(msg.StreamFlag), msg.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, msg.Expire)
	payload = binary.BigEndian.AppendUint64(payload, msg.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, msg.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(msg.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, compatTestFNV64a(msg.Payload))
	payload = appendCompatTestString(payload, msg.MsgKey)
	payload = appendCompatTestString(payload, msg.ClientMsgNo)
	payload = appendCompatTestString(payload, msg.StreamNo)
	payload = appendCompatTestString(payload, msg.ChannelID)
	payload = appendCompatTestString(payload, msg.Topic)
	payload = appendCompatTestString(payload, msg.FromUID)
	payload = appendCompatTestBytes(payload, msg.Payload)
	return payload
}

func compatTestRecord(t *testing.T, messageID uint64, channelID string, clientMsgNo string) channel.Record {
	t.Helper()
	msg := channel.Message{
		MessageID:   messageID,
		ClientMsgNo: clientMsgNo,
		ChannelID:   channelID,
		ChannelType: 1,
		FromUID:     "u1",
		Payload:     []byte("payload"),
	}
	payload := encodeCompatTestMessage(t, msg)
	return channel.Record{ID: messageID, Payload: payload, SizeBytes: len(payload)}
}

func compatExactTestRecord(t *testing.T, epoch uint64, messageID uint64, channelID string, clientMsgNo string) channel.Record {
	t.Helper()
	row := messageRow{
		MessageID: messageID, ClientMsgNo: clientMsgNo, ChannelID: channelID, ChannelType: 1,
		FromUID: "u1", ServerTimestampMS: 1_700_000_000_000 + int64(messageID), Payload: []byte("payload"),
	}
	record, err := compatibilityRecordFromRow(row)
	if err != nil {
		t.Fatalf("compatibilityRecordFromRow(): %v", err)
	}
	record.Epoch = epoch
	return record
}

func sealCompatProposalManifest(t *testing.T, manifest DurableProposalManifest, records []channel.Record) DurableProposalManifest {
	t.Helper()
	rows, err := compatibilityRowsFromRecords(manifest.BaseOffset+1, records)
	if err != nil {
		t.Fatalf("compatibilityRowsFromRecords(): %v", err)
	}
	manifest.Digest = [32]byte{}
	entries, ok := deriveDurableProposalEntries(manifest, records, rows)
	if !ok || len(entries) == 0 {
		t.Fatal("deriveDurableProposalEntries() failed")
	}
	manifest.Digest = entries[len(entries)-1].Digest
	return manifest
}

func appendCompatTestString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendCompatTestBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func compatTestFNV64a(payload []byte) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	hash := uint64(offset)
	for _, b := range payload {
		hash ^= uint64(b)
		hash *= prime
	}
	return hash
}
