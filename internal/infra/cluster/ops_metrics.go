package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	observe "github.com/WuKongIM/WuKongIM/internal/usecase/opsobserve"
)

const maxOpsPrometheusBytes = 4 << 20

var errOpsPrometheusUnavailable = errors.New("operations Prometheus reader unavailable")

var opsMetricQueries = map[string]func(uint64) string{
	observe.MetricQueryTargetsUp: func(nodeID uint64) string {
		return withNodeMatcher(`up{job="wukongim"}`, nodeID)
	},
	observe.MetricQueryMessageIngressRate: func(nodeID uint64) string {
		return sumByNode(`rate(wukongim_gateway_messages_received_total%s[1m])`, nodeID)
	},
	observe.MetricQueryMessageDeliveryRate: func(nodeID uint64) string {
		return sumByNode(`rate(wukongim_gateway_messages_delivered_total%s[1m])`, nodeID)
	},
	observe.MetricQueryAppendErrorRate: func(nodeID uint64) string {
		return sumByNodeAnd(`rate(wukongim_message_append_errors_total%s[1m])`, nodeID, "path", "class")
	},
	observe.MetricQueryRuntimeQueuePressure: func(nodeID uint64) string {
		matcher := nodeMatcher(nodeID)
		return fmt.Sprintf(
			`max by (node_id,node_name,component,pool,queue) (wukongim_runtime_pool_queue_depth%s / (wukongim_runtime_pool_queue_capacity%s > 0))`,
			matcher, matcher,
		)
	},
	observe.MetricQueryProcessCPURate: func(nodeID uint64) string {
		return sumByNode(`wukongim_node_cpu_percent%s / 100`, nodeID)
	},
	observe.MetricQueryProcessResidentMemory: func(nodeID uint64) string {
		return sumByNode(`wukongim_node_memory_rss_bytes%s`, nodeID)
	},
	observe.MetricQueryGoGoroutines: func(nodeID uint64) string {
		return sumByNode(`wukongim_node_goroutines%s`, nodeID)
	},
	observe.MetricQueryGatewayConnections: func(nodeID uint64) string {
		return sumByNode(`wukongim_gateway_connections_active%s`, nodeID)
	},
	observe.MetricQuerySlotApplyGap: func(nodeID uint64) string {
		return fmt.Sprintf(`max by (node_id,node_name) (wukongim_slot_apply_gap%s)`, nodeMatcher(nodeID))
	},
}

// OpsPrometheusReader executes only fixed server-owned query builders.
type OpsPrometheusReader struct {
	baseURL *url.URL
	client  *http.Client
}

// NewOpsPrometheusReader creates an optional fixed-origin Prometheus reader.
func NewOpsPrometheusReader(baseURL string, client *http.Client) (*OpsPrometheusReader, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errOpsPrometheusUnavailable
	}
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return &OpsPrometheusReader{baseURL: parsed, client: client}, nil
}

// QueryOpsMetrics executes one allowlisted query and returns a bounded matrix.
func (r *OpsPrometheusReader) QueryOpsMetrics(ctx context.Context, request observe.MetricsQueryRangeRequest) (observe.MetricRangeData, error) {
	if r == nil || r.baseURL == nil || r.client == nil {
		return observe.MetricRangeData{}, errOpsPrometheusUnavailable
	}
	builder, ok := opsMetricQueries[request.QueryID]
	if !ok {
		return observe.MetricRangeData{}, observe.ErrInvalidToolInput
	}
	target := *r.baseURL
	target.Path = strings.TrimRight(r.baseURL.Path, "/") + "/api/v1/query_range"
	target.RawQuery = url.Values{
		"query": {builder(request.NodeID)},
		"start": {strconv.FormatFloat(float64(request.Start.UnixNano())/1e9, 'f', 3, 64)},
		"end":   {strconv.FormatFloat(float64(request.End.UnixNano())/1e9, 'f', 3, 64)},
		"step":  {strconv.Itoa(request.StepSeconds)},
	}.Encode()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return observe.MetricRangeData{}, err
	}
	response, err := r.client.Do(httpRequest)
	if err != nil {
		return observe.MetricRangeData{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOpsPrometheusBytes+1))
	if err != nil {
		return observe.MetricRangeData{}, err
	}
	if len(body) > maxOpsPrometheusBytes || response.StatusCode != http.StatusOK {
		return observe.MetricRangeData{}, errOpsPrometheusUnavailable
	}
	var envelope prometheusRangeEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Status != "success" || envelope.Data.ResultType != "matrix" {
		return observe.MetricRangeData{}, errOpsPrometheusUnavailable
	}
	if len(envelope.Data.Result) > observe.MaxMetricSeries {
		return observe.MetricRangeData{}, observe.ErrResponseTooLarge
	}
	result := observe.MetricRangeData{QueryID: request.QueryID, Series: make([]observe.MetricSeries, 0, len(envelope.Data.Result))}
	for _, series := range envelope.Data.Result {
		if len(series.Values) > observe.MaxMetricPointsPerSeries {
			return observe.MetricRangeData{}, observe.ErrResponseTooLarge
		}
		result.Series = append(result.Series, observe.MetricSeries{
			Labels: safeMetricLabels(series.Metric),
			Values: append([][]json.RawMessage(nil), series.Values...),
		})
	}
	return result, nil
}

type prometheusRangeEnvelope struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string   `json:"metric"`
			Values [][]json.RawMessage `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func safeMetricLabels(labels map[string]string) map[string]string {
	allowed := map[string]struct{}{
		"node_id": {}, "node_name": {}, "component": {}, "pool": {}, "queue": {},
		"result": {}, "path": {}, "class": {},
	}
	out := make(map[string]string)
	for key, value := range labels {
		if _, ok := allowed[key]; ok {
			out[key] = value
		}
	}
	return out
}

func nodeMatcher(nodeID uint64) string {
	if nodeID == 0 {
		return `{job="wukongim"}`
	}
	return fmt.Sprintf(`{job="wukongim",node_id="%d"}`, nodeID)
}

func withNodeMatcher(metric string, nodeID uint64) string {
	if nodeID == 0 {
		return metric
	}
	return strings.Replace(metric, `{job="wukongim"}`, nodeMatcher(nodeID), 1)
}

func sumByNode(expression string, nodeID uint64) string {
	return fmt.Sprintf(`sum by (node_id,node_name) (`+expression+`)`, nodeMatcher(nodeID))
}

func sumByNodeAnd(expression string, nodeID uint64, labels ...string) string {
	grouping := "node_id,node_name"
	if len(labels) > 0 {
		grouping += "," + strings.Join(labels, ",")
	}
	return fmt.Sprintf(`sum by (`+grouping+`) (`+expression+`)`, nodeMatcher(nodeID))
}
