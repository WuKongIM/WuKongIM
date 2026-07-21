// Package cloudview exposes one run-scoped browser gateway for a Cloud
// Simulation. It proxies only to the private endpoints declared in Options.
package cloudview

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/cloudviewstate"
)

var errInvalidOptions = errors.New("internal/access/cloudview: invalid options")

// GateProbeHeader identifies automated provisioning checks that must not make
// an otherwise untouched benchmark run interactive.
const GateProbeHeader = "X-WuKongIM-Cloud-View-Gate"

const stateHealthHeader = "X-WuKongIM-Cloud-View-State"

type statusResponse struct {
	cloudviewstate.State
	// PersistenceHealthy is false while a failed durable projection is retrying.
	PersistenceHealthy bool `json:"persistence_healthy"`
	// ObservedIPv4 is the direct transport peer used to reach this Cloud View.
	ObservedIPv4 string `json:"observed_ipv4"`
}

// NodeUpstream contains the private endpoints for one WuKongIM cluster node.
// The API readiness endpoint is the health authority for all three endpoints.
type NodeUpstream struct {
	// ID is the positive cluster node identifier.
	ID uint64 `json:"id"`
	// APIBaseURL is the node API origin, including the /readyz endpoint.
	APIBaseURL string `json:"api_base_url"`
	// ManagerBaseURL is the node Manager HTTP origin.
	ManagerBaseURL string `json:"manager_base_url"`
	// WebSocketBaseURL is the node WebSocket gateway origin.
	WebSocketBaseURL string `json:"websocket_base_url"`
}

// Options configures one run-scoped Cloud Simulation browser gateway.
type Options struct {
	// RunID is the exact Simulation Run identity written into Cloud View state.
	RunID string `json:"run_id"`
	// PublicBaseURL is the externally reachable HTTP origin advertised to Demo clients.
	PublicBaseURL string `json:"public_base_url"`
	// Nodes contains the private cluster upstreams eligible for health-based routing.
	Nodes []NodeUpstream `json:"nodes"`
	// PrometheusURL is the simulator-local Prometheus HTTP origin.
	PrometheusURL string `json:"prometheus_url"`
	// HealthCheckTimeout bounds one node readiness request.
	HealthCheckTimeout time.Duration `json:"-"`
	// HealthCheckInterval is the maximum age of one cached readiness result.
	HealthCheckInterval time.Duration `json:"-"`
	// Limits bounds public HTTP request rates and WebSocket concurrency.
	Limits Limits `json:"limits"`
	// StatePath is the optional durable JSON marker written on interaction changes.
	StatePath string `json:"state_path"`
	// MetricsPath is the optional node_exporter textfile marker path.
	MetricsPath string `json:"metrics_path"`
	// GateProbeToken authenticates automated provisioning probes that are
	// excluded from benchmark-purity state transitions.
	GateProbeToken string `json:"gate_probe_token"`
}

type parsedNode struct {
	// id is the cluster node identity.
	id uint64
	// api is the private product API origin.
	api *url.URL
	// manager is the private Manager origin.
	manager *url.URL
	// websocket is the private gateway origin.
	websocket *url.URL
}

// Server routes browser traffic to healthy private Simulation Run endpoints.
type Server struct {
	// publicBaseURL is the public origin advertised by route rewrites.
	publicBaseURL *url.URL
	// nodes contains the private upstreams eligible for request routing.
	nodes []parsedNode
	// prometheus is the simulator-local Prometheus origin.
	prometheus *url.URL
	// healthClient performs bounded readiness probes.
	healthClient *http.Client
	// handler is the complete public HTTP surface.
	handler http.Handler
	// nextNode drives round-robin selection across healthy nodes.
	nextNode atomic.Uint64
	// healthMu protects cached health state.
	healthMu sync.Mutex
	// healthy stores cached readiness by node index.
	healthy []bool
	// healthChecked records when the readiness cache was last refreshed.
	healthChecked time.Time
	// healthRefreshing reports that one readiness refresh is in flight.
	healthRefreshing bool
	// healthRefreshDone is closed when the in-flight refresh completes.
	healthRefreshDone chan struct{}
	// healthInterval bounds readiness cache age.
	healthInterval time.Duration
	// limiter owns per-source and global public traffic limits.
	limiter *requestLimiter
	// state persists benchmark-purity transitions.
	state *cloudviewstate.Recorder
	// gateProbeToken identifies automated provisioning requests.
	gateProbeToken string
}

// New validates options and creates an HTTP handler without opening a listener.
func New(options Options) (*Server, error) {
	publicBaseURL, err := parseHTTPURL(options.PublicBaseURL)
	if err != nil || strings.TrimSpace(options.RunID) == "" || len(options.Nodes) == 0 {
		return nil, errInvalidOptions
	}
	nodes := make([]parsedNode, 0, len(options.Nodes))
	seen := make(map[uint64]struct{}, len(options.Nodes))
	for _, configured := range options.Nodes {
		api, apiErr := parseHTTPURL(configured.APIBaseURL)
		manager, managerErr := parseHTTPURL(configured.ManagerBaseURL)
		websocket, websocketErr := parseHTTPURL(configured.WebSocketBaseURL)
		if configured.ID == 0 || apiErr != nil || managerErr != nil || websocketErr != nil {
			return nil, errInvalidOptions
		}
		if _, ok := seen[configured.ID]; ok {
			return nil, errInvalidOptions
		}
		seen[configured.ID] = struct{}{}
		nodes = append(nodes, parsedNode{id: configured.ID, api: api, manager: manager, websocket: websocket})
	}
	if options.HealthCheckTimeout <= 0 {
		options.HealthCheckTimeout = 2 * time.Second
	}
	if options.HealthCheckInterval <= 0 {
		options.HealthCheckInterval = 2 * time.Second
	}
	limits, err := normalizeLimits(options.Limits)
	if err != nil {
		return nil, err
	}
	server := &Server{
		publicBaseURL:  publicBaseURL,
		nodes:          nodes,
		healthClient:   &http.Client{Timeout: options.HealthCheckTimeout},
		healthy:        make([]bool, len(nodes)),
		healthInterval: options.HealthCheckInterval,
		limiter:        newRequestLimiter(limits),
		gateProbeToken: strings.TrimSpace(options.GateProbeToken),
	}
	server.state, err = cloudviewstate.New(options.RunID, options.StatePath, options.MetricsPath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(options.PrometheusURL) != "" {
		server.prometheus, err = parseHTTPURL(options.PrometheusURL)
		if err != nil {
			return nil, errInvalidOptions
		}
	}
	server.handler = http.HandlerFunc(server.serveHTTP)
	return server, nil
}

// Handler returns the complete public HTTP surface.
func (s *Server) Handler() http.Handler {
	if s == nil || s.handler == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "cloud view unavailable", http.StatusServiceUnavailable)
		})
	}
	return s.handler
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if isWebSocketUpgrade(r) {
		release, ok := s.limiter.acquireWebSocket(requestSource(r), time.Now())
		if !ok {
			http.Error(w, "websocket connection limit exceeded", http.StatusTooManyRequests)
			return
		}
		defer release()
	} else if !s.limiter.allowHTTP(requestSource(r), time.Now()) {
		http.Error(w, "http rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	if r.URL.Path == "/cloud-view/status" {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		state, persistenceHealthy := s.state.StatusSnapshot()
		_ = json.NewEncoder(w).Encode(statusResponse{
			State: state, PersistenceHealthy: persistenceHealthy, ObservedIPv4: requestIPv4(r),
		})
		return
	}
	if r.URL.Path == "/prometheus" {
		http.Redirect(w, r, "/prometheus/", http.StatusPermanentRedirect)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/prometheus/") {
		s.servePrometheus(w, r)
		return
	}
	if !s.isGateProbe(r) {
		var stateErr error
		if isWebSocketUpgrade(r) || isDemoPath(r.URL.Path) {
			stateErr = s.state.MarkInteractive()
		}
		if stateErr == nil && isManagerWrite(r) {
			stateErr = s.state.MarkOperatorModified()
		}
		if stateErr != nil {
			w.Header().Set(stateHealthHeader, "degraded")
			http.Error(w, "cloud view state persistence unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	node := s.nextHealthyNode(r)
	if node == nil {
		http.Error(w, "no healthy cloud simulation node", http.StatusServiceUnavailable)
		return
	}
	s.serveNodeProxy(w, r, node, 0)
}

func (s *Server) serveNodeProxy(w http.ResponseWriter, r *http.Request, node *parsedNode, attempt int) {
	target := node.manager
	if isWebSocketUpgrade(r) {
		target = node.websocket
	} else if isProductAPIPath(r.URL.Path) {
		target = node.api
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, _ error) {
		s.markNodeUnhealthy(node.id)
		if retryableProxyRequest(request) && attempt+1 < len(s.nodes) {
			if next := s.nextHealthyNode(request); next != nil {
				s.serveNodeProxy(writer, request, next, attempt+1)
				return
			}
		}
		http.Error(writer, "cloud simulation upstream unavailable", http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		if r.URL.Path == "/route" {
			if err := s.rewriteRouteResponse(response); err != nil {
				return err
			}
		}
		return nil
	}
	proxy.ServeHTTP(w, r)
}

func retryableProxyRequest(request *http.Request) bool {
	if request == nil || (request.Body != nil && request.Body != http.NoBody && request.GetBody == nil) {
		return false
	}
	if isWebSocketUpgrade(request) {
		return true
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func (s *Server) isGateProbe(request *http.Request) bool {
	if request == nil || s.gateProbeToken == "" {
		return false
	}
	provided := request.Header.Get(GateProbeHeader)
	return len(provided) == len(s.gateProbeToken) &&
		subtle.ConstantTimeCompare([]byte(provided), []byte(s.gateProbeToken)) == 1
}

func (s *Server) markNodeUnhealthy(nodeID uint64) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	for index := range s.nodes {
		if s.nodes[index].id == nodeID {
			s.healthy[index] = false
			return
		}
	}
}

func isDemoPath(path string) bool {
	return path == "/demo" || strings.HasPrefix(path, "/demo/") || isProductAPIPath(path)
}

func isManagerWrite(request *http.Request) bool {
	if request == nil || request.URL.Path == "/manager/login" ||
		!(request.URL.Path == "/manager" || strings.HasPrefix(request.URL.Path, "/manager/")) {
		return false
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func isWebSocketUpgrade(request *http.Request) bool {
	if request == nil || !strings.EqualFold(strings.TrimSpace(request.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, token := range strings.Split(request.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			return true
		}
	}
	return false
}

func (s *Server) servePrometheus(w http.ResponseWriter, r *http.Request) {
	if s.prometheus == nil {
		http.Error(w, "prometheus unavailable", http.StatusServiceUnavailable)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(s.prometheus)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.URL.Path = strings.TrimPrefix(request.URL.Path, "/prometheus")
		if request.URL.Path == "" {
			request.URL.Path = "/"
		}
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(writer, "prometheus unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) rewriteRouteResponse(response *http.Response) error {
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	publicWebSocket := *s.publicBaseURL
	publicWebSocket.Path = ""
	switch publicWebSocket.Scheme {
	case "http":
		publicWebSocket.Scheme = "ws"
		payload["ws_addr"] = publicWebSocket.String()
		payload["wss_addr"] = ""
	case "https":
		publicWebSocket.Scheme = "wss"
		payload["ws_addr"] = ""
		payload["wss_addr"] = publicWebSocket.String()
	}
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	response.Body = io.NopCloser(bytes.NewReader(rewritten))
	response.ContentLength = int64(len(rewritten))
	response.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	return nil
}

func isProductAPIPath(path string) bool {
	for _, prefix := range []string{
		"/demo", "/route", "/user", "/channel", "/tmpchannel", "/message", "/conversation", "/conversations",
		"/streammessage",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (s *Server) nextHealthyNode(r *http.Request) *parsedNode {
	healthState := s.healthState(r)
	healthy := make([]*parsedNode, 0, len(s.nodes))
	for index := range s.nodes {
		node := &s.nodes[index]
		if index >= len(healthState) || !healthState[index] {
			continue
		}
		healthy = append(healthy, node)
	}
	if len(healthy) == 0 {
		return nil
	}
	index := (s.nextNode.Add(1) - 1) % uint64(len(healthy))
	return healthy[index]
}

func (s *Server) healthState(r *http.Request) []bool {
	s.healthMu.Lock()
	if !s.healthChecked.IsZero() && time.Since(s.healthChecked) < s.healthInterval {
		result := append([]bool(nil), s.healthy...)
		s.healthMu.Unlock()
		return result
	}
	if !s.healthChecked.IsZero() {
		if !s.healthRefreshing {
			s.healthRefreshing = true
			s.healthRefreshDone = make(chan struct{})
			go s.refreshHealth(context.Background())
		}
		result := append([]bool(nil), s.healthy...)
		s.healthMu.Unlock()
		return result
	}
	if s.healthRefreshing {
		done := s.healthRefreshDone
		s.healthMu.Unlock()
		select {
		case <-r.Context().Done():
			return nil
		case <-done:
			s.healthMu.Lock()
			result := append([]bool(nil), s.healthy...)
			s.healthMu.Unlock()
			return result
		}
	}
	s.healthRefreshing = true
	s.healthRefreshDone = make(chan struct{})
	s.healthMu.Unlock()
	s.refreshHealth(r.Context())
	s.healthMu.Lock()
	result := append([]bool(nil), s.healthy...)
	s.healthMu.Unlock()
	return result
}

func (s *Server) refreshHealth(ctx context.Context) {
	type healthResult struct {
		index   int
		healthy bool
	}
	results := make(chan healthResult, len(s.nodes))
	resultsByIndex := make([]bool, len(s.nodes))
	for index := range s.nodes {
		go func(index int, node parsedNode) {
			readyURL := *node.api
			readyURL.Path = strings.TrimRight(readyURL.Path, "/") + "/readyz"
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL.String(), nil)
			if err != nil {
				results <- healthResult{index: index}
				return
			}
			response, err := s.healthClient.Do(request)
			if err != nil {
				results <- healthResult{index: index}
				return
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
			_ = response.Body.Close()
			results <- healthResult{
				index: index, healthy: response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices,
			}
		}(index, s.nodes[index])
	}
	for range s.nodes {
		result := <-results
		resultsByIndex[result.index] = result.healthy
	}
	s.healthMu.Lock()
	copy(s.healthy, resultsByIndex)
	s.healthChecked = time.Now()
	s.healthRefreshing = false
	close(s.healthRefreshDone)
	s.healthRefreshDone = nil
	s.healthMu.Unlock()
}

func parseHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errInvalidOptions
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, errInvalidOptions
	}
	parsed.Path = ""
	return parsed, nil
}
