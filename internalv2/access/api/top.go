package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
}

// TopSnapshot is the JSON response model for the node-local top API.
type TopSnapshot struct {
	Version       string          `json:"version"`
	Scope         string          `json:"scope"`
	GeneratedAt   time.Time       `json:"generated_at"`
	WindowSeconds int             `json:"window_seconds"`
	Node          TopNodeSnapshot `json:"node"`
	Verdict       TopVerdict      `json:"verdict"`
	Traffic       *TopTraffic     `json:"traffic,omitempty"`
	Clients       *TopClients     `json:"clients,omitempty"`
	Pressure      *TopPressure    `json:"pressure,omitempty"`
	ChannelV2     *TopChannelV2   `json:"channelv2,omitempty"`
	Storage       *TopStorage     `json:"storage,omitempty"`
	Delivery      *TopDelivery    `json:"delivery,omitempty"`
	Sources       TopSources      `json:"sources"`
}

// TopNodeSnapshot describes the local node identity and cluster readiness state.
type TopNodeSnapshot struct {
	ID               uint64          `json:"id"`
	Name             string          `json:"name"`
	Ready            bool            `json:"ready"`
	ReadyParts       map[string]bool `json:"ready_parts,omitempty"`
	StateRevision    uint64          `json:"state_revision,omitempty"`
	ControllerLeader uint64          `json:"controller_leader,omitempty"`
	SlotCount        uint32          `json:"slot_count,omitempty"`
	HashSlotCount    uint16          `json:"hash_slot_count,omitempty"`
}

// TopVerdict summarizes the node health level for the selected window.
type TopVerdict struct {
	Level   string   `json:"level"`
	Summary string   `json:"summary"`
	Reasons []string `json:"reasons,omitempty"`
}

// TopTraffic contains message traffic rates and append latency percentiles.
type TopTraffic struct {
	SendPerSec         float64 `json:"send_per_sec"`
	SendackPerSec      float64 `json:"sendack_per_sec"`
	SendackErrorPerSec float64 `json:"sendack_error_per_sec"`
	SendackErrorRate   float64 `json:"sendack_error_rate"`
	AppendPerSec       float64 `json:"append_per_sec"`
	AppendP50MS        float64 `json:"append_p50_ms"`
	AppendP99MS        float64 `json:"append_p99_ms"`
	DeliverPerSec      float64 `json:"deliver_per_sec"`
	FanoutRate         float64 `json:"fanout_rate"`
}

// TopClients contains gateway client connection and churn metrics.
type TopClients struct {
	Connections           int64            `json:"connections"`
	ConnectionsByProtocol map[string]int64 `json:"connections_by_protocol,omitempty"`
	AuthFailPerSec        float64          `json:"auth_fail_per_sec"`
	ClosePerSec           float64          `json:"close_per_sec"`
}

// TopPressure contains scored runtime pressure signals.
type TopPressure struct {
	OverallLevel    string             `json:"overall_level"`
	ComponentScores map[string]float64 `json:"component_scores,omitempty"`
	Top             []TopPressureItem  `json:"top,omitempty"`
}

// TopPressureItem describes one high-pressure runtime component or queue.
type TopPressureItem struct {
	Component            string  `json:"component"`
	Pool                 string  `json:"pool,omitempty"`
	Queue                string  `json:"queue,omitempty"`
	Priority             string  `json:"priority,omitempty"`
	Level                string  `json:"level"`
	Score                float64 `json:"score"`
	Depth                int64   `json:"depth,omitempty"`
	Capacity             int64   `json:"capacity,omitempty"`
	Inflight             int64   `json:"inflight,omitempty"`
	Workers              int64   `json:"workers,omitempty"`
	WaitP99MS            float64 `json:"wait_p99_ms,omitempty"`
	TaskP99MS            float64 `json:"task_p99_ms,omitempty"`
	AdmissionErrorPerSec float64 `json:"admission_error_per_sec,omitempty"`
	Hint                 string  `json:"hint,omitempty"`
}

// TopChannelV2 contains ChannelV2 runtime gauges and latency summaries.
type TopChannelV2 struct {
	ActiveTotal            int64              `json:"active_total"`
	ActiveLeader           int64              `json:"active_leader"`
	ActiveFollower         int64              `json:"active_follower"`
	FollowerParked         int64              `json:"follower_parked"`
	ReactorMailboxDepthMax int64              `json:"reactor_mailbox_depth_max"`
	WorkerQueueDepthByPool map[string]int64   `json:"worker_queue_depth_by_pool,omitempty"`
	WorkerInflightByPool   map[string]int64   `json:"worker_inflight_by_pool,omitempty"`
	AppendP99MS            float64            `json:"append_p99_ms"`
	HotStage               string             `json:"hot_stage,omitempty"`
	StageP99MS             map[string]float64 `json:"stage_p99_ms,omitempty"`
}

// TopStorage contains storage commit queue summaries.
type TopStorage struct {
	CommitQueues []TopStorageCommitQueue `json:"commit_queues,omitempty"`
}

// TopStorageCommitQueue describes one storage commit queue.
type TopStorageCommitQueue struct {
	Store              string             `json:"store"`
	Depth              int64              `json:"depth"`
	RequestP99MSByLane map[string]float64 `json:"request_p99_ms_by_lane,omitempty"`
	BatchRecordsP50    float64            `json:"batch_records_p50"`
	BatchCommitP99MS   float64            `json:"batch_commit_p99_ms"`
}

// TopDelivery contains delivery runtime rates, queue gauges, and errors.
type TopDelivery struct {
	PushPerSec             float64 `json:"push_per_sec"`
	RoutesPerSec           float64 `json:"routes_per_sec"`
	PushP99MS              float64 `json:"push_p99_ms"`
	RetryQueueDepth        int64   `json:"retry_queue_depth"`
	AckBindings            int64   `json:"ack_bindings"`
	RecipientQueueDepth    int64   `json:"recipient_queue_depth"`
	RecipientQueueCapacity int64   `json:"recipient_queue_capacity"`
	ErrorRate              float64 `json:"error_rate"`
}

// TopSources reports which sources contributed to the snapshot.
type TopSources struct {
	Collector       TopSourceStatus  `json:"collector"`
	ClusterSnapshot TopSourceStatus  `json:"cluster_snapshot"`
	Metrics         TopMetricsSource `json:"metrics"`
	Notes           []string         `json:"notes,omitempty"`
}

// TopSourceStatus reports availability and sample freshness for one source.
type TopSourceStatus struct {
	Available   bool `json:"available"`
	SampleCount int  `json:"sample_count,omitempty"`
	WarmingUp   bool `json:"warming_up,omitempty"`
}

// TopMetricsSource reports metrics integration status for the top API.
type TopMetricsSource struct {
	Enabled  bool `json:"enabled"`
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
			writeTopError(c, http.StatusServiceUnavailable, err.Error())
			return
		}
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
