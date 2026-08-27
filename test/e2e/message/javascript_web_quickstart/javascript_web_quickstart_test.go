//go:build e2e

package javascript_web_quickstart

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
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

const (
	docsQuickstartOutputTailLimit    = 64 << 10
	docsQuickstartProcessStopTimeout = 5 * time.Second
	docsQuickstartScreenshotMaxBytes = 2 << 20
)

func TestJavaScriptWebQuickstartInChromium(t *testing.T) {
	if os.Getenv("WK_E2E_DOCS_JAVASCRIPT_WEB") != "1" {
		t.Skip("set WK_E2E_DOCS_JAVASCRIPT_WEB=1 to run the JavaScript web quickstart smoke")
	}

	s := suite.New(t)
	node := s.StartSingleNodeCluster(
		suite.WithWebSocketGateway(),
		suite.WithNodeConfigOverrides(1, map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256",
		}),
	)

	routeCtx, cancelRoute := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRoute()
	var route struct {
		WebSocketURL string `json:"ws_addr"`
	}
	_, err := suite.GetJSON(routeCtx, "http://"+node.APIAddr()+"/route", &route)
	require.NoError(t, err)
	require.Equal(t, node.WebSocketURL(), route.WebSocketURL)

	uiAddr := suite.ReserveLoopbackPorts(t).APIAddr
	browserCtx, cancelBrowser := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelBrowser()
	root := repositoryRoot(t)
	docsSiteURL := startDocsSiteServer(t, root)
	browserArtifacts, err := newDocsQuickstartBrowserArtifacts(root, time.Now())
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), docsQuickstartProcessStopTimeout)
		defer cancel()
		if cleanupErr := browserArtifacts.Cleanup(ctx, t.Failed()); cleanupErr != nil {
			t.Errorf("clean browser artifacts: %v", cleanupErr)
		}
	})
	browserOutputDir := browserArtifacts.Dir()
	cmd := exec.Command("npm", "run", "test:e2e")
	cmd.Dir = filepath.Join(root, "docs-site", "examples", "javascript-web-quickstart")
	cmd.Env = docsQuickstartEnvironment(map[string]string{
		"WK_DOCS_QUICKSTART_E2E_PRODUCT_HTTP_URL": "http://" + node.APIAddr(),
		"WK_DOCS_QUICKSTART_E2E_UI_URL":           "http://" + uiAddr,
		"WK_DOCS_QUICKSTART_E2E_OUTPUT_DIR":       browserOutputDir,
		"WK_DOCS_SITE_E2E_URL":                    docsSiteURL,
	})
	output := &boundedTailBuffer{limit: docsQuickstartOutputTailLimit}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := runBrowserCommand(browserCtx, cmd, docsQuickstartProcessStopTimeout); err != nil {
		t.Fatalf(
			"JavaScript web quickstart browser smoke failed: %v; subprocess output withheld; bounded screenshots: %s (%s)",
			err,
			browserOutputDir,
			output.Summary(),
		)
	}
	if attestationOutput := strings.TrimSpace(os.Getenv(goldenPathAttestationOutput)); attestationOutput != "" {
		runtimeEvidence, evidenceErr := collectGoldenPathRuntimeEvidence(cmd.Dir)
		require.NoError(t, evidenceErr)
		require.NoError(t, writeGoldenPathAttestation(root, attestationOutput, runtimeEvidence))
	}
	t.Logf("JavaScript web quickstart browser smoke passed (%s)", output.Summary())
}

func TestBoundedTailBufferDoesNotExposeCapturedBrowserOutput(t *testing.T) {
	buffer := &boundedTailBuffer{limit: 32}
	_, err := buffer.Write([]byte(`docs-dev-secret alice-offline-123 {"client_msg_no":"secret"}`))
	require.NoError(t, err)
	require.NotContains(t, buffer.Summary(), "secret")
	require.NotContains(t, buffer.Summary(), "alice")
}

func runBrowserCommand(ctx context.Context, cmd *exec.Cmd, cleanupTimeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	suite.PrepareCommandProcessTree(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- cmd.Wait()
	}()

	select {
	case err := <-waitResult:
		return errors.Join(err, suite.ReapCommandProcessTree(cmd.Process, cleanupTimeout))
	case <-ctx.Done():
		select {
		case err := <-waitResult:
			return errors.Join(err, suite.ReapCommandProcessTree(cmd.Process, cleanupTimeout))
		default:
		}

		cleanupErr := suite.ReapCommandProcessTree(cmd.Process, cleanupTimeout)
		timer := time.NewTimer(cleanupTimeout)
		defer timer.Stop()
		select {
		case <-waitResult:
		case <-timer.C:
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("wait for browser parent exceeded %s", cleanupTimeout))
		}
		return errors.Join(ctx.Err(), cleanupErr)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve JavaScript web quickstart source path")
	root, err := filepath.Abs(filepath.Join(filepath.Dir(filename), "../../../.."))
	require.NoError(t, err)
	return root
}

func startDocsSiteServer(t *testing.T, root string) string {
	t.Helper()
	outDir := filepath.Join(root, "docs-site", "out")
	_, err := os.Stat(filepath.Join(outDir, "en", "index.html"))
	require.NoError(t, err, "build docs-site/out before the browser integration check")

	addr := suite.ReserveLoopbackPorts(t).APIAddr
	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	server := &http.Server{
		Handler:           http.FileServer(http.Dir(outDir)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), docsQuickstartProcessStopTimeout)
		defer cancel()
		shutdownErr := server.Shutdown(ctx)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		var serveErr error
		select {
		case serveErr = <-serveDone:
		case <-ctx.Done():
			serveErr = fmt.Errorf("wait for docs site server cleanup: %w", ctx.Err())
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			shutdownErr = errors.Join(shutdownErr, serveErr)
		}
		if shutdownErr != nil {
			t.Errorf("stop docs site server: %v", shutdownErr)
		}
	})
	return "http://" + addr
}

func docsQuickstartEnvironment(overrides map[string]string) []string {
	blocked := map[string]struct{}{
		"NO_COLOR":                  {},
		goldenPathAttestationOutput: {},
	}
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
	return b.Summary()
}

// Summary reports only capture bounds; raw browser output can contain fixture identities or messages.
func (b *boundedTailBuffer) Summary() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return fmt.Sprintf("captured=%d bytes retained-in-memory=%d bytes", b.total, len(b.data))
}
