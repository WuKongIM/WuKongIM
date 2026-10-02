package meta

import (
	"context"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/stretchr/testify/require"
)

func TestMQTTRuntimeSourcePinsPhysicalLifetimeAcrossRoutingAndRecreation(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	key := ChannelKey{ChannelID: "source", ChannelType: 2}
	q := MQTTRead{Kind: MQTTReadChannelRuntime, RuntimeChannel: key}
	read := func() *MQTTRuntimeView {
		r, err := s.db.ReadMQTTState(ctx, 7, q)
		require.NoError(t, err)
		require.True(t, r.Done)
		require.Equal(t, MQTTReadCursor{}, r.After)
		require.NotNil(t, r.Runtime)
		require.NoError(t, ValidateMQTTRuntimeView(key, r.Runtime))
		return r.Runtime
	}
	initial := read()
	require.Nil(t, initial.Meta)
	require.Zero(t, initial.RetiredThrough)
	m := createRuntimeIncarnation(t, s.db, 7, testRuntimeMeta(key.ChannelID, key.ChannelType))
	require.Equal(t, m, *read().Meta)
	m.ChannelEpoch, m.LeaderEpoch, m.RouteGeneration = 19, 20, 21
	_, err := s.db.HashSlot(7).UpsertChannelRuntimeMeta(ctx, m)
	require.NoError(t, err)
	live := read()
	require.Zero(t, live.RetiredThrough, "ordinary authority changes retain physical identity")
	_, _, err = s.db.HashSlot(7).GetChannelRuntimeMeta(ctx, key.ChannelID, key.ChannelType)
	require.NoError(t, err)
	snap, err := s.db.engine.NewSnapshot()
	require.NoError(t, err)
	defer snap.Close()
	require.NoError(t, s.db.HashSlot(7).DeleteChannelRuntimeMeta(ctx, key.ChannelID, key.ChannelType))
	retired := read()
	require.Nil(t, retired.Meta)
	require.Equal(t, live.Meta.RouteGeneration, retired.RetiredThrough)
	fresh := createRuntimeIncarnation(t, s.db, 7, testRuntimeMeta(key.ChannelID, key.ChannelType))
	now := read()
	require.Equal(t, retired.RetiredThrough, now.RetiredThrough)
	require.Equal(t, fresh, *now.Meta)
	old, err := (&Shard{db: s.db, hashSlot: 7, readSnapshot: snap}).readMQTTState(ctx, q)
	require.NoError(t, err)
	require.Equal(t, live, old.Runtime, "pinned reads cannot borrow the new cache or floor")
	now.Meta.Replicas[0] = 999
	require.NotEqual(t, now.Meta.Replicas, read().Meta.Replicas)
	for _, other := range []MQTTRead{{Kind: MQTTReadChannelRuntime, RuntimeChannel: ChannelKey{ChannelID: "source", ChannelType: 1}}, {Kind: MQTTReadChannelRuntime, RuntimeChannel: ChannelKey{ChannelID: "other", ChannelType: 2}}} {
		r, err := s.db.ReadMQTTState(ctx, 7, other)
		require.NoError(t, err)
		require.Nil(t, r.Runtime.Meta)
		require.Zero(t, r.Runtime.RetiredThrough)
	}
	r, err := s.db.ReadMQTTState(ctx, 8, q)
	require.NoError(t, err)
	require.Zero(t, r.Runtime.RetiredThrough)
}

func TestMQTTRuntimeSourceRejectsCorruptOrInconsistentFloor(t *testing.T) {
	for _, mode := range []string{"zero", "checksum", "key", "version", "live_below_floor", "maximum"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			createRuntimeIncarnation(t, s.db, 7, testRuntimeMeta("source", 2))
			key := runtimeRetirementKey(7, "source", 2)
			wrapKey, version, floor := key, byte(1), uint64(99)
			switch mode {
			case "zero":
				floor = 0
			case "key":
				wrapKey = runtimeRetirementKey(8, "source", 2)
			case "version":
				version = 2
			case "maximum":
				floor = math.MaxUint64
			}
			value := rowcodec.Wrap(wrapKey, version, rowcodec.CodecFixed, rowcodec.FlagChecksum, binary.BigEndian.AppendUint64(nil, floor))
			if mode == "checksum" {
				value[len(value)-1] ^= 1
			}
			b := s.engine.NewBatch()
			require.NoError(t, b.Set(key, value))
			require.NoError(t, b.Commit(true))
			require.NoError(t, b.Close())
			_, err := s.db.ReadMQTTState(context.Background(), 7, MQTTRead{Kind: MQTTReadChannelRuntime, RuntimeChannel: ChannelKey{ChannelID: "source", ChannelType: 2}})
			require.Error(t, err)
		})
	}
}

func TestMQTTRuntimeSourceRequestIsBoundedAndClosed(t *testing.T) {
	q := MQTTRead{Kind: MQTTReadChannelRuntime, RuntimeChannel: ChannelKey{ChannelID: "source", ChannelType: 2}}
	require.NoError(t, ValidateMQTTRead(q))
	for _, mutate := range []func(*MQTTRead){
		func(v *MQTTRead) { v.Limit = 1 }, func(v *MQTTRead) { v.Namespace = "n" },
		func(v *MQTTRead) { v.RuntimeChannel.ChannelID = strings.Repeat("a", 4097) },
		func(v *MQTTRead) { v.RuntimeChannel.ChannelType = 3 },
		func(v *MQTTRead) { v.RuntimeChannel.ChannelID = "\x00" },
		func(v *MQTTRead) { v.Kind = MQTTReadInboxAdmission; v.AdmissionChannel = "alice@bob" },
	} {
		bad := q
		mutate(&bad)
		require.Error(t, ValidateMQTTRead(bad))
	}
}
