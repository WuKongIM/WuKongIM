package nodeops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Second

// Config controls manager HTTP access for node operations.
type Config struct {
	Server  string
	Token   string
	Timeout time.Duration
}

// Client calls manager HTTP node operation routes.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// APIError preserves non-2xx manager status and response body details.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Body == "" {
		return fmt.Sprintf("manager API status %d", e.StatusCode)
	}
	return fmt.Sprintf("manager API status %d: %s", e.StatusCode, e.Body)
}

// AsAPIError reports whether err wraps an APIError.
func AsAPIError(err error, target **APIError) bool {
	return errors.As(err, target)
}

// LifecycleResponse mirrors manager node lifecycle transition responses.
type LifecycleResponse struct {
	Changed   bool   `json:"changed"`
	NodeID    uint64 `json:"node_id"`
	Addr      string `json:"addr"`
	JoinState string `json:"join_state"`
	Revision  uint64 `json:"revision"`
}

// MoveRequest bounds one manager-driven slot movement step.
type MoveRequest struct {
	MaxSlotMoves uint32 `json:"max_slot_moves"`
}

// DrainRequest toggles gateway draining for a scale-in target.
type DrainRequest struct {
	Draining bool `json:"draining"`
}

// NodeScaleInStatus preserves manager root-cause fields for scale-in decisions.
type NodeScaleInStatus struct {
	NodeID                   uint64   `json:"node_id"`
	JoinState                string   `json:"join_state"`
	StateRevision            uint64   `json:"state_revision"`
	SafeToProceed            bool     `json:"safe_to_proceed"`
	SafeToRemove             bool     `json:"safe_to_remove"`
	BlockedByHealth          bool     `json:"blocked_by_health"`
	BlockedByStaleRevision   bool     `json:"blocked_by_stale_revision"`
	BlockedByControlRevision bool     `json:"blocked_by_control_revision"`
	BlockedBySlots           bool     `json:"blocked_by_slots"`
	BlockedBySlotLeadership  bool     `json:"blocked_by_slot_leadership"`
	BlockedBySlotRuntime     bool     `json:"blocked_by_slot_runtime"`
	BlockedByTasks           bool     `json:"blocked_by_tasks"`
	BlockedByChannels        bool     `json:"blocked_by_channels"`
	BlockedByRuntimeDrain    bool     `json:"blocked_by_runtime_drain"`
	UnknownRuntime           bool     `json:"unknown_runtime"`
	RuntimeUnknown           bool     `json:"runtime_unknown"`
	UnknownControlRevision   bool     `json:"unknown_control_revision"`
	UnknownChannelInventory  bool     `json:"unknown_channel_inventory"`
	HealthFresh              bool     `json:"health_fresh"`
	HealthStatus             string   `json:"health_status"`
	HealthFreshness          string   `json:"health_freshness"`
	ObservedControlRevision  uint64   `json:"observed_control_revision"`
	RequiredControlRevision  uint64   `json:"required_control_revision"`
	BlockedReasons           []string `json:"blocked_reasons"`
	SlotReplicaCount         int      `json:"slot_replica_count"`
	SlotLeaderCount          int      `json:"slot_leader_count"`
	ActiveTaskCount          int      `json:"active_task_count"`
	FailedTaskCount          int      `json:"failed_task_count"`
	ChannelLeaderCount       int      `json:"channel_leader_count"`
	ChannelReplicaCount      int      `json:"channel_replica_count"`
	ChannelISRCount          int      `json:"channel_isr_count"`
	GatewayDraining          bool     `json:"gateway_draining"`
	AcceptingNewSessions     bool     `json:"accepting_new_sessions"`
	GatewaySessions          int      `json:"gateway_sessions"`
	ActiveOnline             int      `json:"active_online"`
	ClosingOnline            int      `json:"closing_online"`
	TotalOnline              int      `json:"total_online"`
	PendingActivations       int      `json:"pending_activations"`
}

// NewClient returns a manager HTTP client with conservative defaults.
func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.Server), "/"),
		token:   strings.TrimSpace(cfg.Token),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) doJSON(ctx context.Context, method, path string, in any, out any) error {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return errors.New("manager server must be configured")
	}
	endpoint, err := c.endpoint(path)
	if err != nil {
		return err
	}

	var body io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("build request %s %s: %w", method, endpoint, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call manager %s %s: %w", method, endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if readErr != nil {
			return fmt.Errorf("read manager error response: %w", readErr)
		}
		return &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode manager response: %w", err)
	}
	return nil
}

func (c *Client) endpoint(path string) (string, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("manager server %q must be an absolute http or https URL", c.baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("manager server %q must use http or https", c.baseURL)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func nodePath(nodeID uint64, suffix string) string {
	return fmt.Sprintf("/manager/nodes/%d/%s", nodeID, strings.TrimLeft(suffix, "/"))
}

func (c *Client) ListNodes(ctx context.Context, out any) error {
	return c.doJSON(ctx, http.MethodGet, "/manager/nodes", nil, out)
}

func (c *Client) ActivateNode(ctx context.Context, nodeID uint64) (LifecycleResponse, error) {
	var out LifecycleResponse
	err := c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "activate"), nil, &out)
	return out, err
}

func (c *Client) OnboardingPlan(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "onboarding/plan"), MoveRequest{MaxSlotMoves: maxSlotMoves}, out)
}

func (c *Client) OnboardingStart(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "onboarding/start"), MoveRequest{MaxSlotMoves: maxSlotMoves}, out)
}

func (c *Client) OnboardingAdvance(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "onboarding/advance"), MoveRequest{MaxSlotMoves: maxSlotMoves}, out)
}

func (c *Client) OnboardingStatus(ctx context.Context, nodeID uint64, out any) error {
	return c.doJSON(ctx, http.MethodGet, nodePath(nodeID, "onboarding/status"), nil, out)
}

func (c *Client) ScaleInStart(ctx context.Context, nodeID uint64) (LifecycleResponse, error) {
	var out LifecycleResponse
	err := c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "scale-in/start"), nil, &out)
	return out, err
}

func (c *Client) ScaleInPlan(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "scale-in/plan"), MoveRequest{MaxSlotMoves: maxSlotMoves}, out)
}

func (c *Client) ScaleInAdvance(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "scale-in/advance"), MoveRequest{MaxSlotMoves: maxSlotMoves}, out)
}

func (c *Client) SetScaleInDrain(ctx context.Context, nodeID uint64, draining bool, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "scale-in/drain"), DrainRequest{Draining: draining}, out)
}

func (c *Client) ScaleInStatus(ctx context.Context, nodeID uint64) (NodeScaleInStatus, error) {
	var out NodeScaleInStatus
	err := c.doJSON(ctx, http.MethodGet, nodePath(nodeID, "scale-in/status"), nil, &out)
	return out, err
}

func (c *Client) RemoveScaleInNode(ctx context.Context, nodeID uint64) (LifecycleResponse, error) {
	var out LifecycleResponse
	err := c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "scale-in/remove"), nil, &out)
	return out, err
}
