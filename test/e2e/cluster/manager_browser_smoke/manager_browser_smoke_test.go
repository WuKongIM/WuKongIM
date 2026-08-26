//go:build e2e

package manager_browser_smoke

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const managerBrowserUsername = "manager-browser-e2e"

const managerBrowserOutputTailLimit = 64 << 10

func TestManagerProductionBundleInChromium(t *testing.T) {
	if os.Getenv("WK_E2E_MANAGER_BROWSER") != "1" {
		t.Skip("set WK_E2E_MANAGER_BROWSER=1 to run the Chromium Manager smoke")
	}

	password := randomSecret(t)
	jwtSecret := randomSecret(t)
	users, err := json.Marshal([]map[string]any{
		{
			"username": managerBrowserUsername,
			"password": password,
			"permissions": []map[string]any{
				{"resource": "*", "actions": []string{"*"}},
			},
		},
	})
	require.NoError(t, err)

	options := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		options = append(options, suite.WithNodeConfigOverrides(nodeID, map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256",
			"WK_MANAGER_AUTH_ON":         "true",
			"WK_MANAGER_JWT_SECRET":      jwtSecret,
			"WK_MANAGER_JWT_ISSUER":      "wukongim-manager-browser-e2e",
			"WK_MANAGER_JWT_EXPIRE":      "15m",
			"WK_MANAGER_USERS":           string(users),
		}))
	}

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(options...)
	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	browserCtx, cancelBrowser := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelBrowser()
	root := repositoryRoot(t)
	cmd := exec.CommandContext(browserCtx, "bun", "run", "test:e2e:manager")
	cmd.Dir = filepath.Join(root, "web")
	cmd.Env = managerBrowserEnvironment(map[string]string{
		"WK_MANAGER_E2E_URL":      "http://" + cluster.MustNode(1).ManagerAddr(),
		"WK_MANAGER_E2E_USERNAME": managerBrowserUsername,
		"WK_MANAGER_E2E_PASSWORD": password,
	})
	output := &boundedTailBuffer{limit: managerBrowserOutputTailLimit}
	cmd.Stdout = output
	cmd.Stderr = output
	err = cmd.Run()
	if err != nil {
		t.Fatalf("Manager browser smoke failed: %v\n%s\n%s", err, output, cluster.DumpDiagnostics())
	}
	t.Logf("Manager browser smoke passed:\n%s", output)
}

func randomSecret(t *testing.T) string {
	t.Helper()
	value := make([]byte, 24)
	_, err := rand.Read(value)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(value)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve Manager browser smoke source path")
	root, err := filepath.Abs(filepath.Join(filepath.Dir(filename), "../../../.."))
	require.NoError(t, err)
	return root
}

func managerBrowserEnvironment(overrides map[string]string) []string {
	blocked := map[string]struct{}{"NO_COLOR": {}}
	for key := range overrides {
		blocked[key] = struct{}{}
	}

	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := blocked[key]; replace {
			continue
		}
		env = append(env, entry)
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

// boundedTailBuffer retains only the final bytes from browser process output.
type boundedTailBuffer struct {
	mu    sync.Mutex
	data  []byte
	total int
	limit int
}

func (b *boundedTailBuffer) Write(value []byte) (int, error) {
	written := len(value)
	b.mu.Lock()
	defer b.mu.Unlock()

	b.total += written
	if written >= b.limit {
		b.data = append(b.data[:0], value[written-b.limit:]...)
		return written, nil
	}

	overflow := len(b.data) + written - b.limit
	if overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, value...)
	return written, nil
}

func (b *boundedTailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	output := string(b.data)
	omitted := b.total - len(b.data)
	if omitted <= 0 {
		return output
	}
	return fmt.Sprintf("[truncated %d browser output bytes; showing final %d bytes]\n%s", omitted, len(b.data), output)
}
