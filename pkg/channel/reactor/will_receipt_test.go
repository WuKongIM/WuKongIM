package reactor

import (
	"context"
	"strings"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/stretchr/testify/require"
)

type willReceiptReactorStore struct{ store.ChannelStore }

func (willReceiptReactorStore) LookupWillReceipt(context.Context, string, string) (ch.WillReceipt, bool, error) {
	return ch.WillReceipt{}, false, ch.ErrNotReady
}

func willReceiptCompletionFixture(t *testing.T) (*Reactor, *runtimeChannel, worker.Result, context.CancelFunc) {
	r, rc, res, cancel := mqttSourceCompletionFixture(t)
	rc.quorumReadReady = true
	rc.store = willReceiptReactorStore{rc.store}
	rc.lookupWaiters[11].source = nil
	rc.lookupWaiters[11].will = &willReceiptWaiter{request: ch.WillReceiptRequest{ChannelID: rc.state.ID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, FromUID: "sender", ServerWillKey: "mqtt-will-v1:" + strings.Repeat("a", 64)}, committedThrough: 8}
	res.Kind, res.StoreMQTTSource = worker.TaskStoreWillReceipt, nil
	res.StoreWillReceipt = &worker.StoreWillReceiptResult{Result: ch.WillReceiptResult{CommittedThrough: 8, Found: true, Receipt: ch.WillReceipt{MessageSeq: 7, MessageID: 99, ServerTimestampMS: 1000, ContentHash: [32]byte{1}}}}
	return r, rc, res, cancel
}
func TestWillReceiptCompletionRequiresCurrentRecoveredAuthority(t *testing.T) {
	for _, fault := range []string{"success", "absent", "stable_fence", "cancel", "generation", "epoch", "leader_epoch", "route", "follower", "recovering", "fence_changed", "guard", "guard_cancel", "error", "nil", "hw", "future", "partial_absent", "unsupported"} {
		t.Run(fault, func(t *testing.T) {
			r, rc, res, cancel := willReceiptCompletionFixture(t)
			f := rc.lookupWaiters[11].future
			require.True(t, r.hasPendingRuntimeWork(rc))
			switch fault {
			case "absent":
				res.StoreWillReceipt.Result.Found = false
				res.StoreWillReceipt.Result.Receipt = ch.WillReceipt{}
			case "stable_fence":
				rc.state.CommitReady = false
				rc.state.WriteFence = ch.WriteFence{Token: "move", Version: 1}
				rc.lookupWaiters[11].will.writeFence = rc.state.WriteFence
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
			case "recovering":
				rc.quorumReadReady = false
			case "fence_changed":
				rc.state.WriteFence = ch.WriteFence{Token: "move", Version: 1}
			case "guard":
				r.appendAdmissionGuard = ch.AppendAdmissionGuardFunc(func(context.Context, ch.AppendAdmissionRequest) error { return ch.ErrNotReady })
			case "guard_cancel":
				r.appendAdmissionGuard = ch.AppendAdmissionGuardFunc(func(context.Context, ch.AppendAdmissionRequest) error { cancel(); return nil })
			case "error":
				res.Err = ch.ErrClosed
			case "nil":
				res.StoreWillReceipt = nil
			case "hw":
				res.StoreWillReceipt.Result.CommittedThrough++
			case "future":
				res.StoreWillReceipt.Result.Receipt.MessageSeq = 9
			case "partial_absent":
				res.StoreWillReceipt.Result.Found = false
			case "unsupported":
				rc.store = rc.store.(willReceiptReactorStore).ChannelStore
			}
			r.handleStoreWillReceiptResult(res)
			select {
			case <-f.Done():
			default:
				t.Fatal("receipt waiter not completed")
			}
			if fault == "success" || fault == "absent" || fault == "stable_fence" {
				require.NoError(t, f.Result().Err)
				require.Equal(t, res.StoreWillReceipt.Result, f.Result().WillReceipt)
			} else {
				require.Error(t, f.Result().Err)
				require.Zero(t, f.Result().WillReceipt)
			}
			require.Empty(t, rc.lookupWaiters)
			require.Empty(t, r.lookupCancelChannels)
		})
	}
}
func TestWillReceiptWaiterRejectsForeignCompletion(t *testing.T) {
	r, rc, res, _ := willReceiptCompletionFixture(t)
	f := rc.lookupWaiters[11].future
	r.handleStoreLookupMessageResult(worker.Result{Kind: worker.TaskStoreLookupMessage, Fence: res.Fence, StoreLookupMessage: &worker.StoreLookupMessageResult{}})
	r.handleStoreMQTTSourceResult(worker.Result{Kind: worker.TaskStoreMQTTSource, Fence: res.Fence, StoreMQTTSource: &worker.StoreMQTTSourceResult{}})
	foreign := res
	foreign.Fence.OpID++
	r.handleStoreWillReceiptResult(foreign)
	select {
	case <-f.Done():
		t.Fatal("foreign completion consumed receipt waiter")
	default:
	}
	require.True(t, r.cancelLookupWaiter(rc, 11, context.Canceled))
	r.handleStoreWillReceiptResult(res)
	require.ErrorIs(t, f.Result().Err, context.Canceled)
}
