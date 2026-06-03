package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
)

const versionV1 = "bench/v1"

// ErrListenAddrRequired reports that the HTTP API listen address is empty.
var ErrListenAddrRequired = errors.New("internalv2/access/api: listen address required")

// GatewayAddresses are the externally reachable client gateway addresses exposed to wkbench.
type GatewayAddresses struct {
	// TCPAddr is the WKProto TCP gateway address used by wkbench workers.
	TCPAddr string `json:"tcp_addr"`
	// WSAddr is the WebSocket gateway address reserved for future benchmark workers.
	WSAddr string `json:"ws_addr"`
	// WSSAddr is the secure WebSocket gateway address reserved for future benchmark workers.
	WSSAddr string `json:"wss_addr"`
}

// ChannelRuntimeBenchController exposes benchmark-only ChannelV2 runtime controls.
type ChannelRuntimeBenchController interface {
	Snapshot(context.Context, model.ChannelRuntimeQuery) (model.ChannelRuntimeSnapshot, error)
	Probe(context.Context, model.ChannelRuntimeQuery) (model.ChannelRuntimeProbeResult, error)
	Evict(context.Context, model.ChannelRuntimeQuery) (model.ChannelRuntimeEvictResult, error)
}

// PresenceBenchController exposes benchmark-only presence route diagnostics.
type PresenceBenchController interface {
	Snapshot(context.Context) (model.PresenceSnapshot, error)
}

// BenchData accepts benchmark setup mutations backed by the composition root.
type BenchData interface {
	UpsertChannels(context.Context, []BenchChannelMutation) (int, error)
	AddSubscribers(context.Context, []BenchSubscriberMutation) (int, error)
}

// BenchChannelMutation describes one benchmark channel metadata upsert.
type BenchChannelMutation struct {
	// ChannelID identifies the benchmark channel.
	ChannelID string
	// ChannelType is the WuKong channel type for ChannelID.
	ChannelType uint8
	// Large marks a large-group channel.
	Large bool
	// Ban blocks channel messaging when true.
	Ban bool
	// Disband marks a channel as disbanded.
	Disband bool
	// SendBan blocks sending while allowing receives.
	SendBan bool
	// AllowStranger permits stranger sends for compatible legacy semantics.
	AllowStranger bool
}

// BenchSubscriberMutation describes one benchmark channel subscriber append.
type BenchSubscriberMutation struct {
	// ChannelID identifies the benchmark channel.
	ChannelID string
	// ChannelType is the WuKong channel type for ChannelID.
	ChannelType uint8
	// Subscribers are user IDs appended to the benchmark channel subscriber set.
	Subscribers []string
}

// Options configures the minimal internalv2 HTTP API server.
type Options struct {
	// ListenAddr is the HTTP API listen address. An empty value makes Start fail.
	ListenAddr string
	// Readyz reports whether the node is ready for benchmark traffic.
	Readyz func(context.Context) (bool, any)
	// BenchEnabled exposes unauthenticated /bench/v1/* routes for controlled benchmark runs.
	BenchEnabled bool
	// BenchMaxBatchSize limits top-level records accepted by one bench mutation request.
	BenchMaxBatchSize int
	// BenchMaxPayloadBytes limits bench mutation JSON request bodies in bytes.
	BenchMaxPayloadBytes int64
	// Gateway contains the published gateway addresses returned by /bench/v1/capacity-target.
	Gateway GatewayAddresses
	// BenchRuntime controls benchmark-only ChannelV2 runtime diagnostics when configured.
	BenchRuntime ChannelRuntimeBenchController
	// BenchPresence controls benchmark-only presence route diagnostics when configured.
	BenchPresence PresenceBenchController
	// BenchData stores benchmark channel/subscriber setup when configured.
	BenchData BenchData
	// MetricsHandler serves Prometheus metrics when configured.
	MetricsHandler http.Handler
	// PProfEnabled exposes net/http/pprof endpoints for controlled performance runs.
	PProfEnabled bool
}

// Server exposes health, readiness, and the minimum bench/v1 target surface for wukongimv2.
type Server struct {
	mu                   sync.RWMutex
	mux                  *http.ServeMux
	httpServer           *http.Server
	listener             net.Listener
	listenAddr           string
	addr                 string
	readyz               func(context.Context) (bool, any)
	benchEnabled         bool
	benchMaxBatchSize    int
	benchMaxPayloadBytes int64
	gateway              GatewayAddresses
	benchRuntime         ChannelRuntimeBenchController
	benchPresence        PresenceBenchController
	benchData            BenchData
	metricsHandler       http.Handler
	pprofEnabled         bool
	counts               map[string]int
	started              bool
}

// New creates a minimal internalv2 API server.
func New(opts Options) *Server {
	s := &Server{
		mux:                  http.NewServeMux(),
		listenAddr:           strings.TrimSpace(opts.ListenAddr),
		readyz:               opts.Readyz,
		benchEnabled:         opts.BenchEnabled,
		benchMaxBatchSize:    opts.BenchMaxBatchSize,
		benchMaxPayloadBytes: opts.BenchMaxPayloadBytes,
		gateway:              opts.Gateway,
		benchRuntime:         opts.BenchRuntime,
		benchPresence:        opts.BenchPresence,
		benchData:            opts.BenchData,
		metricsHandler:       opts.MetricsHandler,
		pprofEnabled:         opts.PProfEnabled,
		counts:               map[string]int{},
	}
	s.registerRoutes()
	return s
}

// Handler returns the HTTP handler for tests and in-process harnesses.
func (s *Server) Handler() http.Handler {
	if s == nil || s.mux == nil {
		return http.NotFoundHandler()
	}
	return s.mux
}

// Addr returns the bound listen address after Start succeeds.
func (s *Server) Addr() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// Start begins serving the HTTP API.
func (s *Server) Start() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	listenAddr := s.listenAddr
	handler := s.Handler()
	s.mu.Unlock()
	if listenAddr == "" {
		return ErrListenAddrRequired
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: handler}
	s.mu.Lock()
	s.listener = ln
	s.httpServer = httpServer
	s.addr = ln.Addr().String()
	s.started = true
	s.mu.Unlock()
	go func() {
		if serveErr := httpServer.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			// The lifecycle owner observes startup errors synchronously; runtime serve
			// errors are intentionally kept local for this benchmark-only surface.
		}
	}()
	return nil
}

// Stop gracefully shuts down the HTTP API.
func (s *Server) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	httpServer := s.httpServer
	s.httpServer = nil
	s.listener = nil
	s.started = false
	s.mu.Unlock()
	if httpServer == nil {
		return nil
	}
	return httpServer.Shutdown(ctx)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/healthz", s.method(http.MethodGet, s.handleHealthz))
	s.mux.HandleFunc("/readyz", s.method(http.MethodGet, s.handleReadyz))
	if s.metricsHandler != nil {
		s.mux.Handle("/metrics", s.metricsHandler)
	}
	if s.pprofEnabled {
		s.registerPProfRoutes()
	}
	if !s.benchEnabled {
		return
	}
	s.mux.HandleFunc("/bench/v1/capabilities", s.method(http.MethodGet, s.handleBenchCapabilities))
	s.mux.HandleFunc("/bench/v1/capacity-target", s.method(http.MethodGet, s.handleBenchCapacityTarget))
	s.mux.HandleFunc("/bench/v1/snapshot", s.method(http.MethodGet, s.handleBenchSnapshot))
	s.mux.HandleFunc("/bench/v1/presence/snapshot", s.method(http.MethodGet, s.handleBenchPresenceSnapshot))
	s.mux.HandleFunc("/bench/v1/channel-runtime/snapshot", s.method(http.MethodGet, s.handleBenchChannelRuntimeSnapshot))
	s.mux.HandleFunc("/bench/v1/channel-runtime/probe", s.method(http.MethodPost, s.handleBenchChannelRuntimeProbe))
	s.mux.HandleFunc("/bench/v1/channel-runtime/evict", s.method(http.MethodPost, s.handleBenchChannelRuntimeEvict))
	s.mux.HandleFunc("/bench/v1/users/tokens", s.method(http.MethodPost, s.handleBenchTokens))
	s.mux.HandleFunc("/bench/v1/channels", s.method(http.MethodPost, s.handleBenchChannels))
	s.mux.HandleFunc("/bench/v1/channels/subscribers", s.method(http.MethodPost, s.handleBenchSubscribers))
}

func (s *Server) method(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

func (s *Server) registerPProfRoutes() {
	s.mux.HandleFunc("/debug/pprof/", pprof.Index)
	s.mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	s.mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	s.mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	s.mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.readyz == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"ready": true})
		return
	}
	ready, body := s.readyz(r.Context())
	if ready {
		writeJSON(w, http.StatusOK, body)
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, body)
}

type capabilitiesResponse struct {
	// Enabled confirms the target exposes the benchmark-only API surface.
	Enabled bool `json:"enabled"`
	// Version is the target bench API version.
	Version string `json:"version"`
	// Supports lists preparation features supported by this target.
	Supports capabilitiesSupports `json:"supports"`
	// Limits lists request limits visible to wkbench.
	Limits capabilitiesLimits `json:"limits"`
}

type capabilitiesSupports struct {
	UsersTokensBatch        bool `json:"users_tokens_batch"`
	ChannelsBatch           bool `json:"channels_batch"`
	ChannelSubscribersBatch bool `json:"channel_subscribers_batch"`
	Snapshot                bool `json:"snapshot"`
	// PresenceSnapshot indicates support for connection-route presence snapshots.
	PresenceSnapshot bool `json:"presence_snapshot"`
	// ChannelRuntimeSnapshot indicates support for local ChannelV2 runtime snapshots.
	ChannelRuntimeSnapshot bool `json:"channel_runtime_snapshot"`
	// ChannelRuntimeProbe indicates support for bounded ChannelV2 runtime probes.
	ChannelRuntimeProbe bool `json:"channel_runtime_probe"`
	// ChannelRuntimeEvict indicates support for bounded ChannelV2 runtime eviction.
	ChannelRuntimeEvict bool `json:"channel_runtime_evict"`
	// ChannelRuntimeFaults indicates support for runtime fault injection controls.
	ChannelRuntimeFaults bool `json:"channel_runtime_faults"`
	// ChannelRuntimeActivate indicates support for server-side diagnostic activation.
	ChannelRuntimeActivate bool     `json:"channel_runtime_activate"`
	ChannelTypes           []string `json:"channel_types"`
}

type capabilitiesLimits struct {
	MaxBatchSize    int   `json:"max_batch_size"`
	MaxPayloadBytes int64 `json:"max_payload_bytes"`
}

func (s *Server) handleBenchCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, capabilitiesResponse{
		Enabled: true,
		Version: versionV1,
		Supports: capabilitiesSupports{
			UsersTokensBatch:        true,
			ChannelsBatch:           s.benchData != nil,
			ChannelSubscribersBatch: s.benchData != nil,
			Snapshot:                true,
			PresenceSnapshot:        s.benchPresence != nil,
			ChannelRuntimeSnapshot:  s.benchRuntime != nil,
			ChannelRuntimeProbe:     s.benchRuntime != nil,
			ChannelRuntimeEvict:     s.benchRuntime != nil,
			ChannelRuntimeFaults:    false,
			ChannelRuntimeActivate:  false,
			ChannelTypes:            []string{"group"},
		},
		Limits: capabilitiesLimits{
			MaxBatchSize:    s.benchMaxBatchSize,
			MaxPayloadBytes: s.benchMaxPayloadBytes,
		},
	})
}

type capacityTargetResponse struct {
	// Version is the benchmark API version that produced this target document.
	Version string `json:"version"`
	// Gateway contains gateway addresses published by this target node.
	Gateway GatewayAddresses `json:"gateway"`
}

func (s *Server) handleBenchCapacityTarget(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, capacityTargetResponse{Version: versionV1, Gateway: s.gateway})
}

type snapshotResponse struct {
	Version string         `json:"version"`
	Counts  map[string]int `json:"counts,omitempty"`
}

func (s *Server) handleBenchSnapshot(w http.ResponseWriter, _ *http.Request) {
	counts := s.snapshotCounts()
	resp := snapshotResponse{Version: versionV1}
	if len(counts) > 0 {
		resp.Counts = counts
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleBenchPresenceSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.benchPresence == nil {
		writeBenchError(w, http.StatusNotImplemented, "bench presence controller is not configured")
		return
	}
	resp, err := s.benchPresence.Snapshot(r.Context())
	if err != nil {
		writeBenchError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp.Version == "" {
		resp.Version = versionV1
	}
	writeJSON(w, http.StatusOK, resp)
}

type tokensRequest struct {
	RunID   string          `json:"run_id"`
	BatchID string          `json:"batch_id"`
	Upsert  bool            `json:"upsert,omitempty"`
	Users   []userTokenItem `json:"users,omitempty"`
	Items   []userTokenItem `json:"items,omitempty"`
}

type userTokenItem struct {
	UID         string `json:"uid"`
	Token       string `json:"token"`
	DeviceFlag  uint8  `json:"device_flag,omitempty"`
	DeviceLevel uint8  `json:"device_level,omitempty"`
}

func (r tokensRequest) tokenItems() []userTokenItem {
	if len(r.Users) > 0 {
		return r.Users
	}
	return r.Items
}

func (s *Server) handleBenchTokens(w http.ResponseWriter, r *http.Request) {
	var req tokensRequest
	if !s.bindBenchJSON(w, r, &req) {
		return
	}
	items := req.tokenItems()
	if err := s.validateMutation(req.RunID, req.BatchID, len(items)); err != nil {
		writeBenchError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.addCount("accepted_users", len(items))
	writeJSON(w, http.StatusOK, mutationResponse{RunID: req.RunID, BatchID: req.BatchID, Accepted: len(items)})
}

type channelsRequest struct {
	RunID    string        `json:"run_id"`
	BatchID  string        `json:"batch_id"`
	Upsert   bool          `json:"upsert,omitempty"`
	Channels []channelItem `json:"channels,omitempty"`
	Items    []channelItem `json:"items,omitempty"`
}

type channelItem struct {
	ChannelID     string `json:"channel_id"`
	ChannelType   uint8  `json:"channel_type"`
	Large         bool   `json:"large,omitempty"`
	Ban           bool   `json:"ban,omitempty"`
	Disband       bool   `json:"disband,omitempty"`
	SendBan       bool   `json:"send_ban,omitempty"`
	AllowStranger bool   `json:"allow_stranger,omitempty"`
}

func (r channelsRequest) channelItems() []channelItem {
	if len(r.Channels) > 0 {
		return r.Channels
	}
	return r.Items
}

func (s *Server) handleBenchChannels(w http.ResponseWriter, r *http.Request) {
	if s.benchData == nil {
		writeBenchError(w, http.StatusNotImplemented, "bench channel data writer is not configured")
		return
	}
	var req channelsRequest
	if !s.bindBenchJSON(w, r, &req) {
		return
	}
	items := req.channelItems()
	if err := s.validateMutation(req.RunID, req.BatchID, len(items)); err != nil {
		writeBenchError(w, http.StatusBadRequest, err.Error())
		return
	}
	mutations := make([]BenchChannelMutation, 0, len(items))
	for _, item := range items {
		mutations = append(mutations, BenchChannelMutation{
			ChannelID:     item.ChannelID,
			ChannelType:   item.ChannelType,
			Large:         item.Large,
			Ban:           item.Ban,
			Disband:       item.Disband,
			SendBan:       item.SendBan,
			AllowStranger: item.AllowStranger,
		})
	}
	accepted, err := s.benchData.UpsertChannels(r.Context(), mutations)
	if err != nil {
		writeBenchError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.addCount("accepted_channels", accepted)
	writeJSON(w, http.StatusOK, mutationResponse{RunID: req.RunID, BatchID: req.BatchID, Accepted: accepted})
}

type subscribersRequest struct {
	RunID   string           `json:"run_id"`
	BatchID string           `json:"batch_id"`
	Items   []subscriberItem `json:"items"`
}

type subscriberItem struct {
	ChannelID   string   `json:"channel_id"`
	ChannelType uint8    `json:"channel_type"`
	Reset       bool     `json:"reset,omitempty"`
	Subscribers []string `json:"subscribers"`
}

func (s *Server) handleBenchSubscribers(w http.ResponseWriter, r *http.Request) {
	if s.benchData == nil {
		writeBenchError(w, http.StatusNotImplemented, "bench subscriber data writer is not configured")
		return
	}
	var req subscribersRequest
	if !s.bindBenchJSON(w, r, &req) {
		return
	}
	if err := s.validateMutation(req.RunID, req.BatchID, len(req.Items)); err != nil {
		writeBenchError(w, http.StatusBadRequest, err.Error())
		return
	}
	acceptedSubscribers := 0
	mutations := make([]BenchSubscriberMutation, 0, len(req.Items))
	for _, item := range req.Items {
		if item.Reset {
			writeBenchError(w, http.StatusBadRequest, "bench/v1 subscribers reset=true is not supported")
			return
		}
		acceptedSubscribers += len(item.Subscribers)
		mutations = append(mutations, BenchSubscriberMutation{
			ChannelID:   item.ChannelID,
			ChannelType: item.ChannelType,
			Subscribers: append([]string(nil), item.Subscribers...),
		})
	}
	acceptedSubscribers, err := s.benchData.AddSubscribers(r.Context(), mutations)
	if err != nil {
		writeBenchError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.addCount("accepted_subscriber_items", len(req.Items))
	s.addCount("accepted_subscribers", acceptedSubscribers)
	writeJSON(w, http.StatusOK, subscribersResponse{
		RunID:               req.RunID,
		BatchID:             req.BatchID,
		Accepted:            len(req.Items),
		AcceptedSubscribers: acceptedSubscribers,
	})
}

type mutationResponse struct {
	RunID    string `json:"run_id"`
	BatchID  string `json:"batch_id"`
	Accepted int    `json:"accepted"`
}

type subscribersResponse struct {
	RunID               string `json:"run_id"`
	BatchID             string `json:"batch_id"`
	Accepted            int    `json:"accepted"`
	AcceptedSubscribers int    `json:"accepted_subscribers"`
}

func (s *Server) bindBenchJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	body := r.Body
	if s.benchMaxPayloadBytes > 0 {
		body = http.MaxBytesReader(w, r.Body, s.benchMaxPayloadBytes)
	}
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(out); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeBenchError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("payload too large: max %d bytes", maxBytesErr.Limit))
			return false
		}
		writeBenchError(w, http.StatusBadRequest, "invalid request")
		return false
	}
	return true
}

func (s *Server) validateMutation(runID, batchID string, n int) error {
	switch {
	case strings.TrimSpace(runID) == "":
		return fmt.Errorf("run_id is required")
	case strings.TrimSpace(batchID) == "":
		return fmt.Errorf("batch_id is required")
	case s.benchMaxBatchSize > 0 && n > s.benchMaxBatchSize:
		return fmt.Errorf("batch size %d exceeds max %d", n, s.benchMaxBatchSize)
	default:
		return nil
	}
}

func (s *Server) addCount(name string, n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[name] += n
}

func (s *Server) snapshotCounts() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.counts) == 0 {
		return nil
	}
	out := make(map[string]int, len(s.counts))
	for key, value := range s.counts {
		out[key] = value
	}
	return out
}

func writeBenchError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"status": status, "msg": msg})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
