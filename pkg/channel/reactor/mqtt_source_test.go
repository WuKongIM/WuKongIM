package reactor

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type mqttSourceCapableFactory struct{ *store.MemoryFactory }

func (mqttSourceCapableFactory) SupportsMQTTSourceActivation() bool { return true }

func mqttSourceCompletionFixture(t *testing.T) (*Reactor, *runtimeChannel, worker.Result, context.CancelFunc) {
	t.Helper()
	factory := mqttSourceCapableFactory{store.NewMemoryFactory()}
	r := NewReactor(ReactorConfig{LocalNode: 1, Store: factory, QuorumLog: &reactorCaptureQuorumLog{}})
	state := machine.NewChannelState("1:source", 1, 7)
	state.ID = ch.ChannelID{ID: "source", Type: 1}
	state.Epoch = 2
	state.LeaderEpoch = 3
	state.Leader = 1
	state.Role = ch.RoleLeader
	state.Status = ch.StatusActive
	state.CommitReady = true
	state.HW = 9
	state.LEO = 9
	lease, err := factory.ChannelStore(state.Key, state.ID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lease.Close() })
	rc := &runtimeChannel{state: state, store: lease, quorumAuthority: replication.Authority{ID: replication.AuthorityID{ChannelEpoch: 2, LeaderTerm: 3, FenceVersion: 4}}}
	r.channels[state.Key] = rc
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, r.registerLookupWaiter(rc, 11, ctx, 0, NewFuture()))
	rc.lookupWaiters[11].source = &mqttSourceWaiter{request: ch.MQTTSourceRequest{ChannelID: state.ID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, MessageID: 101, ServerTimestampMS: 1000}, committedThrough: 8}
	result := worker.Result{Kind: worker.TaskStoreMQTTSource, Fence: ch.Fence{ChannelKey: state.Key, Generation: 7, Epoch: 2, LeaderEpoch: 3, OpID: 11}, StoreMQTTSource: &worker.StoreMQTTSourceResult{Found: true, Snapshot: ch.MQTTSourceSnapshot{Generation: quorumlog.MQTTSourceGeneration(ch.CommandID{1}), StartAfter: 4, CommittedThrough: 8}}}
	return r, rc, result, cancel
}

func TestMQTTSourceCompletionFencesLateResults(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Reactor, *runtimeChannel, *worker.Result, context.CancelFunc)
		want   error
	}{
		{"success", func(*Reactor, *runtimeChannel, *worker.Result, context.CancelFunc) {}, nil},
		{"canceled", func(_ *Reactor, _ *runtimeChannel, _ *worker.Result, c context.CancelFunc) { c() }, context.Canceled},
		{"generation", func(_ *Reactor, rc *runtimeChannel, _ *worker.Result, _ context.CancelFunc) { rc.state.Generation++ }, ch.ErrStaleMeta},
		{"epoch", func(_ *Reactor, rc *runtimeChannel, _ *worker.Result, _ context.CancelFunc) { rc.state.Epoch++ }, ch.ErrStaleMeta},
		{"leader-epoch", func(_ *Reactor, rc *runtimeChannel, _ *worker.Result, _ context.CancelFunc) { rc.state.LeaderEpoch++ }, ch.ErrStaleMeta},
		{"route", func(_ *Reactor, rc *runtimeChannel, _ *worker.Result, _ context.CancelFunc) {
			rc.quorumAuthority.ID.FenceVersion++
		}, ch.ErrStaleMeta},
		{"follower", func(_ *Reactor, rc *runtimeChannel, _ *worker.Result, _ context.CancelFunc) {
			rc.state.Role = ch.RoleFollower
		}, ch.ErrNotLeader},
		{"not-ready", func(_ *Reactor, rc *runtimeChannel, _ *worker.Result, _ context.CancelFunc) {
			rc.state.CommitReady = false
		}, ch.ErrNotReady},
		{"write-fence", func(_ *Reactor, rc *runtimeChannel, _ *worker.Result, _ context.CancelFunc) {
			rc.state.WriteFence = ch.WriteFence{Token: "transfer", Version: 1}
		}, ch.ErrWriteFenced},
		{"guard", func(r *Reactor, _ *runtimeChannel, _ *worker.Result, _ context.CancelFunc) {
			r.appendAdmissionGuard = ch.AppendAdmissionGuardFunc(func(context.Context, ch.AppendAdmissionRequest) error { return ch.ErrNotReady })
		}, ch.ErrNotReady},
		{"guard-cancels", func(r *Reactor, _ *runtimeChannel, _ *worker.Result, cancel context.CancelFunc) {
			r.appendAdmissionGuard = ch.AppendAdmissionGuardFunc(func(context.Context, ch.AppendAdmissionRequest) error { cancel(); return nil })
		}, context.Canceled},
		{"io-error", func(_ *Reactor, _ *runtimeChannel, res *worker.Result, _ context.CancelFunc) { res.Err = ch.ErrClosed }, ch.ErrClosed},
		{"nil-result", func(_ *Reactor, _ *runtimeChannel, res *worker.Result, _ context.CancelFunc) {
			res.StoreMQTTSource = nil
		}, ch.ErrInvalidConfig},
		{"invented-hw", func(_ *Reactor, _ *runtimeChannel, res *worker.Result, _ context.CancelFunc) {
			res.StoreMQTTSource.Snapshot.CommittedThrough = 9
		}, ch.ErrLogConflict},
		{"future-activation", func(_ *Reactor, _ *runtimeChannel, res *worker.Result, _ context.CancelFunc) {
			res.StoreMQTTSource.Snapshot.StartAfter = 8
		}, ch.ErrLogConflict},
		{"empty-generation", func(_ *Reactor, _ *runtimeChannel, res *worker.Result, _ context.CancelFunc) {
			res.StoreMQTTSource.Snapshot.Generation = ""
		}, ch.ErrLogConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, rc, res, cancel := mqttSourceCompletionFixture(t)
			f := rc.lookupWaiters[11].future
			require.True(t, r.hasPendingRuntimeWork(rc), "query must block lifecycle eviction")
			tc.change(r, rc, &res, cancel)
			r.handleStoreMQTTSourceResult(res)
			select {
			case <-f.Done():
			default:
				t.Fatal("source waiter not completed")
			}
			got := f.Result()
			require.ErrorIs(t, got.Err, tc.want)
			require.Empty(t, rc.lookupWaiters)
			require.Empty(t, r.lookupCancelChannels)
			if tc.want == nil {
				require.True(t, got.MQTTSourceFound)
				require.Equal(t, uint64(8), got.MQTTSource.CommittedThrough, "later reactor HW must not move this admitted boundary")
			}
		})
	}
}

func TestMQTTSourceCancellationAndForeignCompletion(t *testing.T) {
	r, rc, res, _ := mqttSourceCompletionFixture(t)
	f := rc.lookupWaiters[11].future
	r.handleStoreLookupMessageResult(worker.Result{Kind: worker.TaskStoreLookupMessage, Fence: res.Fence, StoreLookupMessage: &worker.StoreLookupMessageResult{}})
	select {
	case <-f.Done():
		t.Fatal("message lookup completed a source query")
	default:
	}
	require.True(t, r.cancelLookupWaiter(rc, 11, context.Canceled))
	r.handleStoreMQTTSourceResult(res)
	require.ErrorIs(t, f.Result().Err, context.Canceled)
	require.Empty(t, rc.lookupWaiters)
}

func mqttSourceAppendEvent() Event {
	req := ch.MQTTSourceRequest{ChannelID: ch.ChannelID{ID: "source", Type: 1}, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, MessageID: 101, ServerTimestampMS: 1000}
	return Event{Kind: EventAppend, Key: "1:source", Context: context.Background(), OpID: 12, Future: NewFuture(), MQTTSourceActivation: true, MQTTSource: req,
		Append: ch.AppendBatchRequest{ChannelID: req.ChannelID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, CommitMode: ch.CommitModeQuorum, Messages: []ch.Message{{MessageID: 101, ServerTimestampMS: 1000, SyncOnce: true, Payload: []byte(quorumlog.MQTTSourceActivationPayload)}}}}
}

func TestMQTTSourceControlAdmissionIsClosed(t *testing.T) {
	for _, modify := range []func(*Event){
		func(e *Event) { e.Append.CommitMode = ch.CommitModeLocal }, func(e *Event) { e.MQTTSource.ExpectedRouteGeneration = 0 },
		func(e *Event) { e.Append.Messages = append(e.Append.Messages, e.Append.Messages[0]) }, func(e *Event) { e.Append.Messages[0].FromUID = "sender" },
		func(e *Event) { e.Append.Messages[0].Payload = []byte("other") }, func(e *Event) { e.Append.Messages[0].SyncOnce = false },
		func(e *Event) { e.Append.Messages[0].RedDot = true }, func(e *Event) { e.Append.Messages[0].Expire = 1 }, func(e *Event) { e.Append.Messages[0].PublicationMetadata = []byte{1} },
		func(e *Event) { e.Append.Messages[0].Setting = 1 }, func(e *Event) { e.Append.Messages[0].ClientMsgNo = "client" }, func(e *Event) { e.Append.Messages[0].MessageID++ },
	} {
		r, rc, _, _ := mqttSourceCompletionFixture(t)
		e := mqttSourceAppendEvent()
		modify(&e)
		require.Error(t, r.validateAppendEvent(e.Context, rc, e))
	}
	r, rc, _, _ := mqttSourceCompletionFixture(t)
	e := mqttSourceAppendEvent()
	require.NoError(t, r.validateAppendEvent(e.Context, rc, e))
	req := newAppendRequest(e, time.Time{})
	require.True(t, req.mqttSourceActivation)
	e.MQTTSourceActivation = false
	require.False(t, newAppendRequest(e, time.Time{}).mqttSourceActivation)
	r.cfg.QuorumLog = nil
	e.MQTTSourceActivation = true
	require.ErrorIs(t, r.validateAppendEvent(e.Context, rc, e), ch.ErrInvalidConfig)
}
