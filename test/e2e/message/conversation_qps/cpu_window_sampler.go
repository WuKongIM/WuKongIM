package conversation_qps

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const cpuSamplerMaxPoints = 70
const cpuSamplerMaxThreads = 256
const cpuSamplerMaxBytes = 64 << 20

// cpuSampleProcess binds an owned process to its expected lifetime and CPU mask.
type cpuSampleProcess struct {
	Role           string `json:"role"`
	PID            int    `json:"pid"`
	StartTimeTicks uint64 `json:"starttime_ticks"`
	Affinity       string `json:"affinity"`
}

type cpuSampleFile struct {
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
	Error     string `json:"error,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type cpuProcStat struct {
	PID            int    `json:"pid"`
	StartTimeTicks uint64 `json:"starttime_ticks"`
	UserTicks      uint64 `json:"user_ticks"`
	SystemTicks    uint64 `json:"system_ticks"`
	MinorFaults    uint64 `json:"minor_faults"`
	MajorFaults    uint64 `json:"major_faults"`
}

// cpuSchedstat describes one Linux task, not a whole Go process or goroutine.
type cpuSchedstat struct {
	RuntimeNS  uint64 `json:"runtime_ns"`
	WaitNS     uint64 `json:"runqueue_wait_ns"`
	Timeslices uint64 `json:"timeslices"`
}

type cpuThreadSample struct {
	TID            int           `json:"tid"`
	StartTimeTicks uint64        `json:"starttime_ticks"`
	Affinity       string        `json:"affinity,omitempty"`
	Valid          bool          `json:"valid"`
	Issue          string        `json:"issue,omitempty"`
	FirstStat      cpuSampleFile `json:"stat_before"`
	FinalStat      cpuSampleFile `json:"stat_after"`
	Status         cpuSampleFile `json:"status"`
	SchedstatFile  cpuSampleFile `json:"schedstat_file"`
	Schedstat      *cpuSchedstat `json:"schedstat,omitempty"`
	Delta          *cpuSchedstat `json:"matched_previous_delta,omitempty"`
}

type cpuProcessSample struct {
	FinalStatFile     cpuSampleFile     `json:"stat_after_threads"`
	Membership        cpuSampleFile     `json:"cgroup_membership"`
	Expected          cpuSampleProcess  `json:"expected"`
	StatFile          cpuSampleFile     `json:"stat_file"`
	Status            cpuSampleFile     `json:"status"`
	Stat              *cpuProcStat      `json:"stat,omitempty"`
	Affinity          string            `json:"affinity,omitempty"`
	Valid             bool              `json:"valid"`
	Issues            []string          `json:"issues,omitempty"`
	EnumeratedThreads int               `json:"enumerated_threads"`
	ValidThreads      int               `json:"valid_threads"`
	MatchedThreads    int               `json:"matched_threads"`
	NewThreads        []string          `json:"new_threads,omitempty"`
	ExitedThreads     []string          `json:"exited_or_unobserved_threads,omitempty"`
	Threads           []cpuThreadSample `json:"threads"`
}

type cpuWindowPoint struct {
	Slot              int                        `json:"slot"`
	StartedAt         time.Time                  `json:"started_at"`
	FinishedAt        time.Time                  `json:"finished_at"`
	StartOffsetNS     int64                      `json:"start_offset_ns"`
	DeadlineLagNS     int64                      `json:"deadline_lag_ns"`
	ReadDurationNS    int64                      `json:"read_duration_ns"`
	SamplerCPUStartNS int64                      `json:"sampler_cpu_start_ns"`
	SamplerCPUEndNS   int64                      `json:"sampler_cpu_end_ns"`
	ReadBytes         int64                      `json:"read_bytes"`
	MissedBefore      int                        `json:"missed_before"`
	Processes         []cpuProcessSample         `json:"processes"`
	Host              []cpuSampleFile            `json:"host"`
	Cgroups           map[string][]cpuSampleFile `json:"cgroups"`
	Valid             bool                       `json:"valid"`
	Issues            []string                   `json:"issues,omitempty"`
}

// CPUWindowSamples preserves incomplete evidence. Valid means the collector's
// bounded observations are usable; it never means a workload or release passed.
type CPUWindowSamples struct {
	Schema                   string           `json:"schema"`
	Valid                    bool             `json:"valid"`
	Issues                   []string         `json:"issues,omitempty"`
	Start                    time.Time        `json:"start"`
	DurationNS               int64            `json:"duration_ns"`
	FinishedAt               time.Time        `json:"finished_at"`
	FinishedOffsetNS         int64            `json:"finished_offset_ns"`
	StopReason               string           `json:"stop_reason"`
	SamplerTID               int              `json:"sampler_tid"`
	SamplerCPUNS             int64            `json:"sampler_cpu_ns"`
	ReadBytes                int64            `json:"read_bytes"`
	ConservativeReceiptBytes int64            `json:"conservative_receipt_bytes"`
	MissedSlots              int              `json:"missed_slots"`
	SchedSchedstats          cpuSampleFile    `json:"sched_schedstats"`
	Clock                    string           `json:"clock"`
	Points                   []cpuWindowPoint `json:"points"`
}

// cpuJSONEscapedBytes counts encoding/json's escaped string payload without
// allocating a serialized copy during the measurement window.
func cpuJSONEscapedBytes(raw string) int64 {
	var n int64
	for i := 0; i < len(raw); {
		c := raw[i]
		if c < utf8.RuneSelf {
			switch c {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				n += 2
			case '<', '>', '&':
				n += 6
			default:
				if c < 0x20 {
					n += 6
				} else {
					n++
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(raw[i:])
		if (r == utf8.RuneError && size == 1) || r == '\u2028' || r == '\u2029' {
			n += 6
		} else {
			n += int64(size)
		}
		i += size
	}
	return n
}

// classifyCPUThreadGaps distinguishes a confirmed exit between observations
// from an unreadable or reused TID. Every retained exit leaves a partial final
// interval, so matched deltas cannot represent a complete process wait total.
func classifyCPUThreadGaps(previous map[string]cpuThreadSample, before, after map[int]bool) ([]string, []string) {
	var gaps, issues []string
	for key, thread := range previous {
		gaps = append(gaps, key)
		if before[thread.TID] || after[thread.TID] {
			issues = append(issues, "prior thread identity unobserved or reused while its TID remains: "+key)
		}
	}
	sort.Strings(gaps)
	sort.Strings(issues)
	return gaps, issues
}

// cpuSchedstatProgressThreads verifies capability on distinct matched task
// lifetimes. A zero wait counter is allowed; a zero runtime and timeslice delta
// does not demonstrate that schedstat works for this controlled active child.
func cpuSchedstatProgressThreads(samples CPUWindowSamples) int {
	type identity struct {
		pid, tid int
		start    uint64
	}
	seen := map[identity]bool{}
	for _, point := range samples.Points {
		for _, process := range point.Processes {
			for _, thread := range process.Threads {
				if thread.Valid && thread.Delta != nil && (thread.Delta.RuntimeNS > 0 || thread.Delta.Timeslices > 0) {
					seen[identity{process.Expected.PID, thread.TID, thread.StartTimeTicks}] = true
				}
			}
		}
	}
	return len(seen)
}

func parseCPUSampleStat(raw string) (cpuProcStat, error) {
	var out cpuProcStat
	open, end := strings.IndexByte(raw, '('), strings.LastIndexByte(raw, ')')
	if open < 1 || end <= open {
		return out, fmt.Errorf("proc stat comm delimiters missing")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(raw[:open]))
	if err != nil || pid <= 0 {
		return out, fmt.Errorf("proc stat PID invalid")
	}
	f := strings.Fields(raw[end+1:])
	if len(f) < 20 {
		return out, fmt.Errorf("proc stat fields missing")
	}
	out.PID = pid
	for index, target := range map[int]*uint64{7: &out.MinorFaults, 9: &out.MajorFaults, 11: &out.UserTicks, 12: &out.SystemTicks, 19: &out.StartTimeTicks} {
		n, e := strconv.ParseUint(f[index], 10, 64)
		if e != nil {
			return cpuProcStat{}, fmt.Errorf("proc stat numeric field %d invalid", index+3)
		}
		*target = n
	}
	if out.StartTimeTicks == 0 {
		return cpuProcStat{}, fmt.Errorf("proc stat starttime missing")
	}
	return out, nil
}

func parseCPUSampleSchedstat(raw string) (cpuSchedstat, error) {
	var out cpuSchedstat
	f := strings.Fields(raw)
	if len(f) != 3 {
		return out, fmt.Errorf("schedstat requires exactly three counters")
	}
	for i, target := range []*uint64{&out.RuntimeNS, &out.WaitNS, &out.Timeslices} {
		n, e := strconv.ParseUint(f[i], 10, 64)
		if e != nil {
			return cpuSchedstat{}, fmt.Errorf("schedstat counter invalid")
		}
		*target = n
	}
	return out, nil
}

func cpuThreadDifference(a, b cpuThreadSample) (cpuSchedstat, error) {
	if !a.Valid || !b.Valid || a.TID != b.TID || a.StartTimeTicks != b.StartTimeTicks || a.Schedstat == nil || b.Schedstat == nil {
		return cpuSchedstat{}, fmt.Errorf("thread lifetime or observation changed")
	}
	x, y := a.Schedstat, b.Schedstat
	if y.RuntimeNS < x.RuntimeNS || y.WaitNS < x.WaitNS || y.Timeslices < x.Timeslices {
		return cpuSchedstat{}, fmt.Errorf("thread counter regressed")
	}
	return cpuSchedstat{RuntimeNS: y.RuntimeNS - x.RuntimeNS, WaitNS: y.WaitNS - x.WaitNS, Timeslices: y.Timeslices - x.Timeslices}, nil
}

func cpuAffinitySet(mask string) (map[uint64]bool, error) {
	out := map[uint64]bool{}
	if len(mask) == 0 || len(mask) > 4096 {
		return nil, fmt.Errorf("invalid affinity length")
	}
	for _, part := range strings.Split(mask, ",") {
		a, b, isRange := strings.Cut(part, "-")
		first, e := strconv.ParseUint(a, 10, 32)
		if e != nil {
			return nil, e
		}
		last := first
		if isRange {
			last, e = strconv.ParseUint(b, 10, 32)
		}
		if e != nil || last < first || last-first > 4096 {
			return nil, fmt.Errorf("invalid affinity range")
		}
		for n := first; n <= last; n++ {
			if out[n] || len(out) >= 4096 {
				return nil, fmt.Errorf("duplicate or excessive affinity")
			}
			out[n] = true
		}
	}
	return out, nil
}
func cpuAffinitiesEqual(a, b string) bool {
	x, e := cpuAffinitySet(a)
	if e != nil {
		return false
	}
	y, e := cpuAffinitySet(b)
	if e != nil || len(x) != len(y) {
		return false
	}
	for n := range x {
		if !y[n] {
			return false
		}
	}
	return true
}
func cpuStatusAffinity(raw string) (string, error) {
	for _, line := range strings.Split(raw, "\n") {
		if value, ok := strings.CutPrefix(line, "Cpus_allowed_list:"); ok {
			mask := strings.TrimSpace(value)
			_, e := cpuAffinitySet(mask)
			return mask, e
		}
	}
	return "", fmt.Errorf("status affinity missing")
}

func validateCPUSamplerInputs(ps []cpuSampleProcess, cgroups []string, d time.Duration) ([]string, error) {
	if len(ps) == 0 || len(ps) > 4 || d <= 0 || d > 60*time.Second {
		return nil, fmt.Errorf("sampler requires 1-4 processes and at most 60 seconds")
	}
	seen := map[int]bool{}
	for _, p := range ps {
		if p.PID <= 0 || p.StartTimeTicks == 0 || p.Role == "" || len(p.Role) > 64 || seen[p.PID] {
			return nil, fmt.Errorf("invalid or duplicate process identity")
		}
		if _, e := cpuAffinitySet(p.Affinity); e != nil {
			return nil, e
		}
		seen[p.PID] = true
	}
	if len(cgroups) == 0 || len(cgroups) > 128 {
		return nil, fmt.Errorf("sampler requires bounded resolved cgroups")
	}
	groups := map[string]bool{}
	for _, g := range cgroups {
		if len(g) > 4096 || filepath.Clean(g) != g || !(g == "/sys/fs/cgroup" || strings.HasPrefix(g, "/sys/fs/cgroup/")) {
			return nil, fmt.Errorf("invalid resolved cgroup path")
		}
		groups[g] = true
	}
	out := make([]string, 0, len(groups))
	for g := range groups {
		out = append(out, g)
	}
	sort.Strings(out)
	return out, nil
}

// nextCPUSampleSlot skips expired deadlines; the sampler never catches up by
// issuing a burst of reads after it has been descheduled.
func nextCPUSampleSlot(completed int, elapsed time.Duration) (next, missed int) {
	next = completed + 1
	if floor := int(elapsed/time.Second) + 1; floor > next {
		missed = floor - next
		next = floor
	}
	return
}
