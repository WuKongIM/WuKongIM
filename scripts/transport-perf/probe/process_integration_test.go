//go:build integration && (linux || darwin)

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProbeProcessHelper(t *testing.T) {
	if os.Getenv("WK_RPC_PROBE_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(run(os.Args[i+1:], os.Stdout, os.Stderr))
		}
	}
	os.Exit(2)
}

// The real client/server processes must echo bytes, conserve counters, and fail
// an exhausted sample budget without hiding the partial report.
func TestProbeProcessesAndSampleCap(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := func(args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestProbeProcessHelper$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "WK_RPC_PROBE_TEST_HELPER=1", "GOMAXPROCS=4")
		return cmd
	}
	server := command("-mode=server", "-addr="+addr, "-lifetime=15s")
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var serverErr bytes.Buffer
	server.Stderr = &serverErr
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = server.Process.Signal(syscall.SIGTERM)
		if err := server.Wait(); err != nil {
			t.Errorf("server exit: %v %s", err, serverErr.String())
		}
	}()
	ready := make(chan bool, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		ready <- s.Scan() && s.Text() == "RPC_PROBE_READY"
		for s.Scan() {
		}
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("server did not become ready")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, cap := range []string{"100000", "1"} {
		client := command("-addr="+addr, "-workers=2", "-shards=1", "-bytes=64", "-duration=100ms", "-warmup=100ms", "-sample-cap="+cap)
		var out, errOut bytes.Buffer
		client.Stdout = &out
		client.Stderr = &errOut
		err = client.Run()
		var r report
		if decodeErr := json.Unmarshal(out.Bytes(), &r); decodeErr != nil {
			t.Fatalf("decode: %v, stderr=%s", decodeErr, errOut.String())
		}
		if cap == "1" {
			if err == nil || !r.SampleCapHit || !strings.Contains(errOut.String(), "sample cap") {
				t.Fatalf("cap silently accepted: %v %+v", err, r)
			}
			continue
		}
		if err != nil {
			t.Fatalf("client: %v %s", err, errOut.String())
		}
		if runtime.GOOS == "linux" {
			if r.Client.PID <= 0 || r.Client.StartTicks == 0 || r.Client.BootID == "" ||
				r.Timeline.BeforeRPC.LowNS <= 0 || r.Timeline.BeforeRPC.HighNS > r.Timeline.Start.LowNS ||
				r.Timeline.Start.LowNS > r.Timeline.Start.HighNS ||
				r.Timeline.Start.HighNS+int64(r.Options.Duration) > r.Timeline.AfterRPC.LowNS ||
				r.Timeline.AfterRPC.LowNS > r.Timeline.AfterRPC.HighNS ||
				r.ServerBefore.MonotonicNS > r.Timeline.BeforeRPC.HighNS ||
				r.ServerBefore.MonotonicNS < r.Timeline.BeforeRPC.LowNS ||
				r.ServerAfter.MonotonicNS < r.Timeline.AfterRPC.LowNS ||
				r.ServerAfter.MonotonicNS > r.Timeline.AfterRPC.HighNS {
				t.Fatal("invalid Linux identity or same-host boundary clock anchors")
			}
		}
		if r.Errors != 0 || r.Summary.Calls == 0 || r.ServerAfter.EchoCalls-r.ServerBefore.EchoCalls != uint64(r.Summary.Calls) {
			t.Fatal("counter mismatch")
		}
		var calls int
		for _, n := range r.WorkerCalls {
			calls += n
		}
		if calls != r.Summary.Calls {
			t.Fatal("worker count mismatch")
		}
		calls = 0
		for _, q := range r.Seconds {
			calls += q.Calls
		}
		if calls != r.Summary.Calls {
			t.Fatal("second count mismatch")
		}
		if r.ClientBefore.CPUSeconds > r.ClientAfter.CPUSeconds || r.ServerBefore.Stats.CPUSeconds > r.ServerAfter.Stats.CPUSeconds {
			t.Fatal("CPU counter regressed")
		}
	}
}
