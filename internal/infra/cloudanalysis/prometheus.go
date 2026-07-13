package cloudanalysis

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

type prometheusClient struct {
	baseURL *url.URL
	client  *http.Client
}

func (c *prometheusClient) queryRange(ctx context.Context, req analysis.MetricsQueryRangeRequest, query string) (analysis.SourceResult, error) {
	target := *c.baseURL
	target.Path = strings.TrimRight(c.baseURL.Path, "/") + "/api/v1/query_range"
	values := url.Values{
		"query": {query},
		"start": {strconv.FormatFloat(float64(req.Start.UnixNano())/1e9, 'f', 3, 64)},
		"end":   {strconv.FormatFloat(float64(req.End.UnixNano())/1e9, 'f', 3, 64)},
		"step":  {strconv.FormatFloat(req.Step.Seconds(), 'f', -1, 64)},
	}
	target.RawQuery = values.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return analysis.SourceResult{}, err
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return analysis.SourceResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPrivateJSONBytes+1))
	if err != nil {
		return analysis.SourceResult{}, err
	}
	if len(body) > maxPrivateJSONBytes {
		return analysis.SourceResult{}, fmt.Errorf("prometheus response exceeds %d bytes", maxPrivateJSONBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return analysis.SourceResult{}, fmt.Errorf("prometheus query_range: status %d: %s", resp.StatusCode, boundedText(body, 512))
	}
	var envelope struct {
		Status    string `json:"status"`
		Data      any    `json:"data"`
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return analysis.SourceResult{}, fmt.Errorf("prometheus query_range decode: %w", err)
	}
	if envelope.Status != "success" {
		return analysis.SourceResult{}, fmt.Errorf("prometheus query_range failed: %s: %s", envelope.ErrorType, envelope.Error)
	}
	return analysis.SourceResult{
		Node: "cluster", Source: "prometheus", Completeness: analysis.CompletenessComplete,
		Window: &analysis.TimeWindow{Start: req.Start, End: req.End}, Data: envelope.Data,
	}, nil
}
