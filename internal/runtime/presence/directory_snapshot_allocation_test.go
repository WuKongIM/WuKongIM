package presence_test

import (
	"fmt"
	"reflect"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// This runtime consumes an already resolved authority target. The fixture tests
// the public snapshot contract, not UID hash routing or recovery transport.
func snapshotAllocationDirectory(t *testing.T, devices int) (*authority.Directory, authority.RouteTarget, []string, []authority.Route) {
	t.Helper()
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	target := authority.RouteTarget{HashSlot: 37, SlotID: 7, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2}
	directory.BecomeAuthority(target)
	uids := make([]string, 512)
	want := make([]authority.Route, 0, len(uids)*devices)
	for i := range uids {
		uids[i] = fmt.Sprintf("snapshot-%03d", i)
		for device := 0; device < devices; device++ {
			want = append(want, authority.Route{
				UID: uids[i], OwnerNodeID: uint64(device + 1), OwnerBootID: 1,
				OwnerSeq: 1, SessionID: uint64(i*devices + device + 1),
				DeviceFlag: uint8(device + 1),
			})
		}
	}
	// Reverse installation order so successful reads must sort per UID.
	rows := append([]authority.Route(nil), want...)
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	if err := directory.InstallRecoveredRoutes(target, uids, rows); err != nil {
		t.Fatal(err)
	}
	return directory, target, uids, want
}

func TestDirectorySingleTargetSnapshotsAvoidPerUIDAllocation(t *testing.T) {
	for _, devices := range []int{1, 2} {
		t.Run(fmt.Sprintf("devices_%d", devices), func(t *testing.T) {
			directory, target, uids, want := snapshotAllocationDirectory(t, devices)
			_, readback, err := directory.RecoveryUIDsWithReadback(target, uids)
			if err != nil || readback == nil {
				t.Fatalf("capture current proof: %v", err)
			}
			for _, mode := range []string{"direct", "readback"} {
				t.Run(mode, func(t *testing.T) {
					read := readback
					if mode == "direct" {
						read = func() ([]authority.Route, error) { return directory.EndpointsByUIDs(target, uids) }
					}
					got, err := read()
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("ordered snapshot changed: rows=%d error=%v", len(got), err)
					}
					allocs := testing.AllocsPerRun(25, func() {
						rows, err := read()
						if err != nil || len(rows) != len(want) {
							panic("incomplete current snapshot")
						}
					})
					t.Logf("512 UID snapshot: devices=%d mode=%s allocations=%.0f", devices, mode, allocs)
					// A call-owned output is required; per-recipient temporary output
					// and reflective sorting allocations must not grow with fanout.
					if allocs > 8 {
						t.Fatalf("snapshot allocations=%.0f; want <=8 independent of UID/device count", allocs)
					}
				})
			}
		})
	}
}

func TestDirectorySingleTargetSnapshotOwnsBackingAndPreservesDuplicates(t *testing.T) {
	directory, target, uids, all := snapshotAllocationDirectory(t, 2)
	query := []string{uids[1], "", "absent", uids[0], uids[1]}
	if err := directory.InstallRecoveredRoutes(target, []string{"absent"}, nil); err != nil {
		t.Fatal(err)
	}
	want := []authority.Route{all[2], all[3], all[0], all[1], all[2], all[3]}
	_, readback, err := directory.RecoveryUIDsWithReadback(target, query)
	if err != nil || readback == nil {
		t.Fatalf("capture ordered proof: %v", err)
	}
	for _, read := range []func() ([]authority.Route, error){
		func() ([]authority.Route, error) { return directory.EndpointsByUIDs(target, query) }, readback,
	} {
		first, err := read()
		if err != nil || !reflect.DeepEqual(first, want) {
			t.Fatalf("duplicate/empty UID order changed: %+v error=%v", first, err)
		}
		second, err := read()
		if err != nil {
			t.Fatal(err)
		}
		first[0].UID = "caller mutation"
		first = append(first, authority.Route{UID: "caller append"})
		third, err := read()
		if err != nil || !reflect.DeepEqual(second, want) || !reflect.DeepEqual(third, want) {
			t.Fatal("call-owned snapshot aliases Directory or another call")
		}
	}
	if rows, err := directory.EndpointsByUIDs(target, nil); err != nil || rows != nil {
		t.Fatalf("empty input changed: %+v error=%v", rows, err)
	}
}
