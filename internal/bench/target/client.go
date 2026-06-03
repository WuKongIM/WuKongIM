package target

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
)

const defaultTimeout = 60 * time.Second

// Config controls the black-box target bench API client.
type Config struct {
	// APIAddrs are target HTTP API base addresses tried in deterministic order.
	APIAddrs []string
	// Token is an optional bearer token for protected bench API routes.
	Token string
	// HTTPClient overrides the default HTTP client for tests or custom transports.
	HTTPClient *http.Client
}

// Client calls the target HTTP API without importing server internals.
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient creates a target API client using stdlib HTTP and JSON only.
func NewClient(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{cfg: cfg, http: hc}
}

// Healthz checks /healthz on configured target API addresses.
func (c *Client) Healthz(ctx context.Context) error {
	return c.getAny(ctx, "/healthz", nil)
}

// Readyz checks /readyz on configured target API addresses.
func (c *Client) Readyz(ctx context.Context) error {
	return c.getAny(ctx, "/readyz", nil)
}

// Capabilities reads the target bench/v1 capability document.
func (c *Client) Capabilities(ctx context.Context) (model.BenchCapabilities, error) {
	var out model.BenchCapabilities
	if err := c.getAny(ctx, "/bench/v1/capabilities", &out); err != nil {
		return model.BenchCapabilities{}, fmt.Errorf("bench api capabilities unavailable: %w", err)
	}
	return out, nil
}

// Snapshot reads a lightweight target bench setup snapshot.
func (c *Client) Snapshot(ctx context.Context) (model.BenchSnapshot, error) {
	var out model.BenchSnapshot
	if err := c.getAny(ctx, "/bench/v1/snapshot", &out); err != nil {
		return model.BenchSnapshot{}, err
	}
	return out, nil
}

// PresenceSnapshots reads connection-route presence snapshots from every target API address.
// When any target fails, it returns the successfully decoded snapshots with a non-nil error.
func (c *Client) PresenceSnapshots(ctx context.Context) ([]model.PresenceSnapshot, error) {
	addrs := c.addrs()
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no target api addresses configured")
	}
	snapshots := make([]model.PresenceSnapshot, 0, len(addrs))
	var errs []string
	for _, addr := range addrs {
		var out model.PresenceSnapshot
		if err := c.doJSON(ctx, http.MethodGet, addr, "/bench/v1/presence/snapshot", nil, &out); err != nil {
			if isUnsupportedStatus(err) {
				continue
			}
			errs = append(errs, err.Error())
			continue
		}
		snapshots = append(snapshots, out)
	}
	if len(errs) > 0 {
		return snapshots, fmt.Errorf("one or more target api addresses failed: %s", strings.Join(errs, "; "))
	}
	return snapshots, nil
}

// CapacityTarget reads the target node address document used by capacity tests.
func (c *Client) CapacityTarget(ctx context.Context) (model.CapacityTarget, error) {
	var out model.CapacityTarget
	if err := c.getAny(ctx, "/bench/v1/capacity-target", &out); err != nil {
		return model.CapacityTarget{}, fmt.Errorf("bench api capacity target unavailable: %w", err)
	}
	return out, nil
}

// ChannelRuntimeSnapshots reads local runtime snapshots from every target API address.
// When any target fails, it returns the successfully decoded snapshots with a non-nil error.
func (c *Client) ChannelRuntimeSnapshots(ctx context.Context, query model.ChannelRuntimeQuery) ([]model.ChannelRuntimeSnapshot, error) {
	addrs := c.addrs()
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no target api addresses configured")
	}
	path := "/bench/v1/channel-runtime/snapshot" + channelRuntimeQueryString(query)
	snapshots := make([]model.ChannelRuntimeSnapshot, 0, len(addrs))
	var errs []string
	for _, addr := range addrs {
		var out model.ChannelRuntimeSnapshot
		if err := c.doJSON(ctx, http.MethodGet, addr, path, nil, &out); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		snapshots = append(snapshots, out)
	}
	if len(errs) > 0 {
		return snapshots, fmt.Errorf("one or more target api addresses failed: %s", strings.Join(errs, "; "))
	}
	return snapshots, nil
}

// ProbeChannelRuntime posts a bounded local runtime probe request.
func (c *Client) ProbeChannelRuntime(ctx context.Context, req model.ChannelRuntimeProbeRequest) (model.ChannelRuntimeProbeResult, error) {
	var out model.ChannelRuntimeProbeResult
	if err := c.postAnyOut(ctx, "/bench/v1/channel-runtime/probe", req, &out); err != nil {
		return model.ChannelRuntimeProbeResult{}, err
	}
	return out, nil
}

// ProbeChannelRuntimeAll asks every configured target node to inspect selected generated channels.
func (c *Client) ProbeChannelRuntimeAll(ctx context.Context, req model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error) {
	results := make([]model.ChannelRuntimeProbeResult, 0, len(c.addrs()))
	err := c.postAll(func(addr string) error {
		var out model.ChannelRuntimeProbeResult
		if err := c.doJSON(ctx, http.MethodPost, addr, "/bench/v1/channel-runtime/probe", req, &out); err != nil {
			return err
		}
		results = append(results, out)
		return nil
	})
	return results, err
}

// EvictChannelRuntime posts a bounded local runtime eviction request.
func (c *Client) EvictChannelRuntime(ctx context.Context, req model.ChannelRuntimeEvictRequest) (model.ChannelRuntimeEvictResult, error) {
	var out model.ChannelRuntimeEvictResult
	if err := c.postAnyOut(ctx, "/bench/v1/channel-runtime/evict", req, &out); err != nil {
		return model.ChannelRuntimeEvictResult{}, err
	}
	return out, nil
}

// EvictChannelRuntimeAll asks every configured target node to evict selected generated runtime state.
func (c *Client) EvictChannelRuntimeAll(ctx context.Context, req model.ChannelRuntimeEvictRequest) ([]model.ChannelRuntimeEvictResult, error) {
	results := make([]model.ChannelRuntimeEvictResult, 0, len(c.addrs()))
	err := c.postAll(func(addr string) error {
		var out model.ChannelRuntimeEvictResult
		if err := c.doJSON(ctx, http.MethodPost, addr, "/bench/v1/channel-runtime/evict", req, &out); err != nil {
			return err
		}
		results = append(results, out)
		return nil
	})
	return results, err
}

func (c *Client) postAll(call func(addr string) error) error {
	addrs := c.addrs()
	if len(addrs) == 0 {
		return fmt.Errorf("no target api addresses configured")
	}
	var errs []string
	for _, addr := range addrs {
		if err := call(addr); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) != 0 {
		return fmt.Errorf("target api addresses failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// UpsertTokens posts a spec-shaped batch user token request.
func (c *Client) UpsertTokens(ctx context.Context, req model.BatchTokensRequest) error {
	return c.postAny(ctx, "/bench/v1/users/tokens", req)
}

// UpsertChannels posts a spec-shaped batch channel upsert request.
func (c *Client) UpsertChannels(ctx context.Context, req model.BatchChannelsRequest) error {
	return c.postAny(ctx, "/bench/v1/channels", req)
}

// AddSubscribers posts a spec-shaped batch subscribers request.
func (c *Client) AddSubscribers(ctx context.Context, req model.BatchSubscribersRequest) error {
	return c.postAny(ctx, "/bench/v1/channels/subscribers", req)
}

func (c *Client) getAny(ctx context.Context, path string, out any) error {
	addrs := c.addrs()
	if len(addrs) == 0 {
		return fmt.Errorf("no target api addresses configured")
	}
	var errs []string
	for _, addr := range addrs {
		attemptOut, err := freshDecodeTarget(out)
		if err != nil {
			return err
		}
		if err := c.doJSON(ctx, http.MethodGet, addr, path, nil, attemptOut); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		copyDecodeTarget(out, attemptOut)
		return nil
	}
	return fmt.Errorf("all target api addresses failed: %s", strings.Join(errs, "; "))
}

func (c *Client) postAny(ctx context.Context, path string, body any) error {
	return c.postAnyOut(ctx, path, body, nil)
}

func (c *Client) postAnyOut(ctx context.Context, path string, body any, out any) error {
	addrs := c.addrs()
	if len(addrs) == 0 {
		return fmt.Errorf("no target api addresses configured")
	}
	var errs []string
	for _, addr := range addrs {
		attemptOut, err := freshDecodeTarget(out)
		if err != nil {
			return err
		}
		if err := c.doJSON(ctx, http.MethodPost, addr, path, body, attemptOut); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		copyDecodeTarget(out, attemptOut)
		return nil
	}
	return fmt.Errorf("all target api addresses failed: %s", strings.Join(errs, "; "))
}

func freshDecodeTarget(out any) (any, error) {
	if out == nil {
		return nil, nil
	}
	value := reflect.ValueOf(out)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return nil, fmt.Errorf("decode target must be a non-nil pointer")
	}
	return reflect.New(value.Elem().Type()).Interface(), nil
}

func copyDecodeTarget(out any, attemptOut any) {
	if out == nil {
		return
	}
	reflect.ValueOf(out).Elem().Set(reflect.ValueOf(attemptOut).Elem())
}

func (c *Client) doJSON(ctx context.Context, method, base, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, joinURL(base, path), reader)
	if err != nil {
		return fmt.Errorf("build %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, req.URL.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(method, req.URL.String(), resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, req.URL.String(), err)
	}
	return nil
}

func (c *Client) addrs() []string {
	addrs := make([]string, 0, len(c.cfg.APIAddrs))
	for _, addr := range c.cfg.APIAddrs {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

func statusError(method, url string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	snippet := strings.TrimSpace(string(body))
	return &httpStatusError{
		method:     method,
		url:        url,
		statusCode: resp.StatusCode,
		body:       snippet,
	}
}

type httpStatusError struct {
	method     string
	url        string
	statusCode int
	body       string
}

func (e *httpStatusError) Error() string {
	if e == nil {
		return "http status error"
	}
	if e.body == "" {
		return fmt.Sprintf("%s %s returned status %d", e.method, e.url, e.statusCode)
	}
	return fmt.Sprintf("%s %s returned status %d: %s", e.method, e.url, e.statusCode, e.body)
}

func isUnsupportedStatus(err error) bool {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.statusCode == http.StatusNotFound || statusErr.statusCode == http.StatusNotImplemented
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func channelRuntimeQueryString(query model.ChannelRuntimeQuery) string {
	parts := make([]string, 0, 5)
	if query.RunID != "" {
		parts = append(parts, "run_id="+url.QueryEscape(query.RunID))
	}
	if query.Profile != "" {
		parts = append(parts, "profile="+url.QueryEscape(query.Profile))
	}
	if query.ChannelType != 0 {
		parts = append(parts, "channel_type="+strconv.Itoa(int(query.ChannelType)))
	}
	if query.Range.Start != 0 {
		parts = append(parts, "start="+strconv.Itoa(query.Range.Start))
	}
	if query.Range.End != 0 {
		parts = append(parts, "end="+strconv.Itoa(query.Range.End))
	}
	if len(parts) == 0 {
		return ""
	}
	return "?" + strings.Join(parts, "&")
}
