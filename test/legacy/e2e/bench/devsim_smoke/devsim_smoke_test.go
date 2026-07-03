//go:build e2e && legacy_e2e

package devsim_smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/legacy/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestDevSimRunsTrafficAgainstThreeNodeCluster(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(devSimClusterOptions()...)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	statusAddr := suite.ReserveLoopbackPorts(t).ManagerAddr
	proc := startDevSimProcess(t, writeDevSimConfig(t, statusAddr, cluster))
	t.Cleanup(func() { proc.stop(t) })

	status := waitForDevSimStatus(ctx, t, statusAddr, proc, func(status devSimStatus) bool {
		return status.State == "running" && status.ConnectedUsers == 8 && status.MessagesSent > 0
	})
	require.Equal(t, 2, status.PersonChannels)
	require.Equal(t, 1, status.GroupChannels)
}

func devSimClusterOptions() []suite.Option {
	overrides := map[string]string{
		"WK_BENCH_API_ENABLE": "true",
	}
	return []suite.Option{
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	}
}

type devSimStatus struct {
	State          string `json:"state"`
	RunID          string `json:"run_id"`
	ConnectedUsers int    `json:"connected_users"`
	PersonChannels int    `json:"person_channels"`
	GroupChannels  int    `json:"group_channels"`
	MessagesSent   uint64 `json:"messages_sent"`
	SendErrors     uint64 `json:"send_errors"`
	RecvErrors     uint64 `json:"recv_errors"`
	LastError      string `json:"last_error"`
}

type devSimProcess struct {
	cmd        *exec.Cmd
	outputPath string
	done       chan struct{}
	mu         sync.Mutex
	err        error
	stopOnce   sync.Once
}

func startDevSimProcess(t *testing.T, configPath string) *devSimProcess {
	t.Helper()
	binary := buildWkbenchBinary(t)
	outputPath := filepath.Join(t.TempDir(), "wkbench-dev-sim.log")
	output, err := os.Create(outputPath)
	require.NoError(t, err)

	cmd := exec.Command(binary, "dev-sim", "--config", configPath)
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "GOWORK=off")
	cmd.Stdout = output
	cmd.Stderr = output
	require.NoError(t, cmd.Start())

	proc := &devSimProcess{cmd: cmd, outputPath: outputPath, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		_ = output.Close()
		proc.mu.Lock()
		proc.err = err
		proc.mu.Unlock()
		close(proc.done)
	}()
	return proc
}

func (p *devSimProcess) stop(t *testing.T) {
	t.Helper()
	p.stopOnce.Do(func() {
		select {
		case <-p.done:
			err := p.waitErr()
			require.NoError(t, normalizeDevSimExit(err), p.diagnostics())
			return
		default:
		}
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-p.done:
			err := p.waitErr()
			require.NoError(t, normalizeDevSimExit(err), p.diagnostics())
		case <-time.After(10 * time.Second):
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
			require.FailNow(t, "wkbench dev-sim did not stop after SIGTERM", p.diagnostics())
		}
	})
}

func (p *devSimProcess) waitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *devSimProcess) diagnostics() string {
	data, err := os.ReadFile(p.outputPath)
	if err != nil {
		return fmt.Sprintf("read %s: %v", p.outputPath, err)
	}
	return fmt.Sprintf("wkbench dev-sim log %s:\n%s", p.outputPath, string(data))
}

func normalizeDevSimExit(err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 0 {
		return nil
	}
	return err
}

func waitForDevSimStatus(ctx context.Context, t *testing.T, addr string, proc *devSimProcess, accept func(devSimStatus) bool) devSimStatus {
	t.Helper()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var last devSimStatus
	var lastErr error
	for {
		status, err := fetchDevSimStatus(ctx, addr)
		if err == nil {
			last = status
			if accept(status) {
				return status
			}
		} else {
			lastErr = err
		}

		select {
		case <-proc.done:
			err := proc.waitErr()
			require.NoError(t, normalizeDevSimExit(err), proc.diagnostics())
			require.FailNowf(t, "wkbench dev-sim exited before expected status", "last_status=%+v last_error=%v\n%s", last, lastErr, proc.diagnostics())
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), "last_status=%+v last_error=%v\n%s", last, lastErr, proc.diagnostics())
		case <-ticker.C:
		}
	}
}

func fetchDevSimStatus(ctx context.Context, addr string) (devSimStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/status", nil)
	if err != nil {
		return devSimStatus{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return devSimStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return devSimStatus{}, fmt.Errorf("status returned %d", resp.StatusCode)
	}
	var status devSimStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return devSimStatus{}, err
	}
	return status, nil
}

func writeDevSimConfig(t *testing.T, statusAddr string, cluster *suite.StartedCluster) string {
	t.Helper()
	apiAddrs := make([]string, 0, len(cluster.Nodes))
	gatewayAddrs := make([]string, 0, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		apiAddrs = append(apiAddrs, "http://"+node.Spec.APIAddr)
		gatewayAddrs = append(gatewayAddrs, node.Spec.GatewayAddr)
	}
	content := fmt.Sprintf(`version: wkbench/dev-sim/v1
status:
  listen: %s
target:
  api_addrs: [%s]
  gateway_tcp_addrs: [%s]
identity:
  uid_prefix: e2e-sim-u
  device_prefix: e2e-sim-d
  client_msg_prefix: e2e-sim-msg
  token_mode: none
online:
  total_users: 8
  connect_rate: 50/s
profiles:
  person_channels: 2
  group_channels: 1
  group_members: 4
traffic:
  payload_size_bytes: 32
  person_rate_per_channel: 2/s
  group_rate_per_channel: 2/s
  verify_recv: sampled
  window: 500ms
  cooldown: 100ms
retry:
  readiness_timeout: 30s
  restart_backoff: 100ms
`, statusAddr, quoteYAMLStrings(apiAddrs), quoteYAMLStrings(gatewayAddrs))
	path := filepath.Join(t.TempDir(), "dev-sim.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func quoteYAMLStrings(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return strings.Join(quoted, ", ")
}

var wkbenchBinaryCache struct {
	once sync.Once
	path string
	err  error
}

func buildWkbenchBinary(t *testing.T) string {
	t.Helper()
	wkbenchBinaryCache.once.Do(func() {
		root, err := os.MkdirTemp("", "wkbench-devsim-e2e-bin-*")
		if err != nil {
			wkbenchBinaryCache.err = err
			return
		}
		wkbenchBinaryCache.path = filepath.Join(root, "wkbench-devsim-e2e")
		cmd := exec.Command("go", "build", "-o", wkbenchBinaryCache.path, "./cmd/wkbench")
		cmd.Dir = repoRoot()
		cmd.Env = append(os.Environ(), "GOWORK=off")
		output, err := cmd.CombinedOutput()
		if err != nil {
			wkbenchBinaryCache.err = fmt.Errorf("go build ./cmd/wkbench: %w\n%s", err, output)
		}
	})
	require.NoError(t, wkbenchBinaryCache.err)
	return wkbenchBinaryCache.path
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}
