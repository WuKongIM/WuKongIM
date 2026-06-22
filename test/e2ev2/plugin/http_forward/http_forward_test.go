//go:build e2e

package http_forward

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
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

const (
	httpForwardPluginNo     = "httpforward"
	httpForwardResultsJSONL = "http_forward.jsonl"
	httpForwardPollInterval = 50 * time.Millisecond
)

func TestPluginHTTPForwardRoutesLocalAndRemotePluginHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("real .wkp plugin compatibility test skipped in short mode")
	}
	root := t.TempDir()
	node1 := newHTTPForwardNodeFiles(t, root, 1, "initiator")
	node2 := newHTTPForwardNodeFiles(t, root, 2, "responder")
	buildHTTPForwardPlugin(t, filepath.Join(node1.pluginDir, httpForwardPluginNo+".wkp"))
	buildHTTPForwardPlugin(t, filepath.Join(node2.pluginDir, httpForwardPluginNo+".wkp"))

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithNodeConfigOverrides(1, node1.configOverrides()),
		suite.WithNodeConfigOverrides(2, node2.configOverrides()),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	records := waitHTTPForwardRecords(t, node1.resultPath, 2, cluster)
	requireHTTPForwardRecord(t, records, "local", 200, "route:httpforward:/local:local-payload")
	requireHTTPForwardRecord(t, records, "remote", 200, "route:httpforward:/remote:remote-payload")
}

type httpForwardNodeFiles struct {
	pluginDir  string
	socketPath string
	sandboxDir string
	stateDir   string
	resultPath string
}

type httpForwardRecord struct {
	Mode   string `json:"mode"`
	Status int32  `json:"status"`
	Body   string `json:"body"`
}

func newHTTPForwardNodeFiles(t *testing.T, root string, nodeID int, role string) httpForwardNodeFiles {
	t.Helper()
	base := filepath.Join(root, fmt.Sprintf("node-%d-plugin", nodeID))
	files := httpForwardNodeFiles{
		pluginDir:  filepath.Join(base, "plugins"),
		socketPath: shortHTTPForwardSocketPath(t),
		sandboxDir: filepath.Join(base, "sandbox"),
		stateDir:   filepath.Join(base, "state"),
	}
	pluginSandbox := filepath.Join(files.sandboxDir, httpForwardPluginNo)
	require.NoError(t, os.MkdirAll(pluginSandbox, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pluginSandbox, "role"), []byte(role+"\n"), 0o644))
	require.NoError(t, os.MkdirAll(files.pluginDir, 0o755))
	files.resultPath = filepath.Join(pluginSandbox, httpForwardResultsJSONL)
	return files
}

func (f httpForwardNodeFiles) configOverrides() map[string]string {
	return map[string]string{
		"WK_PLUGIN_ENABLE":                   "true",
		"WK_PLUGIN_DIR":                      f.pluginDir,
		"WK_PLUGIN_SOCKET_PATH":              f.socketPath,
		"WK_PLUGIN_SANDBOX_DIR":              f.sandboxDir,
		"WK_PLUGIN_STATE_DIR":                f.stateDir,
		"WK_PLUGIN_TIMEOUT":                  "5s",
		"WK_PLUGIN_HOT_RELOAD":               "false",
		"WK_PLUGIN_PERSIST_AFTER_QUEUE_SIZE": "16",
		"WK_PLUGIN_PERSIST_AFTER_WORKERS":    "1",
	}
}

func buildHTTPForwardPlugin(t *testing.T, outputPath string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", outputPath, "./test/e2ev2/plugin/http_forward/testdata/httpforward")
	cmd.Dir = repoRoot(t)
	cmd.Env = goBuildEnv(t)
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "go build httpforward plugin:\n%s", output)
}

func waitHTTPForwardRecords(t *testing.T, path string, want int, cluster *suite.StartedCluster) []httpForwardRecord {
	t.Helper()
	var records []httpForwardRecord
	var lastErr error
	require.Eventuallyf(t, func() bool {
		var err error
		records, err = readHTTPForwardRecords(path)
		if err != nil {
			lastErr = err
			return false
		}
		return len(records) >= want
	}, 10*time.Second, httpForwardPollInterval, "http forward records path=%s got=%d want=%d lastErr=%v\n%s", path, len(records), want, lastErr, cluster.DumpDiagnostics())
	return records
}

func readHTTPForwardRecords(path string) ([]httpForwardRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []httpForwardRecord
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var record httpForwardRecord
			if unmarshalErr := json.Unmarshal(line, &record); unmarshalErr != nil {
				return nil, unmarshalErr
			}
			records = append(records, record)
		}
		if err == io.EOF {
			return records, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func requireHTTPForwardRecord(t *testing.T, records []httpForwardRecord, mode string, status int32, body string) {
	t.Helper()
	for _, record := range records {
		if record.Mode != mode {
			continue
		}
		require.Equal(t, status, record.Status)
		require.Equal(t, body, record.Body)
		return
	}
	t.Fatalf("record mode %q not found in %#v", mode, records)
}

func goBuildEnv(t *testing.T) []string {
	t.Helper()
	env := append(os.Environ(), "GOWORK=off")
	if os.Getenv("GOCACHE") == "" {
		env = append(env, "GOCACHE="+filepath.Join(t.TempDir(), "gocache"))
	}
	return env
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

func shortHTTPForwardSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wk-v2-http-forward-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "plugin.sock")
}
