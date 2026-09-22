//go:build linux || darwin

// Command probe measures bounded, separate-process transport echo workloads.
// It is a diagnostic tool; it neither provisions hosts nor qualifies production.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// sourceRevision is supplied by the build command; executable SHA is authoritative.
var sourceRevision = "unspecified"

type options struct {
	Mode, Addr                                string
	Duration, Warmup, Lifetime, TelemetryTail time.Duration
	Workers, Shards, Bytes, Samples, GC       int
	Budgets, Read                             bool
}

func parse(args []string, out io.Writer) (options, error) {
	o := options{}
	f := flag.NewFlagSet("rpc-probe", flag.ContinueOnError)
	f.SetOutput(out)
	f.StringVar(&o.Mode, "mode", "client", "client or server")
	f.StringVar(&o.Addr, "addr", "127.0.0.1:7001", "explicit peer/listen address")
	f.DurationVar(&o.Duration, "duration", 20*time.Second, "measured duration, 100ms to 60s")
	f.DurationVar(&o.Warmup, "warmup", 10*time.Second, "concurrent warmup, 0 to 30s")
	f.DurationVar(&o.Lifetime, "lifetime", 30*time.Minute, "server hard lifetime, 1s to 2h")
	f.DurationVar(&o.TelemetryTail, "telemetry-tail", 0, "diagnostic-only post-report lifetime, 0 to 5s, for final host sample")
	f.IntVar(&o.Workers, "workers", 16, "concurrent callers, 1 to 16")
	f.IntVar(&o.Shards, "shards", 1, "active connection slots, 1 to 16")
	f.IntVar(&o.Bytes, "bytes", 64, "echo bytes, 1 to 65537")
	f.IntVar(&o.Samples, "sample-cap", 1000000, "preallocated samples per worker, 1 to 1000000")
	f.IntVar(&o.GC, "gc", 400, "client GOGC, 25 to 1000; server always uses 100")
	f.BoolVar(&o.Budgets, "budgets", false, "negotiate request budgets")
	f.BoolVar(&o.Read, "read", false, "server propagates cancellation into running handlers")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if (o.Mode == "server" && o.TelemetryTail != 0) || o.TelemetryTail < 0 || o.TelemetryTail > 5*time.Second || f.NArg() != 0 || (o.Mode != "client" && o.Mode != "server") || o.Duration < 100*time.Millisecond || o.Duration > 60*time.Second || o.Warmup < 0 || o.Warmup > 30*time.Second || o.Lifetime < time.Second || o.Lifetime > 2*time.Hour || o.Workers < 1 || o.Workers > 16 || o.Shards < 1 || o.Shards > 16 || o.Bytes < 1 || o.Bytes > 65537 || o.Samples < 1 || o.Samples > 1000000 || o.GC < 25 || o.GC > 1000 {
		return o, errors.New("invalid bounded probe options")
	}
	return o, nil
}

type host struct {
	OS, Arch, KernelArch, CPUModel, HostID, BinarySHA256, Source, GoVersion, AllowedCPUs string
	CPUCount, Procs                                                                      int
	Container                                                                            bool
	Ineligible                                                                           []string
	// BootID, PID and StartTicks prevent joining another boot or a reused PID.
	BootID     string
	PID        int
	StartTicks uint64
	// MemoryLimit and RuntimeDebug make hidden runtime overrides visible to the gate.
	MemoryLimit  int64
	RuntimeDebug string
}

// identify records process identity without exposing machine IDs or credentials.
func identify() (host, error) {
	h := host{OS: runtime.GOOS, Arch: runtime.GOARCH, Source: sourceRevision, GoVersion: runtime.Version(), CPUCount: runtime.NumCPU(), Procs: runtime.GOMAXPROCS(0)}
	h.PID = os.Getpid()
	if err := processIdentity(&h); err != nil {
		return h, err
	}
	h.MemoryLimit = debug.SetMemoryLimit(-1)
	h.RuntimeDebug = os.Getenv("GODEBUG")
	name, err := os.Executable()
	if err != nil {
		return h, err
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return h, err
	}
	digest := sha256.Sum256(data)
	h.BinarySHA256 = hex.EncodeToString(digest[:])
	identity, _ := os.ReadFile("/etc/machine-id")
	if len(identity) == 0 {
		n, _ := os.Hostname()
		identity = []byte(n)
	}
	id := sha256.Sum256(identity)
	h.HostID = hex.EncodeToString(id[:])
	u, _ := exec.Command("uname", "-m").Output()
	h.KernelArch = strings.TrimSpace(string(u))
	cpu, _ := os.ReadFile("/proc/cpuinfo")
	for _, line := range strings.Split(string(cpu), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "model name" {
			h.CPUModel = strings.TrimSpace(v)
			break
		}
	}
	status, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "Cpus_allowed_list:") {
			h.AllowedCPUs = strings.TrimSpace(strings.TrimPrefix(line, "Cpus_allowed_list:"))
		}
	}
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			h.Container = true
		}
	}
	cgroup, _ := os.ReadFile("/proc/1/cgroup")
	for _, marker := range []string{"docker", "kubepods", "containerd", "libpod"} {
		if strings.Contains(string(cgroup), marker) {
			h.Container = true
		}
	}
	if h.OS != "linux" {
		h.Ineligible = append(h.Ineligible, "requires Linux")
	}
	if h.Arch != "amd64" || h.KernelArch != "x86_64" {
		h.Ineligible = append(h.Ineligible, "requires native amd64 process and x86_64 kernel")
	}
	if h.Container {
		h.Ineligible = append(h.Ineligible, "container is functional-smoke only")
	}
	if h.CPUModel == "" {
		h.Ineligible = append(h.Ineligible, "missing CPU identity")
	}
	if h.Procs != 4 || h.CPUCount < 4 {
		h.Ineligible = append(h.Ineligible, "requires GOMAXPROCS=4 and at least four CPUs")
	}
	return h, nil
}

type processStats struct {
	TotalAlloc, Mallocs, PauseNS uint64
	NumGC                        uint32
	CPUSeconds                   float64
}

// snapshot is sampled outside timing; server deltas include the two control RPCs.
func snapshot() (processStats, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	var u syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &u); err != nil {
		return processStats{}, err
	}
	return processStats{m.TotalAlloc, m.Mallocs, m.PauseTotalNs, m.NumGC, float64(u.Utime.Sec+u.Stime.Sec) + float64(u.Utime.Usec+u.Stime.Usec)/1e6}, nil
}

type serverState struct {
	Host        host
	Instance    string
	Options     transport.ServiceOptions
	GC          int
	EchoCalls   uint64
	Stats       processStats
	MonotonicNS int64
}

func serve(o options, out io.Writer) error {
	debug.SetGCPercent(100)
	h, err := identify()
	if err != nil {
		return err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	opts := transport.ServiceOptions{Concurrency: 64, QueueSize: 4096, MaxQueueBytes: 64 << 20, MaxRetainedBytes: 128 << 20, MaxPayload: 65537, Timeout: 30 * time.Second, QueueTimeout: 5 * time.Second, CancelRunning: o.Read}
	s, err := transport.NewServer(transport.ServerConfig{NodeID: 2})
	if err != nil {
		return err
	}
	defer s.Stop()
	var calls atomic.Uint64
	if err = s.Handle(1, func(_ context.Context, p []byte) ([]byte, error) { calls.Add(1); return p, nil }, opts); err != nil {
		return err
	}
	if err = s.Handle(2, func(_ context.Context, _ []byte) ([]byte, error) {
		stats, err := snapshot()
		if err != nil {
			return nil, err
		}
		return json.Marshal(serverState{Host: h, Instance: hex.EncodeToString(nonce), Options: opts, GC: 100, EchoCalls: calls.Load(), Stats: stats, MonotonicNS: monotonicNS()})
	}, transport.ServiceOptions{Concurrency: 1, QueueSize: 4, MaxQueueBytes: 4096, MaxPayload: 1024, Timeout: 2 * time.Second, CancelRunning: true}); err != nil {
		return err
	}
	if err = s.ListenAndServe(o.Addr); err != nil {
		return err
	}
	if _, err = fmt.Fprintln(out, "RPC_PROBE_READY"); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timer := time.NewTimer(o.Lifetime)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return nil
}

type discovery string

func (d discovery) Resolve(transport.NodeID) (string, error) { return string(d), nil }

type quantiles struct {
	Calls                       int
	MeanMS, P50MS, P95MS, P99MS float64
}

func summarize(samples []int64) quantiles {
	q := quantiles{Calls: len(samples)}
	if len(samples) == 0 {
		return q
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	var total int64
	for _, v := range samples {
		total += v
	}
	q.MeanMS = float64(total) / float64(len(samples)) / 1e6
	percentile := func(p float64) float64 { return float64(samples[int(float64(len(samples)-1)*p)]) / 1e6 }
	q.P50MS = percentile(.50)
	q.P95MS = percentile(.95)
	q.P99MS = percentile(.99)
	return q
}

type worker struct {
	Samples             []int64
	Ends                []int
	WarmupCalls, Errors int
	Capped              bool
	Error               string
}

// clockSpan bounds an event in the Linux CLOCK_MONOTONIC domain.
type clockSpan struct{ LowNS, HighNS int64 }

// timeline uses only existing boundary RPCs, never per-request telemetry.
type timeline struct{ Start, BeforeRPC, AfterRPC clockSpan }

type report struct {
	Timeline                       timeline
	Schema, StartedUTC             string
	Client                         host
	Options                        options
	ElapsedSeconds, CallsPerSecond float64
	Summary                        quantiles
	Seconds                        []quantiles
	WorkerCalls, WarmupCalls       []int
	Errors                         int
	SampleCapHit                   bool
	FirstError                     string
	ClientBefore, ClientAfter      processStats
	ServerBefore, ServerAfter      serverState
}

func load(o options, out io.Writer) error {
	debug.SetGCPercent(o.GC)
	h, err := identify()
	if err != nil {
		return err
	}
	c, err := transport.NewClient(transport.ClientConfig{NodeID: 1, Discovery: discovery(o.Addr), PoolSize: 16, RequestBudgets: o.Budgets})
	if err != nil {
		return err
	}
	defer c.Stop()
	payload := bytes.Repeat([]byte{0x5a}, o.Bytes)
	call := func(shard int) error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		p, err := c.Call(ctx, 2, uint64(shard), transport.PriorityRPC, 1, payload)
		if err != nil {
			return err
		}
		if !bytes.Equal(p, payload) {
			return errors.New("echo payload mismatch")
		}
		return nil
	}
	state := func() (serverState, error) {
		var result serverState
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		p, err := c.Call(ctx, 2, 0, transport.PriorityRPC, 2, nil)
		if err != nil {
			return result, err
		}
		err = json.Unmarshal(p, &result)
		return result, err
	}
	for i := 0; i < o.Shards; i++ {
		if err = call(i); err != nil {
			return err
		}
	}
	seconds := int((o.Duration + time.Second - 1) / time.Second)
	workers := make([]worker, o.Workers)
	for i := range workers {
		workers[i].Samples = make([]int64, 0, o.Samples)
		workers[i].Ends = make([]int, seconds)
	}
	var wg sync.WaitGroup
	// Concurrent warmup exercises the same connection and handler distribution.
	warmEnd := time.Now().Add(o.Warmup)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := &workers[i]
			for time.Now().Before(warmEnd) {
				if err := call(i % o.Shards); err != nil {
					w.Errors++
					w.Error = err.Error()
					return
				}
				w.WarmupCalls++
			}
		}(i)
	}
	wg.Wait()
	for _, w := range workers {
		if w.Errors != 0 {
			return fmt.Errorf("warmup failed: %s", w.Error)
		}
	}
	runtime.GC()
	r := report{Schema: "wkrpc-process-probe/v1", Client: h, Options: o, WorkerCalls: make([]int, o.Workers), WarmupCalls: make([]int, o.Workers)}
	r.Timeline.BeforeRPC.LowNS = monotonicNS()
	if r.ServerBefore, err = state(); err != nil {
		return err
	}
	r.Timeline.BeforeRPC.HighNS = monotonicNS()
	if r.ClientBefore, err = snapshot(); err != nil {
		return err
	}
	ready := make(chan struct{})
	var started time.Time
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-ready
			w := &workers[i]
			end := started.Add(o.Duration)
			sec := 0
			for time.Now().Before(end) {
				t := time.Now()
				bucket := int(t.Sub(started) / time.Second)
				if bucket >= seconds {
					bucket = seconds - 1
				}
				for sec < bucket {
					w.Ends[sec] = len(w.Samples)
					sec++
				}
				if err := call(i % o.Shards); err != nil {
					w.Errors++
					w.Error = err.Error()
					break
				}
				w.Samples = append(w.Samples, time.Since(t).Nanoseconds())
				if len(w.Samples) >= o.Samples {
					w.Capped = true
					break
				}
			}
			for sec < seconds {
				w.Ends[sec] = len(w.Samples)
				sec++
			}
		}(i)
	}
	started, r.Timeline.Start = measurementStart()
	r.StartedUTC = started.UTC().Format(time.RFC3339Nano)
	close(ready)
	wg.Wait()
	r.ElapsedSeconds = time.Since(started).Seconds()
	if r.ClientAfter, err = snapshot(); err != nil {
		return err
	}
	r.Timeline.AfterRPC.LowNS = monotonicNS()
	if r.ServerAfter, err = state(); err != nil {
		return err
	}
	r.Timeline.AfterRPC.HighNS = monotonicNS()
	// All copying and sorting occurs after both measurement boundary snapshots.
	for sec := 0; sec < seconds; sec++ {
		var values []int64
		for _, w := range workers {
			from := 0
			if sec > 0 {
				from = w.Ends[sec-1]
			}
			values = append(values, w.Samples[from:w.Ends[sec]]...)
		}
		r.Seconds = append(r.Seconds, summarize(values))
	}
	var all []int64
	for i, w := range workers {
		all = append(all, w.Samples...)
		r.WorkerCalls[i] = len(w.Samples)
		r.WarmupCalls[i] = w.WarmupCalls
		r.Errors += w.Errors
		r.SampleCapHit = r.SampleCapHit || w.Capped
		if r.FirstError == "" {
			r.FirstError = w.Error
		}
	}
	r.Summary = summarize(all)
	r.CallsPerSecond = float64(r.Summary.Calls) / r.ElapsedSeconds
	if err = json.NewEncoder(out).Encode(r); err != nil {
		return err
	}
	if r.Errors != 0 || r.SampleCapHit || r.Summary.Calls == 0 || r.ElapsedSeconds < o.Duration.Seconds() {
		return errors.New("measurement incomplete, failed, or sample cap reached")
	}
	if r.ServerBefore.Instance != r.ServerAfter.Instance || r.ServerAfter.EchoCalls < r.ServerBefore.EchoCalls || r.ServerAfter.EchoCalls-r.ServerBefore.EchoCalls != uint64(r.Summary.Calls) {
		return errors.New("server identity or exact echo-count boundary mismatch")
	}
	return nil
}

func run(args []string, out, errOut io.Writer) int {
	o, err := parse(args, errOut)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err == nil {
		if o.Mode == "server" {
			err = serve(o, out)
		} else {
			err = load(o, out)
			if err == nil && o.TelemetryTail > 0 {
				time.Sleep(o.TelemetryTail)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
