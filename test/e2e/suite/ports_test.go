//go:build e2e

package suite

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const (
	portLeaseHelperOutputEnv  = "WK_E2E_PORT_LEASE_HELPER_OUTPUT"
	portLeaseHelperCountEnv   = "WK_E2E_PORT_LEASE_HELPER_COUNT"
	portLeaseHelperReadyEnv   = "WK_E2E_PORT_LEASE_HELPER_READY"
	portLeaseHelperReleaseEnv = "WK_E2E_PORT_LEASE_HELPER_RELEASE"
)

func TestReserveLoopbackPortsReturnsDistinctNonEphemeralPorts(t *testing.T) {
	seen := make(map[string]struct{})
	for range 16 {
		ports := ReserveLoopbackPorts(t)
		for _, addr := range []string{ports.ClusterAddr, ports.GatewayAddr, ports.APIAddr, ports.ManagerAddr} {
			if _, ok := seen[addr]; ok {
				t.Fatalf("ReserveLoopbackPorts() reused %s", addr)
			}
			seen[addr] = struct{}{}
			_, rawPort, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("SplitHostPort(%q): %v", addr, err)
			}
			port, err := strconv.Atoi(rawPort)
			if err != nil {
				t.Fatalf("Atoi(%q): %v", rawPort, err)
			}
			if port < loopbackPortStart || port >= loopbackPortStart+loopbackPortCount {
				t.Fatalf("reserved port %d outside [%d,%d)", port, loopbackPortStart, loopbackPortStart+loopbackPortCount)
			}
		}
	}
}

func TestReserveLoopbackPortsReturnsDisjointAddressesAcrossProcesses(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}

	const (
		processCount       = 4
		portSetsPerProcess = 32
	)
	type helperProcess struct {
		command     *exec.Cmd
		outputPath  string
		readyPath   string
		releasePath string
		output      bytes.Buffer
	}
	helpers := make([]helperProcess, processCount)
	for index := range helpers {
		helperRoot := t.TempDir()
		outputPath := filepath.Join(helperRoot, "ports.json")
		readyPath := filepath.Join(helperRoot, "ready")
		releasePath := filepath.Join(helperRoot, "release")
		command := exec.Command(executable, "-test.run=^TestReserveLoopbackPortsHelperProcess$")
		command.Env = append(os.Environ(),
			portLeaseHelperOutputEnv+"="+outputPath,
			portLeaseHelperCountEnv+"="+strconv.Itoa(portSetsPerProcess),
			portLeaseHelperReadyEnv+"="+readyPath,
			portLeaseHelperReleaseEnv+"="+releasePath,
		)
		helpers[index] = helperProcess{
			command: command, outputPath: outputPath,
			readyPath: readyPath, releasePath: releasePath,
		}
		command.Stdout = &helpers[index].output
		command.Stderr = &helpers[index].output
		if err := command.Start(); err != nil {
			t.Fatalf("start helper %d: %v", index, err)
		}
		t.Cleanup(func() {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		})
	}
	for index := range helpers {
		waitForPortLeaseHelperFile(t, helpers[index].readyPath, 10*time.Second, &helpers[index].output)
	}
	for index := range helpers {
		if err := os.WriteFile(helpers[index].releasePath, []byte("release"), 0o600); err != nil {
			t.Fatalf("release helper %d: %v", index, err)
		}
	}

	seen := make(map[string]int, processCount*portSetsPerProcess*4)
	for index := range helpers {
		if err := helpers[index].command.Wait(); err != nil {
			t.Fatalf("wait helper %d: %v\n%s", index, err, helpers[index].output.String())
		}
		body, err := os.ReadFile(helpers[index].outputPath)
		if err != nil {
			t.Fatalf("read helper %d output: %v\n%s", index, err, helpers[index].output.String())
		}
		var sets []PortSet
		if err := json.Unmarshal(body, &sets); err != nil {
			t.Fatalf("decode helper %d output: %v body=%s", index, err, body)
		}
		if len(sets) != portSetsPerProcess {
			t.Fatalf("helper %d returned %d port sets, want %d", index, len(sets), portSetsPerProcess)
		}
		for _, ports := range sets {
			for _, addr := range []string{ports.ClusterAddr, ports.GatewayAddr, ports.APIAddr, ports.ManagerAddr} {
				if previous, ok := seen[addr]; ok {
					t.Fatalf("loopback address %s returned by helper processes %d and %d", addr, previous, index)
				}
				seen[addr] = index
			}
		}
	}
}

func TestReserveLoopbackPortsHelperProcess(t *testing.T) {
	outputPath := os.Getenv(portLeaseHelperOutputEnv)
	if outputPath == "" {
		t.Skip("helper process only")
	}
	readyPath := os.Getenv(portLeaseHelperReadyEnv)
	releasePath := os.Getenv(portLeaseHelperReleaseEnv)
	if readyPath == "" || releasePath == "" {
		t.Fatal("helper process barrier paths are required")
	}
	count, err := strconv.Atoi(os.Getenv(portLeaseHelperCountEnv))
	if err != nil || count <= 0 {
		t.Fatalf("invalid helper count: %q", os.Getenv(portLeaseHelperCountEnv))
	}
	sets := make([]PortSet, 0, count)
	for range count {
		sets = append(sets, ReserveLoopbackPorts(t))
	}
	if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
		t.Fatalf("write helper ready marker: %v", err)
	}
	waitForPortLeaseHelperFile(t, releasePath, 10*time.Second, nil)
	body, err := json.Marshal(sets)
	if err != nil {
		t.Fatalf("encode helper output: %v", err)
	}
	if err := os.WriteFile(outputPath, body, 0o600); err != nil {
		t.Fatalf("write helper output: %v", err)
	}
}

func waitForPortLeaseHelperFile(t *testing.T, path string, timeout time.Duration, output *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat helper barrier %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if output != nil {
		t.Fatalf("helper barrier %s not reached within %s\n%s", path, timeout, output.String())
	}
	t.Fatalf("helper barrier %s not reached within %s", path, timeout)
}
