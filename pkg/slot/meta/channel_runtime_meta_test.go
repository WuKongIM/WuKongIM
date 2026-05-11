package meta

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShardStoreUpsertAndGetChannelRuntimeMeta(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	meta := ChannelRuntimeMeta{
		ChannelID:    "u1",
		ChannelType:  1,
		ChannelEpoch: 3,
		LeaderEpoch:  2,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       2,
		Status:       2,
		LeaseUntilMS: 1700000000000,
	}

	if err := shard.UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta() error = %v", err)
	}

	got, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}

	if !reflect.DeepEqual(got, meta) {
		t.Fatalf("ChannelRuntimeMeta mismatch:\n got: %#v\nwant: %#v", got, meta)
	}
}

func TestShardStoreChannelRuntimeMetaPersistsRetentionBoundary(t *testing.T) {
	shard := newTestShardStore(t, 7)
	ctx := context.Background()
	meta := ChannelRuntimeMeta{
		ChannelID:            "retention-meta",
		ChannelType:          2,
		ChannelEpoch:         3,
		LeaderEpoch:          4,
		Replicas:             []uint64{1, 2},
		ISR:                  []uint64{1, 2},
		Leader:               1,
		MinISR:               2,
		Status:               2,
		Features:             1,
		LeaseUntilMS:         1234,
		RetentionThroughSeq:  99,
		RetentionUpdatedAtMS: 5678,
	}
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, meta))
	got, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	require.NoError(t, err)
	require.Equal(t, normalizeChannelRuntimeMeta(meta), got)
}

func TestChannelRuntimeMetaWriteFenceRoundTrip(t *testing.T) {
	shard := newTestShardStore(t, 7)
	ctx := context.Background()
	meta := testRuntimeMeta("write-fence-round-trip", 1)
	meta.WriteFenceToken = "task-1"
	meta.WriteFenceVersion = 3
	meta.WriteFenceReason = 1
	meta.WriteFenceUntilMS = 1710000000000

	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, meta))
	got, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	require.NoError(t, err)
	require.Equal(t, meta.WriteFenceToken, got.WriteFenceToken)
	require.Equal(t, meta.WriteFenceVersion, got.WriteFenceVersion)
	require.Equal(t, meta.WriteFenceReason, got.WriteFenceReason)
	require.Equal(t, meta.WriteFenceUntilMS, got.WriteFenceUntilMS)
}

func TestShardStoreAdvanceChannelRetentionThroughSeqOnlyMutatesRetention(t *testing.T) {
	shard := newTestShardStore(t, 7)
	ctx := context.Background()
	base := testRuntimeMeta("retain-advance", 2)
	base.ChannelEpoch = 10
	base.LeaderEpoch = 20
	base.Leader = 1
	base.LeaseUntilMS = 3000
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, base))

	req := ChannelRetentionAdvance{
		ChannelID:            base.ChannelID,
		ChannelType:          base.ChannelType,
		ExpectedChannelEpoch: base.ChannelEpoch,
		ExpectedLeaderEpoch:  base.LeaderEpoch,
		ExpectedLeader:       base.Leader,
		ExpectedLeaseUntilMS: base.LeaseUntilMS,
		RetentionThroughSeq:  42,
		RetentionUpdatedAtMS: 4000,
	}
	require.NoError(t, shard.AdvanceChannelRetentionThroughSeq(ctx, req))
	got, err := shard.GetChannelRuntimeMeta(ctx, base.ChannelID, base.ChannelType)
	require.NoError(t, err)
	base.RetentionThroughSeq = 42
	base.RetentionUpdatedAtMS = 4000
	require.Equal(t, normalizeChannelRuntimeMeta(base), got)
}

func TestShardStoreAdvanceChannelRetentionThroughSeqRejectsStaleFences(t *testing.T) {
	shard := newTestShardStore(t, 7)
	ctx := context.Background()
	base := testRuntimeMeta("retain-stale", 2)
	base.ChannelEpoch = 10
	base.LeaderEpoch = 20
	base.Leader = 1
	base.LeaseUntilMS = 3000
	base.RetentionThroughSeq = 30
	base.RetentionUpdatedAtMS = 3500
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, base))

	tests := []struct {
		name   string
		mutate func(*ChannelRetentionAdvance)
	}{
		{
			name: "wrong_channel_epoch",
			mutate: func(req *ChannelRetentionAdvance) {
				req.ExpectedChannelEpoch++
			},
		},
		{
			name: "wrong_leader_epoch",
			mutate: func(req *ChannelRetentionAdvance) {
				req.ExpectedLeaderEpoch++
			},
		},
		{
			name: "wrong_leader",
			mutate: func(req *ChannelRetentionAdvance) {
				req.ExpectedLeader = 2
			},
		},
		{
			name: "wrong_lease",
			mutate: func(req *ChannelRetentionAdvance) {
				req.ExpectedLeaseUntilMS++
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := channelRetentionAdvanceRequest(base, 42, 4000)
			tt.mutate(&req)
			err := shard.AdvanceChannelRetentionThroughSeq(ctx, req)
			require.ErrorIs(t, err, ErrStaleMeta)

			got, err := shard.GetChannelRuntimeMeta(ctx, base.ChannelID, base.ChannelType)
			require.NoError(t, err)
			require.Equal(t, normalizeChannelRuntimeMeta(base), got)
		})
	}
}

func TestShardStoreAdvanceChannelRetentionThroughSeqNoopsForLowerOrEqualBoundary(t *testing.T) {
	shard := newTestShardStore(t, 7)
	ctx := context.Background()
	base := testRuntimeMeta("retain-noop", 2)
	base.ChannelEpoch = 10
	base.LeaderEpoch = 20
	base.Leader = 1
	base.LeaseUntilMS = 3000
	base.RetentionThroughSeq = 42
	base.RetentionUpdatedAtMS = 4000
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, base))

	for _, seq := range []uint64{41, 42} {
		req := channelRetentionAdvanceRequest(base, seq, 5000)
		require.NoError(t, shard.AdvanceChannelRetentionThroughSeq(ctx, req))
		got, err := shard.GetChannelRuntimeMeta(ctx, base.ChannelID, base.ChannelType)
		require.NoError(t, err)
		require.Equal(t, normalizeChannelRuntimeMeta(base), got)
	}
}

func TestShardStoreUpsertChannelRuntimeMetaCanonicalizesSets(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	meta := ChannelRuntimeMeta{
		ChannelID:    "u2",
		ChannelType:  2,
		ChannelEpoch: 9,
		LeaderEpoch:  4,
		Replicas:     []uint64{3, 1, 2, 2},
		ISR:          []uint64{3, 1, 3},
		Leader:       3,
		MinISR:       2,
		Status:       1,
		Features:     8,
		LeaseUntilMS: 1700000000999,
	}

	if err := shard.UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta() error = %v", err)
	}

	got, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}

	want := meta
	want.Replicas = []uint64{1, 2, 3}
	want.ISR = []uint64{1, 3}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical ChannelRuntimeMeta mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestShardStoreDeleteChannelRuntimeMeta(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	meta := ChannelRuntimeMeta{
		ChannelID:   "u3",
		ChannelType: 3,
		Replicas:    []uint64{1, 2, 3},
		ISR:         []uint64{1, 2},
		Leader:      1,
		MinISR:      2,
	}

	if err := shard.UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta() error = %v", err)
	}
	if err := shard.DeleteChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType); err != nil {
		t.Fatalf("DeleteChannelRuntimeMeta() error = %v", err)
	}

	_, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannelRuntimeMeta() err = %v, want ErrNotFound", err)
	}
}

func TestShardStoreUpsertChannelRuntimeMetaRejectsStaleLeaderEpoch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	shard := db.ForSlot(7)

	current := ChannelRuntimeMeta{
		ChannelID:    "runtime-stale",
		ChannelType:  1,
		ChannelEpoch: 9,
		LeaderEpoch:  4,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       2,
		Features:     1,
		LeaseUntilMS: 2000,
	}
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, current))

	stale := current
	stale.LeaderEpoch = current.LeaderEpoch - 1
	stale.Leader = 1
	stale.LeaseUntilMS = 5000
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, stale))

	got, err := shard.GetChannelRuntimeMeta(ctx, current.ChannelID, current.ChannelType)
	require.NoError(t, err)
	require.Equal(t, normalizeChannelRuntimeMeta(current), got)
}

func TestShardStoreUpsertChannelRuntimeMetaPreservesLongerLeaseForSameEpoch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	shard := db.ForSlot(7)

	current := ChannelRuntimeMeta{
		ChannelID:    "runtime-lease",
		ChannelType:  1,
		ChannelEpoch: 9,
		LeaderEpoch:  4,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       2,
		Features:     1,
		LeaseUntilMS: 5000,
	}
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, current))

	shorter := current
	shorter.LeaseUntilMS = 1000
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, shorter))

	got, err := shard.GetChannelRuntimeMeta(ctx, current.ChannelID, current.ChannelType)
	require.NoError(t, err)
	require.Equal(t, int64(5000), got.LeaseUntilMS)
}

func TestResolveMonotonicChannelRuntimeMetaPreservesRetentionOnSameEpoch(t *testing.T) {
	existing := baseChannelRuntimeMetaForRetentionTest()
	existing.RetentionThroughSeq = 80
	existing.RetentionUpdatedAtMS = 2000

	candidate := existing
	candidate.RetentionThroughSeq = 40
	candidate.RetentionUpdatedAtMS = 1000
	candidate.LeaseUntilMS = existing.LeaseUntilMS + 100

	got, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
	require.True(t, shouldWrite)
	require.Equal(t, uint64(80), got.RetentionThroughSeq)
	require.Equal(t, int64(2000), got.RetentionUpdatedAtMS)
}

func TestResolveMonotonicChannelRuntimeMetaPreservesRetentionAcrossHigherChannelEpoch(t *testing.T) {
	existing := baseChannelRuntimeMetaForRetentionTest()
	existing.RetentionThroughSeq = 90
	existing.RetentionUpdatedAtMS = 3000

	candidate := existing
	candidate.ChannelEpoch = existing.ChannelEpoch + 1
	candidate.LeaderEpoch = existing.LeaderEpoch + 1
	candidate.RetentionThroughSeq = 20
	candidate.RetentionUpdatedAtMS = 1000

	got, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
	require.True(t, shouldWrite)
	require.Equal(t, uint64(90), got.RetentionThroughSeq)
	require.Equal(t, int64(3000), got.RetentionUpdatedAtMS)
}

func TestResolveMonotonicChannelRuntimeMetaKeepsNewestRetentionTimestampForEqualBoundary(t *testing.T) {
	existing := baseChannelRuntimeMetaForRetentionTest()
	existing.RetentionThroughSeq = 70
	existing.RetentionUpdatedAtMS = 5000

	candidate := existing
	candidate.RetentionUpdatedAtMS = 4000
	candidate.LeaseUntilMS = existing.LeaseUntilMS + 100

	got, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
	require.True(t, shouldWrite)
	require.Equal(t, uint64(70), got.RetentionThroughSeq)
	require.Equal(t, int64(5000), got.RetentionUpdatedAtMS)
}

func TestChannelRuntimeMetaMonotonicPreservesWriteFence(t *testing.T) {
	existing := testRuntimeMeta("write-fence-monotonic", 1)
	existing.ChannelEpoch = 5
	existing.LeaderEpoch = 7
	existing.WriteFenceToken = "task-1"
	existing.WriteFenceVersion = 9
	existing.WriteFenceReason = 2
	existing.WriteFenceUntilMS = 1710000000000

	candidate := existing
	candidate.ChannelEpoch = existing.ChannelEpoch + 1
	candidate.WriteFenceToken = ""
	candidate.WriteFenceVersion = 0
	candidate.WriteFenceReason = 0
	candidate.WriteFenceUntilMS = 0

	got, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
	require.True(t, shouldWrite)
	require.Equal(t, existing.WriteFenceToken, got.WriteFenceToken)
	require.Equal(t, existing.WriteFenceVersion, got.WriteFenceVersion)
	require.Equal(t, existing.WriteFenceReason, got.WriteFenceReason)
	require.Equal(t, existing.WriteFenceUntilMS, got.WriteFenceUntilMS)
}

func TestChannelRuntimeMetaWriteFenceMonotonicPreservesSameVersionRegressions(t *testing.T) {
	existing := testRuntimeMeta("write-fence-same-version", 1)
	existing.ChannelEpoch = 5
	existing.LeaderEpoch = 7
	existing.WriteFenceToken = "task-1"
	existing.WriteFenceVersion = 9
	existing.WriteFenceReason = 2
	existing.WriteFenceUntilMS = 1710000000000

	tests := []struct {
		name   string
		mutate func(*ChannelRuntimeMeta)
	}{
		{
			name: "changed_token",
			mutate: func(candidate *ChannelRuntimeMeta) {
				candidate.WriteFenceToken = "task-2"
				candidate.WriteFenceUntilMS = existing.WriteFenceUntilMS + 1000
			},
		},
		{
			name: "changed_reason",
			mutate: func(candidate *ChannelRuntimeMeta) {
				candidate.WriteFenceReason = existing.WriteFenceReason + 1
				candidate.WriteFenceUntilMS = existing.WriteFenceUntilMS + 1000
			},
		},
		{
			name: "shorter_until",
			mutate: func(candidate *ChannelRuntimeMeta) {
				candidate.WriteFenceUntilMS = existing.WriteFenceUntilMS - 1000
			},
		},
		{
			name: "zero_until",
			mutate: func(candidate *ChannelRuntimeMeta) {
				candidate.WriteFenceUntilMS = 0
			},
		},
		{
			name: "clear",
			mutate: func(candidate *ChannelRuntimeMeta) {
				candidate.WriteFenceToken = ""
				candidate.WriteFenceReason = 0
				candidate.WriteFenceUntilMS = 0
			},
		},
		{
			name: "replacement",
			mutate: func(candidate *ChannelRuntimeMeta) {
				candidate.WriteFenceToken = "task-2"
				candidate.WriteFenceReason = existing.WriteFenceReason + 1
				candidate.WriteFenceUntilMS = existing.WriteFenceUntilMS + 1000
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := existing
			candidate.ChannelEpoch = existing.ChannelEpoch + 1
			tt.mutate(&candidate)

			got, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
			require.True(t, shouldWrite)
			require.Equal(t, existing.WriteFenceToken, got.WriteFenceToken)
			require.Equal(t, existing.WriteFenceVersion, got.WriteFenceVersion)
			require.Equal(t, existing.WriteFenceReason, got.WriteFenceReason)
			require.Equal(t, existing.WriteFenceUntilMS, got.WriteFenceUntilMS)
		})
	}
}

func TestChannelRuntimeMetaWriteFenceMonotonicRejectsSameVersionLeaseExtension(t *testing.T) {
	existing := testRuntimeMeta("write-fence-same-version-extend", 1)
	existing.ChannelEpoch = 5
	existing.LeaderEpoch = 7
	existing.WriteFenceToken = "task-1"
	existing.WriteFenceVersion = 9
	existing.WriteFenceReason = 2
	existing.WriteFenceUntilMS = 1710000000000

	candidate := existing
	candidate.ChannelEpoch = existing.ChannelEpoch + 1
	candidate.WriteFenceUntilMS = existing.WriteFenceUntilMS + 1000

	got, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
	require.True(t, shouldWrite)
	require.Equal(t, existing.WriteFenceToken, got.WriteFenceToken)
	require.Equal(t, existing.WriteFenceVersion, got.WriteFenceVersion)
	require.Equal(t, existing.WriteFenceReason, got.WriteFenceReason)
	require.Equal(t, existing.WriteFenceUntilMS, got.WriteFenceUntilMS)
}

func TestChannelRuntimeMetaWriteFenceMonotonicDoesNotFabricateSameVersionFence(t *testing.T) {
	existing := testRuntimeMeta("write-fence-no-fabricate", 1)
	existing.ChannelEpoch = 5
	existing.LeaderEpoch = 7
	existing.WriteFenceVersion = 9

	candidate := existing
	candidate.ChannelEpoch = existing.ChannelEpoch + 1
	candidate.WriteFenceToken = "task-1"
	candidate.WriteFenceReason = 2
	candidate.WriteFenceUntilMS = 1710000000000

	got, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
	require.True(t, shouldWrite)
	require.Empty(t, got.WriteFenceToken)
	require.Equal(t, existing.WriteFenceVersion, got.WriteFenceVersion)
	require.Zero(t, got.WriteFenceReason)
	require.Zero(t, got.WriteFenceUntilMS)
}

func TestChannelRuntimeMetaWriteFenceMonotonicAllowsHigherVersionUpdate(t *testing.T) {
	existing := testRuntimeMeta("write-fence-higher-version", 1)
	existing.ChannelEpoch = 5
	existing.LeaderEpoch = 7
	existing.WriteFenceToken = "task-1"
	existing.WriteFenceVersion = 9
	existing.WriteFenceReason = 2
	existing.WriteFenceUntilMS = 1710000000000

	candidate := existing
	candidate.ChannelEpoch = existing.ChannelEpoch + 1
	candidate.WriteFenceToken = "task-2"
	candidate.WriteFenceVersion = existing.WriteFenceVersion + 1
	candidate.WriteFenceReason = 3
	candidate.WriteFenceUntilMS = existing.WriteFenceUntilMS + 1000

	got, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
	require.True(t, shouldWrite)
	require.Equal(t, candidate.WriteFenceToken, got.WriteFenceToken)
	require.Equal(t, candidate.WriteFenceVersion, got.WriteFenceVersion)
	require.Equal(t, candidate.WriteFenceReason, got.WriteFenceReason)
	require.Equal(t, candidate.WriteFenceUntilMS, got.WriteFenceUntilMS)
}

func TestChannelRuntimeMetaWriteFenceEncodingSkipsZeroFenceColumns(t *testing.T) {
	shard := newTestShardStore(t, 7)
	ctx := context.Background()
	meta := testRuntimeMeta("write-fence-zero-encode", 1)

	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, meta))
	key := encodeChannelRuntimeMetaPrimaryKey(7, meta.ChannelID, meta.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	value, err := shard.db.getValue(key)
	require.NoError(t, err)
	_, payload, err := decodeWrappedValue(key, value)
	require.NoError(t, err)

	columns, err := decodeTestColumnIDs(payload)
	require.NoError(t, err)
	require.NotContains(t, columns, channelRuntimeMetaColumnIDWriteFenceToken)
	require.NotContains(t, columns, channelRuntimeMetaColumnIDWriteFenceVersion)
	require.NotContains(t, columns, channelRuntimeMetaColumnIDWriteFenceReason)
	require.NotContains(t, columns, channelRuntimeMetaColumnIDWriteFenceUntilMS)
}

func TestDecodeChannelRuntimeMetaDefaultsMissingRetentionToZero(t *testing.T) {
	key := encodeChannelRuntimeMetaPrimaryKey(7, "old-retention", 2, channelRuntimeMetaPrimaryFamilyID)
	payload := make([]byte, 0, 128)
	payload = appendUint64Value(payload, channelRuntimeMetaColumnIDChannelEpoch, 0, 3)
	payload = appendUint64Value(payload, channelRuntimeMetaColumnIDLeaderEpoch, channelRuntimeMetaColumnIDChannelEpoch, 4)
	payload = appendRawBytesValue(payload, channelRuntimeMetaColumnIDReplicas, channelRuntimeMetaColumnIDLeaderEpoch, encodeUint64Slice([]uint64{1, 2}))
	payload = appendRawBytesValue(payload, channelRuntimeMetaColumnIDISR, channelRuntimeMetaColumnIDReplicas, encodeUint64Slice([]uint64{1, 2}))
	payload = appendUint64Value(payload, channelRuntimeMetaColumnIDLeader, channelRuntimeMetaColumnIDISR, 1)
	payload = appendIntValue(payload, channelRuntimeMetaColumnIDMinISR, channelRuntimeMetaColumnIDLeader, 2)
	payload = appendUint64Value(payload, channelRuntimeMetaColumnIDStatus, channelRuntimeMetaColumnIDMinISR, 2)
	payload = appendUint64Value(payload, channelRuntimeMetaColumnIDFeatures, channelRuntimeMetaColumnIDStatus, 1)
	payload = appendIntValue(payload, channelRuntimeMetaColumnIDLeaseUntilMS, channelRuntimeMetaColumnIDFeatures, 1234)

	got, err := decodeChannelRuntimeMetaFamilyValue(key, wrapFamilyValue(key, payload))
	require.NoError(t, err)
	require.Zero(t, got.RetentionThroughSeq)
	require.Zero(t, got.RetentionUpdatedAtMS)
	require.Empty(t, got.WriteFenceToken)
	require.Zero(t, got.WriteFenceVersion)
	require.Zero(t, got.WriteFenceReason)
	require.Zero(t, got.WriteFenceUntilMS)
}

func TestWriteBatchUpsertChannelRuntimeMetaRejectsStaleLeaderEpoch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	shard := db.ForSlot(7)

	current := ChannelRuntimeMeta{
		ChannelID:    "runtime-batch-stale",
		ChannelType:  1,
		ChannelEpoch: 9,
		LeaderEpoch:  4,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       2,
		Features:     1,
		LeaseUntilMS: 2000,
	}
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, current))

	stale := current
	stale.LeaderEpoch = current.LeaderEpoch - 1
	stale.Leader = 1
	stale.LeaseUntilMS = 5000
	wb := db.NewWriteBatch()
	require.NoError(t, wb.UpsertChannelRuntimeMeta(7, stale))
	require.NoError(t, wb.Commit())
	wb.Close()

	got, err := shard.GetChannelRuntimeMeta(ctx, current.ChannelID, current.ChannelType)
	require.NoError(t, err)
	require.Equal(t, normalizeChannelRuntimeMeta(current), got)
}

func TestWriteBatchAdvanceChannelRetentionThroughSeqUpdatesLaterBatchReads(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	shard := db.ForSlot(7)
	base := testRuntimeMeta("retain-batch", 2)
	base.ChannelEpoch = 10
	base.LeaderEpoch = 20
	base.Leader = 1
	base.LeaseUntilMS = 3000
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, base))

	wb := db.NewWriteBatch()
	require.NoError(t, wb.AdvanceChannelRetentionThroughSeq(7, channelRetentionAdvanceRequest(base, 42, 4000)))
	next := base
	next.Features = 99
	require.NoError(t, wb.UpsertChannelRuntimeMeta(7, next))
	require.NoError(t, wb.Commit())
	wb.Close()

	got, err := shard.GetChannelRuntimeMeta(ctx, base.ChannelID, base.ChannelType)
	require.NoError(t, err)
	next.RetentionThroughSeq = 42
	next.RetentionUpdatedAtMS = 4000
	require.Equal(t, normalizeChannelRuntimeMeta(next), got)
}

func TestWriteBatchAdvanceChannelRetentionThroughSeqAfterDeleteSeesNotFound(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	shard := db.ForSlot(7)
	base := testRuntimeMeta("retain-batch-delete-first", 2)
	base.ChannelEpoch = 10
	base.LeaderEpoch = 20
	base.Leader = 1
	base.LeaseUntilMS = 3000
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, base))

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.DeleteChannelRuntimeMeta(7, base.ChannelID, base.ChannelType))
	err := wb.AdvanceChannelRetentionThroughSeq(7, channelRetentionAdvanceRequest(base, 42, 4000))
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, wb.Commit())

	_, err = shard.GetChannelRuntimeMeta(ctx, base.ChannelID, base.ChannelType)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestWriteBatchDeleteChannelRuntimeMetaAfterRetentionAdvanceWins(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	shard := db.ForSlot(7)
	base := testRuntimeMeta("retain-batch-delete-last", 2)
	base.ChannelEpoch = 10
	base.LeaderEpoch = 20
	base.Leader = 1
	base.LeaseUntilMS = 3000
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, base))

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.AdvanceChannelRetentionThroughSeq(7, channelRetentionAdvanceRequest(base, 42, 4000)))
	require.NoError(t, wb.DeleteChannelRuntimeMeta(7, base.ChannelID, base.ChannelType))
	require.NoError(t, wb.Commit())

	_, err := shard.GetChannelRuntimeMeta(ctx, base.ChannelID, base.ChannelType)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestShardStoreUpsertChannelRuntimeMetaRejectsInvalidTopology(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	tests := []struct {
		name string
		meta ChannelRuntimeMeta
	}{
		{
			name: "leader_not_in_replicas",
			meta: ChannelRuntimeMeta{
				ChannelID:   "invalid-1",
				ChannelType: 1,
				Replicas:    []uint64{2, 3},
				ISR:         []uint64{2, 3},
				Leader:      1,
				MinISR:      1,
			},
		},
		{
			name: "isr_outside_replicas",
			meta: ChannelRuntimeMeta{
				ChannelID:   "invalid-2",
				ChannelType: 2,
				Replicas:    []uint64{1, 2},
				ISR:         []uint64{1, 3},
				Leader:      1,
				MinISR:      1,
			},
		},
		{
			name: "min_isr_exceeds_replicas",
			meta: ChannelRuntimeMeta{
				ChannelID:   "invalid-3",
				ChannelType: 3,
				Replicas:    []uint64{1, 2},
				ISR:         []uint64{1, 2},
				Leader:      1,
				MinISR:      3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := shard.UpsertChannelRuntimeMeta(ctx, tt.meta)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("UpsertChannelRuntimeMeta() err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestDBListChannelRuntimeMeta(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	first := ChannelRuntimeMeta{
		ChannelID:    "list-1",
		ChannelType:  1,
		ChannelEpoch: 3,
		LeaderEpoch:  4,
		Replicas:     []uint64{1, 2},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       1,
		Status:       2,
		Features:     1,
		LeaseUntilMS: 1700000000001,
	}
	second := ChannelRuntimeMeta{
		ChannelID:    "list-2",
		ChannelType:  2,
		ChannelEpoch: 5,
		LeaderEpoch:  6,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       3,
		Features:     2,
		LeaseUntilMS: 1700000000002,
	}

	require.NoError(t, db.ForSlot(7).UpsertChannelRuntimeMeta(ctx, first))
	require.NoError(t, db.ForSlot(9).UpsertChannelRuntimeMeta(ctx, second))

	got, err := db.ListChannelRuntimeMeta(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []ChannelRuntimeMeta{first, second}, got)
}

func decodeTestColumnIDs(payload []byte) ([]uint16, error) {
	columns := make([]uint16, 0, 16)
	var colID uint16
	for len(payload) > 0 {
		tag := payload[0]
		payload = payload[1:]

		delta := uint16(tag >> 4)
		if delta == 0 {
			return nil, ErrCorruptValue
		}
		colID += delta
		columns = append(columns, colID)

		switch tag & 0x0f {
		case valueTypeBytes:
			length, n := binary.Uvarint(payload)
			if n <= 0 || uint64(len(payload[n:])) < length {
				return nil, ErrCorruptValue
			}
			payload = payload[n+int(length):]
		case valueTypeInt, valueTypeUint:
			_, n := binary.Uvarint(payload)
			if n <= 0 {
				return nil, ErrCorruptValue
			}
			payload = payload[n:]
		default:
			return nil, ErrCorruptValue
		}
	}
	return columns, nil
}

func newTestShardStore(t *testing.T, slot uint64) *ShardStore {
	t.Helper()
	return openTestDB(t).ForSlot(slot)
}

func baseChannelRuntimeMetaForRetentionTest() ChannelRuntimeMeta {
	return ChannelRuntimeMeta{
		ChannelID:    "retention-monotonic",
		ChannelType:  1,
		ChannelEpoch: 10,
		LeaderEpoch:  5,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       1,
		MinISR:       2,
		Status:       2,
		Features:     1,
		LeaseUntilMS: 1000,
	}
}

func testRuntimeMeta(channelID string, channelType int64) ChannelRuntimeMeta {
	return ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  channelType,
		ChannelEpoch: 1,
		LeaderEpoch:  1,
		Replicas:     []uint64{1, 2},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       1,
		Status:       2,
		Features:     1,
		LeaseUntilMS: 1000,
	}
}

func channelRetentionAdvanceRequest(meta ChannelRuntimeMeta, seq uint64, updatedAtMS int64) ChannelRetentionAdvance {
	return ChannelRetentionAdvance{
		ChannelID:            meta.ChannelID,
		ChannelType:          meta.ChannelType,
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedLeaseUntilMS: meta.LeaseUntilMS,
		RetentionThroughSeq:  seq,
		RetentionUpdatedAtMS: updatedAtMS,
	}
}
