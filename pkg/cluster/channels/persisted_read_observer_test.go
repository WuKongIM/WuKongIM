package channels

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

type persistedReadProbe struct {
	accepted, rejected, finished int
	kind, result                 string
	limit, items                 int
}

func (p *persistedReadProbe) ObservePersistedReadAdmission(kind string, accepted bool, inUse, limit int) {
	p.kind, p.limit = kind, limit
	if accepted {
		p.accepted++
	} else {
		p.rejected++
	}
}
func (p *persistedReadProbe) ObservePersistedReadCompletion(kind, result string, items int, duration time.Duration) {
	p.finished++
	p.kind, p.result, p.items = kind, result, items
}

func TestPersistedReadObserverAttributesAdmissionAndReleasesOnFailure(t *testing.T) {
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, Store: &persistedFailingFactory{}})
	require.NoError(t, err)
	probe := &persistedReadProbe{}
	svc.observer = probe
	for i := 0; i < cap(svc.persistedReads); i++ {
		svc.persistedReads <- struct{}{}
	}
	req := []ConversationHeadRequest{{ChannelID: ch.ChannelID{ID: "failure", Type: 2}}}
	rows := svc.readSelectedConversationHeads(context.Background(), "reader", req, true)
	require.ErrorIs(t, rows[0].Err, ch.ErrBackpressured)
	require.Equal(t, 1, probe.rejected, "serving-node admission rejection must be observable")
	require.Zero(t, probe.finished)
	<-svc.persistedReads
	rows = svc.readSelectedConversationHeads(context.Background(), "reader", req, true)
	require.ErrorIs(t, rows[0].Err, errPersistedDiskFailure)
	require.Equal(t, 1, probe.accepted)
	require.Equal(t, 1, probe.finished)
	require.Equal(t, "heads", probe.kind)
	require.Equal(t, "error", probe.result)
	require.Equal(t, 1, probe.items)
	require.Equal(t, 16, probe.limit)
	require.Equal(t, 15, len(svc.persistedReads))
}

func TestPersistedRecentObserverExcludesUnadmittedCancellation(t *testing.T) {
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, Store: &persistedFailingFactory{}})
	require.NoError(t, err)
	probe := &persistedReadProbe{}
	svc.observer = probe
	req := []CommittedReadRequest{{CommittedRead: CommittedRead{ChannelID: ch.ChannelID{ID: "failure", Type: 2}, Request: channelstore.ReadCommittedRequest{Limit: 1, MaxBytes: 1024}}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows := svc.readSelectedMessageBatch(ctx, req, true)
	require.ErrorIs(t, rows[0].Err, context.Canceled)
	require.Zero(t, probe.accepted)
	require.Zero(t, probe.finished)
	rows = svc.readSelectedMessageBatch(context.Background(), req, true)
	require.ErrorIs(t, rows[0].Err, errPersistedDiskFailure)
	require.Equal(t, "recents", probe.kind)
	require.Equal(t, "error", probe.result)
	require.Equal(t, 1, probe.accepted)
	require.Equal(t, 1, probe.finished)
	require.Zero(t, len(svc.persistedReads))
}

func TestPersistedRecentObserverSeparatesByteBudgetFromAdmission(t *testing.T) {
	factory := channelstore.NewMemoryFactory()
	id := ch.ChannelID{ID: "budget", Type: 2}
	store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = store.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 1, Payload: make([]byte, 900<<10)}}})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, Store: factory})
	require.NoError(t, err)
	probe := &persistedReadProbe{}
	svc.observer = probe
	requests := make([]CommittedReadRequest, 10)
	for i := range requests {
		requests[i] = CommittedReadRequest{CommittedRead: CommittedRead{ChannelID: id, Request: channelstore.ReadCommittedRequest{Limit: 1, MaxBytes: 1 << 20}}}
	}
	rows := svc.readSelectedMessageBatch(context.Background(), requests, true)
	for _, row := range rows {
		require.ErrorIs(t, row.Err, ch.ErrBackpressured)
	}
	require.Equal(t, 1, probe.accepted)
	require.Zero(t, probe.rejected)
	require.Equal(t, 1, probe.finished)
	require.Equal(t, "byte_budget", probe.result)
	require.Zero(t, len(svc.persistedReads))
}
