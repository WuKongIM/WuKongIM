package meta

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessageUpdateBatchPreservesShardAlignmentAndFreshReads(t *testing.T) {
	db, err := Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := context.Background()
	init := db.NewWriteBatch()
	for _, slot := range []uint16{1, 255} {
		require.NoError(t, init.UpsertChannel(slot, Channel{ChannelID: "same-id", ChannelType: 2}))
		require.NoError(t, init.UpsertChannelRuntimeMeta(slot, ChannelRuntimeMeta{ChannelID: "same-id", ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 1, MinISR: 1, Replicas: []uint64{1}, ISR: []uint64{1}}))
		_, err = init.ApplyMessageUpdate(slot, MessageUpdateMutation{Op: "init", ChannelID: "same-id", ChannelType: 2, Generation: "generation"})
		require.NoError(t, err)
	}
	require.NoError(t, init.Commit())
	require.NoError(t, init.Close())
	reads := []MessageUpdateRead{{ChannelID: "same-id", ChannelType: 2, IDs: []uint64{42}}, {ChannelID: "same-id", ChannelType: 2, IDs: []uint64{42}}, {ChannelID: "missing", ChannelType: 2, Limit: 2}}
	slots := []uint16{255, 1, 7}
	before, err := db.ReadMessageUpdatesBatch(ctx, slots, reads)
	require.NoError(t, err)
	require.Len(t, before, 3)
	for _, p := range before {
		require.Empty(t, p.Updates)
	}
	// The shared decoder must retain one view even if a commit lands between
	// logical-shard reads; a subsequent public batch must observe that commit.
	pinned, err := db.engine.NewSnapshot()
	require.NoError(t, err)
	defer pinned.Close()
	edit := db.NewWriteBatch()
	_, err = edit.ApplyMessageUpdate(255, MessageUpdateMutation{Op: "update", ChannelID: "same-id", ChannelType: 2, Generation: "generation", MessageID: 42, MessageSeq: 1, ExpectedChannelEpoch: 1, ExpectedRouteGeneration: 1, RequestID: "r", Digest: strings.Repeat("a", 64), Payload: []byte("edited")})
	require.NoError(t, err)
	require.NoError(t, edit.Commit())
	require.NoError(t, edit.Close())
	for i, read := range reads {
		old, err := readMessageUpdatesSnapshot(ctx, pinned, HashSlot(slots[i]), read)
		require.NoError(t, err)
		require.Equal(t, before[i], old)
	}
	after, err := db.ReadMessageUpdatesBatch(ctx, slots, reads)
	require.NoError(t, err)
	for i := range reads {
		want, err := db.ForHashSlot(slots[i]).ReadMessageUpdates(ctx, reads[i])
		require.NoError(t, err)
		require.Equal(t, want, after[i])
	}
	require.Len(t, after[0].Updates, 1)
	require.Empty(t, after[1].Updates)
	after[0].Updates[0].Payload[0] = 'X'
	again, err := db.ReadMessageUpdatesBatch(ctx, slots, reads)
	require.NoError(t, err)
	require.Equal(t, "edited", string(again[0].Updates[0].Payload))
	pendingRead := reads[0]
	pendingRead.IncludePending = true
	pending, err := db.ReadMessageUpdatesBatch(ctx, slots[:1], []MessageUpdateRead{pendingRead})
	require.NoError(t, err)
	wantPending, err := db.ForHashSlot(255).ReadMessageUpdates(ctx, pendingRead)
	require.NoError(t, err)
	require.Len(t, pending[0].Updates, 1)
	require.Equal(t, wantPending, pending[0])
	reads[0].IDs = nil
	reads[0].Limit = 1
	page, err := db.ReadMessageUpdatesBatch(ctx, slots, reads)
	require.NoError(t, err)
	require.Equal(t, uint64(1), page[0].Next)
	reads[0].After = page[0].Next
	page, err = db.ReadMessageUpdatesBatch(ctx, slots, reads)
	require.NoError(t, err)
	require.Empty(t, page[0].Updates)
	large := db.NewWriteBatch()
	_, err = large.ApplyMessageUpdate(255, MessageUpdateMutation{Op: "update", ChannelID: "same-id", ChannelType: 2, Generation: "generation", MessageID: 42, MessageSeq: 1, ExpectedVersion: 1, ExpectedChannelEpoch: 1, ExpectedRouteGeneration: 1, RequestID: "large", Digest: strings.Repeat("b", 64), Payload: make([]byte, MaxMessageUpdatePayload)})
	require.NoError(t, err)
	require.NoError(t, large.Commit())
	require.NoError(t, large.Close())
	var bigReads []MessageUpdateRead
	var bigSlots []uint16
	for i := 0; i < 8; i++ {
		bigReads = append(bigReads, MessageUpdateRead{ChannelID: "same-id", ChannelType: 2, IDs: []uint64{42}})
		bigSlots = append(bigSlots, 255)
	}
	_, err = db.ReadMessageUpdatesBatch(ctx, bigSlots[:7], bigReads[:7])
	require.NoError(t, err)
	rejected, err := db.ReadMessageUpdatesBatch(ctx, bigSlots, bigReads)
	require.ErrorIs(t, err, ErrInvalidArgument, "the retained byte limit applies to the complete batch")
	require.Nil(t, rejected)
}

func TestMessageUpdateBatchRejectsInvalidOrCanceledBatch(t *testing.T) {
	db, err := Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	valid := MessageUpdateRead{ChannelID: "g", ChannelType: 2, IDs: []uint64{1}}
	for _, tc := range []struct {
		slots []uint16
		reads []MessageUpdateRead
	}{
		{nil, []MessageUpdateRead{valid}},
		{[]uint16{1}, []MessageUpdateRead{{}}},
		{[]uint16{1}, []MessageUpdateRead{{ChannelID: "g", ChannelType: 2, Limit: -1}}},
		{[]uint16{1, 2}, []MessageUpdateRead{{ChannelID: "g", ChannelType: 2, Limit: 200}, valid}},
		{make([]uint16, 201), make([]MessageUpdateRead, 201)},
	} {
		pages, err := db.ReadMessageUpdatesBatch(context.Background(), tc.slots, tc.reads)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.Nil(t, pages)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pages, err := db.ReadMessageUpdatesBatch(ctx, []uint16{1}, []MessageUpdateRead{valid})
	require.True(t, errors.Is(err, context.Canceled))
	require.Nil(t, pages)
}

// cancelBatchReadContext deterministically cancels between two zero-limit reads.
// The initial check and first read succeed; the next checkpoint closes Done.
type cancelBatchReadContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelBatchReadContext) Err() error {
	c.checks++
	if c.checks == 3 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestMessageUpdateBatchDiscardsPartialPagesOnCancellationOrCorruption(t *testing.T) {
	db, err := Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	reads := []MessageUpdateRead{{ChannelID: "first", ChannelType: 2}, {ChannelID: "second", ChannelType: 2}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controlled := &cancelBatchReadContext{Context: ctx, cancel: cancel}
	pages, err := db.ReadMessageUpdatesBatch(controlled, []uint16{1, 2}, reads)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, pages)
	key, err := messageUpdateHeadTable.primaryRowKey(2, KeyParts{String("second"), Int64Ordered(2)})
	require.NoError(t, err)
	broken := db.engine.NewBatch()
	defer broken.Close()
	require.NoError(t, broken.Set(key, []byte{0xff}))
	require.NoError(t, broken.Commit(true))
	pages, err = db.ReadMessageUpdatesBatch(context.Background(), []uint16{1, 2}, reads)
	require.Error(t, err)
	require.Nil(t, pages)
}
