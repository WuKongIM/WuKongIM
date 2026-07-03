package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

const (
	defaultTopWindow = 10 * time.Second
	minTopWindow     = 2 * time.Second
	defaultTopLimit  = 20
	maxTopLimit      = 100
	topVersionV1     = "top/v1"
	topScopeLocal    = "local_node"
)

// ErrTopWarmingUp reports that the top collector cannot serve the requested window yet.
var ErrTopWarmingUp = errors.New("internalv2/access/api: top collector warming up")

// TopView selects the optional snapshot sections returned for wkcli top.
type TopView string

const (
	// TopViewOverview returns the default overview payload.
	TopViewOverview TopView = "overview"
	// TopViewRuntime returns runtime pressure-oriented payload sections.
	TopViewRuntime TopView = "runtime"
	// TopViewTraffic returns traffic-oriented payload sections.
	TopViewTraffic TopView = "traffic"
	// TopViewChannel returns ChannelV2-oriented payload sections.
	TopViewChannel TopView = "channel"
	// TopViewStorage returns storage-oriented payload sections.
	TopViewStorage TopView = "storage"
	// TopViewDelivery returns delivery-oriented payload sections.
	TopViewDelivery TopView = "delivery"
	// TopViewAll returns all available payload sections.
	TopViewAll TopView = "all"
)

// TopSnapshotProvider returns node-local operations snapshots for wkcli top.
type TopSnapshotProvider interface {
	SnapshotTop(context.Context, TopSnapshotQuery) (TopSnapshot, error)
}

// TopSnapshotQuery contains validated query parameters for /top/v1/snapshot.
type TopSnapshotQuery struct {
	// Window is the aggregation window used for rates and percentiles.
	Window time.Duration
	// View selects optional response sections.
	View TopView
	// Limit caps the number of pressure items returned.
	Limit int
	// NodeID optionally selects a cluster node for manager-mediated runtime views.
	NodeID uint64
}

// TopSnapshot is the JSON response model for the node-local top API.
type TopSnapshot struct {
	// Version is the top API version that produced this snapshot.
	Version string `json:"version"`
	// Scope is local_node because top snapshots are never cluster fan-out responses.
	Scope string `json:"scope"`
	// GeneratedAt is the UTC time when the provider assembled the snapshot.
	GeneratedAt time.Time `json:"generated_at"`
	// WindowSeconds is the aggregation window represented in seconds.
	WindowSeconds int `json:"window_seconds"`
	// Node identifies the local cluster node and readiness state.
	Node TopNodeSnapshot `json:"node"`
	// Verdict summarizes node health for the selected window.
	Verdict TopVerdict `json:"verdict"`
	// Traffic contains SEND, append, and delivery rates when requested.
	Traffic *TopTraffic `json:"traffic,omitempty"`
	// Clients contains gateway connection and churn data when requested.
	Clients *TopClients `json:"clients,omitempty"`
	// Resources contains local process CPU and memory usage for the node.
	Resources *TopResources `json:"resources,omitempty"`
	// Alerts contains sticky node-local warnings and errors retained across refreshes.
	Alerts *TopAlerts `json:"alerts,omitempty"`
	// Pressure contains scored runtime bottlenecks when available.
	Pressure *TopPressure `json:"pressure,omitempty"`
	// ChannelV2 contains channel runtime gauges and latency summaries.
	ChannelV2 *TopChannelV2 `json:"channelv2,omitempty"`
	// Storage contains local storage commit queue summaries.
	Storage *TopStorage `json:"storage,omitempty"`
	// Delivery contains delivery runtime rates, queues, and errors.
	Delivery *TopDelivery `json:"delivery,omitempty"`
	// Sources reports data source availability for this response.
	Sources TopSources `json:"sources"`
}

// TopNodeSnapshot describes the local node identity and cluster readiness state.
type TopNodeSnapshot struct {
	// ID is the numeric cluster node ID.
	ID uint64 `json:"id"`
	// Name is the operator-facing node name.
	Name string `json:"name"`
	// Ready reports whether required node-local cluster parts are ready.
	Ready bool `json:"ready"`
	// ReadyParts reports readiness by subsystem using stable low-cardinality keys.
	ReadyParts map[string]bool `json:"ready_parts,omitempty"`
	// StateRevision is the latest observed cluster state revision.
	StateRevision uint64 `json:"state_revision,omitempty"`
	// ControllerLeader is the node ID of the controller leader when known.
	ControllerLeader uint64 `json:"controller_leader,omitempty"`
	// SlotCount is the number of Slot replicas owned by this node.
	SlotCount uint32 `json:"slot_count,omitempty"`
	// HashSlotCount is the number of hash slots owned by this node.
	HashSlotCount uint16 `json:"hash_slot_count,omitempty"`
}

// TopVerdict summarizes the node health level for the selected window.
type TopVerdict struct {
	// Level is one of ok, busy, degraded, or critical.
	Level string `json:"level"`
	// Summary is a short operator-facing explanation.
	Summary string `json:"summary"`
	// Reasons lists the main signals that drove the verdict.
	Reasons []string `json:"reasons,omitempty"`
}

// TopTraffic contains message traffic rates and append latency percentiles.
type TopTraffic struct {
	// SendPerSec is inbound SEND packets per second.
	SendPerSec float64 `json:"send_per_sec"`
	// SendackPerSec is SENDACK responses per second.
	SendackPerSec float64 `json:"sendack_per_sec"`
	// SendackErrorPerSec is failed SENDACK responses per second.
	SendackErrorPerSec float64 `json:"sendack_error_per_sec"`
	// SendackErrorRate is failed SENDACK responses divided by total SENDACK responses.
	SendackErrorRate float64 `json:"sendack_error_rate"`
	// AppendPerSec is durable message appends per second.
	AppendPerSec float64 `json:"append_per_sec"`
	// AppendP50MS is the p50 append latency in milliseconds.
	AppendP50MS float64 `json:"append_p50_ms"`
	// AppendP99MS is the p99 append latency in milliseconds.
	AppendP99MS float64 `json:"append_p99_ms"`
	// DeliverPerSec is resolved message deliveries per second.
	DeliverPerSec float64 `json:"deliver_per_sec"`
	// FanoutRate is resolved delivery routes divided by inbound SEND count.
	FanoutRate float64 `json:"fanout_rate"`
}

// TopClients contains gateway client connection and churn metrics.
type TopClients struct {
	// Connections is the current number of gateway sessions.
	Connections int64 `json:"connections"`
	// ConnectionsByProtocol splits current sessions by transport protocol.
	ConnectionsByProtocol map[string]int64 `json:"connections_by_protocol,omitempty"`
	// AuthFailPerSec is authentication failures per second.
	AuthFailPerSec float64 `json:"auth_fail_per_sec"`
	// ClosePerSec is gateway session closes per second.
	ClosePerSec float64 `json:"close_per_sec"`
}

// TopResources contains process-level resource usage for one node.
type TopResources struct {
	// CPUPercent is process CPU usage for the latest sampling interval; one full core is 100%.
	CPUPercent float64 `json:"cpu_percent"`
	// MemoryRSSBytes is resident process memory in bytes.
	MemoryRSSBytes uint64 `json:"memory_rss_bytes"`
	// MemoryVMSBytes is virtual process memory in bytes.
	MemoryVMSBytes uint64 `json:"memory_vms_bytes"`
	// Goroutines is the current number of goroutines in the process.
	Goroutines int `json:"goroutines"`
	// Threads is the current number of OS threads in the process when available.
	Threads int `json:"threads"`
}

// TopAlerts contains active and recently resolved operational alerts.
type TopAlerts struct {
	// Counts summarizes active and recent alert severities.
	Counts TopAlertCounts `json:"counts"`
	// Active lists alerts that are still present in the latest collector sample.
	Active []TopAlert `json:"active,omitempty"`
	// Recent lists active and recently resolved alerts retained by the top collector.
	Recent []TopAlert `json:"recent,omitempty"`
}

// TopAlertCounts summarizes alert counts for a node-local top snapshot.
type TopAlertCounts struct {
	// Active is the number of currently active alerts.
	Active int `json:"active"`
	// Recent is the number of retained active or recently resolved alerts.
	Recent int `json:"recent"`
	// Warning is the number of active warning alerts.
	Warning int `json:"warning"`
	// Error is the number of active error alerts.
	Error int `json:"error"`
	// Critical is the number of active critical alerts.
	Critical int `json:"critical"`
}

// TopAlert describes one sticky warning or error surfaced by wkcli top.
type TopAlert struct {
	// ID is a stable node-local alert identity for this collector process.
	ID string `json:"id"`
	// Fingerprint is the stable de-duplication key for the alert kind and message.
	Fingerprint string `json:"fingerprint,omitempty"`
	// NodeID is the numeric cluster node ID that observed the alert.
	NodeID uint64 `json:"node_id,omitempty"`
	// NodeName is the operator-facing node name that observed the alert.
	NodeName string `json:"node_name,omitempty"`
	// Severity is one of warn, error, or critical.
	Severity string `json:"severity"`
	// Component is the subsystem that produced the alert.
	Component string `json:"component"`
	// Kind is a stable low-cardinality alert category.
	Kind string `json:"kind"`
	// Message is the concise operator-facing alert text.
	Message string `json:"message"`
	// Hint is an optional short remediation clue.
	Hint string `json:"hint,omitempty"`
	// Evidence contains stable key/value facts that explain why the alert fired.
	Evidence map[string]string `json:"evidence,omitempty"`
	// FirstSeen is the first time this alert fingerprint was observed.
	FirstSeen time.Time `json:"first_seen"`
	// LastSeen is the most recent time this alert fingerprint was observed.
	LastSeen time.Time `json:"last_seen"`
	// ResolvedAt is set when the latest collector sample no longer contains the alert.
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	// Count is the number of collector samples that observed this alert fingerprint.
	Count uint64 `json:"count"`
	// Active reports whether the latest collector sample still contains the alert.
	Active bool `json:"active"`
}

// TopPressure contains scored runtime pressure signals.
type TopPressure struct {
	// OverallLevel is the highest pressure level across components.
	OverallLevel string `json:"overall_level"`
	// ComponentScores maps component names to normalized pressure scores.
	ComponentScores map[string]float64 `json:"component_scores,omitempty"`
	// Top lists the highest pressure queues or runtime components.
	Top []TopPressureItem `json:"top,omitempty"`
}

// TopPressureItem describes one high-pressure runtime component or queue.
type TopPressureItem struct {
	// Component is the subsystem that owns the pressure signal.
	Component string `json:"component"`
	// Pool is the worker or resource pool name when applicable.
	Pool string `json:"pool,omitempty"`
	// Queue is the queue name when applicable.
	Queue string `json:"queue,omitempty"`
	// Priority is the queue priority label when applicable.
	Priority string `json:"priority,omitempty"`
	// Level is the pressure level for this item.
	Level string `json:"level"`
	// Score is a normalized pressure score, usually 0 through 1.
	Score float64 `json:"score"`
	// Depth is the current queue depth.
	Depth int64 `json:"depth,omitempty"`
	// Capacity is the queue capacity used to score depth pressure.
	Capacity int64 `json:"capacity,omitempty"`
	// Inflight is the current number of running tasks.
	Inflight int64 `json:"inflight,omitempty"`
	// Workers is the worker capacity used to score inflight pressure.
	Workers int64 `json:"workers,omitempty"`
	// WaitP99MS is p99 queue wait time in milliseconds.
	WaitP99MS float64 `json:"wait_p99_ms,omitempty"`
	// TaskP99MS is p99 task execution time in milliseconds.
	TaskP99MS float64 `json:"task_p99_ms,omitempty"`
	// AdmissionErrorPerSec is rejected or timed-out admissions per second.
	AdmissionErrorPerSec float64 `json:"admission_error_per_sec,omitempty"`
	// Hint is a short operator-facing remediation clue.
	Hint string `json:"hint,omitempty"`
}

// TopChannelV2 contains ChannelV2 runtime gauges and latency summaries.
type TopChannelV2 struct {
	// ActiveTotal is the number of active ChannelV2 runtimes.
	ActiveTotal int64 `json:"active_total"`
	// ActiveLeader is the number of active leader runtimes.
	ActiveLeader int64 `json:"active_leader"`
	// ActiveFollower is the number of active follower runtimes.
	ActiveFollower int64 `json:"active_follower"`
	// FollowerParked is the number of follower runtimes parked for backpressure.
	FollowerParked int64 `json:"follower_parked"`
	// ReactorMailboxDepthMax is the maximum observed reactor mailbox depth.
	ReactorMailboxDepthMax int64 `json:"reactor_mailbox_depth_max"`
	// ReactorMailboxCapacityMax is the maximum configured reactor mailbox capacity.
	ReactorMailboxCapacityMax int64 `json:"reactor_mailbox_capacity_max"`
	// WorkerQueueDepthByPool reports queue depth by ChannelV2 worker pool.
	WorkerQueueDepthByPool map[string]int64 `json:"worker_queue_depth_by_pool,omitempty"`
	// WorkerQueueCapacityByPool reports queue capacity by ChannelV2 worker pool.
	WorkerQueueCapacityByPool map[string]int64 `json:"worker_queue_capacity_by_pool,omitempty"`
	// WorkerInflightByPool reports in-flight tasks by ChannelV2 worker pool.
	WorkerInflightByPool map[string]int64 `json:"worker_inflight_by_pool,omitempty"`
	// WorkerCapacityByPool reports worker capacity by ChannelV2 worker pool.
	WorkerCapacityByPool map[string]int64 `json:"worker_capacity_by_pool,omitempty"`
	// AppendP99MS is p99 ChannelV2 append latency in milliseconds.
	AppendP99MS float64 `json:"append_p99_ms"`
	// HotStage is the append stage with the largest p99 latency.
	HotStage string `json:"hot_stage,omitempty"`
	// StageP99MS maps append stage names to p99 latency in milliseconds.
	StageP99MS map[string]float64 `json:"stage_p99_ms,omitempty"`
}

// TopStorage contains storage commit queue summaries.
type TopStorage struct {
	// CommitQueues lists local storage commit queues included in the snapshot.
	CommitQueues []TopStorageCommitQueue `json:"commit_queues,omitempty"`
}

// TopStorageCommitQueue describes one storage commit queue.
type TopStorageCommitQueue struct {
	// Store is the storage subsystem name.
	Store string `json:"store"`
	// Depth is the current commit queue depth.
	Depth int64 `json:"depth"`
	// Capacity is the configured commit queue capacity used for pressure scoring.
	Capacity int64 `json:"capacity"`
	// RequestP99MSByLane maps request lanes to p99 wait latency in milliseconds.
	RequestP99MSByLane map[string]float64 `json:"request_p99_ms_by_lane,omitempty"`
	// BatchRecordsP50 is the p50 number of records per commit batch.
	BatchRecordsP50 float64 `json:"batch_records_p50"`
	// BatchCommitP99MS is p99 physical commit duration in milliseconds.
	BatchCommitP99MS float64 `json:"batch_commit_p99_ms"`
}

// TopDelivery contains delivery runtime rates, queue gauges, and errors.
type TopDelivery struct {
	// PushPerSec is successful delivery pushes per second.
	PushPerSec float64 `json:"push_per_sec"`
	// RoutesPerSec is resolved recipient routes per second.
	RoutesPerSec float64 `json:"routes_per_sec"`
	// PushP99MS is p99 delivery push latency in milliseconds.
	PushP99MS float64 `json:"push_p99_ms"`
	// RetryQueueDepth is the current delivery retry queue depth.
	RetryQueueDepth int64 `json:"retry_queue_depth"`
	// AckBindings is the current number of delivery acknowledgment bindings.
	AckBindings int64 `json:"ack_bindings"`
	// RecipientQueueDepth is the current recipient queue depth.
	RecipientQueueDepth int64 `json:"recipient_queue_depth"`
	// RecipientQueueCapacity is the recipient queue capacity used for pressure scoring.
	RecipientQueueCapacity int64 `json:"recipient_queue_capacity"`
	// ErrorRate is delivery errors divided by delivery attempts.
	ErrorRate float64 `json:"error_rate"`
}

// TopSources reports which sources contributed to the snapshot.
type TopSources struct {
	// Collector reports top collector availability and sample count.
	Collector TopSourceStatus `json:"collector"`
	// ClusterSnapshot reports local cluster-state snapshot availability.
	ClusterSnapshot TopSourceStatus `json:"cluster_snapshot"`
	// Metrics reports whether Prometheus metrics are enabled or required.
	Metrics TopMetricsSource `json:"metrics"`
	// Notes lists non-fatal source omissions or partial data details.
	Notes []string `json:"notes,omitempty"`
}

// TopSourceStatus reports availability and sample freshness for one source.
type TopSourceStatus struct {
	// Available reports whether the source contributed data.
	Available bool `json:"available"`
	// SampleCount is the number of samples used from this source.
	SampleCount int `json:"sample_count,omitempty"`
	// WarmingUp reports that the source exists but lacks enough samples.
	WarmingUp bool `json:"warming_up,omitempty"`
}

// TopMetricsSource reports metrics integration status for the top API.
type TopMetricsSource struct {
	// Enabled reports whether the Prometheus metrics endpoint is enabled.
	Enabled bool `json:"enabled"`
	// Required is always false for top/v1 because top does not depend on metrics.
	Required bool `json:"required"`
}

func parseTopSnapshotQuery(c *gin.Context) (TopSnapshotQuery, error) {
	query := TopSnapshotQuery{Window: defaultTopWindow, View: TopViewOverview, Limit: defaultTopLimit}
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return query, fmt.Errorf("window must be a duration")
		}
		query.Window = d
	}
	if query.Window < minTopWindow {
		return query, fmt.Errorf("window must be at least %s", minTopWindow)
	}
	if raw := strings.TrimSpace(c.Query("view")); raw != "" {
		view := TopView(raw)
		switch view {
		case TopViewOverview, TopViewRuntime, TopViewTraffic, TopViewChannel, TopViewStorage, TopViewDelivery, TopViewAll:
			query.View = view
		default:
			return query, fmt.Errorf("invalid view")
		}
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return query, fmt.Errorf("limit must be positive")
		}
		if n > maxTopLimit {
			n = maxTopLimit
		}
		query.Limit = n
	}
	return query, nil
}

func (s *Server) handleTopSnapshot(c *gin.Context) {
	if s == nil || s.top == nil {
		writeTopError(c, http.StatusNotFound, "top snapshot provider is not configured")
		return
	}
	query, err := parseTopSnapshotQuery(c)
	if err != nil {
		writeTopError(c, http.StatusBadRequest, err.Error())
		return
	}
	snapshot, err := s.top.SnapshotTop(c.Request.Context(), query)
	if err != nil {
		if errors.Is(err, ErrTopWarmingUp) {
			writeTopError(c, http.StatusServiceUnavailable, "top collector warming up")
			return
		}
		s.logTopSnapshotFailure(c, err)
		writeTopError(c, http.StatusInternalServerError, "top snapshot provider failed")
		return
	}
	if snapshot.Version == "" {
		snapshot.Version = topVersionV1
	}
	if snapshot.Scope == "" {
		snapshot.Scope = topScopeLocal
	}
	c.JSON(http.StatusOK, snapshot)
}

func writeTopError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": message})
}

func (s *Server) logTopSnapshotFailure(c *gin.Context, err error) {
	if err == nil {
		return
	}
	path := ""
	method := ""
	if c != nil && c.Request != nil {
		r := c.Request
		method = r.Method
		if r.URL != nil {
			path = r.URL.Path
		}
	}
	s.httpLogger().Error("top snapshot request failed",
		wklog.Event("internalv2.access.api.top_snapshot_failed"),
		wklog.String("method", method),
		wklog.String("path", path),
		wklog.Error(err),
	)
}
