package presence_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// This structural interface keeps the baseline test compilable before the new
// capability exists. The separately retained 1,025-UID RED proves the bug itself.
type recoveryReadbackFactory interface {
	RecoveryUIDsWithReadback(authority.RouteTarget, []string) ([]string, func() ([]authority.Route, error), error)
}

func readbackDirectory(t *testing.T) (*authority.Directory, recoveryReadbackFactory, authority.RouteTarget) {
	t.Helper()
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, ShardCount: 1, RequireRecovery: true})
	target := authority.RouteTarget{HashSlot: 7, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2, RouteRevision: 11}
	directory.BecomeAuthority(target)
	factory, ok := any(directory).(recoveryReadbackFactory)
	if !ok {
		t.Fatal("real Directory lacks scoped recovery readback capability")
	}
	return directory, factory, target
}

func readbackFixtureRoute(uid string) authority.Route {
	return authority.Route{UID: uid, OwnerNodeID: 2, OwnerBootID: 22, SessionID: 1, OwnerSeq: 1, ConnectedUnix: 100, LastSeenUnix: 101}
}

// Pressure uses only public complete negative-proof installations. This runtime
// consumes an already resolved target; routing/hash distribution is tested above it.
func evictReadbackProofs(t *testing.T, directory *authority.Directory, target authority.RouteTarget) {
	t.Helper()
	for start := 0; start < 1024; start += 512 {
		uids := make([]string, 512)
		for i := range uids {
			uids[i] = fmt.Sprintf("readback-pressure-%d", start+i)
		}
		if err := directory.InstallRecoveredRoutes(target, uids, nil); err != nil {
			t.Fatalf("install complete pressure page: %v", err)
		}
	}
}

func requireReadbackRows(t *testing.T, read func() ([]authority.Route, error), want []authority.Route) {
	t.Helper()
	if read == nil {
		t.Fatal("valid capture has no scoped readback")
	}
	got, err := read()
	same := len(got) == 0 && len(want) == 0 || reflect.DeepEqual(got, want)
	if err != nil || !same {
		t.Fatalf("current scoped routes=%+v err=%v; want %+v", got, err, want)
	}
}

func TestRecoveryUIDReadbackKnownEvictionReadsCurrentRoutes(t *testing.T) {
	for _, change := range []string{"touch", "unregister", "owner_boot_replaced"} {
		t.Run(change, func(t *testing.T) {
			directory, factory, target := readbackDirectory(t)
			original := readbackFixtureRoute("known")
			if err := directory.InstallRecoveredRoutes(target, []string{original.UID}, []authority.Route{original}); err != nil {
				t.Fatal(err)
			}
			missing, read, err := factory.RecoveryUIDsWithReadback(target, []string{original.UID})
			if err != nil || len(missing) != 0 {
				t.Fatalf("capture known proof: missing=%v err=%v", missing, err)
			}
			evictReadbackProofs(t, directory, target)
			if rows, err := directory.EndpointsByUIDs(target, []string{original.UID}); !errors.Is(err, authority.ErrRouteNotReady) || len(rows) != 0 {
				t.Fatalf("ordinary lookup invented proof after eviction: %+v/%v", rows, err)
			}
			var want []authority.Route
			switch change {
			case "touch":
				updated := original
				updated.OwnerSeq, updated.LastSeenUnix = 2, 102
				if err := directory.TouchRoutes(target, []authority.Route{updated}); err != nil {
					t.Fatal(err)
				}
				want = []authority.Route{updated}
			case "unregister", "owner_boot_replaced":
				if err := directory.UnregisterRoute(target, original.Identity(), 3); err != nil {
					t.Fatal(err)
				}
				if change == "owner_boot_replaced" {
					updated := original
					updated.OwnerBootID, updated.SessionID = 23, 2
					if err := directory.TouchRoutes(target, []authority.Route{updated}); err != nil {
						t.Fatal(err)
					}
					want = []authority.Route{updated}
				}
			}
			requireReadbackRows(t, read, want)
		})
	}
}

func TestRecoveryUIDReadbackOriginallyMissingRequiresCurrentProof(t *testing.T) {
	for _, state := range []string{"never_completed", "completed_then_evicted"} {
		t.Run(state, func(t *testing.T) {
			directory, factory, target := readbackDirectory(t)
			missing, read, err := factory.RecoveryUIDsWithReadback(target, []string{"missing"})
			if err != nil || !reflect.DeepEqual(missing, []string{"missing"}) || read == nil {
				t.Fatalf("capture missing proof: missing=%v read=%t err=%v", missing, read != nil, err)
			}
			if state == "completed_then_evicted" {
				if err := directory.InstallRecoveredRoutes(target, missing, nil); err != nil {
					t.Fatal(err)
				}
				evictReadbackProofs(t, directory, target)
			}
			if rows, err := read(); !errors.Is(err, authority.ErrRouteNotReady) || len(rows) != 0 {
				t.Fatalf("missing current proof became offline: routes=%+v err=%v", rows, err)
			}
		})
	}
}

func TestRecoveryUIDReadbackAuthorityInstanceAndRevision(t *testing.T) {
	for _, change := range []string{"term", "lose_and_become_same_fence", "revision_only"} {
		t.Run(change, func(t *testing.T) {
			directory, factory, target := readbackDirectory(t)
			route := readbackFixtureRoute("known")
			if err := directory.InstallRecoveredRoutes(target, []string{route.UID}, []authority.Route{route}); err != nil {
				t.Fatal(err)
			}
			_, read, err := factory.RecoveryUIDsWithReadback(target, []string{route.UID})
			if err != nil || read == nil {
				t.Fatalf("capture scope: read=%t err=%v", read != nil, err)
			}
			next := target
			switch change {
			case "term":
				next.LeaderTerm++
			case "lose_and_become_same_fence":
				directory.LoseAuthority(target.HashSlot)
			case "revision_only":
				next.RouteRevision++
				next.AuthorityEpoch++
			}
			directory.BecomeAuthority(next)
			if change == "revision_only" {
				requireReadbackRows(t, read, []authority.Route{route})
				return
			}
			rows, err := read()
			if len(rows) != 0 || !(errors.Is(err, authority.ErrNotLeader) || errors.Is(err, authority.ErrRouteNotReady)) {
				t.Fatalf("replaced authority accepted old scope: %+v/%v", rows, err)
			}
			if change == "term" && !errors.Is(err, authority.ErrNotLeader) {
				t.Fatalf("distributed fence failure changed family: %v", err)
			}
		})
	}
}

func TestRecoveryUIDReadbackMissingOutputCannotMutateCapture(t *testing.T) {
	directory, factory, target := readbackDirectory(t)
	known := readbackFixtureRoute("known")
	if err := directory.InstallRecoveredRoutes(target, []string{known.UID}, []authority.Route{known}); err != nil {
		t.Fatal(err)
	}
	missing, read, err := factory.RecoveryUIDsWithReadback(target, []string{known.UID, "missing"})
	if err != nil || !reflect.DeepEqual(missing, []string{"missing"}) || read == nil {
		t.Fatalf("capture missing proof: missing=%v read=%t err=%v", missing, read != nil, err)
	}
	missing[0] = known.UID
	if rows, err := read(); !errors.Is(err, authority.ErrRouteNotReady) || len(rows) != 0 {
		t.Fatalf("mutated missing output changed immutable guard: %+v/%v", rows, err)
	}
}

func TestRecoveryUIDReadbackPreservesDuplicateAndEmptyUIDOrder(t *testing.T) {
	directory, factory, target := readbackDirectory(t)
	alpha, beta := readbackFixtureRoute("alpha"), readbackFixtureRoute("beta")
	beta.SessionID = 2
	if err := directory.InstallRecoveredRoutes(target, []string{alpha.UID, beta.UID}, []authority.Route{alpha, beta}); err != nil {
		t.Fatal(err)
	}
	missing, read, err := factory.RecoveryUIDsWithReadback(target, []string{beta.UID, "", alpha.UID, beta.UID, "missing", ""})
	if err != nil || !reflect.DeepEqual(missing, []string{"missing"}) || read == nil {
		t.Fatalf("ordered capture: missing=%v read=%t err=%v", missing, read != nil, err)
	}
	if err := directory.InstallRecoveredRoutes(target, missing, nil); err != nil {
		t.Fatal(err)
	}
	requireReadbackRows(t, read, []authority.Route{beta, alpha, beta})
}

func TestRecoveryUIDReadbackRejectsOversizedCapture(t *testing.T) {
	_, factory, target := readbackDirectory(t)
	uids := make([]string, 513)
	for i := range uids {
		uids[i] = fmt.Sprintf("oversized-%d", i)
	}
	missing, read, err := factory.RecoveryUIDsWithReadback(target, uids)
	if !errors.Is(err, authority.ErrRouteNotReady) || len(missing) != 0 || read != nil {
		t.Fatalf("oversized capture published partial scope: missing=%d read=%t err=%v", len(missing), read != nil, err)
	}
}

func TestRecoveryUIDReadbackAcceptsBoundaryCapture(t *testing.T) {
	_, factory, target := readbackDirectory(t)
	uids := make([]string, 512)
	for i := range uids {
		uids[i] = fmt.Sprintf("boundary-%d", i)
	}
	missing, read, err := factory.RecoveryUIDsWithReadback(target, uids)
	if err != nil || !reflect.DeepEqual(missing, uids) || read == nil {
		t.Fatalf("boundary capture: missing=%d read=%t err=%v", len(missing), read != nil, err)
	}
	if rows, err := read(); !errors.Is(err, authority.ErrRouteNotReady) || len(rows) != 0 {
		t.Fatalf("boundary missing proof became offline: %+v/%v", rows, err)
	}
}
