package meta

import (
	"context"
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
