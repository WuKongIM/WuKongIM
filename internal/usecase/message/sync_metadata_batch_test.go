package message

import (
	"context"
	"errors"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

type syncBatchFacts struct {
	membershipKeys []ChannelID
	channelKeys    []ChannelID
	memberships    []SyncMembershipReadResult
	channels       []SyncChannelStateReadResult
	points         int
	err            error
}

func (s *syncBatchFacts) GetUserChannelMembership(context.Context, string, string, int64) (metadb.UserChannelMembership, bool, error) {
	s.points++
	return metadb.UserChannelMembership{}, false, errors.New("point membership read")
}
func (s *syncBatchFacts) GetChannelForMessagePull(context.Context, string, int64) (metadb.Channel, error) {
	s.points++
	return metadb.Channel{}, errors.New("point channel read")
}
func (s *syncBatchFacts) GetUserChannelMemberships(_ context.Context, _ string, keys []ChannelID) ([]SyncMembershipReadResult, error) {
	s.membershipKeys = keys
	return s.memberships, s.err
}
func (s *syncBatchFacts) GetChannelsForMessagePull(_ context.Context, keys []ChannelID) ([]SyncChannelStateReadResult, error) {
	s.channelKeys = keys
	return s.channels, nil
}

func TestSyncBatchMetadataAvoidsPointReadsAndPreservesVisibility(t *testing.T) {
	facts := &syncBatchFacts{memberships: []SyncMembershipReadResult{{Found: true, Membership: metadb.UserChannelMembership{JoinSeq: 5, DeletedToSeq: 8}}, {Found: false}}, channels: []SyncChannelStateReadResult{{}}}
	reader := &recordingChannelMessageReader{batchResults: []ChannelMessageReadResult{{}}}
	app := New(Options{Reader: reader, PersistedReader: reader, Memberships: facts, ChannelState: facts})
	got, err := app.SyncPersistedChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{LoginUID: " u1 ", Items: []SyncChannelMessagesQuery{{ChannelID: " g1 ", ChannelType: 2}, {ChannelID: "u2", ChannelType: 1}}})
	require.NoError(t, err)
	require.Len(t, got.Items, 2)
	require.Zero(t, facts.points)
	require.Equal(t, []ChannelID{{ID: "g1", Type: 2}}, facts.channelKeys)
	require.Len(t, reader.batchQueries, 1)
	require.Equal(t, uint64(9), reader.batchQueries[0].MinSeq)
	require.NotEqual(t, "u2", facts.membershipKeys[1].ID)
}

func TestSyncBatchMetadataFailsBeforeMessages(t *testing.T) {
	for _, name := range []string{"membership failure", "missing group", "tombstone", "disband", "channel failure", "short result"} {
		t.Run(name, func(t *testing.T) {
			failure := errors.New("disk failure")
			facts := &syncBatchFacts{memberships: []SyncMembershipReadResult{{Found: true}}, channels: []SyncChannelStateReadResult{{}}}
			want := failure
			switch name {
			case "membership failure":
				facts.err = failure
			case "missing group":
				facts.memberships[0].Found = false
				want = ErrSyncMembershipRequired
			case "tombstone":
				facts.memberships[0].Membership.Tombstone = true
				want = ErrSyncMembershipRequired
			case "disband":
				facts.channels[0].Channel.Disband = 1
				want = ErrSyncChannelDisbanded
			case "channel failure":
				facts.channels[0].Err = failure
			case "short result":
				facts.memberships = nil
				want = ErrSyncBatchResultMismatch
			}
			reader := &recordingChannelMessageReader{}
			app := New(Options{Reader: reader, PersistedReader: reader, Memberships: facts, ChannelState: facts})
			_, err := app.SyncPersistedChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{LoginUID: "u1", Items: []SyncChannelMessagesQuery{{ChannelID: "g1", ChannelType: 2}}})
			require.ErrorIs(t, err, want)
			require.Empty(t, reader.batchQueries)
			require.Zero(t, facts.points)
		})
	}
}

func TestSyncBatchMetadataRetainsInputOrderFailurePrecedence(t *testing.T) {
	failure := errors.New("second item membership read failed")
	facts := &syncBatchFacts{memberships: []SyncMembershipReadResult{{Found: true}, {Err: failure}}, channels: []SyncChannelStateReadResult{{Channel: metadb.Channel{Disband: 1}}}}
	reader := &recordingChannelMessageReader{}
	app := New(Options{Reader: reader, PersistedReader: reader, Memberships: facts, ChannelState: facts})
	_, err := app.SyncPersistedChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{LoginUID: "u1", Items: []SyncChannelMessagesQuery{{ChannelID: "g1", ChannelType: 2}, {ChannelID: "g2", ChannelType: 2}}})
	require.ErrorIs(t, err, ErrSyncChannelDisbanded)
	require.Empty(t, reader.batchQueries)
	require.Equal(t, []ChannelID{{ID: "g1", Type: 2}}, facts.channelKeys)
}

func TestSyncBatchMetadataKeepsMissingChannelAndMaximumFloorPolicy(t *testing.T) {
	facts := &syncBatchFacts{memberships: []SyncMembershipReadResult{{Found: true, Membership: metadb.UserChannelMembership{DeletedToSeq: ^uint64(0)}}}, channels: []SyncChannelStateReadResult{{Err: metadb.ErrNotFound}}}
	reader := &recordingChannelMessageReader{batchResults: []ChannelMessageReadResult{{}}}
	app := New(Options{Reader: reader, PersistedReader: reader, Memberships: facts, ChannelState: facts})
	_, err := app.SyncPersistedChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{LoginUID: "u1", Items: []SyncChannelMessagesQuery{{ChannelID: "g1", ChannelType: 2, StartMessageSeq: 3, EndMessageSeq: 7, Limit: 25000, PullMode: PullModeUp}}})
	require.NoError(t, err)
	require.Equal(t, []ChannelMessageQuery{{ChannelID: ChannelID{ID: "g1", Type: 2}, StartSeq: 3, EndSeq: 7, MinSeq: ^uint64(0), Limit: maxSyncMessagesLimit, PullMode: PullModeUp}}, reader.batchQueries)
}
