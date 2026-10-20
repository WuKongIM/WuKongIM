//go:build integration && linux

package conversation_qps

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestCPUPlacementLinuxAllThreads proves the actual syscall and inheritance path.
// It intentionally avoids product load and leaves the parent test's affinity alone.
func TestCPUPlacementLinuxAllThreads(t *testing.T) {
	var host unix.CPUSet
	if err := unix.SchedGetaffinity(0, &host); err != nil {
		t.Fatal(err)
	}
	if host.Count() != 4 {
		t.Skip("CPU-placement preflight requires exactly four eligible logical CPUs")
	}
	for _, mode := range []string{"S", "O", "D", "nonleader-negative"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var children []*cpuPlacementChild
			for i := 0; i < 4; i++ {
				children = append(children, startCPUPlacementChild(t, ctx))
			}
			driver := children[0].cmd.Process.Pid
			nodes := []int{children[1].cmd.Process.Pid, children[2].cmd.Process.Pid, children[3].cmd.Process.Pid}
			if mode == "nonleader-negative" {
				var narrow unix.CPUSet
				for cpu := 0; cpu < 1024; cpu++ {
					if host.IsSet(cpu) {
						narrow.Set(cpu)
						break
					}
				}
				entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", driver))
				if err != nil {
					t.Fatal(err)
				}
				var nonleader int
				for _, entry := range entries {
					tid, parseErr := strconv.Atoi(entry.Name())
					if parseErr == nil && tid != driver {
						nonleader = tid
						break
					}
				}
				if nonleader == 0 {
					t.Fatal("no non-leader task for negative preflight")
				}
				if err := unix.SchedSetaffinity(nonleader, &narrow); err != nil {
					t.Fatal(err)
				}
				receipt, err := applyCPUPlacement(driver, nodes, "D")
				if err == nil || receipt == nil || receipt.Valid {
					t.Fatalf("wrong non-leader initial mask was accepted: %+v %v", receipt, err)
				}
				return
			}
			receipt, err := applyCPUPlacement(driver, nodes, mode)
			if err != nil || !receipt.Valid {
				t.Fatalf("placement failed: %+v %v", receipt, err)
			}
			if len(receipt.Initial) != 4 || len(receipt.Applied) != 4 {
				t.Fatalf("incomplete process inventory: %+v", receipt)
			}
			for i, child := range children {
				want := receipt.NodeCPUs
				if i == 0 {
					want = receipt.DriverCPUs
				}
				for _, thread := range receipt.Initial[i].Threads {
					if !slices.Equal(thread.Affinity, receipt.HostCPUs) {
						t.Fatal("initial preparation mask was not preserved")
					}
				}
				if len(receipt.Applied[i].Threads) < 3 {
					t.Fatal("preflight did not observe multiple OS threads")
				}
				for _, thread := range receipt.Applied[i].Threads {
					assertCPUPlacementThreadMask(t, thread.TID, want)
				}
				assertCPUPlacementAllTaskMasks(t, child.cmd.Process.Pid, want)
				before := make(map[int]bool)
				for _, thread := range receipt.Applied[i].Threads {
					before[thread.TID] = true
				}
				// Lock more threads than the complete earlier inventory, so at
				// least one must be a newly created kernel task.
				late := child.spawn(t, len(before)+1)
				newThread := false
				for _, tid := range late {
					newThread = newThread || !before[tid]
					assertCPUPlacementThreadMask(t, tid, want)
				}
				if !newThread {
					t.Fatal("preflight did not create a late kernel thread")
				}
				assertCPUPlacementAllTaskMasks(t, child.cmd.Process.Pid, want)
			}
		})
	}
}

type cpuPlacementChild struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *json.Decoder
}

func startCPUPlacementChild(t *testing.T, ctx context.Context) *cpuPlacementChild {
	t.Helper()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCPUPlacementHelperProcess$")
	cmd.Env = append(os.Environ(), "WK_CPU_PLACEMENT_HELPER=1", "GOMAXPROCS=2")
	cmd.Stderr = os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	child := &cpuPlacementChild{cmd: cmd, input: input, output: json.NewDecoder(output)}
	child.spawn(t, 2)
	return child
}

func (c *cpuPlacementChild) spawn(t *testing.T, count int) []int {
	t.Helper()
	if count < 1 || count > 64 {
		t.Fatal("helper thread bound exceeded")
	}
	if _, err := fmt.Fprintf(c.input, "spawn %d\n", count); err != nil {
		t.Fatal(err)
	}
	var tids []int
	if err := c.output.Decode(&tids); err != nil {
		t.Fatal(err)
	}
	if len(tids) != count {
		t.Fatalf("invalid helper thread inventory: %v", tids)
	}
	return tids
}

func assertCPUPlacementThreadMask(t *testing.T, tid int, want []int) {
	t.Helper()
	var actual unix.CPUSet
	if err := unix.SchedGetaffinity(tid, &actual); err != nil {
		t.Fatal(err)
	}
	var got []int
	for cpu := 0; cpu < 1024; cpu++ {
		if actual.IsSet(cpu) {
			got = append(got, cpu)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("TID %d inherited %v, want %v", tid, got, want)
	}
}

func assertCPUPlacementAllTaskMasks(t *testing.T, pid int, want []int) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "task"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 3 {
		t.Fatal("actual thread inventory is not multithreaded")
	}
	for _, entry := range entries {
		tid, err := strconv.Atoi(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		assertCPUPlacementThreadMask(t, tid, want)
	}
}

// The helper creates locked, blocked OS threads on command without CPU load.
func TestCPUPlacementHelperProcess(t *testing.T) {
	if os.Getenv("WK_CPU_PLACEMENT_HELPER") != "1" {
		t.Skip("owned integration-test child only")
	}
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var count int
		if n, err := fmt.Sscanf(scan.Text(), "spawn %d", &count); err != nil || n != 1 || count < 1 || count > 64 {
			os.Exit(2)
		}
		var tids []int
		for i := 0; i < count; i++ {
			ready := make(chan int)
			go func() {
				runtime.LockOSThread()
				ready <- unix.Gettid()
				select {}
			}()
			tids = append(tids, <-ready)
		}
		if err := json.NewEncoder(os.Stdout).Encode(tids); err != nil {
			os.Exit(3)
		}
	}
	os.Exit(0)
}
