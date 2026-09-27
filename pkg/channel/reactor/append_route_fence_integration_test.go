//go:build integration

package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

type routeBlockedCommit struct {
	reactorCaptureQuorumLog
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (l *routeBlockedCommit) Commit(ctx context.Context, p replication.Proposal) (replication.Receipt, error) {
	if p.Records[0].ID == 11 {
		l.once.Do(func() { close(l.started) })
		select {
		case <-ctx.Done():
			return replication.Receipt{}, ctx.Err()
		case <-l.release:
		}
		// The delayed old operation supplies no durable receipt to the new owner.
		return replication.Receipt{}, ch.ErrStaleMeta
	}
	return l.reactorCaptureQuorumLog.Commit(ctx, p)
}

func TestAppendRouteFenceQueuedWorkCannotAdoptNewAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	log := &routeBlockedCommit{started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	releaseCommit := func() { release.Do(func() { close(log.release) }) }
	admitted := make(chan struct{}, 4)
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: store.NewMemoryFactory(), QuorumLog: log, AppendBatchMaxRecords: 1,
		AppendAdmissionGuard: ch.AppendAdmissionGuardFunc(func(context.Context, ch.AppendAdmissionRequest) error { admitted <- struct{}{}; return nil })})
	require.NoError(t, err)
	t.Cleanup(func() { releaseCommit(); require.NoError(t, g.Close()) })
	m := testMeta("queued-preparation", 1, 1)
	m.RouteGeneration = 7
	require.NoError(t, awaitSubmit(g, m.Key, Event{Kind: EventApplyMeta, Key: m.Key, Meta: m}))
	appendPrepared := func(id uint64) *Future {
		e := appendEvent(m, id, "body")
		e.Append.ExpectedChannelEpoch = m.Epoch
		e.Append.ExpectedLeaderEpoch = m.LeaderEpoch
		e.Append.ExpectedRouteGeneration = m.RouteGeneration
		e.Append.CommitMode = ch.CommitModeQuorum
		f, err := g.Submit(ctx, m.Key, e)
		require.NoError(t, err)
		select {
		case <-admitted:
		case <-ctx.Done():
			t.Fatal("append was not admitted")
		}
		return f
	}
	first := appendPrepared(11)
	select {
	case <-log.started:
	case <-ctx.Done():
		t.Fatal("first commit did not start")
	}
	queued := appendPrepared(12)
	m.RouteGeneration++
	updated, err := g.Submit(ctx, m.Key, Event{Kind: EventApplyMeta, Key: m.Key, Meta: m})
	require.NoError(t, err)
	_, err = queued.Await(ctx)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	_, err = first.Await(ctx)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	releaseCommit()
	_, err = updated.Await(ctx)
	require.NoError(t, err)
	current := appendPrepared(13)
	result, err := current.Await(ctx)
	require.NoError(t, err)
	require.Len(t, result.AppendBatch.Items, 1)
	proposals := log.proposals()
	require.Len(t, proposals, 1)
	require.Equal(t, uint64(13), proposals[0].Records[0].ID)
	require.Equal(t, uint64(8), proposals[0].Expected.FenceVersion)
	t.Log("append_route_queue_evidence: queued_old_request_failed=true old_completion_fenced=true replacement_authority=8 proposed_ids=13")
}
