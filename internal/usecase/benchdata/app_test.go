package benchdata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCapabilitiesReportsV1GroupSupport(t *testing.T) {
	app := New(Config{MaxBatchSize: 100, MaxPayloadBytes: 1024})

	got := app.Capabilities(context.Background())

	require.True(t, got.Enabled)
	require.Equal(t, "bench/v1", got.Version)
	require.Contains(t, got.Supports.ChannelTypes, "group")
	require.True(t, got.Supports.UsersTokensBatch)
	require.True(t, got.Supports.ChannelsBatch)
	require.True(t, got.Supports.ChannelSubscribersBatch)
	require.True(t, got.Supports.Snapshot)
	require.Equal(t, 100, got.Limits.MaxBatchSize)
}

func TestUpsertTokensAcceptsSpecUsersSchema(t *testing.T) {
	users := &recordingUsers{}
	app := New(Config{Users: users, MaxBatchSize: 100, MaxPayloadBytes: 1024})

	got, err := app.UpsertTokens(context.Background(), TokensRequest{
		RunID:   "bench-run",
		BatchID: "batch-1",
		Upsert:  true,
		Users:   []UserTokenCommand{{UID: "u1", Token: "t1"}},
	})

	require.NoError(t, err)
	require.Equal(t, 1, got.Accepted)
	require.Equal(t, []UserTokenCommand{{UID: "u1", Token: "t1"}}, users.updated)
}

func TestUpsertTokensRejectsMissingUsers(t *testing.T) {
	users := &recordingUsers{}
	app := New(Config{Users: users, MaxBatchSize: 100, MaxPayloadBytes: 1024})

	_, err := app.UpsertTokens(context.Background(), TokensRequest{RunID: "bench-run", BatchID: "batch-1", Upsert: true})

	require.ErrorContains(t, err, "users")
	require.ErrorIs(t, err, ErrValidation)
	require.Empty(t, users.updated)
}

func TestUpsertChannelsAcceptsSpecChannelsSchema(t *testing.T) {
	channels := &recordingChannels{}
	app := New(Config{Channels: channels, MaxBatchSize: 100, MaxPayloadBytes: 1024})

	got, err := app.UpsertChannels(context.Background(), ChannelsRequest{
		RunID:    "bench-run",
		BatchID:  "batch-1",
		Upsert:   true,
		Channels: []ChannelRecord{{ChannelID: "g1", ChannelType: 2}},
	})

	require.NoError(t, err)
	require.Equal(t, 1, got.Accepted)
	require.Equal(t, []ChannelRecord{{ChannelID: "g1", ChannelType: 2}}, channels.upserted)
}

func TestUpsertChannelsRejectsMissingChannels(t *testing.T) {
	channels := &recordingChannels{}
	app := New(Config{Channels: channels, MaxBatchSize: 100, MaxPayloadBytes: 1024})

	_, err := app.UpsertChannels(context.Background(), ChannelsRequest{RunID: "bench-run", BatchID: "batch-1", Upsert: true})

	require.ErrorContains(t, err, "channels")
	require.ErrorIs(t, err, ErrValidation)
	require.Empty(t, channels.upserted)
}

func TestBatchSubscribersRejectsResetTrue(t *testing.T) {
	app := New(Config{Channels: fakeChannels{}, MaxBatchSize: 100, MaxPayloadBytes: 1024})

	_, err := app.AddSubscribers(context.Background(), SubscribersRequest{
		RunID:   "bench-run",
		BatchID: "batch-1",
		Items: []SubscriberItem{{
			ChannelID:   "g1",
			ChannelType: 2,
			Reset:       true,
			Subscribers: []string{"u1"},
		}},
	})

	require.ErrorContains(t, err, "reset=true")
}

func TestBatchSubscribersRejectsResetTrueBeforeDependencyCheck(t *testing.T) {
	app := New(Config{MaxBatchSize: 100, MaxPayloadBytes: 1024})

	_, err := app.AddSubscribers(context.Background(), SubscribersRequest{
		RunID:   "bench-run",
		BatchID: "batch-1",
		Items: []SubscriberItem{{
			ChannelID:   "g1",
			ChannelType: 2,
			Reset:       true,
			Subscribers: []string{"u1"},
		}},
	})

	require.ErrorContains(t, err, "reset=true")
}

func TestBatchChannelsValidateAllItemsBeforeMutation(t *testing.T) {
	channels := &recordingChannels{}
	app := New(Config{Channels: channels, MaxBatchSize: 100, MaxPayloadBytes: 1024})

	_, err := app.UpsertChannels(context.Background(), ChannelsRequest{
		RunID:   "bench-run",
		BatchID: "batch-1",
		Items: []ChannelRecord{
			{ChannelID: "g1", ChannelType: 2},
			{ChannelID: "p1", ChannelType: 1},
		},
	})

	require.ErrorContains(t, err, "group")
	require.ErrorIs(t, err, ErrValidation)
	require.Empty(t, channels.upserted)
}

func TestBatchTokensValidateAllItemsBeforeMutation(t *testing.T) {
	users := &recordingUsers{}
	app := New(Config{Users: users, MaxBatchSize: 100, MaxPayloadBytes: 1024})

	_, err := app.UpsertTokens(context.Background(), TokensRequest{
		RunID:   "bench-run",
		BatchID: "batch-1",
		Items: []UserTokenCommand{
			{UID: "u1", Token: "t1"},
			{UID: "bad@uid", Token: "t2"},
		},
	})

	require.ErrorContains(t, err, "uid")
	require.ErrorIs(t, err, ErrValidation)
	require.Empty(t, users.updated)
}

func TestBatchChannelsRejectEmptyChannelIDBeforeMutation(t *testing.T) {
	channels := &recordingChannels{}
	app := New(Config{Channels: channels, MaxBatchSize: 100, MaxPayloadBytes: 1024})

	_, err := app.UpsertChannels(context.Background(), ChannelsRequest{
		RunID:   "bench-run",
		BatchID: "batch-1",
		Items: []ChannelRecord{
			{ChannelID: "g1", ChannelType: 2},
			{ChannelID: "", ChannelType: 2},
		},
	})

	require.ErrorContains(t, err, "channel_id")
	require.ErrorIs(t, err, ErrValidation)
	require.Empty(t, channels.upserted)
}

func TestBatchSubscribersValidateAllItemsBeforeMutation(t *testing.T) {
	channels := &recordingChannels{}
	app := New(Config{Channels: channels, MaxBatchSize: 100, MaxPayloadBytes: 1024})

	_, err := app.AddSubscribers(context.Background(), SubscribersRequest{
		RunID:   "bench-run",
		BatchID: "batch-1",
		Items: []SubscriberItem{
			{ChannelID: "g1", ChannelType: 2, Subscribers: []string{"u1"}},
			{ChannelID: "g2", ChannelType: 2, Subscribers: []string{"bad#uid"}},
		},
	})

	require.ErrorContains(t, err, "subscriber")
	require.ErrorIs(t, err, ErrValidation)
	require.Empty(t, channels.added)
}

func TestMissingWritersReturnDependencyErrors(t *testing.T) {
	app := New(Config{MaxBatchSize: 100, MaxPayloadBytes: 1024})

	_, tokenErr := app.UpsertTokens(context.Background(), TokensRequest{
		RunID:   "bench-run",
		BatchID: "batch-1",
		Items:   []UserTokenCommand{{UID: "u1", Token: "t1"}},
	})
	_, channelErr := app.UpsertChannels(context.Background(), ChannelsRequest{
		RunID:   "bench-run",
		BatchID: "batch-1",
		Items:   []ChannelRecord{{ChannelID: "g1", ChannelType: 2}},
	})
	_, subscriberErr := app.AddSubscribers(context.Background(), SubscribersRequest{
		RunID:   "bench-run",
		BatchID: "batch-1",
		Items:   []SubscriberItem{{ChannelID: "g1", ChannelType: 2, Subscribers: []string{"u1"}}},
	})

	require.ErrorIs(t, tokenErr, ErrDependency)
	require.ErrorIs(t, channelErr, ErrDependency)
	require.ErrorIs(t, subscriberErr, ErrDependency)
}

type recordingUsers struct {
	updated []UserTokenCommand
}

func (r *recordingUsers) UpdateToken(_ context.Context, cmd UserTokenCommand) error {
	r.updated = append(r.updated, cmd)
	return nil
}

type fakeChannels struct{}

func (fakeChannels) UpsertChannel(context.Context, ChannelRecord) error { return nil }

func (fakeChannels) AddSubscribers(context.Context, string, uint8, []string) error { return nil }

type recordingChannels struct {
	upserted []ChannelRecord
	added    []subscriberAdd
}

func (r *recordingChannels) UpsertChannel(_ context.Context, ch ChannelRecord) error {
	r.upserted = append(r.upserted, ch)
	return nil
}

func (r *recordingChannels) AddSubscribers(_ context.Context, channelID string, channelType uint8, uids []string) error {
	r.added = append(r.added, subscriberAdd{channelID: channelID, channelType: channelType, uids: append([]string(nil), uids...)})
	return nil
}

type subscriberAdd struct {
	channelID   string
	channelType uint8
	uids        []string
}
