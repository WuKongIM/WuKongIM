//go:build e2e

package dynamic_node_readiness

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

func TestRequireMetricSamplesWaitsForTransientMissingLabels(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if request == 1 {
			_, _ = w.Write([]byte(`# HELP wukongim_node_health_freshness_nodes Current node count grouped by health freshness and health status.
# TYPE wukongim_node_health_freshness_nodes gauge
wukongim_node_health_freshness_nodes{freshness="stale",status="alive"} 3
`))
			return
		}
		_, _ = w.Write([]byte(`# HELP wukongim_node_health_freshness_nodes Current node count grouped by health freshness and health status.
# TYPE wukongim_node_health_freshness_nodes gauge
wukongim_node_health_freshness_nodes{freshness="fresh",status="alive"} 3
`))
	}))
	defer server.Close()

	node := &suite.StartedNode{Spec: suite.NodeSpec{
		ID:      1,
		APIAddr: strings.TrimPrefix(server.URL, "http://"),
	}}
	requireMetricSamples(t, node, metricExpectation{
		name:     "wukongim_node_health_freshness_nodes",
		labels:   map[string]string{"freshness": "fresh", "status": "alive"},
		minValue: 3,
	})
	if got := requests.Load(); got < 2 {
		t.Fatalf("metrics helper made %d request(s), want retry after missing labels", got)
	}
}
