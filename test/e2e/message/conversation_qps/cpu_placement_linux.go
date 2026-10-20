//go:build linux

package conversation_qps

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const cpuPlacementMaxTasks = 128

// applyCPUPlacement changes every owned task only after proving the common
// preparation mask. Failed application is terminal; it never claims rollback.
func applyCPUPlacement(driverPID int, nodePIDs []int, mode string) (receipt *CPUPlacementReceipt, err error) {
	start := time.Now()
	receipt = &CPUPlacementReceipt{StartedAt: start.UTC(), CPUPlacementPlan: CPUPlacementPlan{Mode: mode}}
	defer func() {
		finish := time.Now()
		receipt.FinishedAt = finish.UTC()
		receipt.DurationNS = finish.Sub(start).Nanoseconds()
		if err == nil && receipt.DurationNS > int64(500*time.Millisecond) {
			receipt.Valid = false
			err = fmt.Errorf("CPU placement exceeded 500 ms")
		}
		if err != nil {
			receipt.Failure = err.Error()
		}
	}()
	if len(nodePIDs) != 3 {
		return receipt, fmt.Errorf("CPU placement requires three node PIDs")
	}
	pids := append([]int{driverPID}, nodePIDs...)
	seen := map[int]bool{}
	for _, pid := range pids {
		if pid <= 0 || seen[pid] {
			return receipt, fmt.Errorf("invalid or duplicate placement PID")
		}
		seen[pid] = true
	}
	host, err := cpuPlacementAffinity(driverPID)
	if err != nil {
		return receipt, err
	}
	receipt.HostCPUs = slices.Clone(host)
	rows, err := readCPUPlacementTopology(host)
	receipt.Topology = rows
	if err != nil {
		return receipt, err
	}
	receipt.CPUPlacementPlan, err = planCPUPlacement(mode, rows)
	if err != nil {
		return receipt, err
	}
	deadline := start.Add(500 * time.Millisecond)
	initialReady := false
	// Both initial and applied inventories must fit one bounded attempt budget.
	for receipt.Attempts < 8 && time.Now().Before(deadline) {
		receipt.Attempts++
		if !initialReady {
			receipt.Initial = nil
			initialReady = true
			for _, pid := range pids {
				process, readErr := observeCPUPlacementProcess(pid, deadline)
				receipt.Initial = append(receipt.Initial, process)
				if readErr != nil {
					if errors.Is(readErr, errCPUPlacementTaskChanged) {
						initialReady = false
						break
					}
					return receipt, readErr
				}
				for _, thread := range process.Threads {
					if !slices.Equal(thread.Affinity, host) {
						return receipt, fmt.Errorf("PID %d TID %d did not retain the initial four-CPU mask", pid, thread.TID)
					}
				}
			}
			if !initialReady {
				// A racing initial inventory cannot become preparation evidence.
				continue
			}
		}
		receipt.Applied = nil
		retry := false
		for index, pid := range pids {
			current, readErr := observeCPUPlacementProcess(pid, deadline)
			receipt.Applied = append(receipt.Applied, current)
			if readErr != nil {
				if errors.Is(readErr, errCPUPlacementTaskChanged) {
					retry = true
					break
				}
				return receipt, readErr
			}
			if current.StartTimeTicks != receipt.Initial[index].StartTimeTicks {
				return receipt, fmt.Errorf("placement process identity changed")
			}
			mask := receipt.NodeCPUs
			if index == 0 {
				mask = receipt.DriverCPUs
			}
			var set unix.CPUSet
			for _, cpu := range mask {
				set.Set(cpu)
			}
			for _, thread := range current.Threads {
				if !time.Now().Before(deadline) {
					return receipt, fmt.Errorf("CPU placement exceeded 500 ms")
				}
				// Do not mutate a task that disappeared or was replaced since
				// its inventory. The proc path also binds it to this process.
				raw, statErr := readCPUPlacementFile(fmt.Sprintf("/proc/%d/task/%d/stat", pid, thread.TID))
				if statErr != nil {
					if errors.Is(statErr, os.ErrNotExist) {
						retry = true
						break
					}
					return receipt, statErr
				}
				tid, ticks, statErr := parseCPUPlacementTaskStat(raw)
				if statErr != nil {
					return receipt, statErr
				}
				if tid != thread.TID || ticks != thread.StartTimeTicks {
					retry = true
					break
				}
				if setErr := unix.SchedSetaffinity(thread.TID, &set); setErr != nil {
					if errors.Is(setErr, unix.ESRCH) {
						retry = true
						break
					}
					return receipt, fmt.Errorf("set PID %d TID %d affinity: %w", pid, thread.TID, setErr)
				}
			}
			if retry {
				break
			}
		}
		if retry {
			continue
		}
		// Two full inventories detect tasks created while older tasks were moved.
		// Once all creators have the mask, later threads inherit that same mask.
		var previous []CPUPlacementProcess
		for verification := 0; verification < 2; verification++ {
			receipt.Applied = nil
			for index, pid := range pids {
				current, readErr := observeCPUPlacementProcess(pid, deadline)
				receipt.Applied = append(receipt.Applied, current)
				if readErr != nil {
					if errors.Is(readErr, errCPUPlacementTaskChanged) {
						retry = true
						break
					}
					return receipt, readErr
				}
				if current.StartTimeTicks != receipt.Initial[index].StartTimeTicks {
					return receipt, fmt.Errorf("placement process identity changed")
				}
				want := receipt.NodeCPUs
				if index == 0 {
					want = receipt.DriverCPUs
				}
				for _, thread := range current.Threads {
					if !slices.Equal(thread.Affinity, want) {
						retry = true
					}
				}
				if verification == 1 && !sameCPUPlacementTasks(previous[index], current) {
					retry = true
				}
			}
			if retry {
				break
			}
			previous = receipt.Applied
		}
		if !retry && time.Now().Before(deadline) {
			receipt.Valid = true
			return receipt, nil
		}
	}
	return receipt, fmt.Errorf("CPU placement did not converge within eight rounds and 500 ms")
}

var errCPUPlacementTaskChanged = errors.New("task inventory changed during observation")

func cpuPlacementAffinity(tid int) ([]int, error) {
	var mask unix.CPUSet
	if err := unix.SchedGetaffinity(tid, &mask); err != nil {
		return nil, fmt.Errorf("read TID %d affinity: %w", tid, err)
	}
	var cpus []int
	for cpu := 0; cpu < 1024; cpu++ {
		if mask.IsSet(cpu) {
			cpus = append(cpus, cpu)
		}
	}
	if len(cpus) == 0 {
		return nil, fmt.Errorf("empty task affinity")
	}
	return cpus, nil
}

func readCPUPlacementFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(data) > 16384 {
		return "", fmt.Errorf("read %s failed or exceeded 16 KiB: %v", path, err)
	}
	return string(data), nil
}

func readCPUPlacementTopology(cpus []int) ([]CPUPlacementCPU, error) {
	if len(cpus) != 4 {
		return nil, fmt.Errorf("initial driver affinity is not four logical CPUs")
	}
	onlineRaw, err := readCPUPlacementFile("/sys/devices/system/cpu/online")
	if err != nil {
		return nil, err
	}
	online, err := parseCPUPlacementList(onlineRaw)
	if err != nil {
		return nil, err
	}
	var rows []CPUPlacementCPU
	for _, cpu := range cpus {
		if !slices.Contains(online, cpu) {
			return nil, fmt.Errorf("assigned CPU is offline")
		}
		base := fmt.Sprintf("/sys/devices/system/cpu/cpu%d", cpu)
		row := CPUPlacementCPU{CPU: cpu, Raw: map[string]string{"online": onlineRaw}}
		for _, name := range []string{"physical_package_id", "core_id", "thread_siblings_list"} {
			raw, readErr := readCPUPlacementFile(filepath.Join(base, "topology", name))
			if readErr != nil {
				return rows, readErr
			}
			row.Raw[name] = raw
		}
		row.PackageID, err = strconv.Atoi(strings.TrimSpace(row.Raw["physical_package_id"]))
		if err != nil {
			return rows, fmt.Errorf("invalid package ID: %w", err)
		}
		row.CoreID, err = strconv.Atoi(strings.TrimSpace(row.Raw["core_id"]))
		if err != nil {
			return rows, fmt.Errorf("invalid core ID: %w", err)
		}
		row.ThreadSiblings, err = parseCPUPlacementList(row.Raw["thread_siblings_list"])
		if err != nil {
			return rows, err
		}
		nodes, globErr := filepath.Glob(filepath.Join(base, "node[0-9]*"))
		if globErr != nil || len(nodes) != 1 {
			return rows, fmt.Errorf("CPU NUMA membership is absent or ambiguous")
		}
		row.Raw["numa_node_entry"] = filepath.Base(nodes[0])
		row.NUMANode, err = strconv.Atoi(strings.TrimPrefix(filepath.Base(nodes[0]), "node"))
		if err != nil {
			return rows, fmt.Errorf("invalid NUMA node: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// observeCPUPlacementProcess refuses a partial task inventory. Task disappearance
// is retryable only when the second directory inventory proves a changed set.
func observeCPUPlacementProcess(pid int, deadline time.Time) (CPUPlacementProcess, error) {
	result := CPUPlacementProcess{PID: pid}
	base := fmt.Sprintf("/proc/%d/task", pid)
	before, err := cpuPlacementTaskIDs(base)
	if err != nil {
		return result, err
	}
	var transient error
	for _, tid := range before {
		if !time.Now().Before(deadline) {
			return result, fmt.Errorf("CPU placement exceeded 500 ms")
		}
		stat, readErr := readCPUPlacementFile(filepath.Join(base, strconv.Itoa(tid), "stat"))
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				transient = readErr
				break
			}
			return result, readErr
		}
		got, start, parseErr := parseCPUPlacementTaskStat(stat)
		if parseErr != nil || got != tid {
			return result, fmt.Errorf("invalid observed task identity for PID %d TID %d", pid, tid)
		}
		mask, maskErr := cpuPlacementAffinity(tid)
		if maskErr != nil {
			if errors.Is(maskErr, unix.ESRCH) {
				transient = maskErr
				break
			}
			return result, maskErr
		}
		result.Threads = append(result.Threads, CPUPlacementThread{TID: tid, StartTimeTicks: start, Affinity: mask, Stat: stat})
		if tid == pid {
			result.StartTimeTicks = start
		}
	}
	after, err := cpuPlacementTaskIDs(base)
	if err != nil {
		return result, err
	}
	if !slices.Equal(before, after) {
		return result, errCPUPlacementTaskChanged
	}
	if transient != nil {
		return result, fmt.Errorf("still-present task unavailable: %w", transient)
	}
	if result.StartTimeTicks == 0 {
		return result, fmt.Errorf("process leader missing from task inventory")
	}
	return result, nil
}

func cpuPlacementTaskIDs(path string) ([]int, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(cpuPlacementMaxTasks + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) == 0 || len(entries) > cpuPlacementMaxTasks {
		return nil, fmt.Errorf("task inventory outside 1..128 bound")
	}
	var result []int
	for _, entry := range entries {
		tid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || tid <= 0 || !entry.IsDir() {
			return nil, fmt.Errorf("invalid task directory entry")
		}
		result = append(result, tid)
	}
	slices.Sort(result)
	return result, nil
}

func sameCPUPlacementTasks(a, b CPUPlacementProcess) bool {
	if a.PID != b.PID || a.StartTimeTicks != b.StartTimeTicks || len(a.Threads) != len(b.Threads) {
		return false
	}
	for i := range a.Threads {
		if a.Threads[i].TID != b.Threads[i].TID || a.Threads[i].StartTimeTicks != b.Threads[i].StartTimeTicks {
			return false
		}
	}
	return true
}
