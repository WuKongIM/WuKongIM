//go:build e2e

package dynamic_node_readiness

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

type trafficWorker struct {
	client *suite.WKProtoClient
	stop   chan struct{}
	done   chan struct{}
	sent   atomic.Uint64
	errs   atomic.Uint64

	mu      sync.Mutex
	lastErr error
}

func startTrafficWorker(t testing.TB, cluster *suite.StartedCluster, node *suite.StartedNode, prefix string) *trafficWorker {
	t.Helper()
	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	uid := prefix + "-sender"
	require.NoError(t, client.Connect(node.GatewayAddr(), uid, uid+"-device"), node.DumpDiagnostics())

	worker := &trafficWorker{client: client, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(worker.done)
		defer func() { _ = client.Close() }()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for seq := uint64(1); ; seq++ {
			select {
			case <-worker.stop:
				return
			case <-ticker.C:
			}
			msgNo := fmt.Sprintf("%s-%06d", prefix, seq)
			if err := client.SendFrame(&frame.SendPacket{
				ChannelID:   uid,
				ChannelType: frame.ChannelTypePerson,
				ClientSeq:   seq,
				ClientMsgNo: msgNo,
				Payload:     []byte(msgNo),
			}); err != nil {
				worker.recordErr(err)
				continue
			}
			ack, err := client.ReadSendAck()
			if err != nil {
				worker.recordErr(err)
				continue
			}
			if ack.ReasonCode != frame.ReasonSuccess {
				worker.recordErr(fmt.Errorf("sendack reason=%v seq=%d msg_no=%s", ack.ReasonCode, seq, msgNo))
				continue
			}
			worker.sent.Add(1)
		}
	}()
	requireTrafficProgress(t, cluster, worker, 2, 10*time.Second)
	return worker
}

func stopTrafficWorker(t testing.TB, worker *trafficWorker) {
	t.Helper()
	if worker == nil {
		return
	}
	close(worker.stop)
	select {
	case <-worker.done:
	case <-time.After(5 * time.Second):
		t.Fatal("traffic worker did not stop")
	}
}

func requireTrafficProgress(t testing.TB, cluster *suite.StartedCluster, worker *trafficWorker, additional uint64, timeout time.Duration) {
	t.Helper()
	start := worker.sent.Load()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if worker.errs.Load() != 0 {
			t.Fatalf("traffic worker recorded %d errors: %v\n%s", worker.errs.Load(), worker.lastError(), cluster.DumpDiagnostics())
		}
		if worker.sent.Load() >= start+additional {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("traffic worker sent=%d, want at least %d more\n%s", worker.sent.Load(), additional, cluster.DumpDiagnostics())
}

func requireMetricsContain(t testing.TB, node *suite.StartedNode, names ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+node.APIAddr()+"/metrics", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(data)
	for _, name := range names {
		if !strings.Contains(body, name) {
			t.Fatalf("metrics from node %d missing %q", node.Spec.ID, name)
		}
	}
}

func (w *trafficWorker) recordErr(err error) {
	w.errs.Add(1)
	w.mu.Lock()
	w.lastErr = err
	w.mu.Unlock()
}

func (w *trafficWorker) lastError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}
