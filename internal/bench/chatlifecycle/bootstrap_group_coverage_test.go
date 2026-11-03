package chatlifecycle

import (
	"context"
	"fmt"
	"testing"
)

// Failure inventory: bootstrap silently supplies no owner-local sender/recipient
// pair after reducing the online population; the formal control is broken; or
// selector failure is mistaken for a transport/deadline/latency failure. This
// diagnostic invokes the real bootstrap identity planner and SessionPool selector.
// Only SDK CONNECT/full-sync are faked; no product process/network is used.
func TestBootstrapRosterSupportsSampledGroupsAfterPopulationReduction(t *testing.T) {
	for _, population := range []int{10000, 5000} {
		t.Run(fmt.Sprintf("online_%d", population), func(t *testing.T) {
			for worker := uint64(0); worker < 3; worker++ {
				t.Run(fmt.Sprintf("worker_%d", worker), func(t *testing.T) {
					f := newEngineTestFixture(t, engineTestLimits{Formal: true, WorkerCount: 3, WorkerID: worker, OnlineUsers: population, SendRatePerSecond: 1000})
					t.Cleanup(func() {
						if err := f.pool.CloseAll(); err != nil {
							t.Error(err)
						}
					})
					scheduler := f.engine.scheduler
					scheduler.bootstrapping = true
					catalog := f.engine.generator.catalog
					target, err := workerOnlineTarget(population, worker, 3)
					if err != nil {
						t.Fatal(err)
					}
					selected := make(map[GroupCategory]Group)
					for category := GroupSmall; category <= GroupVeryLarge; category++ {
						start, count, ok := catalog.categoryRange(category)
						if !ok {
							continue
						}
						for index := start; index < start+uint64(count); index++ {
							owner, _ := catalog.GroupOwner(index)
							if owner == worker {
								group, e := catalog.Group(index)
								if e != nil {
									t.Fatal(e)
								}
								selected[category] = group
								break
							}
						}
					}
					localMembers := make(map[GroupCategory]int)
					for i := 0; i < target; i++ {
						login, _, _, ok, e := scheduler.planLogin(f.pool, f.graph, f.schedule, catalog, uint64(i)*3+worker, LoginNew)
						if e != nil || !ok {
							t.Fatalf("bootstrap plan admitted=%v err=%v", ok, e)
						}
						scheduler.nextNewIndex++
						for category, group := range selected {
							if group.ContainsIndex(login.UserIndex) {
								if _, e := f.pool.Login(context.Background(), login); e != nil {
									t.Fatal(e)
								}
								localMembers[category]++
							}
						}
					}
					for category, group := range selected {
						sender, available := f.pool.onlineGroupMemberExcluding(group, 0, true, f.clock.Now(), nil)
						recipient, recipientOK := "", false
						if available {
							recipient, recipientOK = f.pool.onlineGroupCorrelationRecipient(group, sender.UID, 0, f.clock.Now())
						}
						if !available || !recipientOK || recipient == sender.UID {
							t.Errorf("sampled group route unavailable: population=%d worker=%d category=%d owner_local_members=%d need=2 product_network_calls=0", population, worker, category, localMembers[category])
						} else {
							t.Logf("route ready: population=%d worker=%d category=%d owner_local_members=%d", population, worker, category, localMembers[category])
						}
					}
				})
			}
		})
	}
}
