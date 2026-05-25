//go:build e2e

package gofail_transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const gofailTransportSmokeEnv = "WK_E2E_GOFAIL_TRANSPORT_SMOKE"

func TestGofailTransportFailpointsAreExposedByBinary(t *testing.T) {
	if os.Getenv(gofailTransportSmokeEnv) != "1" {
		t.Skipf("set %s=1 and WK_E2E_BINARY to a scripts/build-gofail-binary.sh output", gofailTransportSmokeEnv)
	}
	if strings.TrimSpace(os.Getenv("WK_E2E_BINARY")) == "" {
		t.Skip("WK_E2E_BINARY must point at a gofail-enabled binary")
	}

	gofailAddr := reserveLoopbackAddr(t)
	s := suite.New(t)
	node := s.StartSingleNodeCluster(suite.WithNodeEnv(1, "GOFAIL_HTTP="+gofailAddr))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	body := waitForFailpointList(ctx, t, gofailAddr, node)
	for _, want := range []string{"wkTransportSendFault=", "wkTransportRPCFault="} {
		require.Contains(t, body, want, node.Process.DumpDiagnostics())
	}
}

func waitForFailpointList(ctx context.Context, t *testing.T, addr string, node *suite.StartedNode) string {
	t.Helper()

	url := "http://" + addr + "/"
	var lastErr error
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK {
				return string(data)
			}
			if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = fmt.Errorf("status %d body %s", resp.StatusCode, data)
			}
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, ctx.Err(), "last gofail HTTP error: %v\n%s", lastErr, node.Process.DumpDiagnostics())
	return ""
}

func reserveLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}
