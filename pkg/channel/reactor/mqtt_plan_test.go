package reactor

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func mqttPlanCompletionFixture(t *testing.T) (*Reactor, *runtimeChannel, worker.Result, context.CancelFunc) {
	r, rc, res, cancel := mqttSourceCompletionFixture(t)
	r.cfg.Store = anchorCapableFactory{r.cfg.Store.(mqttSourceCapableFactory)}
	gen := quorumlog.MQTTSourceGeneration(ch.CommandID{1})
	rc.lookupWaiters[11].source = nil
	rc.lookupWaiters[11].plan = &mqttPlanWaiter{request: ch.MQTTReplayPlanRequest{ChannelID: rc.state.ID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, Generation: gen}, committedThrough: 8}
	res.Kind, res.StoreMQTTSource = worker.TaskStoreMQTTPlan, nil
	res.StoreMQTTPlan = &worker.StoreMQTTPlanResult{Plan: ch.MQTTReplayPlan{Source: ch.MQTTSourceSnapshot{Generation: gen, StartAfter: 4, CommittedThrough: 8}}}
	return r, rc, res, cancel
}

func TestMQTTPlanCompletionPreservesAdmissionAndLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "cancel", "generation", "epoch", "leader_epoch", "route", "follower", "not_ready", "write_fence", "guard_cancel", "error", "nil", "hw", "foreign_source", "bad_start", "capability"} {
		t.Run(mode, func(t *testing.T) {
			r, rc, res, cancel := mqttPlanCompletionFixture(t)
			defer cancel()
			f := rc.lookupWaiters[11].future
			require.True(t, r.hasPendingRuntimeWork(rc))
			switch mode {
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
				rc.state.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
			case "guard_cancel":
				r.appendAdmissionGuard = ch.AppendAdmissionGuardFunc(func(context.Context, ch.AppendAdmissionRequest) error { cancel(); return nil })
			case "error":
				res.Err = ch.ErrClosed
			case "nil":
				res.StoreMQTTPlan = nil
			case "hw":
				res.StoreMQTTPlan.Plan.Source.CommittedThrough++
			case "foreign_source":
				res.StoreMQTTPlan.Plan.Source.Generation = quorumlog.MQTTSourceGeneration(ch.CommandID{2})
			case "bad_start":
				res.StoreMQTTPlan.Plan.Source.StartAfter = 8
			case "capability":
				r.cfg.Store = r.cfg.Store.(anchorCapableFactory).mqttSourceCapableFactory
			}
			r.handleStoreMQTTPlanResult(res)
			select {
			case <-f.Done():
			default:
				t.Fatal("plan waiter not completed")
			}
			if mode == "success" {
				require.NoError(t, f.Result().Err)
				require.Equal(t, uint64(8), f.Result().MQTTPlan.Source.CommittedThrough, "later HW cannot expand the captured boundary")
			} else {
				require.Error(t, f.Result().Err)
			}
			require.Empty(t, rc.lookupWaiters)
			require.Empty(t, r.lookupCancelChannels)
		})
	}
}

func TestMQTTPlanWaiterRejectsForeignKindsAndOperations(t *testing.T) {
	r, rc, res, cancel := mqttPlanCompletionFixture(t)
	defer cancel()
	f := rc.lookupWaiters[11].future
	r.handleStoreLookupMessageResult(worker.Result{Kind: worker.TaskStoreLookupMessage, Fence: res.Fence, StoreLookupMessage: &worker.StoreLookupMessageResult{}})
	r.handleStoreMQTTSourceResult(worker.Result{Kind: worker.TaskStoreMQTTSource, Fence: res.Fence, StoreMQTTSource: &worker.StoreMQTTSourceResult{}})
	r.handleStoreMQTTReplayResult(worker.Result{Kind: worker.TaskStoreMQTTReplay, Fence: res.Fence, StoreMQTTReplay: &worker.StoreMQTTReplayResult{}})
	foreign := res
	foreign.Fence.OpID++
	r.handleStoreMQTTPlanResult(foreign)
	select {
	case <-f.Done():
		t.Fatal("foreign completion consumed plan")
	default:
	}
	require.True(t, r.cancelLookupWaiter(rc, 11, context.Canceled))
	r.handleStoreMQTTPlanResult(res)
	require.ErrorIs(t, f.Result().Err, context.Canceled)
}
