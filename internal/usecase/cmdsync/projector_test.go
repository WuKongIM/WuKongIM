package cmdsync

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestProjectorCreatesCMDStateForGroupSubscribers(t *testing.T) {
	ctx := context.Background()
	store := &fakeProjectorStore{}
	subscribers := &fakeSubscriberResolver{pages: [][]string{{"u1", "u2"}}}
	now := time.Date(2026, 5, 12, 12, 0, 0, 123, time.UTC)
	projector := NewProjector(ProjectorOptions{Store: store, Subscribers: subscribers, Now: func() time.Time { return now }, PageSize: 2})

	msg := channel.Message{ChannelID: channelid.ToCommandChannel("g1"), ChannelType: frame.ChannelTypeGroup, FromUID: "u1", MessageSeq: 9, Timestamp: 100}
	require.NoError(t, projector.SubmitCommitted(ctx, messageevents.MessageCommitted{Message: msg}))
	require.NoError(t, projector.Flush(ctx))

	activeAt := time.Unix(100, 0).UnixNano()
	require.Equal(t, []metadb.CMDConversationState{
		{UID: "u1", ChannelID: "g1____cmd", ChannelType: int64(frame.ChannelTypeGroup), ReadSeq: 9, ActiveAt: activeAt, UpdatedAt: now.UnixNano()},
		{UID: "u2", ChannelID: "g1____cmd", ChannelType: int64(frame.ChannelTypeGroup), ActiveAt: activeAt, UpdatedAt: now.UnixNano()},
	}, store.flattened())
	require.Equal(t, []channel.ChannelID{{ID: "g1____cmd", Type: frame.ChannelTypeGroup}}, subscribers.beginIDs)
}

func TestProjectorDoesNotCreateSenderStateWhenSenderIsNotRecipient(t *testing.T) {
	ctx := context.Background()
	store := &fakeProjectorStore{}
	projector := NewProjector(ProjectorOptions{
		Store:       store,
		Subscribers: &fakeSubscriberResolver{pages: [][]string{{"u2"}}},
		Now:         func() time.Time { return time.Unix(200, 0) },
	})

	require.NoError(t, projector.SubmitCommitted(ctx, messageevents.MessageCommitted{Message: channel.Message{
		ChannelID: "g1____cmd", ChannelType: frame.ChannelTypeGroup, FromUID: "u1", MessageSeq: 11, Timestamp: 100,
	}}))
	require.NoError(t, projector.Flush(ctx))

	require.Equal(t, []metadb.CMDConversationState{{
		UID: "u2", ChannelID: "g1____cmd", ChannelType: int64(frame.ChannelTypeGroup), ActiveAt: time.Unix(100, 0).UnixNano(), UpdatedAt: time.Unix(200, 0).UnixNano(),
	}}, store.flattened())
}

func TestProjectorUsesMessageScopedUIDsExactly(t *testing.T) {
	ctx := context.Background()
	store := &fakeProjectorStore{}
	subscribers := &fakeSubscriberResolver{pages: [][]string{{"store-user"}}}
	now := time.Unix(300, 0)
	projector := NewProjector(ProjectorOptions{Store: store, Subscribers: subscribers, Now: func() time.Time { return now }})

	require.NoError(t, projector.SubmitCommitted(ctx, messageevents.MessageCommitted{
		Message:           channel.Message{ChannelID: "tmp1____cmd", ChannelType: frame.ChannelTypeTemp, FromUID: "u2", MessageSeq: 12, Timestamp: 101},
		MessageScopedUIDs: []string{"u1", "u2", "u1", ""},
	}))
	require.NoError(t, projector.Flush(ctx))

	require.Empty(t, subscribers.beginIDs)
	require.Equal(t, []metadb.CMDConversationState{
		{UID: "u1", ChannelID: "tmp1____cmd", ChannelType: int64(frame.ChannelTypeTemp), ActiveAt: time.Unix(101, 0).UnixNano(), UpdatedAt: now.UnixNano()},
		{UID: "u2", ChannelID: "tmp1____cmd", ChannelType: int64(frame.ChannelTypeTemp), ReadSeq: 12, ActiveAt: time.Unix(101, 0).UnixNano(), UpdatedAt: now.UnixNano()},
	}, store.flattened())
}

func TestProjectorIgnoresTempCommandWithoutScopedUIDs(t *testing.T) {
	ctx := context.Background()
	store := &fakeProjectorStore{}
	subscribers := &fakeSubscriberResolver{pages: [][]string{{"u1"}}}
	projector := NewProjector(ProjectorOptions{Store: store, Subscribers: subscribers})

	require.NoError(t, projector.SubmitCommitted(ctx, messageevents.MessageCommitted{Message: channel.Message{
		ChannelID: "tmp1____cmd", ChannelType: frame.ChannelTypeTemp, MessageSeq: 12, Timestamp: 101,
	}}))
	require.NoError(t, projector.Flush(ctx))

	require.Empty(t, subscribers.beginIDs)
	require.Empty(t, store.flattened())
}

func TestProjectorSubmitCommittedDoesNotBlockOnSubscriberScan(t *testing.T) {
	ctx := context.Background()
	store := &fakeProjectorStore{}
	subscribers := &fakeSubscriberResolver{beginBlock: make(chan struct{})}
	projector := NewProjector(ProjectorOptions{Store: store, Subscribers: subscribers, QueueSize: 1})

	done := make(chan error, 1)
	go func() {
		done <- projector.SubmitCommitted(ctx, messageevents.MessageCommitted{Message: channel.Message{
			ChannelID: "g1____cmd", ChannelType: frame.ChannelTypeGroup, MessageSeq: 1, Timestamp: 100,
		}})
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("SubmitCommitted blocked on subscriber scan")
	}
	require.Empty(t, subscribers.beginIDs)
}

func TestProjectorIgnoresOrdinaryChatMessages(t *testing.T) {
	ctx := context.Background()
	store := &fakeProjectorStore{}
	projector := NewProjector(ProjectorOptions{Store: store, Subscribers: &fakeSubscriberResolver{pages: [][]string{{"u1"}}}})

	require.NoError(t, projector.SubmitCommitted(ctx, messageevents.MessageCommitted{Message: channel.Message{
		ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageSeq: 1, Timestamp: 100,
	}}))
	require.NoError(t, projector.Flush(ctx))
	require.Empty(t, store.flattened())
}

func TestProjectorProjectsSyncOnceOrdinaryMessageToCommandChannel(t *testing.T) {
	ctx := context.Background()
	store := &fakeProjectorStore{}
	projector := NewProjector(ProjectorOptions{Store: store, Subscribers: &fakeSubscriberResolver{pages: [][]string{{"u1"}}}})

	require.NoError(t, projector.SubmitCommitted(ctx, messageevents.MessageCommitted{Message: channel.Message{
		ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Framer: frame.Framer{SyncOnce: true}, MessageSeq: 1, Timestamp: 100,
	}}))
	require.NoError(t, projector.Flush(ctx))
	require.Equal(t, "g1____cmd", store.flattened()[0].ChannelID)
}

func TestProjectorIgnoresNoPersistOrZeroSeqMessages(t *testing.T) {
	ctx := context.Background()
	store := &fakeProjectorStore{}
	projector := NewProjector(ProjectorOptions{Store: store, Subscribers: &fakeSubscriberResolver{pages: [][]string{{"u1"}}}})

	require.NoError(t, projector.SubmitCommitted(ctx, messageevents.MessageCommitted{Message: channel.Message{
		ChannelID: "g1____cmd", ChannelType: frame.ChannelTypeGroup, Framer: frame.Framer{NoPersist: true}, MessageSeq: 1, Timestamp: 100,
	}}))
	require.NoError(t, projector.SubmitCommitted(ctx, messageevents.MessageCommitted{Message: channel.Message{
		ChannelID: "g2____cmd", ChannelType: frame.ChannelTypeGroup, MessageSeq: 0, Timestamp: 100,
	}}))
	require.NoError(t, projector.Flush(ctx))
	require.Empty(t, store.flattened())
}

type fakeProjectorStore struct {
	batches [][]metadb.CMDConversationState
}

func (f *fakeProjectorStore) UpsertCMDConversationStates(_ context.Context, states []metadb.CMDConversationState) error {
	f.batches = append(f.batches, append([]metadb.CMDConversationState(nil), states...))
	return nil
}

func (f *fakeProjectorStore) flattened() []metadb.CMDConversationState {
	var out []metadb.CMDConversationState
	for _, batch := range f.batches {
		out = append(out, batch...)
	}
	return out
}

type fakeSubscriberResolver struct {
	pages      [][]string
	beginIDs   []channel.ChannelID
	beginReqs  []delivery.SubscriberSnapshotRequest
	beginBlock chan struct{}
}

func (f *fakeSubscriberResolver) BeginSnapshot(ctx context.Context, id channel.ChannelID) (delivery.SnapshotToken, error) {
	return f.BeginSnapshotWithRequest(ctx, id, delivery.SubscriberSnapshotRequest{})
}

func (f *fakeSubscriberResolver) BeginSnapshotWithRequest(ctx context.Context, id channel.ChannelID, req delivery.SubscriberSnapshotRequest) (delivery.SnapshotToken, error) {
	if f.beginBlock != nil {
		<-f.beginBlock
	}
	f.beginIDs = append(f.beginIDs, id)
	f.beginReqs = append(f.beginReqs, req)
	return delivery.SnapshotToken{}, nil
}

func (f *fakeSubscriberResolver) NextPage(_ context.Context, _ delivery.SnapshotToken, cursor string, _ int) ([]string, string, bool, error) {
	idx := 0
	if cursor != "" {
		_, _ = fmt.Sscanf(cursor, "%d", &idx)
	}
	if idx >= len(f.pages) {
		return nil, cursor, true, nil
	}
	next := fmt.Sprintf("%d", idx+1)
	return append([]string(nil), f.pages[idx]...), next, idx+1 >= len(f.pages), nil
}
