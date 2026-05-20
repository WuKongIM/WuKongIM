package store

import (
	"encoding/binary"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestChannelStoreGetMessageBySeqFailsOnOrphanPayloadFamily(t *testing.T) {
	st := newTestChannelStore(t)
	row := messageRowFromChannelMessage(channel.Message{
		MessageID:   41,
		ClientMsgNo: "orphan",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	row.MessageSeq = 1

	_, payloadFamily, err := encodeMessageFamilies(row)
	require.NoError(t, err)
	mustSetDBValue(t, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, row.MessageSeq, messagePayloadFamilyID), payloadFamily)

	_, ok, err := st.GetMessageBySeq(row.MessageSeq)
	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.False(t, ok)
}

func TestMessageTableAppendSingleRowAllocationBudget(t *testing.T) {
	st := newTestChannelStore(t)
	row := messageRowFromChannelMessage(channel.Message{
		MessageID:   42,
		ClientMsgNo: "client-1",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	row.MessageSeq = 1

	allocs := testing.AllocsPerRun(1000, func() {
		batch := st.engine.db.NewBatch()
		if err := st.messageTable().append(batch, []messageRow{row}); err != nil {
			t.Fatalf("messageTable.append() error = %v", err)
		}
		if err := batch.Close(); err != nil {
			t.Fatalf("Batch.Close() error = %v", err)
		}
	})

	require.LessOrEqual(t, allocs, 1.0)
}

func TestLookupMessageIDSeqForAppendAvoidsKeyAndValueCopies(t *testing.T) {
	st := newTestChannelStore(t)
	row := messageRowFromChannelMessage(channel.Message{
		MessageID:   42,
		ClientMsgNo: "client-1",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	row.MessageSeq = 1

	record, err := row.toCompatibilityRecord()
	if err != nil {
		t.Fatalf("toCompatibilityRecord() error = %v", err)
	}
	if _, err := st.Append([]channel.Record{{ID: row.MessageID, Payload: record.Payload, SizeBytes: record.SizeBytes}}); err != nil {
		t.Fatalf("ChannelStore.Append() error = %v", err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		seq, ok, err := st.messageTable().lookupMessageIDSeqForAppend(row.MessageID)
		if err != nil {
			t.Fatalf("lookupMessageIDSeqForAppend() error = %v", err)
		}
		if !ok || seq != row.MessageSeq {
			t.Fatalf("lookupMessageIDSeqForAppend() = (%d, %v), want (%d, true)", seq, ok, row.MessageSeq)
		}
	})

	require.Zero(t, allocs)
}

func TestChannelStoreAppendTrustedSkipsExistingIndexLookupsForTrustedRows(t *testing.T) {
	tests := []struct {
		name   string
		poison func(t *testing.T, st *ChannelStore, existing messageRow, next messageRow)
	}{
		{
			name: "message_id",
			poison: func(t *testing.T, st *ChannelStore, _ messageRow, next messageRow) {
				value := make([]byte, 8)
				binary.BigEndian.PutUint64(value, 1)
				mustSetDBValue(t, st.engine.db, encodeMessageIDIndexKey(st.key, next.MessageID), value)
			},
		},
		{
			name: "idempotency",
			poison: func(t *testing.T, st *ChannelStore, existing messageRow, next messageRow) {
				value := make([]byte, 24)
				binary.BigEndian.PutUint64(value[0:8], existing.MessageSeq)
				binary.BigEndian.PutUint64(value[8:16], existing.MessageID)
				binary.BigEndian.PutUint64(value[16:24], existing.PayloadHash)
				mustSetDBValue(t, st.engine.db, encodeMessageIdempotencyIndexKey(st.key, next.FromUID, next.ClientMsgNo), value)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newTestChannelStore(t)
			existing := messageRowFromChannelMessage(channel.Message{
				MessageID:   41,
				ClientMsgNo: "client-1",
				FromUID:     "u1",
				ChannelID:   st.id.ID,
				ChannelType: st.id.Type,
				Payload:     []byte("one"),
			})
			existing.MessageSeq = 1
			existingRecord, err := existing.toCompatibilityRecord()
			require.NoError(t, err)
			_, err = st.Append([]channel.Record{{
				ID:        existing.MessageID,
				Index:     existing.MessageSeq,
				Payload:   existingRecord.Payload,
				SizeBytes: existingRecord.SizeBytes,
			}})
			require.NoError(t, err)

			next := messageRowFromChannelMessage(channel.Message{
				MessageID:   42,
				ClientMsgNo: "client-2",
				FromUID:     "u2",
				ChannelID:   st.id.ID,
				ChannelType: st.id.Type,
				Payload:     []byte("two"),
			})
			next.MessageSeq = 2
			tt.poison(t, st, existing, next)

			nextRecord, err := next.toCompatibilityRecord()
			require.NoError(t, err)
			base, err := st.AppendTrusted([]channel.Record{{
				ID:        next.MessageID,
				Index:     next.MessageSeq,
				Payload:   nextRecord.Payload,
				SizeBytes: nextRecord.SizeBytes,
			}})
			require.NoError(t, err)
			require.Equal(t, uint64(1), base)

			got, ok, err := st.GetMessageBySeq(next.MessageSeq)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, next.MessageID, got.MessageID)

			seq, ok, err := getStoredMessageIDIndexSeq(t, st, next.MessageID)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, next.MessageSeq, seq)

			hit, ok, err := getIndexedIdempotencyHit(t, st, next.FromUID, next.ClientMsgNo)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, next.MessageSeq, hit.MessageSeq)
			require.Equal(t, next.MessageID, hit.MessageID)
			require.Equal(t, next.PayloadHash, hit.PayloadHash)
		})
	}
}

func TestChannelStoreListMessagesBySeqFailsOnPayloadHashMismatch(t *testing.T) {
	st := newTestChannelStore(t)
	msg := channel.Message{
		MessageID:   42,
		ClientMsgNo: "hash",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	}
	payload := mustEncodeStoreMessage(t, msg)
	_, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
	require.NoError(t, err)

	tampered := messageRowFromChannelMessage(msg)
	tampered.MessageSeq = 1
	tampered.Payload = []byte("tampered")
	tampered.PayloadHash = hashMessagePayload(msg.Payload)

	_, tamperedPayloadFamily, err := encodeMessageFamilies(tampered)
	require.NoError(t, err)
	mustSetDBValue(t, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, tampered.MessageSeq, messagePayloadFamilyID), tamperedPayloadFamily)

	_, err = st.ListMessagesBySeq(1, 10, 4096, false)
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreListMessagesBySeqFailsOnMissingPayloadFamily(t *testing.T) {
	st := newTestChannelStore(t)
	row := messageRowFromChannelMessage(channel.Message{
		MessageID:   48,
		ClientMsgNo: "missing-payload",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	row.MessageSeq = 1

	primaryFamily, _, err := encodeMessageFamilies(row)
	require.NoError(t, err)
	mustSetDBValue(t, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, row.MessageSeq, messagePrimaryFamilyID), primaryFamily)

	_, err = st.ListMessagesBySeq(1, 10, 4096, false)
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreListMessagesBySeqReverseFailsOnMissingPrimaryFamily(t *testing.T) {
	st := newTestChannelStore(t)
	row := messageRowFromChannelMessage(channel.Message{
		MessageID:   49,
		ClientMsgNo: "missing-primary",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	row.MessageSeq = 1

	_, payloadFamily, err := encodeMessageFamilies(row)
	require.NoError(t, err)
	mustSetDBValue(t, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, row.MessageSeq, messagePayloadFamilyID), payloadFamily)

	_, err = st.ListMessagesBySeq(1, 10, 4096, true)
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreListMessagesBySeqRespectsMaxBytes(t *testing.T) {
	st := newTestChannelStore(t)
	first := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   52,
		ClientMsgNo: "max-1",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	second := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   53,
		ClientMsgNo: "max-2",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("two"),
	})
	third := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   54,
		ClientMsgNo: "max-3",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("three"),
	})
	_, err := st.Append([]channel.Record{
		{Payload: first, SizeBytes: len(first)},
		{Payload: second, SizeBytes: len(second)},
		{Payload: third, SizeBytes: len(third)},
	})
	require.NoError(t, err)

	forward, err := st.ListMessagesBySeq(1, 3, len(first)+1, false)
	require.NoError(t, err)
	require.Len(t, forward, 1)
	require.Equal(t, uint64(52), forward[0].MessageID)

	reverse, err := st.ListMessagesBySeq(3, 3, len(third)+1, true)
	require.NoError(t, err)
	require.Len(t, reverse, 1)
	require.Equal(t, uint64(54), reverse[0].MessageID)
}

func TestChannelStoreReadSingleRecordAvoidsBatchRowAllocation(t *testing.T) {
	st := newBenchmarkMessageTableStore(t, 1)

	allocs := testing.AllocsPerRun(100, func() {
		records, err := st.Read(0, 1<<20)
		require.NoError(t, err)
		require.Len(t, records, 1)
	})

	require.Less(t, allocs, float64(12), "single-record log reads should avoid allocating the generic row batch")
}

func TestChannelStoreTruncateFailsOnOrphanPayloadFamily(t *testing.T) {
	st := newTestChannelStore(t)
	validPayload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   46,
		ClientMsgNo: "valid",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	_, err := st.Append([]channel.Record{{Payload: validPayload, SizeBytes: len(validPayload)}})
	require.NoError(t, err)

	orphanRow := messageRowFromChannelMessage(channel.Message{
		MessageID:   47,
		ClientMsgNo: "orphan-truncate",
		FromUID:     "u2",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("two"),
	})
	orphanRow.MessageSeq = 2

	_, payloadFamily, err := encodeMessageFamilies(orphanRow)
	require.NoError(t, err)
	mustSetDBValue(t, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, orphanRow.MessageSeq, messagePayloadFamilyID), payloadFamily)

	err = st.Truncate(0)
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreAppendRejectsDuplicateMessageID(t *testing.T) {
	st := newTestChannelStore(t)
	first := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   43,
		ClientMsgNo: "m1",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	second := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   43,
		ClientMsgNo: "m2",
		FromUID:     "u2",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("two"),
	})

	_, err := st.Append([]channel.Record{{Payload: first, SizeBytes: len(first)}})
	require.NoError(t, err)

	_, err = st.Append([]channel.Record{{Payload: second, SizeBytes: len(second)}})
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreAppendRejectsDuplicateIdempotencyKey(t *testing.T) {
	st := newTestChannelStore(t)
	first := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   44,
		ClientMsgNo: "same",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})
	second := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   45,
		ClientMsgNo: "same",
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("two"),
	})

	_, err := st.Append([]channel.Record{{Payload: first, SizeBytes: len(first)}})
	require.NoError(t, err)

	_, err = st.Append([]channel.Record{{Payload: second, SizeBytes: len(second)}})
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreAppendSkipsOptionalIndexesWhenClientMsgNoEmpty(t *testing.T) {
	st := newTestChannelStore(t)
	payload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   51,
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte("one"),
	})

	_, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
	require.NoError(t, err)

	msg, ok, err := st.GetMessageByMessageID(51)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(1), msg.MessageSeq)
	require.Empty(t, msg.ClientMsgNo)

	_, ok, err = getStoredClientMsgNoIndexSeq(t, st, "", 1)
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = getIndexedIdempotencyHit(t, st, "u1", "")
	require.NoError(t, err)
	require.False(t, ok)
}
