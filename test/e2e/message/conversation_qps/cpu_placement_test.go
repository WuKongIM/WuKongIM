package conversation_qps

import (
	"slices"
	"testing"
)

// These literal topologies exercise the diagnostic's public placement contract.
func TestCPUPlacementPlan(t *testing.T) {
	topology := func() []CPUPlacementCPU {
		return []CPUPlacementCPU{
			{CPU: 8, PackageID: 0, CoreID: 7, NUMANode: 0, ThreadSiblings: []int{4, 8}},
			{CPU: 6, PackageID: 0, CoreID: 3, NUMANode: 0, ThreadSiblings: []int{2, 6}},
			{CPU: 4, PackageID: 0, CoreID: 7, NUMANode: 0, ThreadSiblings: []int{4, 8}},
			{CPU: 2, PackageID: 0, CoreID: 3, NUMANode: 0, ThreadSiblings: []int{2, 6}},
		}
	}
	for _, tc := range []struct {
		mode   string
		driver []int
		nodes  []int
	}{
		{"S", []int{2, 4, 6, 8}, []int{2, 4, 6, 8}},
		{"O", []int{2}, []int{2, 4, 8}},
		{"D", []int{6}, []int{2, 4, 8}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			plan, err := planCPUPlacement(tc.mode, topology())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(plan.DriverCPUs, tc.driver) || !slices.Equal(plan.NodeCPUs, tc.nodes) {
				t.Fatalf("unexpected placement: %+v", plan)
			}
			if !slices.Equal(plan.HostCPUs, []int{2, 4, 6, 8}) {
				t.Fatalf("wrong host inventory: %+v", plan)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func([]CPUPlacementCPU) []CPUPlacementCPU
	}{
		{"missing CPU", func(v []CPUPlacementCPU) []CPUPlacementCPU { return v[:3] }},
		{"duplicate CPU", func(v []CPUPlacementCPU) []CPUPlacementCPU { v[0].CPU = 6; return v }},
		{"third core", func(v []CPUPlacementCPU) []CPUPlacementCPU { v[0].CoreID = 9; return v }},
		{"second NUMA node", func(v []CPUPlacementCPU) []CPUPlacementCPU { v[0].NUMANode = 1; return v }},
		{"second package", func(v []CPUPlacementCPU) []CPUPlacementCPU { v[0].PackageID = 1; return v }},
		{"asymmetric siblings", func(v []CPUPlacementCPU) []CPUPlacementCPU { v[0].ThreadSiblings = []int{2, 8}; return v }},
		{"hidden sibling", func(v []CPUPlacementCPU) []CPUPlacementCPU { v[0].ThreadSiblings = []int{8, 10}; return v }},
		{"unknown NUMA", func(v []CPUPlacementCPU) []CPUPlacementCPU { v[0].NUMANode = -1; return v }},
		{"oversized CPU", func(v []CPUPlacementCPU) []CPUPlacementCPU { v[0].CPU = 1024; return v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := planCPUPlacement("D", tc.change(topology())); err == nil {
				t.Fatal("unproven topology accepted")
			}
		})
	}
	if _, err := planCPUPlacement("d", topology()); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestCPUPlacementParsers(t *testing.T) {
	for _, text := range []string{"0-3\n", "0,1,2,3", "3,1,0,2"} {
		got, err := parseCPUPlacementList(text)
		if err != nil || !slices.Equal(got, []int{0, 1, 2, 3}) {
			t.Fatalf("%q: %v %v", text, got, err)
		}
	}
	for _, text := range []string{"", "1-0", "0,0", "0-2,2", "0,,2", "-1", "1024", "1.2", "1-2-3", "0x1", "0-999999999999999"} {
		if _, err := parseCPUPlacementList(text); err == nil {
			t.Fatalf("invalid mask %q accepted", text)
		}
	}
	raw := "123 (a tricky ) name) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 456 999\n"
	pid, ticks, err := parseCPUPlacementTaskStat(raw)
	if err != nil || pid != 123 || ticks != 456 {
		t.Fatalf("wrong task identity: %d %d %v", pid, ticks, err)
	}
	for _, text := range []string{"", "123 a) S", "no (x) S 1", "123 (x) S 1"} {
		if _, _, err := parseCPUPlacementTaskStat(text); err == nil {
			t.Fatalf("invalid stat %q accepted", text)
		}
	}
}
