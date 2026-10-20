package conversation_qps

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CPUPlacementCPU retains the visible topology used to select one SMT pair.
type CPUPlacementCPU struct {
	CPU            int               `json:"cpu"`
	PackageID      int               `json:"package_id"`
	CoreID         int               `json:"core_id"`
	NUMANode       int               `json:"numa_node"`
	ThreadSiblings []int             `json:"thread_siblings"`
	Raw            map[string]string `json:"sysfs_raw,omitempty"`
}

// CPUPlacementPlan changes only driver overlap while keeping O/D node masks fixed.
type CPUPlacementPlan struct {
	Mode       string            `json:"mode"`
	HostCPUs   []int             `json:"host_cpus"`
	DriverCPUs []int             `json:"driver_cpus"`
	NodeCPUs   []int             `json:"node_cpus"`
	Topology   []CPUPlacementCPU `json:"topology"`
}

// CPUPlacementThread binds observations to a kernel task lifetime, not only TID.
type CPUPlacementThread struct {
	TID            int    `json:"tid"`
	StartTimeTicks uint64 `json:"starttime_ticks"`
	Affinity       []int  `json:"affinity"`
	Stat           string `json:"proc_stat"`
}

// CPUPlacementProcess includes every task observed in one bounded inventory.
type CPUPlacementProcess struct {
	PID            int                  `json:"pid"`
	StartTimeTicks uint64               `json:"starttime_ticks"`
	Threads        []CPUPlacementThread `json:"threads"`
}

// CPUPlacementReceipt preserves original preparation masks and partial failures.
// GOMAXPROCS belongs to the caller and is deliberately not changed by this helper.
type CPUPlacementReceipt struct {
	CPUPlacementPlan
	Initial    []CPUPlacementProcess `json:"initial"`
	Applied    []CPUPlacementProcess `json:"applied"`
	StartedAt  time.Time             `json:"started_at"`
	FinishedAt time.Time             `json:"finished_at"`
	DurationNS int64                 `json:"duration_ns"`
	Attempts   int                   `json:"attempts"`
	Valid      bool                  `json:"valid"`
	Failure    string                `json:"failure,omitempty"`
}

func parseCPUPlacementList(raw string) ([]int, error) {
	if len(raw) > 4096 || strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("missing or oversized CPU list")
	}
	seen := map[int]bool{}
	var cpus []int
	for _, part := range strings.Split(strings.TrimSpace(raw), ",") {
		bounds := strings.Split(part, "-")
		if len(bounds) > 2 {
			return nil, fmt.Errorf("invalid CPU range")
		}
		values := make([]int, len(bounds))
		for i, value := range bounds {
			if value == "" || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
				return nil, fmt.Errorf("invalid CPU number")
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n >= 1024 {
				return nil, fmt.Errorf("CPU number outside bounded mask")
			}
			values[i] = n
		}
		first, last := values[0], values[len(values)-1]
		if first > last {
			return nil, fmt.Errorf("descending CPU range")
		}
		for cpu := first; cpu <= last; cpu++ {
			if seen[cpu] {
				return nil, fmt.Errorf("duplicate CPU")
			}
			seen[cpu] = true
			cpus = append(cpus, cpu)
		}
	}
	slices.Sort(cpus)
	return cpus, nil
}

func parseCPUPlacementTaskStat(raw string) (int, uint64, error) {
	open, close := strings.Index(raw, " ("), strings.LastIndex(raw, ") ")
	if len(raw) > 16384 || open <= 0 || close <= open {
		return 0, 0, fmt.Errorf("invalid task stat framing")
	}
	pid, err := strconv.Atoi(raw[:open])
	fields := strings.Fields(raw[close+2:])
	if err != nil || pid <= 0 || len(fields) < 20 {
		return 0, 0, fmt.Errorf("invalid task stat identity")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return 0, 0, fmt.Errorf("invalid task starttime")
	}
	return pid, ticks, nil
}

func planCPUPlacement(mode string, rows []CPUPlacementCPU) (CPUPlacementPlan, error) {
	plan := CPUPlacementPlan{Mode: mode, Topology: slices.Clone(rows)}
	if mode != "S" && mode != "O" && mode != "D" {
		return plan, fmt.Errorf("unknown CPU placement mode %q", mode)
	}
	if len(rows) != 4 {
		return plan, fmt.Errorf("CPU placement requires four eligible logical CPUs")
	}
	slices.SortFunc(plan.Topology, func(a, b CPUPlacementCPU) int { return a.CPU - b.CPU })
	cores := map[[2]int][]int{}
	for _, row := range plan.Topology {
		if row.CPU < 0 || row.CPU >= 1024 || row.PackageID < 0 || row.CoreID < 0 || row.NUMANode < 0 ||
			slices.Contains(plan.HostCPUs, row.CPU) {
			return plan, fmt.Errorf("invalid or duplicate CPU topology identity")
		}
		if row.PackageID != plan.Topology[0].PackageID || row.NUMANode != plan.Topology[0].NUMANode {
			return plan, fmt.Errorf("CPU placement requires one package and NUMA node")
		}
		plan.HostCPUs = append(plan.HostCPUs, row.CPU)
		key := [2]int{row.PackageID, row.CoreID}
		cores[key] = append(cores[key], row.CPU)
	}
	if len(cores) != 2 {
		return plan, fmt.Errorf("CPU placement requires two visible physical cores")
	}
	for _, row := range plan.Topology {
		actual := slices.Clone(row.ThreadSiblings)
		slices.Sort(actual)
		expected := cores[[2]int{row.PackageID, row.CoreID}]
		if len(expected) != 2 || !slices.Equal(expected, actual) {
			return plan, fmt.Errorf("SMT sibling topology is incomplete or inconsistent")
		}
	}
	if mode == "S" {
		plan.DriverCPUs, plan.NodeCPUs = slices.Clone(plan.HostCPUs), slices.Clone(plan.HostCPUs)
		return plan, nil
	}
	pair := cores[[2]int{plan.Topology[0].PackageID, plan.Topology[0].CoreID}]
	overlap, dedicated := pair[0], pair[1]
	for _, cpu := range plan.HostCPUs {
		if cpu != dedicated {
			plan.NodeCPUs = append(plan.NodeCPUs, cpu)
		}
	}
	plan.DriverCPUs = []int{overlap}
	if mode == "D" {
		plan.DriverCPUs = []int{dedicated}
	}
	return plan, nil
}
