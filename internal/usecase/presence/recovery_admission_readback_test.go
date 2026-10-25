package presence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// recoveryProofPublishedDirectory models another caller publishing an exact
// Directory proof between the lookup and missing-UID reads at the public port.
// The proof and subsequent lookup still use the real authority Directory.
type recoveryProofPublishedDirectory struct {
	*authority.Directory
	beforeRecoveryUIDs func(RouteTarget, []string) error
	readEndpoints      func(RouteTarget, []string) ([]Route, error)
}

func (d *recoveryProofPublishedDirectory) EndpointsByUIDs(target RouteTarget, uids []string) ([]Route, error) {
	if d.readEndpoints != nil {
		return d.readEndpoints(target, uids)
	}
	return d.Directory.EndpointsByUIDs(target, uids)
}

func (d *recoveryProofPublishedDirectory) RecoveryUIDs(target RouteTarget, uids []string) ([]string, error) {
	if d.beforeRecoveryUIDs != nil {
		if err := d.beforeRecoveryUIDs(target, uids); err != nil {
			return nil, err
		}
	}
	return d.Directory.RecoveryUIDs(target, uids)
}

func TestAuthorityRecoveryReadsProofPublishedBeforeMissingUIDSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		routes []Route
	}{
		{name: "present", routes: []Route{{UID: "bob", OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 41}}},
		{name: "confirmed_absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := RouteTarget{HashSlot: 79, SlotID: 4, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2}
			directory := &recoveryProofPublishedDirectory{Directory: authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})}
			directory.BecomeAuthority(target)
			directory.beforeRecoveryUIDs = func(got RouteTarget, uids []string) error {
				if got != target || !reflect.DeepEqual(uids, []string{"bob"}) {
					return fmt.Errorf("published proof changed lookup identity: target=%+v uids=%v", got, uids)
				}
				return directory.InstallRecoveredRoutes(target, []string{"bob"}, tc.routes)
			}
			var ownerCalls atomic.Int32
			recovery := NewAuthorityRecovery(directory, recoveryOwnersFunc(func(context.Context, RouteTarget, []string) ([]Route, error) {
				ownerCalls.Add(1)
				return nil, errors.New("already published proof queried owners")
			}))
			got, err := recovery.Endpoints(context.Background(), target, []string{"bob"})
			if calls := ownerCalls.Load(); calls != 0 {
				t.Fatalf("already published proof queried owners: calls=%d", calls)
			}
			if err != nil || !reflect.DeepEqual(got, tc.routes) {
				t.Fatalf("published proof lookup: routes=%+v err=%v; want routes=%+v", got, err, tc.routes)
			}
		})
	}
}

func TestAuthorityRecoveryPublishedProofReadbackPreservesFailureAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
	}{
		{name: "evicted", want: authority.ErrRouteNotReady},
		{name: "changed_fence", want: authority.ErrNotLeader},
		{name: "cancelled_before_readback", want: context.Canceled},
		{name: "cancelled_during_readback", want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := RouteTarget{HashSlot: 79, SlotID: 4, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2}
			directory := &recoveryProofPublishedDirectory{Directory: authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})}
			directory.BecomeAuthority(target)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			directory.beforeRecoveryUIDs = func(got RouteTarget, uids []string) error {
				rows := []Route{{UID: "bob", OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 41}}
				if err := directory.InstallRecoveredRoutes(got, uids, rows); err != nil {
					return err
				}
				if tc.name == "cancelled_before_readback" {
					cancel()
				}
				return nil
			}
			reads := 0
			directory.readEndpoints = func(got RouteTarget, uids []string) ([]Route, error) {
				reads++
				if reads == 2 {
					switch tc.name {
					case "evicted":
						// A complete cache turn removes the older proof without
						// fabricating an empty authoritative lookup.
						for i := 0; i < 1024; i += authority.RecoveryBatchSize {
							page := make([]string, authority.RecoveryBatchSize)
							for j := range page {
								page[j] = fmt.Sprintf("other-%d", i+j)
							}
							if err := directory.InstallRecoveredRoutes(got, page, nil); err != nil {
								return nil, err
							}
						}
					case "changed_fence":
						next := got
						next.LeaderTerm++
						directory.BecomeAuthority(next)
					}
				}
				rows, err := directory.Directory.EndpointsByUIDs(got, uids)
				if reads == 2 && tc.name == "cancelled_during_readback" {
					cancel()
				}
				return rows, err
			}
			var ownerCalls atomic.Int32
			recovery := NewAuthorityRecovery(directory, recoveryOwnersFunc(func(context.Context, RouteTarget, []string) ([]Route, error) {
				ownerCalls.Add(1)
				return nil, errors.New("proof readback failure started owner retry")
			}))
			rows, err := recovery.Endpoints(ctx, target, []string{"bob"})
			if calls := ownerCalls.Load(); calls != 0 {
				t.Fatalf("proof readback failure started owner retry: calls=%d", calls)
			}
			if !errors.Is(err, tc.want) || len(rows) != 0 {
				t.Fatalf("proof readback: routes=%+v err=%v; want %v", rows, err, tc.want)
			}
		})
	}
}

func TestAuthorityRecoveryFailedReadbackDoesNotRetryWithColdSibling(t *testing.T) {
	target := RouteTarget{HashSlot: 79, SlotID: 4, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2}
	cold := target
	cold.HashSlot++
	proven := target
	proven.HashSlot += 2
	directory := &recoveryProofPublishedDirectory{Directory: authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})}
	for _, tgt := range []RouteTarget{target, cold, proven} {
		directory.BecomeAuthority(tgt)
	}
	knownRoute := Route{UID: "known", OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 42}
	if err := directory.InstallRecoveredRoutes(proven, []string{"known"}, []Route{knownRoute}); err != nil {
		t.Fatal(err)
	}
	directory.beforeRecoveryUIDs = func(got RouteTarget, uids []string) error {
		if got == target {
			return directory.InstallRecoveredRoutes(got, uids, nil)
		}
		return nil
	}
	readbackErr := fmt.Errorf("proof became unavailable: %w", authority.ErrRouteNotReady)
	reads := 0
	directory.readEndpoints = func(got RouteTarget, uids []string) ([]Route, error) {
		if got == target {
			reads++
			if reads == 2 {
				return nil, readbackErr
			}
		}
		return directory.Directory.EndpointsByUIDs(got, uids)
	}
	coldRoute := Route{UID: "cold", OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 43}
	var ownerCalls atomic.Int32
	var unexpectedOwner atomic.Bool
	owners := recoveryOwnersFunc(func(_ context.Context, got RouteTarget, uids []string) ([]Route, error) {
		ownerCalls.Add(1)
		if got != cold || !reflect.DeepEqual(uids, []string{"cold"}) {
			unexpectedOwner.Store(true)
			return nil, errors.New("readback failure retried owners")
		}
		return []Route{coldRoute}, nil
	})
	results := NewAuthorityRecovery(directory, owners).EndpointsByTargets(context.Background(), []EndpointLookupGroup{
		{Target: target, UIDs: []string{"bob"}},
		{Target: cold, UIDs: []string{"cold"}},
		{Target: proven, UIDs: []string{"known"}},
	})
	if calls := ownerCalls.Load(); calls != 1 || unexpectedOwner.Load() {
		t.Fatalf("readback failure changed owner selection: calls=%d unexpected=%t", calls, unexpectedOwner.Load())
	}
	if len(results) != 3 || !errors.Is(results[0].Err, readbackErr) || len(results[0].Routes) != 0 {
		t.Fatalf("failed group lost its readback error: %+v", results)
	}
	if results[1].Err != nil || !reflect.DeepEqual(results[1].Routes, []Route{coldRoute}) || results[2].Err != nil || !reflect.DeepEqual(results[2].Routes, []Route{knownRoute}) {
		t.Fatalf("successful cold/proven siblings were changed: %+v", results)
	}
}
