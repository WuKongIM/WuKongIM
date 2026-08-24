package target

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	defaultTimeout                         = 60 * time.Second
	maxExplicitChannelRuntimeProbeChannels = 1200
	conversationListPageLimit              = 200
	conversationRetryMaxAttempts           = 4
	maxConversationSyncPages               = 4096
	// A valid all-missing explicit response can repeat the configured 10 MiB
	// request identity payload in both compatibility and detailed fields.
	// Thirty-two MiB leaves fixed evidence overhead while keeping allocation finite.
	maxChannelRuntimeProbeResponseBytes int64 = 32 << 20
	// This covers one maximum-size membership-directory page with 16 KiB last
	// message payloads after base64 expansion plus bounded JSON metadata.
	maxConversationSyncResponseBytes   int64 = 256 << 20
	maxObservationDebugResponseBytes   int64 = 64 << 10
	maxObservationClusterResponseBytes int64 = 1 << 20
	maxObservationMetricsResponseBytes int64 = 8 << 20
	maxObservationProfileResponseBytes int64 = 32 << 20
	maxObservationClusterSlots               = 256
	maxObservationSlotReplicas               = 64
	maxObservationMetricLines                = 100_000
	maxObservationMetricLineBytes            = 64 << 10
	maxObservationMetricSeries               = 32_768
	maxObservationQueueSeries                = 128
	maxTerminalFenceResponseBytes      int64 = 4 << 10
	maxTerminalFenceSessions                 = 1_000_000
	maxTerminalFenceIdentityBytes            = 128
	maxTerminalFenceCapabilityBytes          = 64
	// MetaCreateLogicalSlots is the fixed reviewed logical Slot cardinality.
	MetaCreateLogicalSlots = 12
)

// Terminal fence API DTOs are aliases of the shared target contract so the
// black-box client and promoted server surface cannot drift.
const TerminalFenceVersion = model.TerminalFenceVersion

type TerminalFencePrepareRequest = model.TerminalFencePrepareRequest
type TerminalFenceGrant = model.TerminalFenceGrant

// DebugConfig is the bounded effective configuration required by formal preflight.
type DebugConfig struct {
	NodeID              uint64 `json:"node_id"`
	NodeDataDir         string `json:"node_data_dir"`
	InitialSlotCount    uint32 `json:"initial_slot_count"`
	HashSlotCount       uint16 `json:"hash_slot_count"`
	SlotReplicaCount    int    `json:"slot_replica_count"`
	ChannelReplicaCount int    `json:"channel_replica_count"`
	MaxChannels         int    `json:"channel_max_loaded_count"`
}

// DebugGoroutineSummary is the bounded process identity required by the
// chat-lifecycle dataset-generation probe. It intentionally omits task rows.
type DebugGoroutineSummary struct {
	GeneratedAt      time.Time `json:"generated_at"`
	ProcessStartedAt time.Time `json:"process_started_at"`
	BootID           string    `json:"boot_id"`
}

// DebugCluster is one node's bounded live Slot Raft observation.
type DebugCluster struct {
	NodeID        uint64        `json:"node_id"`
	StateRevision uint64        `json:"state_revision"`
	Slots         []ClusterSlot `json:"slots"`
}

// ClusterSlot separates desired replicas from live voters and leader progress.
type ClusterSlot struct {
	SlotID          uint32            `json:"slot_id"`
	LeaderID        uint64            `json:"leader_id"`
	Replicas        []uint64          `json:"replicas"`
	Voters          []uint64          `json:"voters"`
	Term            uint64            `json:"term"`
	CommitIndex     uint64            `json:"commit_index"`
	AppliedIndex    uint64            `json:"applied_index"`
	ReplicaProgress []ReplicaProgress `json:"replica_progress"`
}

// ReplicaProgress is a leader-reported bounded Raft progress projection.
type ReplicaProgress struct {
	NodeID     uint64 `json:"node_id"`
	MatchIndex uint64 `json:"match_index"`
	LagEntries uint64 `json:"lag_entries"`
	State      string `json:"state"`
}

const (
	metricGoGoroutines uint16 = 1 << iota
	metricGoHeapAlloc
	metricProcessRSS
	metricRuntimeQueue
	metricRuntimeInflight
	metricChannelWorkerQueue
	metricActivationRejected
	metricMetaCreated
	metricNodeRSS
	metricRuntimeQueueCapacity
	metricChannelWorkerQueueCapacity
	metricRequired = metricGoGoroutines | metricGoHeapAlloc | metricProcessRSS | metricRuntimeQueue | metricRuntimeQueueCapacity |
		metricRuntimeInflight | metricChannelWorkerQueue | metricChannelWorkerQueueCapacity |
		metricActivationRejected | metricMetaCreated
)

// MetaCreateSlotCounters is one logical Slot's closed durable-create result vector.
type MetaCreateSlotCounters struct {
	Created         uint64
	AlreadyExisting uint64
	Errors          uint64
}

// MetricsSnapshot contains only the low-cardinality families needed by lifecycle observation.
type MetricsSnapshot struct {
	GoGoroutines               float64
	GoHeapAllocBytes           float64
	ProcessResidentMemoryBytes float64
	RuntimeQueueDepth          float64
	RuntimeQueueCapacity       float64
	// RuntimeQueueMaxPercent is the maximum paired per-series depth/capacity utilization.
	RuntimeQueueMaxPercent     float64
	RuntimeInflight            float64
	ChannelWorkerQueueDepth    float64
	ChannelWorkerQueueCapacity float64
	// ChannelWorkerQueueMaxPercent prevents an idle pool from hiding a saturated pool.
	ChannelWorkerQueueMaxPercent float64
	ActivationRejectedTotal      float64
	MetaCreatedBySlot            [MetaCreateLogicalSlots]MetaCreateSlotCounters
	// MetaCreatedTotal retains the existing closed three-result aggregate for
	// compatibility. MetaCreatedBySlot is the authoritative durable evidence.
	MetaCreatedTotal             map[string]float64
	metaCreatedPresent           [MetaCreateLogicalSlots]uint8
	present                      uint16
	runtimeQueueDepthBySeries    map[string]float64
	runtimeQueueCapacityBySeries map[string]float64
	channelQueueDepthBySeries    map[string]float64
	channelQueueCapacityBySeries map[string]float64
}

// ValidateRequired rejects a scrape that omitted a required product or runtime family.
func (s MetricsSnapshot) ValidateRequired() error {
	requiredWithoutRSS := metricRequired &^ metricProcessRSS
	if s.present&requiredWithoutRSS != requiredWithoutRSS || s.present&(metricProcessRSS|metricNodeRSS) == 0 {
		return errors.New("observation metrics missing required families")
	}
	return nil
}

// ConversationSyncRequest starts one stateless complete membership-directory pass.
type ConversationSyncRequest struct {
	UID               string
	CompletedCoverage int64
	MaxConversations  int
}

// ConversationSyncConversation is one validated membership-directory row.
type ConversationSyncConversation struct {
	ChannelID    string
	ChannelType  uint8
	ActiveAt     int64
	ReadSeq      uint64
	DeletedToSeq uint64
	Unread       uint64
	LastMessage  *ConversationSyncMessage
}

// ConversationSyncMessage is the verifier-facing last-message projection.
type ConversationSyncMessage struct {
	MessageID         uint64
	MessageIDStr      string
	MessageSeq        uint64
	ClientMsgNo       string
	FromUID           string
	ServerTimestampMS int64
	Payload           []byte
}

type conversationListRequest struct {
	UID               string `json:"uid"`
	Cursor            string `json:"cursor"`
	Limit             int    `json:"limit"`
	CompletedCoverage int64  `json:"completed_coverage"`
}

type conversationRetryRequest struct {
	UID      string                `json:"uid"`
	Channels []conversationListKey `json:"channels"`
}

type conversationListResponse struct {
	Conversations []conversationListItem `json:"conversations"`
	Deletes       []conversationListKey  `json:"deletes"`
	Unresolved    []conversationListKey  `json:"unresolved"`
	NextCursor    string                 `json:"next_cursor"`
	Done          bool                   `json:"done"`
	ResetRequired bool                   `json:"reset_required"`
}

type conversationListKey struct {
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
}

type conversationListItem struct {
	ChannelID    string                   `json:"channel_id"`
	ChannelType  int64                    `json:"channel_type"`
	ActiveAt     int64                    `json:"active_at"`
	ReadSeq      uint64                   `json:"read_seq"`
	DeletedToSeq uint64                   `json:"deleted_to_seq"`
	Unread       uint64                   `json:"unread"`
	LastMessage  *conversationLastMessage `json:"last_message"`
}

type conversationLastMessage struct {
	MessageID         uint64 `json:"message_id"`
	MessageIDStr      string `json:"message_idstr"`
	MessageSeq        uint64 `json:"message_seq"`
	FromUID           string `json:"from_uid"`
	ClientMsgNo       string `json:"client_msg_no"`
	ServerTimestampMS int64  `json:"server_timestamp_ms"`
	Payload           []byte `json:"payload"`
}

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

// DebugConfig reads the protected effective configuration snapshot.
func (c *Client) DebugConfig(ctx context.Context) (DebugConfig, error) {
	var out DebugConfig
	if err := c.getObservationJSON(ctx, "/debug/config", &out, maxObservationDebugResponseBytes); err != nil {
		return DebugConfig{}, err
	}
	return out, nil
}

// DebugGoroutineSummary reads the protected process identity snapshot.
func (c *Client) DebugGoroutineSummary(ctx context.Context) (DebugGoroutineSummary, error) {
	var out DebugGoroutineSummary
	if err := c.getObservationJSON(ctx, "/debug/goroutines/summary", &out, maxObservationDebugResponseBytes); err != nil {
		return DebugGoroutineSummary{}, err
	}
	return out, nil
}

// DebugCluster reads and validates one bounded live Slot Raft snapshot.
func (c *Client) DebugCluster(ctx context.Context) (DebugCluster, error) {
	var out DebugCluster
	if err := c.getObservationJSON(ctx, "/debug/cluster", &out, maxObservationClusterResponseBytes); err != nil {
		return DebugCluster{}, err
	}
	if err := validateDebugCluster(out); err != nil {
		return DebugCluster{}, err
	}
	return out, nil
}

// Metrics scrapes a bounded allowlist of Prometheus product and Go/process metrics.
func (c *Client) Metrics(ctx context.Context) (MetricsSnapshot, error) {
	encoded, err := c.getObservationBytes(ctx, "/metrics", maxObservationMetricsResponseBytes, "observation metrics response exceeds byte limit")
	if err != nil {
		return MetricsSnapshot{}, err
	}
	return parseObservationMetrics(encoded)
}

// ForceGC uses the stdlib pprof heap gc=1 trigger and discards its bounded profile response.
func (c *Client) ForceGC(ctx context.Context) error {
	_, err := c.getObservationBytes(ctx, "/debug/pprof/heap?gc=1", maxObservationProfileResponseBytes, "observation profile response exceeds byte limit")
	return err
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

// ConversationSync performs one complete stateless membership-directory pass.
// It follows every opaque cursor, retries bounded unresolved keys, and never
// attaches the Bench bearer token to product routes.
func (c *Client) ConversationSync(ctx context.Context, req ConversationSyncRequest) ([]ConversationSyncConversation, error) {
	if req.UID == "" || req.CompletedCoverage < 0 || req.MaxConversations <= 0 {
		return nil, errors.New("invalid conversation sync request")
	}
	pageLimit := min(req.MaxConversations, conversationListPageLimit)
	state := newConversationSyncState(req.MaxConversations)
	cursor := ""
	for pageNumber := 0; pageNumber < maxConversationSyncPages; pageNumber++ {
		page, err := c.conversationListPage(ctx, conversationListRequest{
			UID: req.UID, Cursor: cursor, Limit: pageLimit, CompletedCoverage: req.CompletedCoverage,
		})
		if err != nil {
			return nil, err
		}
		if err := state.applyPage(page, pageLimit); err != nil {
			return nil, err
		}
		if page.ResetRequired {
			return nil, errors.New("invalid conversation sync response: reset required during stateless pass")
		}
		if page.Done {
			if err := c.resolveConversationSyncUnresolved(ctx, req.UID, state); err != nil {
				return nil, err
			}
			return state.rows(), nil
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return nil, errors.New("invalid conversation sync response: cursor did not advance")
		}
		cursor = page.NextCursor
	}
	return nil, errors.New("invalid conversation sync response: page limit exceeded")
}

func (c *Client) conversationListPage(ctx context.Context, req conversationListRequest) (conversationListResponse, error) {
	var out conversationListResponse
	if err := c.postAnyOutMappedAuth(
		ctx,
		"/conversation/list",
		req,
		&out,
		maxConversationSyncResponseBytes,
		safeConversationSyncError,
		false,
		"conversation sync response exceeds byte limit",
	); err != nil {
		return conversationListResponse{}, err
	}
	if out.Conversations == nil || out.Deletes == nil || out.Unresolved == nil {
		return conversationListResponse{}, errors.New("invalid conversation sync response: expected bounded arrays")
	}
	return out, nil
}

func (c *Client) conversationRetryPage(ctx context.Context, req conversationRetryRequest) (conversationListResponse, error) {
	var out conversationListResponse
	if err := c.postAnyOutMappedAuth(
		ctx,
		"/conversation/retry",
		req,
		&out,
		maxConversationSyncResponseBytes,
		safeConversationSyncError,
		false,
		"conversation sync response exceeds byte limit",
	); err != nil {
		return conversationListResponse{}, err
	}
	if out.Conversations == nil || out.Deletes == nil || out.Unresolved == nil {
		return conversationListResponse{}, errors.New("invalid conversation sync response: expected bounded arrays")
	}
	return out, nil
}

func (c *Client) resolveConversationSyncUnresolved(ctx context.Context, uid string, state *conversationSyncState) error {
	for attempt := 0; attempt < conversationRetryMaxAttempts && len(state.unresolved) > 0; attempt++ {
		keys := state.unresolvedKeys()
		state.clearUnresolved()
		for start := 0; start < len(keys); start += conversationListPageLimit {
			end := min(start+conversationListPageLimit, len(keys))
			page, err := c.conversationRetryPage(ctx, conversationRetryRequest{UID: uid, Channels: keys[start:end]})
			if err != nil {
				return err
			}
			if !page.Done || page.NextCursor != "" || page.ResetRequired {
				return errors.New("invalid conversation sync retry response")
			}
			if err := state.applyPage(page, end-start); err != nil {
				return err
			}
		}
	}
	if len(state.unresolved) > 0 {
		return errors.New("conversation sync unresolved retry limit reached")
	}
	return nil
}

type conversationSyncIdentity struct {
	channelID   string
	channelType uint8
}

type conversationSyncState struct {
	max          int
	rowsByKey    map[conversationSyncIdentity]ConversationSyncConversation
	rowOrder     []conversationSyncIdentity
	rowOrdered   map[conversationSyncIdentity]struct{}
	unresolved   map[conversationSyncIdentity]conversationListKey
	unresolvedAt []conversationSyncIdentity
	limitReached bool
}

func newConversationSyncState(maxRows int) *conversationSyncState {
	return &conversationSyncState{
		max: maxRows, rowsByKey: make(map[conversationSyncIdentity]ConversationSyncConversation, maxRows),
		rowOrder:   make([]conversationSyncIdentity, 0, maxRows),
		rowOrdered: make(map[conversationSyncIdentity]struct{}, maxRows),
		unresolved: make(map[conversationSyncIdentity]conversationListKey),
	}
}

func (s *conversationSyncState) applyPage(page conversationListResponse, limit int) error {
	if limit <= 0 || len(page.Conversations)+len(page.Deletes)+len(page.Unresolved) > limit {
		return errors.New("invalid conversation sync response: page cardinality exceeded")
	}
	pageKeys := make(map[conversationSyncIdentity]struct{}, limit)
	for _, item := range page.Conversations {
		key, err := conversationSyncKey(item.ChannelID, item.ChannelType)
		if err != nil {
			return err
		}
		if _, exists := pageKeys[key]; exists {
			return errors.New("invalid conversation sync response: duplicate page identity")
		}
		pageKeys[key] = struct{}{}
		delete(s.unresolved, key)
		row := conversationSyncRow(item)
		if _, exists := s.rowsByKey[key]; exists {
			s.rowsByKey[key] = row
			continue
		}
		if len(s.rowsByKey) >= s.max {
			s.limitReached = true
			continue
		}
		s.rowsByKey[key] = row
		if _, ordered := s.rowOrdered[key]; !ordered {
			s.rowOrder = append(s.rowOrder, key)
			s.rowOrdered[key] = struct{}{}
		}
	}
	for _, item := range page.Deletes {
		key, err := conversationSyncKey(item.ChannelID, item.ChannelType)
		if err != nil {
			return err
		}
		if _, exists := pageKeys[key]; exists {
			return errors.New("invalid conversation sync response: conflicting page identity")
		}
		pageKeys[key] = struct{}{}
		delete(s.rowsByKey, key)
		delete(s.unresolved, key)
	}
	for _, item := range page.Unresolved {
		key, err := conversationSyncKey(item.ChannelID, item.ChannelType)
		if err != nil {
			return err
		}
		if _, exists := pageKeys[key]; exists {
			return errors.New("invalid conversation sync response: conflicting page identity")
		}
		pageKeys[key] = struct{}{}
		if _, exists := s.unresolved[key]; exists {
			continue
		}
		if len(s.unresolved) >= s.max {
			s.limitReached = true
			continue
		}
		s.unresolved[key] = item
		s.unresolvedAt = append(s.unresolvedAt, key)
	}
	return nil
}

func (s *conversationSyncState) unresolvedKeys() []conversationListKey {
	keys := make([]conversationListKey, 0, len(s.unresolved))
	seen := make(map[conversationSyncIdentity]struct{}, len(s.unresolved))
	for _, key := range s.unresolvedAt {
		if item, ok := s.unresolved[key]; ok {
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			keys = append(keys, item)
		}
	}
	return keys
}

func (s *conversationSyncState) clearUnresolved() {
	s.unresolved = make(map[conversationSyncIdentity]conversationListKey)
	s.unresolvedAt = s.unresolvedAt[:0]
}

func (s *conversationSyncState) rows() []ConversationSyncConversation {
	rows := make([]ConversationSyncConversation, 0, len(s.rowsByKey)+1)
	for _, key := range s.rowOrder {
		if row, ok := s.rowsByKey[key]; ok {
			rows = append(rows, row)
		}
	}
	if s.limitReached && len(rows) < s.max {
		rows = append(rows, make([]ConversationSyncConversation, s.max-len(rows))...)
	}
	return rows
}

func conversationSyncKey(channelID string, channelType int64) (conversationSyncIdentity, error) {
	if channelID == "" || channelType <= 0 || channelType > 255 {
		return conversationSyncIdentity{}, errors.New("invalid conversation sync response: invalid identity")
	}
	return conversationSyncIdentity{channelID: channelID, channelType: uint8(channelType)}, nil
}

func conversationSyncRow(item conversationListItem) ConversationSyncConversation {
	row := ConversationSyncConversation{
		ChannelID: item.ChannelID, ChannelType: uint8(item.ChannelType), ActiveAt: item.ActiveAt,
		ReadSeq: item.ReadSeq, DeletedToSeq: item.DeletedToSeq, Unread: item.Unread,
	}
	if item.LastMessage != nil {
		row.LastMessage = &ConversationSyncMessage{
			MessageID: item.LastMessage.MessageID, MessageIDStr: item.LastMessage.MessageIDStr,
			MessageSeq: item.LastMessage.MessageSeq, ClientMsgNo: item.LastMessage.ClientMsgNo,
			FromUID: item.LastMessage.FromUID, ServerTimestampMS: item.LastMessage.ServerTimestampMS,
			Payload: item.LastMessage.Payload,
		}
	}
	return row
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
	if err := validateChannelRuntimeProbeRequest(req); err != nil {
		return model.ChannelRuntimeProbeResult{}, err
	}
	var out model.ChannelRuntimeProbeResult
	if err := c.postAnyOutMapped(ctx, "/bench/v1/channel-runtime/probe", req, &out, maxChannelRuntimeProbeResponseBytes, safeChannelRuntimeProbeError); err != nil {
		return model.ChannelRuntimeProbeResult{}, err
	}
	if err := validateChannelRuntimeProbeResponse(req, out); err != nil {
		return model.ChannelRuntimeProbeResult{}, err
	}
	return out, nil
}

// ProbeChannelRuntimeAll asks every configured target node to inspect the selected channels.
func (c *Client) ProbeChannelRuntimeAll(ctx context.Context, req model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error) {
	if err := validateChannelRuntimeProbeRequest(req); err != nil {
		return nil, err
	}
	results := make([]model.ChannelRuntimeProbeResult, 0, len(c.addrs()))
	err := c.postAll(func(addr string) error {
		var out model.ChannelRuntimeProbeResult
		if err := c.doJSONLimited(ctx, http.MethodPost, addr, "/bench/v1/channel-runtime/probe", req, &out, maxChannelRuntimeProbeResponseBytes); err != nil {
			return safeChannelRuntimeProbeError(err)
		}
		if err := validateChannelRuntimeProbeResponse(req, out); err != nil {
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

// RemoveSubscribers posts a spec-shaped batch subscriber removal request.
func (c *Client) RemoveSubscribers(ctx context.Context, req model.BatchSubscribersRequest) error {
	return c.postAny(ctx, "/bench/v1/channels/subscribers/remove", req)
}

// PrepareTerminalFence calls exactly one target. It never falls back to a
// second address because issuing two independent terminal epochs would make
// the session proof ambiguous.
func (c *Client) PrepareTerminalFence(ctx context.Context, request TerminalFencePrepareRequest) (TerminalFenceGrant, error) {
	if err := validateTerminalFencePrepareRequest(request); err != nil {
		return TerminalFenceGrant{}, err
	}
	addrs := c.addrs()
	if len(addrs) != 1 {
		return TerminalFenceGrant{}, fmt.Errorf("terminal fence requires exactly one target api address, got %d", len(addrs))
	}
	var grant TerminalFenceGrant
	if err := c.doJSONLimited(ctx, http.MethodPost, addrs[0], "/bench/v1/terminal-fence/prepare", request, &grant, maxTerminalFenceResponseBytes); err != nil {
		return TerminalFenceGrant{}, err
	}
	if err := validateTerminalFenceGrant(request, grant); err != nil {
		return TerminalFenceGrant{}, err
	}
	return grant, nil
}

func validateTerminalFencePrepareRequest(request TerminalFencePrepareRequest) error {
	if request.RunID == "" || request.RunID != strings.TrimSpace(request.RunID) || len(request.RunID) > maxTerminalFenceIdentityBytes {
		return errors.New("terminal fence run_id is invalid")
	}
	if request.AssignmentID == "" || request.AssignmentID != strings.TrimSpace(request.AssignmentID) || len(request.AssignmentID) > maxTerminalFenceIdentityBytes {
		return errors.New("terminal fence assignment_id is invalid")
	}
	if request.ExpectedSessions <= 0 || request.ExpectedSessions > maxTerminalFenceSessions {
		return errors.New("terminal fence expected_sessions is outside the supported range")
	}
	return nil
}

func validateTerminalFenceGrant(request TerminalFencePrepareRequest, grant TerminalFenceGrant) error {
	if grant.Version != TerminalFenceVersion || grant.RunID != request.RunID || grant.AssignmentID != request.AssignmentID ||
		grant.ExpectedSessions != request.ExpectedSessions || grant.Epoch == 0 || !validTerminalFenceCapability(grant.Capability) {
		return errors.New("terminal fence grant does not match request")
	}
	return nil
}

func validTerminalFenceCapability(value string) bool {
	if len(value) < 22 || len(value) > maxTerminalFenceCapabilityBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		ch := value[index]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
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

func (c *Client) getObservationJSON(ctx context.Context, path string, out any, limit int64) error {
	encoded, err := c.getObservationBytes(ctx, path, limit, "observation JSON response exceeds byte limit")
	if err != nil {
		return err
	}
	if err := decodeJSONLimited(bytes.NewReader(encoded), out, limit, "observation JSON response exceeds byte limit"); err != nil {
		return errors.New("observation endpoint returned invalid JSON")
	}
	return nil
}

func (c *Client) getObservationBytes(ctx context.Context, path string, limit int64, limitError string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	addrs := c.addrs()
	if len(addrs) == 0 {
		return nil, errors.New("no target api addresses configured")
	}
	var failures int
	for _, addr := range addrs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(addr, path), nil)
		if err != nil {
			failures++
			continue
		}
		if c.cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			failures++
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			status := resp.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
			_ = resp.Body.Close()
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				return nil, fmt.Errorf("GET %s returned status %d", observationPath(path), status)
			}
			failures++
			continue
		}
		if resp.ContentLength > limit {
			_ = resp.Body.Close()
			return nil, errors.New(limitError)
		}
		encoded, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		_ = resp.Body.Close()
		if readErr != nil {
			failures++
			continue
		}
		if int64(len(encoded)) > limit {
			return nil, errors.New(limitError)
		}
		return encoded, nil
	}
	return nil, fmt.Errorf("GET %s failed on %d target addresses", observationPath(path), failures)
}

func observationPath(path string) string {
	if before, _, ok := strings.Cut(path, "?"); ok {
		return before
	}
	return path
}

func (c *Client) postAny(ctx context.Context, path string, body any) error {
	return c.postAnyOut(ctx, path, body, nil)
}

func (c *Client) postAnyOut(ctx context.Context, path string, body any, out any) error {
	return c.postAnyOutMapped(ctx, path, body, out, 0, nil)
}

func (c *Client) postAnyOutMapped(ctx context.Context, path string, body any, out any, maxResponseBytes int64, mapErr func(error) error) error {
	return c.postAnyOutMappedAuth(ctx, path, body, out, maxResponseBytes, mapErr, true, "channel runtime probe response exceeds byte limit")
}

func (c *Client) postAnyOutMappedAuth(ctx context.Context, path string, body any, out any, maxResponseBytes int64, mapErr func(error) error, includeBenchToken bool, responseLimitError string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
		if err := c.doJSONLimitedAuth(ctx, http.MethodPost, addr, path, body, attemptOut, maxResponseBytes, includeBenchToken, responseLimitError); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if mapErr != nil {
				err = mapErr(err)
			}
			errs = append(errs, err.Error())
			continue
		}
		copyDecodeTarget(out, attemptOut)
		return nil
	}
	return fmt.Errorf("all target api addresses failed: %s", strings.Join(errs, "; "))
}

func safeChannelRuntimeProbeError(err error) error {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return err
	}
	return fmt.Errorf("%s %s returned status %d", statusErr.method, statusErr.url, statusErr.statusCode)
}

func safeConversationSyncError(err error) error {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return err
	}
	return fmt.Errorf("%s %s returned status %d", statusErr.method, statusErr.url, statusErr.statusCode)
}

func validateChannelRuntimeProbeRequest(req model.ChannelRuntimeProbeRequest) error {
	if req.Channels == nil {
		return nil
	}
	if len(req.Channels) == 0 || len(req.Channels) > maxExplicitChannelRuntimeProbeChannels {
		return errors.New("invalid channel runtime probe request: explicit selector cardinality is out of bounds")
	}
	return nil
}

func validateChannelRuntimeProbeResponse(req model.ChannelRuntimeProbeRequest, result model.ChannelRuntimeProbeResult) error {
	if req.Channels == nil {
		if len(result.Channels) != 0 {
			return fmt.Errorf("invalid channel runtime probe response: generated selector returned detailed rows")
		}
		return nil
	}
	if len(result.Channels) != len(req.Channels) {
		return errors.New("invalid channel runtime probe response: explicit row count does not match request")
	}
	for i, requested := range req.Channels {
		returned := result.Channels[i]
		if returned.ChannelID != requested.ChannelID || returned.ChannelType != requested.ChannelType {
			return errors.New("invalid channel runtime probe response: explicit row identity does not match request")
		}
	}
	return nil
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
	return c.doJSONLimited(ctx, method, base, path, body, out, 0)
}

func (c *Client) doJSONLimited(ctx context.Context, method, base, path string, body any, out any, maxResponseBytes int64) error {
	return c.doJSONLimitedAuth(ctx, method, base, path, body, out, maxResponseBytes, true, "channel runtime probe response exceeds byte limit")
}

func (c *Client) doJSONLimitedAuth(ctx context.Context, method, base, path string, body any, out any, maxResponseBytes int64, includeBenchToken bool, responseLimitError string) error {
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
	if includeBenchToken && c.cfg.Token != "" {
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
	if maxResponseBytes > 0 && resp.ContentLength > maxResponseBytes {
		return errors.New(responseLimitError)
	}
	if maxResponseBytes > 0 {
		if err := decodeJSONLimited(resp.Body, out, maxResponseBytes, responseLimitError); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, req.URL.String(), err)
		}
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, req.URL.String(), err)
	}
	return nil
}

func decodeJSONLimited(reader io.Reader, out any, maxResponseBytes int64, responseLimitError string) error {
	limited := &io.LimitedReader{R: reader, N: maxResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(out); err != nil {
		if limited.N == 0 {
			return errors.New(responseLimitError)
		}
		return err
	}

	var trailing any
	err := decoder.Decode(&trailing)
	if limited.N == 0 {
		return errors.New(responseLimitError)
	}
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("multiple JSON values in response")
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

func validateDebugCluster(snapshot DebugCluster) error {
	if len(snapshot.Slots) > maxObservationClusterSlots {
		return errors.New("observation cluster slot cardinality exceeds limit")
	}
	seenSlots := make(map[uint32]struct{}, len(snapshot.Slots))
	for _, slot := range snapshot.Slots {
		if slot.SlotID == 0 {
			return errors.New("observation cluster contains invalid slot identity")
		}
		if _, exists := seenSlots[slot.SlotID]; exists {
			return errors.New("observation cluster contains duplicate slot identity")
		}
		seenSlots[slot.SlotID] = struct{}{}
		if len(slot.Replicas) > maxObservationSlotReplicas || len(slot.Voters) > maxObservationSlotReplicas || len(slot.ReplicaProgress) > maxObservationSlotReplicas {
			return errors.New("observation cluster replica cardinality exceeds limit")
		}
		if !validUniqueNodeIDs(slot.Replicas) || !validUniqueNodeIDs(slot.Voters) {
			return errors.New("observation cluster contains invalid replica identity")
		}
		if len(slot.ReplicaProgress) > 0 && slot.LeaderID != snapshot.NodeID {
			return errors.New("observation cluster non-leader reported replica progress")
		}
		seenProgress := make(map[uint64]struct{}, len(slot.ReplicaProgress))
		for _, progress := range slot.ReplicaProgress {
			if progress.NodeID == 0 || !validReplicaProgressState(progress.State) {
				return errors.New("observation cluster contains invalid replica progress")
			}
			if _, exists := seenProgress[progress.NodeID]; exists {
				return errors.New("observation cluster contains duplicate replica progress")
			}
			seenProgress[progress.NodeID] = struct{}{}
			lagEntries := uint64(0)
			if progress.MatchIndex < slot.CommitIndex {
				lagEntries = slot.CommitIndex - progress.MatchIndex
			}
			if lagEntries != progress.LagEntries {
				return errors.New("observation cluster contains inconsistent replica lag")
			}
		}
	}
	return nil
}

func validUniqueNodeIDs(ids []uint64) bool {
	seen := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			return false
		}
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func validReplicaProgressState(state string) bool {
	switch state {
	case "StateProbe", "StateReplicate", "StateSnapshot":
		return true
	default:
		return false
	}
}

func parseObservationMetrics(encoded []byte) (MetricsSnapshot, error) {
	scanner := bufio.NewScanner(bytes.NewReader(encoded))
	scanner.Buffer(make([]byte, 4096), maxObservationMetricLineBytes)
	var snapshot MetricsSnapshot
	var lines, series int
	for scanner.Scan() {
		lines++
		if lines > maxObservationMetricLines {
			return MetricsSnapshot{}, errors.New("observation metrics line limit exceeded")
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		series++
		if series > maxObservationMetricSeries {
			return MetricsSnapshot{}, errors.New("observation metrics series cardinality limit exceeded")
		}
		name := observationMetricName(line)
		kind := observationMetricKind(name)
		if kind == 0 {
			continue
		}
		token, valueText, err := splitObservationMetricSample(line)
		if err != nil {
			return MetricsSnapshot{}, err
		}
		_, labels, err := parseObservationMetricToken(token)
		if err != nil {
			return MetricsSnapshot{}, err
		}
		value, err := strconv.ParseFloat(valueText, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return MetricsSnapshot{}, errors.New("observation metrics contains invalid value")
		}
		if err := snapshot.addMetric(kind, labels, value, valueText); err != nil {
			return MetricsSnapshot{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return MetricsSnapshot{}, errors.New("observation metrics line limit exceeded")
	}
	if snapshot.present&metricMetaCreated != 0 && len(snapshot.MetaCreatedTotal) != 3 {
		return MetricsSnapshot{}, errors.New("observation metrics contains incomplete metadata-create results")
	}
	if err := snapshot.finalizeQueueUtilization(); err != nil {
		return MetricsSnapshot{}, err
	}
	return snapshot, nil
}

func observationMetricName(line string) string {
	for index, character := range line {
		if character == '{' || character == ' ' || character == '\t' {
			return line[:index]
		}
	}
	return line
}

func splitObservationMetricSample(line string) (token, value string, err error) {
	open := strings.IndexByte(line, '{')
	if open < 0 {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return "", "", errors.New("observation metrics contains invalid sample")
		}
		return fields[0], fields[1], nil
	}
	quoted, escaped := false, false
	closeIndex := -1
	for index := open + 1; index < len(line); index++ {
		switch {
		case escaped:
			escaped = false
		case line[index] == '\\' && quoted:
			escaped = true
		case line[index] == '"':
			quoted = !quoted
		case line[index] == '}' && !quoted:
			closeIndex = index
			index = len(line)
		}
	}
	if closeIndex < 0 || quoted {
		return "", "", errors.New("observation metrics contains invalid labels")
	}
	fields := strings.Fields(line[closeIndex+1:])
	if len(fields) != 1 {
		return "", "", errors.New("observation metrics contains invalid sample")
	}
	return line[:closeIndex+1], fields[0], nil
}

func observationMetricKind(name string) uint16 {
	switch name {
	case "go_goroutines":
		return metricGoGoroutines
	case "go_memstats_heap_alloc_bytes":
		return metricGoHeapAlloc
	case "process_resident_memory_bytes":
		return metricProcessRSS
	case "wukongim_node_memory_rss_bytes":
		return metricNodeRSS
	case "wukongim_runtime_pool_queue_depth":
		return metricRuntimeQueue
	case "wukongim_runtime_pool_queue_capacity":
		return metricRuntimeQueueCapacity
	case "wukongim_runtime_pool_inflight":
		return metricRuntimeInflight
	case "wukongim_channelv2_worker_queue_depth":
		return metricChannelWorkerQueue
	case "wukongim_channelv2_worker_queue_capacity":
		return metricChannelWorkerQueueCapacity
	case "wukongim_channelv2_activation_rejected_total":
		return metricActivationRejected
	case "wukongim_channelv2_meta_created_total":
		return metricMetaCreated
	default:
		return 0
	}
}

func (s *MetricsSnapshot) addMetric(kind uint16, labels map[string]string, value float64, valueText string) error {
	s.present |= kind
	switch kind {
	case metricRuntimeQueue, metricRuntimeQueueCapacity, metricChannelWorkerQueue, metricChannelWorkerQueueCapacity:
		if err := s.addQueueSeries(kind, labels, value); err != nil {
			return err
		}
	}
	var destination *float64
	switch kind {
	case metricGoGoroutines:
		destination = &s.GoGoroutines
	case metricGoHeapAlloc:
		destination = &s.GoHeapAllocBytes
	case metricProcessRSS:
		if s.present&metricNodeRSS != 0 {
			s.ProcessResidentMemoryBytes = 0
		}
		destination = &s.ProcessResidentMemoryBytes
	case metricNodeRSS:
		if s.present&metricProcessRSS != 0 {
			return nil
		}
		destination = &s.ProcessResidentMemoryBytes
	case metricRuntimeQueue:
		destination = &s.RuntimeQueueDepth
	case metricRuntimeQueueCapacity:
		destination = &s.RuntimeQueueCapacity
	case metricRuntimeInflight:
		destination = &s.RuntimeInflight
	case metricChannelWorkerQueue:
		destination = &s.ChannelWorkerQueueDepth
	case metricChannelWorkerQueueCapacity:
		destination = &s.ChannelWorkerQueueCapacity
	case metricActivationRejected:
		destination = &s.ActivationRejectedTotal
	case metricMetaCreated:
		if len(labels) != 2 {
			return errors.New("observation metrics contains invalid metadata-create labels")
		}
		slotText, hasSlot := labels["slot_id"]
		result := labels["result"]
		if !hasSlot || result == "" {
			return errors.New("observation metrics contains invalid metadata-create labels")
		}
		slotID, err := strconv.ParseUint(slotText, 10, 8)
		if err != nil || slotID == 0 || slotID > MetaCreateLogicalSlots || slotText != strconv.FormatUint(slotID, 10) {
			return errors.New("observation metrics contains invalid metadata-create Slot")
		}
		counter, ok := exactObservationMetricCounter(valueText)
		if !ok {
			return errors.New("observation metrics contains invalid metadata-create counter")
		}
		index := slotID - 1
		var bit uint8
		var slotDestination *uint64
		switch result {
		case "created":
			bit, slotDestination = 0b001, &s.MetaCreatedBySlot[index].Created
		case "already_existing":
			bit, slotDestination = 0b010, &s.MetaCreatedBySlot[index].AlreadyExisting
		case "error":
			bit, slotDestination = 0b100, &s.MetaCreatedBySlot[index].Errors
		default:
			return errors.New("observation metrics contains invalid metadata-create result")
		}
		if s.metaCreatedPresent[index]&bit != 0 {
			return errors.New("observation metrics contains duplicate metadata-create Slot result")
		}
		s.metaCreatedPresent[index] |= bit
		*slotDestination = counter
		if s.MetaCreatedTotal == nil {
			s.MetaCreatedTotal = make(map[string]float64, 3)
		}
		current := s.MetaCreatedTotal[result]
		if current > float64(uint64(1)<<53)-float64(counter) {
			return errors.New("observation metrics aggregate overflow")
		}
		s.MetaCreatedTotal[result] = current + float64(counter)
		return nil
	default:
		return errors.New("observation metrics contains unsupported family")
	}
	if *destination > math.MaxFloat64-value {
		return errors.New("observation metrics aggregate overflow")
	}
	*destination += value
	return nil
}

func (s *MetricsSnapshot) addQueueSeries(kind uint16, labels map[string]string, value float64) error {
	encoded, err := json.Marshal(labels)
	if err != nil || len(encoded) == 0 {
		return errors.New("observation metrics contains invalid queue labels")
	}
	key := string(encoded)
	var values *map[string]float64
	switch kind {
	case metricRuntimeQueue:
		values = &s.runtimeQueueDepthBySeries
	case metricRuntimeQueueCapacity:
		values = &s.runtimeQueueCapacityBySeries
	case metricChannelWorkerQueue:
		values = &s.channelQueueDepthBySeries
	case metricChannelWorkerQueueCapacity:
		values = &s.channelQueueCapacityBySeries
	default:
		return errors.New("observation metrics contains invalid queue family")
	}
	if *values == nil {
		*values = make(map[string]float64)
	}
	if _, exists := (*values)[key]; exists || len(*values) >= maxObservationQueueSeries {
		return errors.New("observation metrics contains duplicate or excessive queue series")
	}
	(*values)[key] = value
	return nil
}

func (s *MetricsSnapshot) finalizeQueueUtilization() error {
	var err error
	if s.present&(metricRuntimeQueue|metricRuntimeQueueCapacity) == metricRuntimeQueue|metricRuntimeQueueCapacity {
		s.RuntimeQueueMaxPercent, err = maximumQueueUtilization(s.runtimeQueueDepthBySeries, s.runtimeQueueCapacityBySeries)
		if err != nil {
			return err
		}
	}
	if s.present&(metricChannelWorkerQueue|metricChannelWorkerQueueCapacity) == metricChannelWorkerQueue|metricChannelWorkerQueueCapacity {
		s.ChannelWorkerQueueMaxPercent, err = maximumQueueUtilization(s.channelQueueDepthBySeries, s.channelQueueCapacityBySeries)
		if err != nil {
			return err
		}
	}
	s.runtimeQueueDepthBySeries, s.runtimeQueueCapacityBySeries = nil, nil
	s.channelQueueDepthBySeries, s.channelQueueCapacityBySeries = nil, nil
	return nil
}

func maximumQueueUtilization(depths, capacities map[string]float64) (float64, error) {
	if len(depths) == 0 || len(depths) != len(capacities) {
		return 0, errors.New("observation metrics contains unpaired queue series")
	}
	maximum := float64(0)
	for key, depth := range depths {
		capacity, ok := capacities[key]
		if !ok || (capacity > 0 && depth > capacity) {
			return 0, errors.New("observation metrics contains invalid queue utilization")
		}
		// A zero item capacity marks either an inactive queue or a queue bounded
		// only by bytes, so its item utilization has no valid denominator.
		if capacity == 0 {
			continue
		}
		maximum = max(maximum, depth*100/capacity)
	}
	return maximum, nil
}

func exactObservationMetricCounter(valueText string) (uint64, bool) {
	parsed, _, err := big.ParseFloat(valueText, 10, 256, big.ToNearestEven)
	if err != nil || parsed.Sign() < 0 {
		return 0, false
	}
	integer, accuracy := parsed.Int(nil)
	if accuracy != big.Exact || integer.Sign() < 0 || integer.BitLen() > 54 {
		return 0, false
	}
	value := integer.Uint64()
	return value, value <= uint64(1)<<53
}

func parseObservationMetricToken(token string) (string, map[string]string, error) {
	open := strings.IndexByte(token, '{')
	if open < 0 {
		if strings.ContainsRune(token, '}') || token == "" {
			return "", nil, errors.New("observation metrics contains invalid sample name")
		}
		return token, nil, nil
	}
	if !strings.HasSuffix(token, "}") || open == 0 {
		return "", nil, errors.New("observation metrics contains invalid labels")
	}
	labels, err := parseObservationLabels(token[open+1 : len(token)-1])
	if err != nil {
		return "", nil, err
	}
	return token[:open], labels, nil
}

func parseObservationLabels(raw string) (map[string]string, error) {
	labels := make(map[string]string)
	for len(raw) > 0 {
		raw = strings.TrimLeft(raw, " \t")
		equals := strings.IndexByte(raw, '=')
		if equals <= 0 || equals > 64 {
			return nil, errors.New("observation metrics contains invalid labels")
		}
		name := strings.TrimSpace(raw[:equals])
		raw = raw[equals+1:]
		if name == "" || len(raw) == 0 || raw[0] != '"' {
			return nil, errors.New("observation metrics contains invalid labels")
		}
		end := 1
		escaped := false
		for ; end < len(raw); end++ {
			if escaped {
				escaped = false
				continue
			}
			if raw[end] == '\\' {
				escaped = true
				continue
			}
			if raw[end] == '"' {
				break
			}
		}
		if end >= len(raw) {
			return nil, errors.New("observation metrics contains invalid labels")
		}
		value, err := strconv.Unquote(raw[:end+1])
		if err != nil || len(value) > 1024 {
			return nil, errors.New("observation metrics contains invalid labels")
		}
		if _, exists := labels[name]; exists || len(labels) >= 32 {
			return nil, errors.New("observation metrics label cardinality limit exceeded")
		}
		labels[name] = value
		raw = strings.TrimLeft(raw[end+1:], " \t")
		if raw == "" {
			break
		}
		if raw[0] != ',' {
			return nil, errors.New("observation metrics contains invalid labels")
		}
		raw = raw[1:]
	}
	return labels, nil
}
