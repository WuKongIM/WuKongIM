//go:build linux

package conversation_qps

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type cpuSamplerReader struct {
	start     time.Time
	bytes     int64
	reserved  int64
	exhausted bool
}

// reserve bounds retained JSON using escaped payload widths and conservative
// structural overhead. No JSON encoding or disk writes occur in load.
func (r *cpuSamplerReader) reserve(n int64) bool {
	if r.exhausted || n < 0 || r.reserved+n > cpuSamplerMaxBytes {
		r.exhausted = true
		return false
	}
	r.reserved += n
	return true
}
func (r *cpuSamplerReader) read(path string) cpuSampleFile {
	result := cpuSampleFile{Path: path}
	if time.Since(r.start) >= 75*time.Second {
		r.exhausted = true
		result.Error = "sampler automatic deadline reached"
		return result
	}
	// A filesystem error can repeat the path in its error text.
	if !r.reserve(1024 + 2*cpuJSONEscapedBytes(path)) {
		result.Error = "sampler receipt byte bound exceeded"
		return result
	}
	f, e := os.Open(path)
	if e != nil {
		result.Error = e.Error()
		return result
	}
	defer f.Close()
	// The retained byte charge also bounds transient allocation for the next read.
	limit := int64(64 << 10)
	if remaining := cpuSamplerMaxBytes - r.reserved; remaining < limit {
		limit = remaining
	}
	if limit <= 0 {
		r.exhausted = true
		result.Error = "sampler receipt byte bound exceeded"
		return result
	}
	data, e := io.ReadAll(io.LimitReader(f, limit+1))
	r.bytes += int64(len(data))
	if int64(len(data)) > limit {
		data = data[:limit]
		result.Truncated = true
		result.Error = "file exceeded retained byte bound"
		r.exhausted = true
	}
	if e != nil {
		result.Error = e.Error()
	}
	content := string(data)
	if r.reserve(cpuJSONEscapedBytes(content)) {
		result.Content = content
	} else {
		result.Truncated = true
		result.Error = "escaped file content exceeded retained byte bound"
	}
	return result
}

func cpuThreadKey(t cpuThreadSample) string {
	return strconv.Itoa(t.TID) + ":" + strconv.FormatUint(t.StartTimeTicks, 10)
}
func itoaCPU(n int) string { return strconv.Itoa(n) }
func readCPUThread(r *cpuSamplerReader, base string, tid int, mask string) cpuThreadSample {
	t := cpuThreadSample{TID: tid}
	r.reserve(4096)
	path := filepath.Join(base, "task", strconv.Itoa(tid))
	t.FirstStat = r.read(filepath.Join(path, "stat"))
	t.SchedstatFile = r.read(filepath.Join(path, "schedstat"))
	t.Status = r.read(filepath.Join(path, "status"))
	t.FinalStat = r.read(filepath.Join(path, "stat"))
	for _, f := range []cpuSampleFile{t.FirstStat, t.SchedstatFile, t.Status, t.FinalStat} {
		if f.Error != "" {
			t.Issue = "thread observation unavailable: " + f.Path + ": " + f.Error
			return t
		}
	}
	a, e := parseCPUSampleStat(t.FirstStat.Content)
	if e != nil {
		t.Issue = e.Error()
		return t
	}
	b, e := parseCPUSampleStat(t.FinalStat.Content)
	if e != nil {
		t.Issue = e.Error()
		return t
	}
	t.StartTimeTicks = a.StartTimeTicks
	if a.PID != tid || b.PID != tid || a.StartTimeTicks != b.StartTimeTicks {
		t.Issue = "thread identity changed within read bracket"
		return t
	}
	t.Affinity, e = cpuStatusAffinity(t.Status.Content)
	if e != nil {
		t.Issue = e.Error()
		return t
	}
	if !cpuAffinitiesEqual(t.Affinity, mask) {
		t.Issue = "thread affinity differs from expected mask"
		return t
	}
	s, e := parseCPUSampleSchedstat(t.SchedstatFile.Content)
	if e != nil {
		t.Issue = e.Error()
		return t
	}
	t.Schedstat = &s
	t.Valid = true
	return t
}

func readCPUProcess(r *cpuSamplerReader, p cpuSampleProcess, prior *cpuProcessSample) cpuProcessSample {
	out := cpuProcessSample{Expected: p, Valid: true}
	r.reserve(8192)
	fail := func(msg string) { out.Valid = false; out.Issues = append(out.Issues, msg) }
	base := filepath.Join("/proc", strconv.Itoa(p.PID))
	out.StatFile = r.read(filepath.Join(base, "stat"))
	out.Status = r.read(filepath.Join(base, "status"))
	out.Membership = r.read(filepath.Join(base, "cgroup"))
	if out.Membership.Error != "" {
		fail("process cgroup membership unavailable")
	}
	if prior != nil && prior.Membership.Content != out.Membership.Content {
		fail("process cgroup membership changed")
	}
	if out.StatFile.Error != "" || out.Status.Error != "" {
		fail("process stat or status unavailable")
		return out
	}
	stat, e := parseCPUSampleStat(out.StatFile.Content)
	if e != nil {
		fail(e.Error())
		return out
	}
	out.Stat = &stat
	if stat.PID != p.PID || stat.StartTimeTicks != p.StartTimeTicks {
		fail("process lifetime changed")
		return out
	}
	out.Affinity, e = cpuStatusAffinity(out.Status.Content)
	if e != nil || !cpuAffinitiesEqual(out.Affinity, p.Affinity) {
		fail("process affinity differs from expected mask")
	}
	if prior != nil && prior.Stat != nil && (stat.UserTicks < prior.Stat.UserTicks || stat.SystemTicks < prior.Stat.SystemTicks) {
		fail("process CPU counter regressed")
	}
	f, e := os.Open(filepath.Join(base, "task"))
	if e != nil {
		fail("task directory unavailable")
		return out
	}
	names, e := f.Readdirnames(cpuSamplerMaxThreads + 1)
	_ = f.Close()
	if e != nil && e != io.EOF {
		fail("task directory read failed")
		return out
	}
	out.EnumeratedThreads = len(names)
	if len(names) > cpuSamplerMaxThreads {
		fail("thread count exceeded 256; enumeration is a lower bound")
		names = names[:cpuSamplerMaxThreads]
		r.exhausted = true
	}
	ids := make([]int, 0, len(names))
	for _, name := range names {
		tid, e := strconv.Atoi(name)
		if e != nil || tid <= 0 {
			fail("task directory entry invalid")
			continue
		}
		ids = append(ids, tid)
	}
	sort.Ints(ids)
	previous := map[string]cpuThreadSample{}
	if prior != nil {
		for _, t := range prior.Threads {
			if t.Valid {
				previous[cpuThreadKey(t)] = t
			}
		}
	}
	for _, tid := range ids {
		if r.exhausted {
			fail("thread observation stopped at sampler bound")
			break
		}
		t := readCPUThread(r, base, tid, p.Affinity)
		if t.Valid {
			key := cpuThreadKey(t)
			if a, ok := previous[key]; ok {
				d, e := cpuThreadDifference(a, t)
				delete(previous, key)
				if e != nil {
					t.Valid = false
					t.Issue = e.Error()
				} else {
					t.Delta = &d
					out.MatchedThreads++
				}
			} else {
				out.NewThreads = append(out.NewThreads, key)
			}
		}
		if t.Valid {
			out.ValidThreads++
		} else {
			fail("incomplete thread observation: " + strconv.Itoa(tid) + ": " + t.Issue)
		}
		out.Threads = append(out.Threads, t)
	}
	out.FinalStatFile = r.read(filepath.Join(base, "stat"))
	final, finalErr := parseCPUSampleStat(out.FinalStatFile.Content)
	if out.FinalStatFile.Error != "" || finalErr != nil || final.PID != p.PID || final.StartTimeTicks != p.StartTimeTicks {
		fail("process identity unavailable or changed after thread reads")
	}
	for key := range previous {
		out.ExitedThreads = append(out.ExitedThreads, key)
	}
	if len(previous) > 0 {
		// An absent old TID is an ordinary lifetime coverage gap only when two
		// current inventories confirm its absence. Read races remain invalid.
		directory, err := os.Open(filepath.Join(base, "task"))
		if err != nil {
			fail("final task inventory unavailable")
		} else {
			finalNames, readErr := directory.Readdirnames(cpuSamplerMaxThreads + 1)
			_ = directory.Close()
			before, after := map[int]bool{}, map[int]bool{}
			for _, tid := range ids {
				before[tid] = true
			}
			if (readErr != nil && readErr != io.EOF) || len(finalNames) > cpuSamplerMaxThreads {
				fail("final task inventory incomplete")
			}
			for _, name := range finalNames {
				tid, parseErr := strconv.Atoi(name)
				if parseErr != nil || tid <= 0 {
					fail("final task inventory identity invalid")
				} else {
					after[tid] = true
				}
			}
			var issues []string
			out.ExitedThreads, issues = classifyCPUThreadGaps(previous, before, after)
			for _, issue := range issues {
				fail(issue)
			}
		}
	}
	sort.Strings(out.ExitedThreads)
	return out
}

func cpuThreadClock() (int64, error) {
	var t unix.Timespec
	if e := unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &t); e != nil {
		return 0, e
	}
	return t.Nano(), nil
}

func collectCPUWindowPoint(r *cpuSamplerReader, ps []cpuSampleProcess, groups []string, start time.Time, slot, missed int, previous *cpuWindowPoint) cpuWindowPoint {
	begin := time.Now()
	bytesBefore := r.bytes
	point := cpuWindowPoint{Slot: slot, StartedAt: begin.UTC(), StartOffsetNS: begin.Sub(start).Nanoseconds(), DeadlineLagNS: begin.Sub(start.Add(time.Duration(slot) * time.Second)).Nanoseconds(), MissedBefore: missed, Valid: true, Cgroups: map[string][]cpuSampleFile{}}
	fail := func(msg string) { point.Valid = false; point.Issues = append(point.Issues, msg) }
	r.reserve(16384)
	var err error
	point.SamplerCPUStartNS, err = cpuThreadClock()
	if err != nil {
		fail("sampler thread clock unavailable")
	}
	for i, p := range ps {
		if r.exhausted {
			fail("process sampling truncated at receipt bound")
			break
		}
		var old *cpuProcessSample
		if previous != nil && len(previous.Processes) > i {
			old = &previous.Processes[i]
		}
		current := readCPUProcess(r, p, old)
		if !current.Valid {
			fail("process coverage or identity invalid: " + p.Role)
		}
		point.Processes = append(point.Processes, current)
	}
	for _, path := range []string{"/proc/stat", "/proc/pressure/cpu"} {
		f := r.read(path)
		point.Host = append(point.Host, f)
		if f.Error != "" {
			fail("host sample unavailable: " + path)
		}
	}
	for _, group := range groups {
		if !r.reserve(2048 + cpuJSONEscapedBytes(group)) {
			fail("cgroup sampling truncated at receipt bound")
			break
		}
		for _, name := range []string{"cpu.stat", "cpu.pressure"} {
			f := r.read(filepath.Join(group, name))
			point.Cgroups[group] = append(point.Cgroups[group], f)
			if f.Error != "" {
				fail("cgroup sample unavailable: " + f.Path)
			}
		}
	}
	point.SamplerCPUEndNS, err = cpuThreadClock()
	if err != nil {
		fail("sampler thread clock unavailable")
	}
	end := time.Now()
	point.FinishedAt = end.UTC()
	point.ReadDurationNS = end.Sub(begin).Nanoseconds()
	point.ReadBytes = r.bytes - bytesBefore
	if missed > 0 {
		fail("scheduled sample points were missed")
	}
	if r.exhausted {
		fail("sampler bounded collection stopped")
	}
	return point
}

// startCPUWindowSampler samples only explicitly owned Linux processes. The
// returned idempotent stop function cancels and joins the one collector; it must
// run before after-window metrics. No background work survives its return.
func startCPUWindowSampler(ps []cpuSampleProcess, cgroups []string, start time.Time, duration time.Duration) (func() CPUWindowSamples, error) {
	groups, e := validateCPUSamplerInputs(ps, cgroups, duration)
	if e != nil {
		return nil, e
	}
	now := time.Now()
	if start.IsZero() || start.Before(now.Add(-time.Second)) || start.After(now.Add(5*time.Second)) {
		return nil, fmt.Errorf("sampler start must be near the shared monotonic window start")
	}
	processes := append([]cpuSampleProcess(nil), ps...)
	stopCh, done, ready := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	var result CPUWindowSamples
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		result = CPUWindowSamples{Schema: "wukongim/cpu-window-samples/v1", Valid: true, Start: start.UTC(), DurationNS: duration.Nanoseconds(), SamplerTID: unix.Gettid(), Clock: "Go monotonic offsets; CLOCK_THREAD_CPUTIME_ID nanoseconds; proc stat ticks use the enclosing fixed_environment.clock_tick_hz", Points: make([]cpuWindowPoint, 0, cpuSamplerMaxPoints)}
		cpuStart, err := cpuThreadClock()
		if err != nil {
			result.Valid = false
			result.Issues = append(result.Issues, "sampler thread clock unavailable")
			result.StopReason = "clock_unavailable"
			ready <- err
			return
		}
		reader := cpuSamplerReader{start: start, reserved: 262144}
		result.SchedSchedstats = reader.read("/proc/sys/kernel/sched_schedstats")
		// This optional switch controls extended /sched fields, not the independent
		// CONFIG_SCHED_INFO counters read from each task's schedstat file.
		ready <- nil
		defer func() {
			end := time.Now()
			result.FinishedAt = end.UTC()
			result.FinishedOffsetNS = end.Sub(start).Nanoseconds()
			cpuEnd, e := cpuThreadClock()
			if e != nil {
				result.Valid = false
				result.Issues = append(result.Issues, "final sampler clock unavailable")
			} else {
				result.SamplerCPUNS = cpuEnd - cpuStart
			}
			result.ReadBytes = reader.bytes
			result.ConservativeReceiptBytes = reader.reserved
		}()
		deadline := time.NewTimer(max(time.Until(start.Add(75*time.Second)), 0))
		defer deadline.Stop()
		slot, missed := 0, 0
		for {
			if len(result.Points) >= cpuSamplerMaxPoints {
				result.Valid = false
				result.Issues = append(result.Issues, "70-point bound reached before caller joined")
				result.StopReason = "point_bound"
				return
			}
			timer := time.NewTimer(max(time.Until(start.Add(time.Duration(slot)*time.Second)), 0))
			select {
			case <-stopCh:
				timer.Stop()
				result.StopReason = "caller_stop"
				if len(result.Points) == 0 {
					result.Valid = false
					result.Issues = append(result.Issues, "stopped without a window sample")
				}
				return
			case <-deadline.C:
				timer.Stop()
				result.Valid = false
				result.Issues = append(result.Issues, "75-second automatic stop")
				result.StopReason = "automatic_deadline"
				return
			case <-timer.C:
			}
			var prior *cpuWindowPoint
			if len(result.Points) > 0 {
				prior = &result.Points[len(result.Points)-1]
			}
			point := collectCPUWindowPoint(&reader, processes, groups, start, slot, missed, prior)
			result.Points = append(result.Points, point)
			if !point.Valid {
				result.Valid = false
				result.Issues = append(result.Issues, fmt.Sprintf("point %d incomplete; inspect point/process issues", slot))
			}
			if reader.exhausted {
				result.Valid = false
				result.StopReason = "collection_bound"
				return
			}
			slot, missed = nextCPUSampleSlot(slot, time.Since(start))
			result.MissedSlots += missed
			if missed > 0 {
				result.Valid = false
				result.Issues = append(result.Issues, "sample deadlines missed after prior point")
			}
		}
	}()
	if err := <-ready; err != nil {
		<-done
		return nil, err
	}
	return func() CPUWindowSamples { once.Do(func() { close(stopCh) }); <-done; return result }, nil
}
