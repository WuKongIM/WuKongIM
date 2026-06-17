package sim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	benchVersion     = "bench/v1"
	groupChannelType = 2
)

// targetClient calls bench-capable WuKongIM HTTP API targets.
type targetClient struct {
	// servers are normalized HTTP API base URLs in deterministic order.
	servers []string
	// token is the optional bearer token used for bench API calls.
	token string
	// httpClient executes HTTP requests for preflight and setup mutations.
	httpClient *http.Client
}

// targetPreflight describes target facts discovered before the simulation starts.
type targetPreflight struct {
	// GatewayTCPAddrs are deduped WKProto TCP gateway addresses discovered from targets.
	GatewayTCPAddrs []string
	// MaxBatchSize is the smallest positive target mutation batch limit.
	MaxBatchSize int
}

// capabilitiesResponse mirrors the v2 bench capabilities JSON response.
type capabilitiesResponse struct {
	Enabled  bool                 `json:"enabled"`
	Version  string               `json:"version"`
	Supports capabilitiesSupports `json:"supports"`
	Limits   capabilitiesLimits   `json:"limits"`
}

// capabilitiesSupports describes optional bench API features supported by a target.
type capabilitiesSupports struct {
	ChannelsBatch           bool     `json:"channels_batch"`
	ChannelSubscribersBatch bool     `json:"channel_subscribers_batch"`
	Snapshot                bool     `json:"snapshot"`
	ChannelTypes            []string `json:"channel_types"`
}

// capabilitiesLimits describes target-side bench API limits.
type capabilitiesLimits struct {
	MaxBatchSize int `json:"max_batch_size"`
}

// capacityTargetResponse mirrors the v2 bench capacity target JSON response.
type capacityTargetResponse struct {
	Version string        `json:"version"`
	Gateway targetGateway `json:"gateway"`
}

// targetGateway contains externally reachable gateway addresses.
type targetGateway struct {
	TCPAddr string `json:"tcp_addr"`
}

// channelsRequest mirrors the v2 bench channel mutation JSON request.
type channelsRequest struct {
	RunID    string        `json:"run_id"`
	BatchID  string        `json:"batch_id"`
	Channels []channelItem `json:"channels"`
}

// channelItem describes one generated channel mutation item.
type channelItem struct {
	ChannelID     string `json:"channel_id"`
	ChannelType   int    `json:"channel_type"`
	AllowStranger bool   `json:"allow_stranger"`
}

// subscribersRequest mirrors the v2 bench channel subscriber mutation JSON request.
type subscribersRequest struct {
	RunID   string           `json:"run_id"`
	BatchID string           `json:"batch_id"`
	Items   []subscriberItem `json:"items"`
}

// subscriberItem describes one generated subscriber-list mutation item.
type subscriberItem struct {
	ChannelID   string   `json:"channel_id"`
	ChannelType int      `json:"channel_type"`
	Subscribers []string `json:"subscribers"`
}

// mutationResponse mirrors the v2 bench mutation JSON response.
type mutationResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// subscribersResponse mirrors the v2 bench subscribers mutation JSON response.
type subscribersResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func newTargetClient(servers []string, token string) *targetClient {
	return &targetClient{
		servers:    normalizeServers(servers),
		token:      strings.TrimSpace(token),
		httpClient: http.DefaultClient,
	}
}

func (c *targetClient) preflight(ctx context.Context) (targetPreflight, error) {
	var result targetPreflight
	for _, server := range c.servers {
		if err := c.checkStatus(ctx, server, "/healthz"); err != nil {
			return targetPreflight{}, err
		}
		if err := c.checkStatus(ctx, server, "/readyz"); err != nil {
			return targetPreflight{}, err
		}
		caps, err := c.getCapabilities(ctx, server)
		if err != nil {
			return targetPreflight{}, err
		}
		if err := validateCapabilities(server, caps); err != nil {
			return targetPreflight{}, err
		}
		if caps.Limits.MaxBatchSize > 0 && (result.MaxBatchSize == 0 || caps.Limits.MaxBatchSize < result.MaxBatchSize) {
			result.MaxBatchSize = caps.Limits.MaxBatchSize
		}
		capacity, err := c.getCapacityTarget(ctx, server)
		if err != nil {
			return targetPreflight{}, err
		}
		if capacity.Gateway.TCPAddr != "" {
			result.GatewayTCPAddrs = append(result.GatewayTCPAddrs, capacity.Gateway.TCPAddr)
		}
	}
	result.GatewayTCPAddrs = dedupeValues(splitValues(result.GatewayTCPAddrs))
	if len(result.GatewayTCPAddrs) == 0 {
		return targetPreflight{}, fmt.Errorf("target did not report gateway TCP addresses: set WK_EXTERNAL_TCPADDR or pass --gateway")
	}
	return result, nil
}

func (c *targetClient) setup(ctx context.Context, cfg Config, target targetPreflight, plan Plan) error {
	batchSize := target.MaxBatchSize
	if batchSize <= 0 {
		batchSize = len(plan.Groups)
	}
	if batchSize <= 0 {
		return nil
	}
	for start, batchIndex := 0, 0; start < len(plan.Groups); start, batchIndex = start+batchSize, batchIndex+1 {
		end := start + batchSize
		if end > len(plan.Groups) {
			end = len(plan.Groups)
		}
		groups := plan.Groups[start:end]
		channels := make([]channelItem, 0, len(groups))
		subscribers := make([]subscriberItem, 0, len(groups))
		for _, group := range groups {
			channels = append(channels, channelItem{
				ChannelID:     group.ChannelID,
				ChannelType:   groupChannelType,
				AllowStranger: true,
			})
			subscribers = append(subscribers, subscriberItem{
				ChannelID:   group.ChannelID,
				ChannelType: groupChannelType,
				Subscribers: append([]string(nil), group.Subscribers...),
			})
		}
		if err := c.postMutation(ctx, "/bench/v1/channels", channelsRequest{
			RunID:    cfg.RunID,
			BatchID:  fmt.Sprintf("channels-%06d", batchIndex),
			Channels: channels,
		}); err != nil {
			return err
		}
		if err := c.postMutation(ctx, "/bench/v1/channels/subscribers", subscribersRequest{
			RunID:   cfg.RunID,
			BatchID: fmt.Sprintf("subscribers-%06d", batchIndex),
			Items:   subscribers,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *targetClient) checkStatus(ctx context.Context, server string, path string) error {
	resp, err := c.do(ctx, http.MethodGet, server, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s%s returned %s", server, path, resp.Status)
	}
	return nil
}

func (c *targetClient) getCapabilities(ctx context.Context, server string) (capabilitiesResponse, error) {
	var caps capabilitiesResponse
	resp, err := c.do(ctx, http.MethodGet, server, "/bench/v1/capabilities", nil)
	if err != nil {
		return caps, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return caps, fmt.Errorf("%s missing bench capabilities endpoint: set WK_BENCH_API_ENABLE=true", server)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return caps, fmt.Errorf("%s/bench/v1/capabilities returned %s; set WK_BENCH_API_ENABLE=true", server, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		return caps, fmt.Errorf("decode %s/bench/v1/capabilities: %w", server, err)
	}
	return caps, nil
}

func (c *targetClient) getCapacityTarget(ctx context.Context, server string) (capacityTargetResponse, error) {
	var capacity capacityTargetResponse
	resp, err := c.do(ctx, http.MethodGet, server, "/bench/v1/capacity-target", nil)
	if err != nil {
		return capacity, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return capacity, fmt.Errorf("%s/bench/v1/capacity-target returned %s", server, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&capacity); err != nil {
		return capacity, fmt.Errorf("decode %s/bench/v1/capacity-target: %w", server, err)
	}
	return capacity, nil
}

func (c *targetClient) postMutation(ctx context.Context, path string, value any) error {
	var lastErr error
	for _, server := range c.servers {
		if err := c.postJSON(ctx, server, path, value); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		return fmt.Errorf("no HTTP servers configured for %s", path)
	}
	return lastErr
}

func (c *targetClient) postJSON(ctx context.Context, server string, path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodPost, server, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s%s returned %s", server, path, resp.Status)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (c *targetClient) do(ctx context.Context, method string, server string, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, server+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s%s: %w", method, server, path, err)
	}
	return resp, nil
}

func validateCapabilities(server string, caps capabilitiesResponse) error {
	if !caps.Enabled {
		return fmt.Errorf("%s bench API is disabled: set WK_BENCH_API_ENABLE=true", server)
	}
	if caps.Version != benchVersion {
		return fmt.Errorf("%s bench capabilities version = %q, want %q", server, caps.Version, benchVersion)
	}
	if !caps.Supports.ChannelsBatch {
		return fmt.Errorf("%s bench capabilities missing channels_batch support", server)
	}
	if !caps.Supports.ChannelSubscribersBatch {
		return fmt.Errorf("%s bench capabilities missing channel_subscribers_batch support", server)
	}
	if !caps.Supports.Snapshot {
		return fmt.Errorf("%s bench capabilities missing snapshot support", server)
	}
	if !containsValue(caps.Supports.ChannelTypes, "group") {
		return fmt.Errorf("%s bench capabilities missing channel_types group support", server)
	}
	return nil
}

func normalizeServers(servers []string) []string {
	values := splitValues(servers)
	out := make([]string, 0, len(values))
	for _, value := range values {
		server := strings.TrimRight(value, "/")
		if !strings.Contains(server, "://") {
			server = "http://" + server
		}
		out = append(out, server)
	}
	return dedupeValues(out)
}

func containsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
