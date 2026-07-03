//go:build integration && legacy_e2e

package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	pluginNo             = "wk.echo"
	pluginSmokePollEvery = 100 * time.Millisecond
)

func TestPluginWKPSmoke(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "wukongim")
	buildRequiredBinary(t, repoRoot(), binaryPath, "./cmd/wukongim")

	pluginDir := filepath.Join(root, "plugins")
	requireNoError(t, os.MkdirAll(pluginDir, 0o755))
	buildEchoPluginOrSkip(t, filepath.Join(pluginDir, pluginNo+".wkp"))

	node := newPluginSmokeNode(t, root, binaryPath, pluginDir)
	requireNoError(t, node.start())
	t.Cleanup(func() { requireNoError(t, node.stop()) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	requireNoError(t, waitHTTPStatus(ctx, "http://"+node.apiAddr+"/healthz", http.StatusOK), node.diagnostics())
	requireNoError(t, waitPluginRunning(ctx, "http://"+node.managerAddr+"/manager/nodes/1/plugins"), node.diagnostics())

	resp, err := http.Get("http://" + node.apiAddr + "/plugins/" + pluginNo + "/echo?q=codex")
	requireNoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	requireNoError(t, err)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("plugin route status=%d body=%s\n%s", resp.StatusCode, string(body), node.diagnostics())
	}
	if got := resp.Header.Get("X-Plugin-No"); got != pluginNo {
		t.Fatalf("X-Plugin-No=%q, want %q", got, pluginNo)
	}
	requireJSONEqual(t, `{"echo":"codex","method":"GET","path":"/echo"}`, string(body))
}

type pluginSmokeNode struct {
	rootDir      string
	dataDir      string
	configPath   string
	stdoutPath   string
	stderrPath   string
	binaryPath   string
	clusterAddr  string
	gatewayAddr  string
	apiAddr      string
	managerAddr  string
	pluginDir    string
	socketPath   string
	logDir       string
	cmd          *exec.Cmd
	stdout       *os.File
	stderr       *os.File
	reservations []net.Listener
}

func newPluginSmokeNode(t *testing.T, root, binaryPath, pluginDir string) *pluginSmokeNode {
	t.Helper()
	nodeRoot := filepath.Join(root, "node-1")
	dataDir := filepath.Join(nodeRoot, "data")
	logDir := filepath.Join(nodeRoot, "logs")
	requireNoError(t, os.MkdirAll(dataDir, 0o755))
	requireNoError(t, os.MkdirAll(logDir, 0o755))

	clusterAddr, clusterReservation := reserveLoopbackAddr(t)
	gatewayAddr, gatewayReservation := reserveLoopbackAddr(t)
	apiAddr, apiReservation := reserveLoopbackAddr(t)
	managerAddr, managerReservation := reserveLoopbackAddr(t)
	node := &pluginSmokeNode{
		rootDir:     nodeRoot,
		dataDir:     dataDir,
		configPath:  filepath.Join(nodeRoot, "wukongim.conf"),
		stdoutPath:  filepath.Join(nodeRoot, "stdout.log"),
		stderrPath:  filepath.Join(nodeRoot, "stderr.log"),
		binaryPath:  binaryPath,
		clusterAddr: clusterAddr,
		gatewayAddr: gatewayAddr,
		apiAddr:     apiAddr,
		managerAddr: managerAddr,
		pluginDir:   pluginDir,
		socketPath:  shortPluginSocketPath(t),
		logDir:      logDir,
		reservations: []net.Listener{
			clusterReservation,
			gatewayReservation,
			apiReservation,
			managerReservation,
		},
	}
	t.Cleanup(node.releaseReservations)
	requireNoError(t, os.WriteFile(node.configPath, []byte(renderPluginSmokeConfig(node)), 0o644))
	return node
}

func renderPluginSmokeConfig(node *pluginSmokeNode) string {
	clusterNodes := fmt.Sprintf(`[{"id":1,"addr":%q}]`, node.clusterAddr)
	listeners := fmt.Sprintf(`[{"name":"tcp-wkproto","network":"tcp","address":%q,"transport":"gnet","protocol":"wkproto"}]`, node.gatewayAddr)
	lines := map[string]string{
		"WK_NODE_ID":                      "1",
		"WK_NODE_NAME":                    "node-1",
		"WK_NODE_DATA_DIR":                node.dataDir,
		"WK_CLUSTER_LISTEN_ADDR":          node.clusterAddr,
		"WK_CLUSTER_SLOT_COUNT":           "1",
		"WK_CLUSTER_INITIAL_SLOT_COUNT":   "1",
		"WK_CLUSTER_CONTROLLER_REPLICA_N": "1",
		"WK_CLUSTER_SLOT_REPLICA_N":       "1",
		"WK_CLUSTER_NODES":                clusterNodes,
		"WK_GATEWAY_LISTENERS":            listeners,
		"WK_API_LISTEN_ADDR":              node.apiAddr,
		"WK_MANAGER_LISTEN_ADDR":          node.managerAddr,
		"WK_MANAGER_AUTH_ON":              "false",
		"WK_PLUGIN_ENABLE":                "true",
		"WK_PLUGIN_HOT_RELOAD":            "false",
		"WK_PLUGIN_DIR":                   node.pluginDir,
		"WK_PLUGIN_SOCKET_PATH":           node.socketPath,
		"WK_LOG_DIR":                      node.logDir,
	}
	keys := make([]string, 0, len(lines))
	for key := range lines {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(lines[key])
		b.WriteByte('\n')
	}
	return b.String()
}

func (n *pluginSmokeNode) start() error {
	stdout, err := os.Create(n.stdoutPath)
	if err != nil {
		return err
	}
	stderr, err := os.Create(n.stderrPath)
	if err != nil {
		_ = stdout.Close()
		return err
	}
	cmd := exec.Command(n.binaryPath, "-config", n.configPath)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	n.releaseReservations()
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return err
	}
	n.stdout = stdout
	n.stderr = stderr
	n.cmd = cmd
	return nil
}

func (n *pluginSmokeNode) stop() error {
	if n == nil || n.cmd == nil || n.cmd.Process == nil {
		return nil
	}
	waitC := make(chan error, 1)
	go func() { waitC <- n.cmd.Wait() }()
	if err := n.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		select {
		case waitErr := <-waitC:
			n.closeLogs()
			return normalizeProcessExit(waitErr)
		default:
			n.closeLogs()
			return err
		}
	}
	select {
	case err := <-waitC:
		n.closeLogs()
		return normalizeProcessExit(err)
	case <-time.After(5 * time.Second):
		if err := n.cmd.Process.Kill(); err != nil {
			n.closeLogs()
			return err
		}
		err := <-waitC
		n.closeLogs()
		return normalizeProcessExit(err)
	}
}

func (n *pluginSmokeNode) releaseReservations() {
	if n == nil {
		return
	}
	for _, ln := range n.reservations {
		_ = ln.Close()
	}
	n.reservations = nil
}

func (n *pluginSmokeNode) closeLogs() {
	if n.stdout != nil {
		_ = n.stdout.Close()
	}
	if n.stderr != nil {
		_ = n.stderr.Close()
	}
}

func (n *pluginSmokeNode) diagnostics() string {
	var b strings.Builder
	fmt.Fprintf(&b, "config: %s\n", n.configPath)
	appendLogTail(&b, "stdout", n.stdoutPath)
	appendLogTail(&b, "stderr", n.stderrPath)
	appendLogTail(&b, "app-log", filepath.Join(n.logDir, "app.log"))
	appendLogTail(&b, "error-log", filepath.Join(n.logDir, "error.log"))
	return b.String()
}

func buildEchoPluginOrSkip(t *testing.T, outputPath string) {
	t.Helper()
	sourceDir := filepath.Join(repoRoot(), "test", "e2e", "plugin", "testdata", "echo_plugin")
	cmd := exec.Command("go", "build", "-mod=readonly", "-o", outputPath, ".")
	cmd.Dir = sourceDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("build echo plugin: %v\n%s", err, output)
	}
}

func buildRequiredBinary(t *testing.T, root, outputPath, pkg string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", outputPath, pkg)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, output)
	}
}

func waitPluginRunning(ctx context.Context, url string) error {
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("manager plugin list status=%d body=%s", resp.StatusCode, string(body))
			} else if pluginListHasRunningEcho(body) {
				return nil
			} else {
				lastErr = fmt.Errorf("manager plugin list missing running %s: %s", pluginNo, string(body))
			}
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-time.After(pluginSmokePollEvery):
		}
	}
}

func pluginListHasRunningEcho(body []byte) bool {
	var payload struct {
		Items []struct {
			PluginNo string   `json:"plugin_no"`
			Status   string   `json:"status"`
			Enabled  bool     `json:"enabled"`
			Methods  []string `json:"methods"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	for _, item := range payload.Items {
		if item.PluginNo == pluginNo && item.Status == "running" && item.Enabled && containsString(item.Methods, "Route") {
			return true
		}
	}
	return false
}

func waitHTTPStatus(ctx context.Context, url string, want int) error {
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode == want {
				return nil
			} else {
				lastErr = fmt.Errorf("%s returned %d: %s", url, resp.StatusCode, string(body))
			}
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-time.After(pluginSmokePollEvery):
		}
	}
}

func reserveLoopbackAddr(t *testing.T) (string, net.Listener) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	requireNoError(t, err)
	return ln.Addr().String(), ln
}

func shortPluginSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wk-plugin-e2e-")
	requireNoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "plugin.sock")
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func requireNoError(t *testing.T, err error, diagnostics ...string) {
	t.Helper()
	if err == nil {
		return
	}
	if len(diagnostics) > 0 && diagnostics[0] != "" {
		t.Fatalf("%v\n%s", err, diagnostics[0])
	}
	t.Fatal(err)
}

func requireJSONEqual(t *testing.T, want, got string) {
	t.Helper()
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want json: %v", err)
	}
	var gotValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("decode got json: %v body=%s", err, got)
	}
	if !reflect.DeepEqual(wantValue, gotValue) {
		t.Fatalf("json mismatch\nwant: %s\ngot:  %s", want, got)
	}
}

func appendLogTail(b *strings.Builder, name, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(b, "%s-read-error: %v\n", name, err)
		return
	}
	if len(data) > 4096 {
		data = data[len(data)-4096:]
		fmt.Fprintf(b, "%s-tail: [truncated]\n", name)
	}
	fmt.Fprintf(b, "%s-content:\n%s", name, data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func normalizeProcessExit(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		switch status.Signal() {
		case syscall.SIGTERM, syscall.SIGKILL:
			return nil
		}
	}
	if ok && status.ExitStatus() == 0 {
		return nil
	}
	return err
}
