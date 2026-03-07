package server

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/stretchr/testify/require"
)

func newEventAPITestServer(t *testing.T) (*Server, string) {
	t.Helper()

	httpPort := getFreePort(t)
	tcpPort := getFreePort(t)
	wsPort := getFreePort(t)
	clusterPort := getFreePort(t)

	httpAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	apiBaseURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	tcpAddr := fmt.Sprintf("tcp://127.0.0.1:%d", tcpPort)
	wsAddr := fmt.Sprintf("ws://127.0.0.1:%d", wsPort)
	clusterAddr := fmt.Sprintf("tcp://127.0.0.1:%d", clusterPort)

	s := NewTestServer(t,
		options.WithMode(options.TestMode),
		options.WithDemoOn(false),
		options.WithManagerOn(false),
		options.WithHTTPAddr(httpAddr),
		options.WithAddr(tcpAddr),
		options.WithWSAddr(wsAddr),
		options.WithClusterAddr(clusterAddr),
		options.WithClusterServerAddr(clusterAddr),
		options.WithClusterAPIURL(apiBaseURL),
	)

	err := s.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		s.StopNoErr()
	})

	s.MustWaitAllSlotsReady(10 * time.Second)
	waitHTTPReady(t, apiBaseURL)

	return s, apiBaseURL
}

func waitHTTPReady(t *testing.T, apiBaseURL string) {
	t.Helper()

	httpClient := &http.Client{Timeout: 500 * time.Millisecond}
	require.Eventually(t, func() bool {
		resp, err := httpClient.Get(apiBaseURL + "/health")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 8*time.Second, 100*time.Millisecond)
}

func getFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}
