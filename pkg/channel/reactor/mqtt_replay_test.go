package reactor

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func mqttReplayCompletionFixture(t *testing.T) (*Reactor, *runtimeChannel, worker.Result, context.CancelFunc) {
	r, rc, res, cancel := mqttSourceCompletionFixture(t)
	gen := quorumlog.MQTTSourceGeneration(ch.CommandID{1})
	rc.lookupWaiters[11].source = nil
	rc.lookupWaiters[11].replay = &mqttReplayWaiter{request: ch.MQTTReplayRequest{ChannelID: rc.state.ID,
		ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4,
		Range: ch.MQTTReplayRange{Generation: gen, From: 1, Through: 8, Limit: 8, MaxBytes: 1024}}, committedThrough: 8}
	res.Kind, res.StoreMQTTSource = worker.TaskStoreMQTTReplay, nil
	res.StoreMQTTReplay = &worker.StoreMQTTReplayResult{CommittedThrough: 8, Page: ch.MQTTReplayPage{
		Before:  ch.MQTTReplayPrefix{Generation: gen},
		After:   ch.MQTTReplayPrefix{Generation: gen, Through: 1, TotalBytes: 1, TotalStoredBytes: 1, Digest: [32]byte{1}},
		Records: []ch.MQTTReplayRecord{{Position: 1, ContentVersion: 1, MessageID: 1, AccountedBytes: 1, TotalBytes: 1, TotalStoredBytes: 1, ContentHash: [32]byte{1}, Digest: [32]byte{1}, Content: []byte{1}}},
	}}
	return r, rc, res, cancel
}

func TestMQTTReplayCompletionFencesAndLifecycle(t *testing.T) {
	for _, fault := range []string{"success", "cancel", "generation", "epoch", "leader_epoch", "route", "follower", "not_ready", "write_fence", "guard_cancel", "error", "nil", "hw", "end", "content", "version", "prefix"} {
		t.Run(fault, func(t *testing.T) {
			r, rc, res, cancel := mqttReplayCompletionFixture(t)
			f := rc.lookupWaiters[11].future
			require.True(t, r.hasPendingRuntimeWork(rc))
			switch fault {
			case "cancel":
				cancel()
			case "generation":
				rc.state.Generation++
			case "epoch":
				rc.state.Epoch++
			case "leader_epoch":
				rc.state.LeaderEpoch++
			case "route":
				rc.quorumAuthority.ID.FenceVersion++
			case "follower":
				rc.state.Role = ch.RoleFollower
			case "not_ready":
				rc.state.CommitReady = false
			case "write_fence":
				rc.state.WriteFence = ch.WriteFence{Token: "migration", Version: 1}
			case "guard_cancel":
				r.appendAdmissionGuard = ch.AppendAdmissionGuardFunc(func(context.Context, ch.AppendAdmissionRequest) error { cancel(); return nil })
			case "error":
				res.Err = ch.ErrClosed
			case "nil":
				res.StoreMQTTReplay = nil
			case "hw":
				res.StoreMQTTReplay.CommittedThrough++
			case "end":
				res.StoreMQTTReplay.Page.After.Through = 9
			case "content":
				res.StoreMQTTReplay.Page.Records[0].Content = make([]byte, 1025)
			case "version":
				res.StoreMQTTReplay.Page.Records[0].ContentVersion++
			case "prefix":
				res.StoreMQTTReplay.Page.Before.TotalBytes++
			}
			r.handleStoreMQTTReplayResult(res)
			select {
			case <-f.Done():
			default:
				t.Fatal("waiter not completed")
			}
			if fault == "success" {
				require.NoError(t, f.Result().Err)
				require.Equal(t, uint64(1), f.Result().MQTTReplay.After.Through)
			} else {
				require.Error(t, f.Result().Err)
			}
			require.Empty(t, rc.lookupWaiters)
			require.Empty(t, r.lookupCancelChannels)
		})
	}
}

func TestMQTTReplayWaiterRejectsOtherCompletions(t *testing.T) {
	r, rc, res, _ := mqttReplayCompletionFixture(t)
	f := rc.lookupWaiters[11].future
	r.handleStoreLookupMessageResult(worker.Result{Kind: worker.TaskStoreLookupMessage, Fence: res.Fence, StoreLookupMessage: &worker.StoreLookupMessageResult{}})
	r.handleStoreMQTTSourceResult(worker.Result{Kind: worker.TaskStoreMQTTSource, Fence: res.Fence, StoreMQTTSource: &worker.StoreMQTTSourceResult{}})
	foreign := res
	foreign.Fence.OpID++
	r.handleStoreMQTTReplayResult(foreign)
	select {
	case <-f.Done():
		t.Fatal("foreign result consumed replay waiter")
	default:
	}
	require.True(t, r.cancelLookupWaiter(rc, 11, context.Canceled))
	r.handleStoreMQTTReplayResult(res)
	require.ErrorIs(t, f.Result().Err, context.Canceled)
}
