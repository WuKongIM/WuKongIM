//go:build e2e

package dynamic_node_readiness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
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
	channelID := prefix + "-recipient"
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
				ChannelID:   channelID,
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

type metricExpectation struct {
	name     string
	labels   map[string]string
	minValue float64
}

func requireMetricSamples(t testing.TB, node *suite.StartedNode, expectations ...metricExpectation) {
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
	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(bytes.NewReader(data))
	require.NoError(t, err)
	for _, expectation := range expectations {
		family := families[expectation.name]
		if family == nil {
			t.Fatalf("metrics from node %d missing family %q", node.Spec.ID, expectation.name)
		}
		value, ok := findMetricSampleValue(family, expectation.labels)
		if !ok {
			t.Fatalf("metrics from node %d family %q missing labels %#v", node.Spec.ID, expectation.name, expectation.labels)
		}
		if value < expectation.minValue {
			t.Fatalf("metrics from node %d family %q labels %#v value=%v, want >= %v",
				node.Spec.ID, expectation.name, expectation.labels, value, expectation.minValue)
		}
	}
}

func findMetricSampleValue(family *dto.MetricFamily, labels map[string]string) (float64, bool) {
	for _, metric := range family.GetMetric() {
		if !metricHasLabels(metric, labels) {
			continue
		}
		value, ok := metricValue(metric)
		if ok {
			return value, true
		}
	}
	return 0, false
}

func metricHasLabels(metric *dto.Metric, want map[string]string) bool {
	for name, value := range want {
		found := false
		for _, label := range metric.GetLabel() {
			if label.GetName() == name && label.GetValue() == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func metricValue(metric *dto.Metric) (float64, bool) {
	switch {
	case metric.GetCounter() != nil:
		return metric.GetCounter().GetValue(), true
	case metric.GetGauge() != nil:
		return metric.GetGauge().GetValue(), true
	case metric.GetUntyped() != nil:
		return metric.GetUntyped().GetValue(), true
	case metric.GetHistogram() != nil:
		return float64(metric.GetHistogram().GetSampleCount()), true
	case metric.GetSummary() != nil:
		return float64(metric.GetSummary().GetSampleCount()), true
	default:
		return 0, false
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
