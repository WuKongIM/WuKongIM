package presence_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	presence "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

type callPageRegistry map[string]int

func (r callPageRegistry) ActiveRoutesByUIDs(uids []string, limit int) ([]presence.OwnerRoute, bool) {
	var rows []presence.OwnerRoute
	for _, uid := range uids {
		for i := 0; i < r[uid]; i++ {
			if len(rows) == limit {
				return nil, false
			}
			rows = append(rows, presence.OwnerRoute{UID: uid, HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 11, SessionID: uint64(i + 1), OwnerSeq: 1})
		}
	}
	return rows, true
}

func TestOwnerRecoveryCallsPreserveIndependent4096Availability(t *testing.T) {
	target := presence.RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1}
	owner := presence.NewOwnerRecovery(callPageRegistry{"a": 2500, "b": 2500}, 1, 11, func(presence.RouteTarget) error { return nil })
	var reader presence.OwnerRouteCallPageReader = owner
	calls := []presence.RecoveryCall{
		{Groups: []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"a"}}}},
		{Groups: []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"b"}}}},
	}
	for start := 0; start < len(calls); start++ {
		page, err := reader.ReadOwnerRouteCalls(context.Background(), calls, start)
		if err != nil || page.Start != start || page.Next != start+1 || len(page.Results) != 1 || len(page.Results[0]) != 1 || page.Results[0][0].Err != nil || len(page.Results[0][0].Snapshot.Routes) != 2500 {
			t.Fatal("caller-boundary continuation lost a legal complete call")
		}
	}
}

// fullPageBudgetContext expires only at a controlled registry boundary. No
// elapsed deadline or scheduler timing is needed to exercise page ownership.
type fullPageBudgetContext struct {
	context.Context
	done chan struct{}
}

func (c *fullPageBudgetContext) Done() <-chan struct{} { return c.done }
func (c *fullPageBudgetContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

type countedCallPageRegistry struct {
	counts     callPageRegistry
	reads      []string
	beforeRead func(string)
}

func (r *countedCallPageRegistry) ActiveRoutesByUIDs(uids []string, limit int) ([]presence.OwnerRoute, bool) {
	for _, uid := range uids {
		r.reads = append(r.reads, uid)
		if r.beforeRead != nil {
			r.beforeRead(uid)
		}
	}
	return r.counts.ActiveRoutesByUIDs(uids, limit)
}

func TestOwnerRecoveryCallsFullPageDoesNotReadFollowingCaller(t *testing.T) {
	ctx := &fullPageBudgetContext{Context: context.Background(), done: make(chan struct{})}
	registry := &countedCallPageRegistry{counts: callPageRegistry{"a": 4096, "b": 1}}
	registry.beforeRead = func(uid string) {
		if uid == "b" {
			close(ctx.done)
		}
	}
	owner := presence.NewOwnerRecovery(registry, 1, 11, func(presence.RouteTarget) error { return nil })
	page, err := owner.ReadOwnerRouteCalls(ctx, fullPageCalls(), 0)
	if err != nil || page.Start != 0 || page.Next != 1 || len(page.Results) != 1 || len(page.Results[0]) != 1 || page.Results[0][0].Err != nil || len(page.Results[0][0].Snapshot.Routes) != 4096 {
		t.Fatalf("full first page was lost to an unnecessary next read: start=%d next=%d calls=%d err=%v", page.Start, page.Next, len(page.Results), err)
	}
	if !reflect.DeepEqual(registry.reads, []string{"a"}) {
		t.Fatalf("full page consumed another caller: reads=%v", registry.reads)
	}
	// The following caller belongs to an explicit continuation call. The infra
	// adapter keeps one owner deadline across every continuation page.
	registry.beforeRead = nil
	page, err = owner.ReadOwnerRouteCalls(context.Background(), fullPageCalls(), 1)
	if err != nil || page.Start != 1 || page.Next != 2 || len(page.Results) != 1 || len(page.Results[0][0].Snapshot.Routes) != 1 || !reflect.DeepEqual(registry.reads, []string{"a", "b"}) {
		t.Fatalf("continuation lost the second independent caller: page=%+v reads=%v err=%v", page, registry.reads, err)
	}
}

func TestOwnerRecoveryCallsUnreturnedPrefixFailsClosedOnLaterDeadline(t *testing.T) {
	ctx := &fullPageBudgetContext{Context: context.Background(), done: make(chan struct{})}
	registry := &countedCallPageRegistry{counts: callPageRegistry{"a": 4095, "b": 1}}
	registry.beforeRead = func(uid string) {
		if uid == "b" {
			close(ctx.done)
		}
	}
	owner := presence.NewOwnerRecovery(registry, 1, 11, func(presence.RouteTarget) error { return nil })
	page, err := owner.ReadOwnerRouteCalls(ctx, fullPageCalls(), 0)
	if !errors.Is(err, context.DeadlineExceeded) || len(page.Results) != 0 || page.Next != 0 {
		t.Fatalf("unreturned prefix became partial proof after deadline: start=%d next=%d calls=%d err=%v", page.Start, page.Next, len(page.Results), err)
	}
	if !reflect.DeepEqual(registry.reads, []string{"a", "b"}) {
		t.Fatalf("nonfull page did not attempt the next caller: reads=%v", registry.reads)
	}
}

func fullPageCalls() []presence.RecoveryCall {
	target := presence.RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1}
	return []presence.RecoveryCall{
		{Groups: []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"a"}}}},
		{Groups: []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"b"}}}},
	}
}
