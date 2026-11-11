package message

import (
	"context"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/stretchr/testify/require"
)

// A quorum may acknowledge A while a funded follower still lacks its original.
// B must request exact repair without replacing/refunding A or spending its ticket.
func TestMQTTStoragePreparedFollowerReportsMissingOriginal(t *testing.T) {
	ctx := context.Background()
	e := openCompatEngine(t)
	e.ConfigureMQTTStorage(MQTTStorageOptions{
		NodeBytes: 1 << 20, ClusterBytes: 1 << 20,
		Members: func() ([]uint64, uint64) { return []uint64{1}, 1 },
		Adjust: func(_ context.Context, q MQTTStorageGrantRequest) (MQTTStorageGrantReply, error) {
			debt := uint64(0)
			if q.InitialDebt != nil {
				debt = *q.InitialDebt
			}
			return MQTTStorageGrantReply{Revision: q.Revision + 1, Bytes: q.Target, Total: q.Target, Limit: 1 << 20, Status: "applied", Ready: true, MembersMatch: true, Initialized: true, Debt: debt, RosterRevision: 1}, nil
		},
	})
	s := mustForChannel(t, e, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	defer s.Close()
	activation, controls := mqttActivationProposal(t, DurableProposalManifest{}, 1)
	appendMQTTActivation(t, s, activation, controls, 1)
	proposal := func(previous DurableProposalManifest, command byte) (DurableProposalManifest, []channel.Record) {
		records := []channel.Record{compatExactTestRecord(t, 1, uint64(600+int(command)), "activation", string([]byte{'m', command}))}
		m := DurableProposalManifest{Version: DurableProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 2, CommandID: [32]byte{command}, BaseOffset: previous.LastOffset, LastOffset: previous.LastOffset + 1, PreviousIndex: previous.LastOffset, PreviousTerm: previous.LeaderTerm, PreviousDigest: previous.Digest}
		return sealCompatProposalManifest(t, m, records), records
	}
	first, firstRecords := proposal(activation, 2)
	next, nextRecords := proposal(first, 3)
	prepared, err := s.PrepareMQTTStorage(ctx, first, firstRecords, 1, 1, false)
	require.NoError(t, err)
	require.True(t, prepared.Prepared)
	debt := e.db.mqttStorage.used
	require.Positive(t, debt)
	retry, err := s.PrepareMQTTStorage(ctx, first, firstRecords, 1, 1, false)
	require.NoError(t, err)
	require.Equal(t, prepared, retry)
	require.Equal(t, debt, e.db.mqttStorage.used)

	missing, err := s.PrepareMQTTStorage(ctx, next, nextRecords, 2, 2, false)
	require.Error(t, err)
	require.Equal(t, first.BaseOffset+1, missing.NeedFrom, "funded-but-missing original must drive follower repair")
	require.False(t, missing.Prepared)
	ticket, found, err := loadMQTTStorageTicket(e.db.engine, s.log.key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, first, ticket.Manifest)
	require.Equal(t, mqttStoragePrepared, ticket.Phase)
	require.Equal(t, debt, e.db.mqttStorage.used, "missing original cannot refund accepted debt")

	rejected, err := s.PrepareMQTTStorage(ctx, next, nextRecords, 2, 1, false)
	require.ErrorIs(t, err, channel.ErrCorruptState, "a conflicting old nonce remains fenced")
	require.Zero(t, rejected.NeedFrom)
	stale := next
	stale.FenceVersion = 1
	stale = sealCompatProposalManifest(t, stale, nextRecords)
	rejected, err = s.PrepareMQTTStorage(ctx, stale, nextRecords, 2, 2, false)
	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Zero(t, rejected.NeedFrom)
	require.False(t, rejected.Prepared)
	require.Equal(t, debt, e.db.mqttStorage.used)

	appendMQTTActivation(t, s, first, firstRecords, 2)
	prepared, err = s.PrepareMQTTStorage(ctx, next, nextRecords, 2, 2, false)
	require.NoError(t, err)
	require.True(t, prepared.Prepared)
	require.Greater(t, e.db.mqttStorage.used, debt)
	appendMQTTActivation(t, s, next, nextRecords, 3)
	state, err := s.LoadDurableRecovery(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(3), state.LEO)
	require.Equal(t, uint64(3), state.Committed)
}
