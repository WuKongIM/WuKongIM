package fsm

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func mqttReclamationCommandFixture() metadb.MQTTSessionReclamation {
	return metadb.MQTTSessionReclamation{Namespace: "main", ClientID: "phone", ExpectedRevision: 3, ThroughGeneration: 1, UpdatedAtMS: 2000}
}
func TestMQTTReclamationCommandAtomicBatchSnapshotAndReplay(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, e := NewStateMachineWithHashSlots(db, 11, []uint16{7})
	require.NoError(t, e)
	s := mqttSessionCommandFixture()
	create, e := EncodeMQTTSessionCASCommand(0, s)
	require.NoError(t, e)
	sub := mqttSubscriptionCommandFixture()
	subscribe, e := EncodeMQTTSubscriptionCommand(sub)
	require.NoError(t, e)
	s.Revision = 3
	s.State = metadb.MQTTSessionEnded
	s.LeaseUntilMS = 0
	s.TerminationReason = metadb.MQTTSessionExplicit
	end, e := EncodeMQTTSessionCASCommand(2, s)
	require.NoError(t, e)
	reclaim, e := EncodeMQTTSessionReclamationCommand(mqttReclamationCommandFixture())
	require.NoError(t, e)
	require.Equal(t, []byte{1, 75}, reclaim[:2])
	var commands []multiraft.Command
	for i, raw := range [][]byte{create, subscribe, end, reclaim, reclaim} {
		commands = append(commands, multiraft.Command{SlotID: 11, HashSlot: 7, Index: uint64(i + 1), Term: 1, Data: raw})
	}
	results, e := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, commands)
	require.NoError(t, e)
	var r metadb.MQTTSessionReclamationResult
	require.NoError(t, json.Unmarshal(results[3], &r))
	require.True(t, r.Done)
	require.Equal(t, 1, r.RemovedSubscriptions)
	require.Equal(t, metadb.MQTTSessionCASApplied, r.Status)
	require.NoError(t, json.Unmarshal(results[4], &r))
	require.Equal(t, metadb.MQTTSessionCASUnchanged, r.Status)
	_, found, e := db.ForHashSlot(7).GetMQTTSubscription(ctx, s.Namespace, s.ClientID, 1, sub.Subscription.Topic)
	require.NoError(t, e)
	require.False(t, found)
	for _, ids := range [][2]uint16{{12, 7}, {11, 8}} {
		_, e = sm.Apply(ctx, multiraft.Command{SlotID: multiraft.SlotID(ids[0]), HashSlot: ids[1], Index: 6, Term: 1, Data: reclaim})
		require.ErrorIs(t, e, metadb.ErrInvalidArgument)
	}
	index, e := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx)
	require.NoError(t, e)
	require.EqualValues(t, 5, index)
	snapshot, e := sm.Snapshot(ctx)
	require.NoError(t, e)
	target := openTestDB(t)
	restored, e := NewStateMachineWithHashSlots(target, 11, []uint16{7})
	require.NoError(t, e)
	require.NoError(t, restored.Restore(ctx, snapshot))
	raw, e := restored.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 7, Index: 6, Term: 1, Data: reclaim})
	require.NoError(t, e)
	require.NoError(t, json.Unmarshal(raw, &r))
	require.Equal(t, metadb.MQTTSessionCASUnchanged, r.Status)
	require.True(t, r.Done)
	got, found, e := target.ForHashSlot(7).GetMQTTSession(ctx, s.Namespace, s.ClientID)
	require.NoError(t, e)
	require.True(t, found)
	require.EqualValues(t, 1, got.ReclaimedThroughGeneration)
	view, e := DecodeCommandInspection(reclaim)
	require.NoError(t, e)
	require.Equal(t, "mqtt_session_reclamation", view.Type)
}
func TestMQTTReclamationCommandStrictBoundedWire(t *testing.T) {
	valid, e := EncodeMQTTSessionReclamationCommand(mqttReclamationCommandFixture())
	require.NoError(t, e)
	for _, raw := range [][]byte{{1, 75}, append([]byte{1, 75}, bytes.Repeat([]byte(" "), (16<<10)+1)...), bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":2`), 1), append(bytes.Clone(valid), []byte(` {}`)...), bytes.Replace(valid, []byte(`"client_id":"phone"`), []byte(`"client_id":"phone","future":1`), 1), bytes.Replace(valid, []byte(`"through_generation":1`), []byte(`"through_generation":0`), 1)} {
		_, e := DecodeCommandInspection(raw)
		require.Error(t, e)
	}
}
