package meta

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/stretchr/testify/require"
)

func inboxDrainFixture() MQTTSourceBinding {
	r := mqttSourceBindingFixture()
	r.Key.Owner = MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}
	r.AuthorizationVersion = 0
	return r
}

func TestMQTTInboxDrainProgressIsIndependentAndMonotonic(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	r := inboxDrainFixture()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, 0, r).Status)
	r.Revision, r.Stage, r.DiscoveryDone, r.DiscoveryAfterChannelID, r.DiscoveryAfterChannelType = 2, MQTTBindingActive, true, "initial", 1
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, 1, r).Status)
	r.Revision, r.IntentRevision, r.Stage, r.DrainVersion = 3, 4, MQTTBindingRemoving, 1
	for _, done := range []bool{false, true} {
		skipped := r
		skipped.DrainDone, skipped.ProgressRevision = done, 4
		skipped.DrainAfterSourceID, skipped.DrainAfterSourceGeneration = "1:z", "z"
		require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, st.db, 2, skipped).Status)
	}
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, 2, r).Status)
	for _, pair := range [][2]string{{"1:z", "z"}, {"1:z", "aa"}, {"1:aa", "a"}} {
		expected := r.Revision
		r.Revision++
		r.ProgressRevision = 4
		r.DrainAfterSourceID, r.DrainAfterSourceGeneration = pair[0], pair[1]
		require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, expected, r).Status)
		require.Equal(t, MQTTSessionCASUnchanged, writeMQTTSourceBinding(t, st.db, expected, r).Status)
	}
	for _, change := range []func(*MQTTSourceBinding){
		func(r *MQTTSourceBinding) { r.DrainAfterSourceID = "1:z"; r.DrainAfterSourceGeneration = "aaa" },
		func(r *MQTTSourceBinding) {
			r.DrainVersion = 0
			r.DrainAfterSourceID = ""
			r.DrainAfterSourceGeneration = ""
		},
		func(r *MQTTSourceBinding) { r.DiscoveryAfterChannelID = "initial-new" },
	} {
		bad := r
		bad.Revision++
		change(&bad)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, st.db, r.Revision, bad).Status)
	}
	r.Revision++
	r.DrainDone = true
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, r.Revision-1, r).Status)
	for _, change := range []func(*MQTTSourceBinding){
		func(r *MQTTSourceBinding) { r.DrainDone = false },
		func(r *MQTTSourceBinding) { r.DrainAfterSourceGeneration = "aa" },
	} {
		bad := r
		bad.Revision++
		change(&bad)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, st.db, r.Revision, bad).Status)
	}
	r.Revision++
	r.Stage, r.ReleaseReason, r.RecoveryAtMS = MQTTBindingRemoved, MQTTBindingDrained, 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, r.Revision-1, r).Status)
	require.Equal(t, "initial", r.DiscoveryAfterChannelID)
	late := inboxDrainFixture()
	late.Revision = r.Revision + 1
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, st.db, r.Revision, late).Status)
}

func TestMQTTInboxDrainValidationAndLegacyClosure(t *testing.T) {
	r := inboxDrainFixture()
	r.Stage, r.DrainVersion = MQTTBindingRemoving, 1
	for _, change := range []func(*MQTTSourceBinding){
		func(r *MQTTSourceBinding) { r.DrainVersion = 2 },
		func(r *MQTTSourceBinding) { r.Stage = MQTTBindingPreparing },
		func(r *MQTTSourceBinding) { r.Stage = MQTTBindingActive; r.DiscoveryDone = true },
		func(r *MQTTSourceBinding) {
			r.Key.Owner = MQTTBindingOwner{Kind: MQTTBindingChannel, ID: "1:person", Generation: "g"}
		},
		func(r *MQTTSourceBinding) { r.DrainAfterSourceID = "1:person" },
		func(r *MQTTSourceBinding) { r.DrainAfterSourceGeneration = "g" },
		func(r *MQTTSourceBinding) {
			r.DrainAfterSourceID = strings.Repeat("a", 4097)
			r.DrainAfterSourceGeneration = "g"
			r.ProgressRevision = 2
		},
		func(r *MQTTSourceBinding) {
			r.DrainAfterSourceID = "1:person"
			r.DrainAfterSourceGeneration = strings.Repeat("g", 129)
			r.ProgressRevision = 2
		},
		func(r *MQTTSourceBinding) { r.DrainDone = true },
		func(r *MQTTSourceBinding) { r.DrainDone = true; r.ProgressRevision = 1 },
		func(r *MQTTSourceBinding) { r.DrainVersion = 0; r.DrainDone = true },
		func(r *MQTTSourceBinding) {
			r.Stage = MQTTBindingRemoved
			r.ReleaseReason = MQTTBindingDrained
			r.RecoveryAtMS = 0
		},
	} {
		bad := r
		change(&bad)
		require.Error(t, ValidateMQTTSourceBinding(bad))
	}
	st := openTestMetaStore(t)
	defer st.close(t)
	legacy := inboxDrainFixture()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, 0, legacy).Status)
	legacy.Revision, legacy.Stage, legacy.IntentRevision = 2, MQTTBindingRemoving, 4
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, 1, legacy).Status)
	bad := legacy
	bad.Revision++
	bad.Stage, bad.ReleaseReason, bad.RecoveryAtMS = MQTTBindingRemoved, MQTTBindingDrained, 0
	require.NoError(t, ValidateMQTTSourceBinding(bad), "old tombstones remain readable")
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, st.db, 2, bad).Status)
	legacy.Revision++
	legacy.DrainVersion = 1
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, 2, legacy).Status)
	legacy.Revision++
	legacy.ReleaseReason, legacy.ProgressRevision = MQTTBindingSessionEnded, 5
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, 3, legacy).Status)
	legacy.Revision++
	legacy.Stage, legacy.RecoveryAtMS = MQTTBindingRemoved, 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, 4, legacy).Status)
}

func TestMQTTInboxDrainCodecRequiresCompleteMarker(t *testing.T) {
	legacy := inboxDrainFixture()
	legacy.Stage = MQTTBindingRemoving
	pk := mqttSourceBindingPrimaryKey(legacy.Key)
	key, err := mqttSourceBindingTable.primaryRowKey(9, pk)
	require.NoError(t, err)
	old, err := mqttSourceBindingTable.encodeValue(key, legacy)
	require.NoError(t, err)
	env, err := rowcodec.Unwrap(key, old)
	require.NoError(t, err)
	decoded, err := mqttSourceBindingTable.decodeValue(key, pk, old)
	require.NoError(t, err)
	require.Equal(t, legacy, decoded)
	raw, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "drain_")
	r := legacy
	r.DrainVersion = 1
	r.ProgressRevision = r.IntentRevision
	r.DrainAfterSourceID = strings.Repeat("s", 4096)
	r.DrainAfterSourceGeneration = strings.Repeat("g", 128)
	encoded, err := mqttSourceBindingTable.encodeValue(key, r)
	require.NoError(t, err)
	decoded, err = mqttSourceBindingTable.decodeValue(key, pk, encoded)
	require.NoError(t, err)
	require.Equal(t, r, decoded)
	inspection := inspectMQTTSourceBindingRow(r)
	require.Equal(t, r.DrainAfterSourceID, inspection["drain_after_source_id"])
	for mask := 1; mask < 16; mask++ {
		var tail rowcodec.Writer
		require.NoError(t, tail.Uint64(27, 0))
		prefix := len(tail.Bytes())
		if mask&1 != 0 {
			require.NoError(t, tail.Uint8(29, 1))
		}
		if mask&2 != 0 {
			require.NoError(t, tail.String(30, ""))
		}
		if mask&4 != 0 {
			require.NoError(t, tail.String(31, ""))
		}
		if mask&8 != 0 {
			require.NoError(t, tail.Uint8(32, 0))
		}
		payload := append(bytes.Clone(env.Payload), tail.Bytes()[prefix:]...)
		_, err := mqttSourceBindingTable.decodeValue(key, pk, rowcodec.Wrap(key, 1, env.Codec, env.Flags, payload))
		if mask == 15 {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	for _, pair := range [][2]uint8{{0, 0}, {2, 0}, {1, 2}} {
		var tail rowcodec.Writer
		require.NoError(t, tail.Uint64(27, 0))
		prefix := len(tail.Bytes())
		require.NoError(t, tail.Uint8(29, pair[0]))
		require.NoError(t, tail.String(30, ""))
		require.NoError(t, tail.String(31, ""))
		require.NoError(t, tail.Uint8(32, pair[1]))
		payload := append(bytes.Clone(env.Payload), tail.Bytes()[prefix:]...)
		_, err := mqttSourceBindingTable.decodeValue(key, pk, rowcodec.Wrap(key, 1, env.Codec, env.Flags, payload))
		require.Error(t, err)
	}
}
