package meta

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/stretchr/testify/require"
)

// Optional accounting columns must be all absent or a complete version-1 tuple;
// otherwise a truncated row can silently erase the receipt obligation.
func TestMQTTAccountingRejectsPartialFormatMarker(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	m := prepareMQTTQualified(t, s.db)
	row, found, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(context.Background(), m.Key)
	require.NoError(t, err)
	require.True(t, found)
	pk := mqttDeliveryCursorPrimaryKey(m.Key)
	key, err := mqttDeliveryCursorTable.primaryRowKey(7, pk)
	require.NoError(t, err)
	encoded, err := encodeMQTTDeliveryCursorRow(key, row)
	require.NoError(t, err)
	env, err := rowcodec.Unwrap(key, encoded)
	require.NoError(t, err)
	// Deltas are relative to the last existing column, 24.
	for name, suffix := range map[string][]byte{
		"version-only":          {0x14, 1},
		"missing-head":          {0x14, 1, 0x24, 0},
		"missing-tail":          {0x14, 1, 0x14, 0},
		"tail-only":             {0x34, 0},
		"explicit-zero-version": {0x14, 0, 0x14, 0, 0x14, 0},
	} {
		t.Run(name, func(t *testing.T) {
			payload := append(append([]byte(nil), env.Payload...), suffix...)
			_, err := decodeMQTTDeliveryCursorRow(key, pk, rowcodec.Wrap(key, 1, env.Codec, env.Flags, payload))
			require.ErrorIs(t, err, dberrors.ErrCorruptValue)
		})
	}
	// An inflight exchange cannot hide byte debt without any unadmitted charge.
	row.AccountingVersion = 1
	row.AccountedThrough, row.WindowThrough = 102, 101
	row.PendingMessages, row.PendingBytes = 1, 20
	row.InflightCount, row.InflightBytes = 1, 10
	row.HeadPacketID, row.TailPacketID = 1, 1
	require.ErrorIs(t, ValidateMQTTDeliveryCursor(row), dberrors.ErrInvalidArgument)
}

// Consumption must validate its successor against the resulting cursor before
// deleting the old head. The range checksum alone cannot prove these relations.
func TestMQTTAccountingRejectsInconsistentSuccessorBeforeProgress(t *testing.T) {
	for _, mode := range []string{"count", "bytes", "revision", "time"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			m := prepareMQTTQualified(t, s.db)
			require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
			m.ExpectedRevision, m.Through, m.AddedMessages, m.AddedBytes = 5, 108, 1, 70
			m.Qualified = &MQTTQualifiedAccounting{From: 105, SubscriptionRevision: 3, Items: []MQTTAccountingItem{{Position: 107, Bytes: 70}}}
			require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
			before := readMQTTAccounting(t, s.db, m.Key)
			next := MQTTAccountingRange{Key: m.Key, From: 105, Through: 108, SubscriptionRevision: 3, EvaluatedAtMS: 1000, Items: append([]MQTTAccountingItem(nil), m.Qualified.Items...)}
			switch mode {
			case "count":
				next.Items = append(next.Items, MQTTAccountingItem{Position: 108})
			case "bytes":
				next.Items[0].Bytes++
			case "revision":
				next.SubscriptionRevision = before.Session.Revision + 2
			case "time":
				next.EvaluatedAtMS = before.Session.UpdatedAtMS + 1
			}
			key, err := mqttAccountingKey(7, m.Key, 105)
			require.NoError(t, err)
			value, err := encodeMQTTAccountingRange(key, next)
			require.NoError(t, err)
			raw := s.db.engine.NewBatch()
			defer raw.Close()
			require.NoError(t, raw.Set(key, value))
			require.NoError(t, raw.Commit(true))
			advance := mqttWindowMutation(t, s.db, MQTTWindowAdvance)
			advance.Through, advance.ReleasedMessages, advance.ReleasedBytes = 104, 2, 80
			batch := s.db.NewBatch()
			defer batch.Close()
			_, err = batch.MutateMQTTWindow(7, advance)
			require.NoError(t, err)
			require.ErrorIs(t, batch.Commit(context.Background()), dberrors.ErrCorruptValue)
			after := readMQTTAccounting(t, s.db, m.Key)
			require.Equal(t, before, after, "a corrupt successor must not erase the valid head")
		})
	}
}
