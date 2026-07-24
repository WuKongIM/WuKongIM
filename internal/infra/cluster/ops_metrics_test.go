package cluster

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	observe "github.com/WuKongIM/WuKongIM/internal/usecase/opsobserve"
)

func TestOpsPrometheusReaderUsesServerQueryAndDropsInternalAddressLabel(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		query = request.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"instance":"10.0.0.1:5300","node_id":"2","node_name":"node-2"},"values":[[1,"3"]]}]}}`)
	}))
	defer server.Close()
	reader, err := NewOpsPrometheusReader(server.URL, nil)
	if err != nil {
		t.Fatalf("NewOpsPrometheusReader() error = %v", err)
	}
	data, err := reader.QueryOpsMetrics(context.Background(), observe.MetricsQueryRangeRequest{
		QueryID: observe.MetricQueryGoGoroutines, NodeID: 2,
		Start: time.Now().Add(-time.Minute), End: time.Now(), StepSeconds: 15,
	})
	if err != nil {
		t.Fatalf("QueryOpsMetrics() error = %v", err)
	}
	got := data
	if !strings.Contains(query, `node_id="2"`) {
		t.Fatalf("query = %q, want fixed node matcher", query)
	}
	if _, found := got.Series[0].Labels["instance"]; found {
		t.Fatalf("internal address label leaked: %#v", got.Series[0].Labels)
	}
	if got.Series[0].Labels["node_id"] != "2" {
		t.Fatalf("labels = %#v", got.Series[0].Labels)
	}
	if !strings.Contains(query, "wukongim_node_goroutines") {
		t.Fatalf("query = %q, want node-labeled WuKongIM resource metric", query)
	}
}
