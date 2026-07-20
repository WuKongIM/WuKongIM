package cloudanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

const maxPrivateJSONBytes = 4 << 20

type managerClient struct {
	baseURL *url.URL
	auth    ManagerAuth
	client  *http.Client
	now     func() time.Time
	tokenMu sync.Mutex
	token   string
}

func newManagerClient(baseURL *url.URL, auth ManagerAuth, client *http.Client, now func() time.Time) *managerClient {
	return &managerClient{baseURL: baseURL, auth: auth, client: client, now: now, token: strings.TrimSpace(auth.BearerToken)}
}

func (c *managerClient) clusterSnapshot(ctx context.Context) (analysis.SourceResult, error) {
	type result struct {
		name string
		data any
		err  error
	}
	results := make(chan result, 2)
	for _, item := range []struct {
		name string
		path string
	}{{name: "nodes", path: "/manager/nodes"}, {name: "workqueues", path: "/manager/runtime/workqueues"}} {
		item := item
		go func() {
			var data any
			err := c.doJSON(ctx, http.MethodGet, item.path, nil, nil, &data)
			results <- result{name: item.name, data: data, err: err}
		}()
	}
	data := make(map[string]any, 2)
	warnings := make([]string, 0, 1)
	completeness := analysis.CompletenessComplete
	for range 2 {
		result := <-results
		if result.err != nil {
			if result.name == "nodes" {
				return analysis.SourceResult{}, result.err
			}
			completeness = analysis.CompletenessPartial
			warnings = append(warnings, "manager workqueue snapshot unavailable")
			continue
		}
		data[result.name] = result.data
	}
	return analysis.SourceResult{Node: "cluster", Source: "manager", Completeness: completeness, Warnings: warnings, Data: data}, nil
}

func (c *managerClient) logsSearch(ctx context.Context, req analysis.LogsSearchRequest) (analysis.SourceResult, error) {
	query := url.Values{
		"node_id": {strconv.FormatUint(req.NodeID, 10)},
		"source":  {req.Source},
		"keyword": {req.Keyword},
		"limit":   {strconv.Itoa(req.Limit)},
	}
	if len(req.Levels) > 0 {
		query.Set("levels", strings.Join(req.Levels, ","))
	}
	var data any
	if err := c.doJSON(ctx, http.MethodGet, "/manager/app-logs", query, nil, &data); err != nil {
		return analysis.SourceResult{}, err
	}
	return resultFromManager(req.NodeID, data), nil
}

func (c *managerClient) logsContext(ctx context.Context, req analysis.LogsContextRequest) (analysis.SourceResult, error) {
	query := url.Values{
		"node_id": {strconv.FormatUint(req.NodeID, 10)},
		"source":  {req.Source},
		"cursor":  {req.Cursor},
		"limit":   {strconv.Itoa(req.Before + req.After)},
	}
	var data any
	if err := c.doJSON(ctx, http.MethodGet, "/manager/app-logs", query, nil, &data); err != nil {
		return analysis.SourceResult{}, err
	}
	result := resultFromManager(req.NodeID, data)
	result.Warnings = append(result.Warnings, "manager log cursor returns a bounded page; before and after are applied as one combined limit")
	return result, nil
}

func (c *managerClient) diagnosticsQuery(ctx context.Context, req analysis.DiagnosticsQueryRequest) (analysis.SourceResult, error) {
	query := url.Values{"limit": {strconv.Itoa(req.Limit)}}
	setOptionalUint64(query, "node_id", req.NodeID)
	if req.SlotID != 0 {
		query.Set("slot_id", strconv.FormatUint(uint64(req.SlotID), 10))
	}
	setOptional(query, "trace_id", req.TraceID)
	setOptional(query, "client_msg_no", req.ClientMsgNo)
	setOptional(query, "channel_key", req.ChannelKey)
	setOptional(query, "uid", req.UID)
	setOptional(query, "stage", req.Stage)
	setOptional(query, "result", req.Result)
	var data any
	if err := c.doJSON(ctx, http.MethodGet, "/manager/diagnostics/events", query, nil, &data); err != nil {
		return analysis.SourceResult{}, err
	}
	return resultFromManager(req.NodeID, data), nil
}

func (c *managerClient) taskAuditsQuery(ctx context.Context, req analysis.TaskAuditsQueryRequest) (analysis.SourceResult, error) {
	query := url.Values{"limit": {strconv.Itoa(req.Limit)}}
	setOptionalUint64(query, "node_id", req.NodeID)
	if req.SlotID != 0 {
		query.Set("slot_id", strconv.FormatUint(uint64(req.SlotID), 10))
	}
	setOptional(query, "kind", req.Kind)
	setOptional(query, "status", req.Status)
	setOptional(query, "keyword", req.Keyword)
	var data any
	if err := c.doJSON(ctx, http.MethodGet, "/manager/controller/task-audits", query, nil, &data); err != nil {
		return analysis.SourceResult{}, err
	}
	return analysis.SourceResult{Node: "cluster", Source: "manager", Completeness: completenessFromManager(data), Warnings: warningsFromManager(data), Data: data}, nil
}

func (c *managerClient) traceStart(ctx context.Context, req analysis.TraceStartRequest) (analysis.SourceResult, error) {
	start := c.now().UTC()
	body := map[string]any{
		"node_id": req.NodeID, "target": req.Target, "ttl_seconds": int(req.TTL.Seconds()), "sample_rate": 1.0,
	}
	if req.Target == "sender_uid" {
		body["uid"] = req.UID
	} else {
		body["channel_id"] = req.ChannelID
		body["channel_type"] = req.ChannelType
	}
	var data any
	if err := c.doJSON(ctx, http.MethodPost, "/manager/diagnostics/tracking-rules", nil, body, &data); err != nil {
		return analysis.SourceResult{}, err
	}
	result := resultFromManager(req.NodeID, data)
	result.Window = &analysis.TimeWindow{Start: start, End: start.Add(req.TTL)}
	return result, nil
}

func (c *managerClient) traceQuery(ctx context.Context, req analysis.TraceQueryRequest) (analysis.SourceResult, error) {
	query := url.Values{"limit": {strconv.Itoa(req.Limit)}}
	setOptionalUint64(query, "node_id", req.NodeID)
	var data any
	path := "/manager/diagnostics/trace/" + url.PathEscape(req.TraceID)
	if err := c.doJSON(ctx, http.MethodGet, path, query, nil, &data); err != nil {
		return analysis.SourceResult{}, err
	}
	return resultFromManager(req.NodeID, data), nil
}

func (c *managerClient) configReadRedacted(ctx context.Context, req analysis.ConfigReadRequest) (analysis.SourceResult, error) {
	var data any
	path := "/manager/nodes/" + strconv.FormatUint(req.NodeID, 10) + "/config"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, nil, &data); err != nil {
		return analysis.SourceResult{}, err
	}
	return resultFromManager(req.NodeID, data), nil
}

func (c *managerClient) doJSON(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.ensureToken(ctx)
		if err != nil {
			return err
		}
		status, response, err := c.request(ctx, method, path, query, body, token)
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized && token != "" && c.auth.Username != "" && attempt == 0 {
			c.clearToken(token)
			continue
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("manager %s %s: status %d: %s", method, path, status, boundedText(response, 512))
		}
		if len(response) > maxPrivateJSONBytes {
			return fmt.Errorf("manager %s: response exceeds %d bytes", path, maxPrivateJSONBytes)
		}
		if err := json.Unmarshal(response, out); err != nil {
			return fmt.Errorf("manager %s decode: %w", path, err)
		}
		return nil
	}
	return errors.New("manager authentication failed")
}

func (c *managerClient) ensureToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token != "" || c.auth.Username == "" {
		return c.token, nil
	}
	if c.auth.Password == "" {
		return "", errors.New("manager capability password is empty")
	}
	status, response, err := c.request(ctx, http.MethodPost, "/manager/login", nil, map[string]string{
		"username": c.auth.Username, "password": c.auth.Password,
	}, "")
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("manager login: status %d", status)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(response, &payload); err != nil || strings.TrimSpace(payload.AccessToken) == "" {
		return "", errors.New("manager login returned no access token")
	}
	c.token = payload.AccessToken
	return c.token, nil
}

func (c *managerClient) clearToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token == token {
		c.token = ""
	}
}

func (c *managerClient) request(ctx context.Context, method, path string, query url.Values, body any, token string) (int, []byte, error) {
	target := *c.baseURL
	target.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	target.RawQuery = query.Encode()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(io.LimitReader(resp.Body, maxPrivateJSONBytes+1))
	return resp.StatusCode, response, err
}

func resultFromManager(nodeID uint64, data any) analysis.SourceResult {
	node := "cluster"
	if nodeID != 0 {
		node = "node-" + strconv.FormatUint(nodeID, 10)
	}
	return analysis.SourceResult{Node: node, Source: "manager", Completeness: completenessFromManager(data), Warnings: warningsFromManager(data), Data: data}
}

func completenessFromManager(data any) analysis.Completeness {
	object, ok := data.(map[string]any)
	if !ok {
		return analysis.CompletenessComplete
	}
	if truncated, _ := object["truncated"].(bool); truncated {
		return analysis.CompletenessPartial
	}
	if status, _ := object["status"].(string); status == "partial" || status == "unavailable" {
		return analysis.CompletenessPartial
	}
	return analysis.CompletenessComplete
}

func warningsFromManager(data any) []string {
	object, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	warnings := make([]string, 0)
	if notes, ok := object["notes"].([]any); ok {
		for _, note := range notes {
			if text, ok := note.(string); ok && text != "" {
				warnings = append(warnings, text)
			}
		}
	}
	if truncated, _ := object["truncated"].(bool); truncated {
		warnings = append(warnings, "manager response was truncated")
	}
	if rotated, _ := object["rotated"].(bool); rotated {
		warnings = append(warnings, "application log rotated during the cursor window")
	}
	return warnings
}

func setOptional(values url.Values, key, value string) {
	if value != "" {
		values.Set(key, value)
	}
}

func setOptionalUint64(values url.Values, key string, value uint64) {
	if value != 0 {
		values.Set(key, strconv.FormatUint(value, 10))
	}
}

func boundedText(data []byte, max int) string {
	if len(data) > max {
		data = data[:max]
	}
	return strings.TrimSpace(string(data))
}
