package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

// ErrListenAddrRequired reports that the manager listen address is missing.
var ErrListenAddrRequired = errors.New("access/manager: listen address required")

// Management exposes the manager usecases needed by HTTP handlers.
type Management interface {
	// ListNodes returns manager-facing node DTOs.
	ListNodes(ctx context.Context) (managementusecase.NodeList, error)
	// GetNode returns one manager-facing node detail DTO.
	GetNode(ctx context.Context, nodeID uint64) (managementusecase.NodeDetail, error)
	// MarkNodeDraining marks a node as draining and returns the latest detail DTO.
	MarkNodeDraining(ctx context.Context, nodeID uint64) (managementusecase.NodeDetail, error)
	// ResumeNode marks a node as alive and returns the latest detail DTO.
	ResumeNode(ctx context.Context, nodeID uint64) (managementusecase.NodeDetail, error)
	// PlanNodeScaleIn returns the manager-driven scale-in safety report without side effects.
	PlanNodeScaleIn(ctx context.Context, nodeID uint64, req managementusecase.NodeScaleInPlanRequest) (managementusecase.NodeScaleInReport, error)
	// StartNodeScaleIn marks a preflight-safe node as draining and returns the refreshed report.
	StartNodeScaleIn(ctx context.Context, nodeID uint64, req managementusecase.NodeScaleInPlanRequest) (managementusecase.NodeScaleInReport, error)
	// GetNodeScaleInStatus returns the current manager-driven scale-in report.
	GetNodeScaleInStatus(ctx context.Context, nodeID uint64) (managementusecase.NodeScaleInReport, error)
	// AdvanceNodeScaleIn performs one bounded scale-in advancement step.
	AdvanceNodeScaleIn(ctx context.Context, nodeID uint64, req managementusecase.AdvanceNodeScaleInRequest) (managementusecase.NodeScaleInReport, error)
	// CancelNodeScaleIn resumes a draining scale-in target and returns the refreshed report.
	CancelNodeScaleIn(ctx context.Context, nodeID uint64) (managementusecase.NodeScaleInReport, error)
	// ListSlots returns manager-facing slot DTOs.
	ListSlots(ctx context.Context, opts managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error)
	// GetSlot returns one manager-facing slot detail DTO.
	GetSlot(ctx context.Context, slotID uint32) (managementusecase.SlotDetail, error)
	// ListSlotLogEntries returns one node-local Slot Raft log page.
	ListSlotLogEntries(ctx context.Context, req managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error)
	// ListControllerLogEntries returns one node-local Controller Raft log page.
	ListControllerLogEntries(ctx context.Context, req managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error)
	// GetControllerRaftStatus returns one node-local Controller Raft status snapshot.
	GetControllerRaftStatus(ctx context.Context, nodeID uint64) (managementusecase.ControllerRaftStatusResponse, error)
	// CompactControllerRaftLogs triggers node-local Controller Raft log compaction on all Controller voters.
	CompactControllerRaftLogs(ctx context.Context) (managementusecase.CompactControllerRaftLogsResponse, error)
	// CompactControllerRaftLog triggers node-local Controller Raft log compaction on one selected node.
	CompactControllerRaftLog(ctx context.Context, nodeID uint64) (managementusecase.CompactControllerRaftLogsResponse, error)
	// CompactSlotRaftLog triggers node-local Slot Raft log compaction on one selected node and Slot.
	CompactSlotRaftLog(ctx context.Context, nodeID uint64, slotID uint32) (managementusecase.CompactSlotRaftLogResponse, error)
	// AddSlot creates a new physical slot and returns the latest detail DTO.
	AddSlot(ctx context.Context) (managementusecase.SlotDetail, error)
	// RemoveSlot starts physical slot removal and returns the accepted outcome DTO.
	RemoveSlot(ctx context.Context, slotID uint32) (managementusecase.SlotRemoveResult, error)
	// TransferSlotLeader transfers one slot leader and returns the latest detail DTO.
	TransferSlotLeader(ctx context.Context, slotID uint32, targetNodeID uint64) (managementusecase.SlotDetail, error)
	// RecoverSlot runs one slot recover flow and returns the latest outcome DTO.
	RecoverSlot(ctx context.Context, slotID uint32, strategy managementusecase.SlotRecoverStrategy) (managementusecase.SlotRecoverResult, error)
	// RebalanceSlots starts one slot rebalance flow and returns the started plan DTO.
	RebalanceSlots(ctx context.Context) (managementusecase.SlotRebalanceResult, error)
	// ListTasks returns manager-facing reconcile task DTOs.
	ListTasks(ctx context.Context) ([]managementusecase.Task, error)
	// GetTask returns one manager-facing reconcile task detail DTO.
	GetTask(ctx context.Context, slotID uint32) (managementusecase.TaskDetail, error)
	// GetDistributedTasksSummary returns normalized read-only task counts across task domains.
	GetDistributedTasksSummary(ctx context.Context) (managementusecase.DistributedTaskSummary, error)
	// ListDistributedTasks returns one normalized read-only distributed task page.
	ListDistributedTasks(ctx context.Context, query managementusecase.DistributedTaskQuery) (managementusecase.DistributedTaskListResult, error)
	// GetDistributedTask returns one normalized read-only distributed task detail.
	GetDistributedTask(ctx context.Context, domain managementusecase.DistributedTaskDomain, id string) (managementusecase.DistributedTaskDetail, error)
	// ListConnections returns manager-facing node-local connection DTOs.
	ListConnections(ctx context.Context, req managementusecase.ListConnectionsRequest) ([]managementusecase.Connection, error)
	// GetConnection returns one manager-facing node-local connection detail DTO.
	GetConnection(ctx context.Context, req managementusecase.GetConnectionRequest) (managementusecase.ConnectionDetail, error)
	// ListUsers returns one manager-facing user page.
	ListUsers(ctx context.Context, req managementusecase.ListUsersRequest) (managementusecase.ListUsersResponse, error)
	// GetUser returns one manager-facing user detail.
	GetUser(ctx context.Context, uid string) (managementusecase.UserDetail, error)
	// KickUser forces one user's sessions offline.
	KickUser(ctx context.Context, req managementusecase.KickUserRequest) (managementusecase.KickUserResponse, error)
	// ResetUserToken resets one user device token.
	ResetUserToken(ctx context.Context, req managementusecase.ResetUserTokenRequest) (managementusecase.ResetUserTokenResponse, error)
	// ListSystemUsers returns persisted manager-facing system UIDs.
	ListSystemUsers(ctx context.Context) (managementusecase.ListSystemUsersResponse, error)
	// AddSystemUsers persists system UIDs.
	AddSystemUsers(ctx context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error)
	// RemoveSystemUsers removes persisted system UIDs.
	RemoveSystemUsers(ctx context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error)
	// ListBusinessChannels returns one manager-facing business channel page.
	ListBusinessChannels(ctx context.Context, req managementusecase.ListBusinessChannelsRequest) (managementusecase.ListBusinessChannelsResponse, error)
	// GetBusinessChannel returns one manager-facing business channel detail.
	GetBusinessChannel(ctx context.Context, channelID string, channelType int64) (managementusecase.BusinessChannelDetail, error)
	// UpsertBusinessChannel creates or updates business channel metadata.
	UpsertBusinessChannel(ctx context.Context, req managementusecase.UpsertBusinessChannelRequest) (managementusecase.BusinessChannelDetail, error)
	// ListBusinessChannelMembers returns one manager-facing channel member page.
	ListBusinessChannelMembers(ctx context.Context, req managementusecase.ListBusinessChannelMembersRequest) (managementusecase.ListBusinessChannelMembersResponse, error)
	// MutateBusinessChannelMembers adds or removes manager-facing channel members.
	MutateBusinessChannelMembers(ctx context.Context, req managementusecase.MutateBusinessChannelMembersRequest) (managementusecase.MutateBusinessChannelMembersResponse, error)
	// ListChannelRuntimeMeta returns one manager-facing channel runtime metadata page.
	ListChannelRuntimeMeta(ctx context.Context, req managementusecase.ListChannelRuntimeMetaRequest) (managementusecase.ListChannelRuntimeMetaResponse, error)
	// GetChannelRuntimeMeta returns one manager-facing channel runtime metadata detail DTO.
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (managementusecase.ChannelRuntimeMetaDetail, error)
	// GetChannelClusterSummary returns manager-facing channel ISR health counters.
	GetChannelClusterSummary(ctx context.Context) (managementusecase.ChannelClusterSummary, error)
	// ListChannelClusterUnhealthy returns one manager-facing unhealthy channel page.
	ListChannelClusterUnhealthy(ctx context.Context, req managementusecase.ListChannelClusterUnhealthyRequest) (managementusecase.ListChannelClusterUnhealthyResponse, error)
	// GetChannelClusterReplicaDetail returns authoritative and proven runtime replica detail.
	GetChannelClusterReplicaDetail(ctx context.Context, channelID string, channelType int64) (managementusecase.ChannelClusterReplicaDetail, error)
	// RepairChannelClusterLeader runs policy-driven safe leader repair for one channel.
	RepairChannelClusterLeader(ctx context.Context, req managementusecase.RepairChannelClusterLeaderRequest) (managementusecase.RepairChannelClusterLeaderResponse, error)
	// TransferChannelClusterLeader safely transfers one channel leader to an explicit replica.
	TransferChannelClusterLeader(ctx context.Context, req managementusecase.TransferChannelClusterLeaderRequest) (managementusecase.TransferChannelClusterLeaderResponse, error)
	// ListMessages returns one manager-facing channel message page.
	ListMessages(ctx context.Context, req managementusecase.ListMessagesRequest) (managementusecase.ListMessagesResponse, error)
	// ListRecentConversations returns one manager-facing UID recent conversation working set.
	ListRecentConversations(ctx context.Context, req managementusecase.RecentConversationsRequest) (managementusecase.RecentConversationsResponse, error)
	// AdvanceMessageRetention advances one channel's history retention boundary.
	AdvanceMessageRetention(ctx context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error)
	// TransferChannelLeader validates or creates a channel leader-transfer task.
	TransferChannelLeader(ctx context.Context, id channel.ChannelID, req managementusecase.TransferChannelLeaderRequest) (managementusecase.ChannelMigrationResult, error)
	// MigrateChannelReplica validates or creates a channel replica replacement task.
	MigrateChannelReplica(ctx context.Context, id channel.ChannelID, req managementusecase.MigrateChannelReplicaRequest) (managementusecase.ChannelMigrationResult, error)
	// GetChannelMigration returns the active channel migration task.
	GetChannelMigration(ctx context.Context, id channel.ChannelID) (managementusecase.ChannelMigrationDetail, error)
	// AbortChannelMigration aborts the active channel migration task.
	AbortChannelMigration(ctx context.Context, id channel.ChannelID, taskID string) (managementusecase.ChannelMigrationDetail, error)
	// GetOverview returns the manager homepage overview DTO.
	GetOverview(ctx context.Context) (managementusecase.Overview, error)
	// ListNetworkSummary returns the manager-facing local-node cluster network summary.
	ListNetworkSummary(ctx context.Context) (managementusecase.NetworkSummary, error)
	// ListNodeOnboardingCandidates returns nodes eligible for explicit onboarding allocation.
	ListNodeOnboardingCandidates(ctx context.Context) (managementusecase.NodeOnboardingCandidatesResponse, error)
	// CreateNodeOnboardingPlan creates a durable planned onboarding job for review.
	CreateNodeOnboardingPlan(ctx context.Context, req managementusecase.CreateNodeOnboardingPlanRequest) (managementusecase.NodeOnboardingJobResponse, error)
	// StartNodeOnboardingJob starts a reviewed onboarding job.
	StartNodeOnboardingJob(ctx context.Context, jobID string) (managementusecase.NodeOnboardingJobResponse, error)
	// ListNodeOnboardingJobs returns durable onboarding jobs.
	ListNodeOnboardingJobs(ctx context.Context, req managementusecase.ListNodeOnboardingJobsRequest) (managementusecase.NodeOnboardingJobsResponse, error)
	// GetNodeOnboardingJob returns one durable onboarding job.
	GetNodeOnboardingJob(ctx context.Context, jobID string) (managementusecase.NodeOnboardingJobResponse, error)
	// RetryNodeOnboardingJob creates a new plan from a failed onboarding job.
	RetryNodeOnboardingJob(ctx context.Context, jobID string) (managementusecase.NodeOnboardingJobResponse, error)
	// QueryDiagnostics returns a manager-facing diagnostics aggregate query result.
	QueryDiagnostics(ctx context.Context, req managementusecase.DiagnosticsQueryRequest) (managementusecase.DiagnosticsQueryResponse, error)
	// CreateDiagnosticsTrackingRule installs a temporary diagnostics tracking rule.
	CreateDiagnosticsTrackingRule(ctx context.Context, req managementusecase.DiagnosticsTrackingCreateRequest) (managementusecase.DiagnosticsTrackingMutationResponse, error)
	// ListDiagnosticsTrackingRules returns active temporary diagnostics tracking rules.
	ListDiagnosticsTrackingRules(ctx context.Context) (managementusecase.DiagnosticsTrackingListResponse, error)
	// DeleteDiagnosticsTrackingRule removes a temporary diagnostics tracking rule.
	DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) (managementusecase.DiagnosticsTrackingDeleteResponse, error)
	// GetDashboardMetrics returns time-series dashboard metrics for the given window and step.
	GetDashboardMetrics(window, step time.Duration) (managementusecase.DashboardMetricsResult, error)
	// ListNodePlugins returns one node's local plugin inventory.
	ListNodePlugins(ctx context.Context, nodeID uint64) (pluginusecase.LocalPluginList, error)
	// GetNodePlugin returns one node-local plugin detail.
	GetNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error)
	// UpdateNodePluginConfig persists desired config for one node-local plugin.
	UpdateNodePluginConfig(ctx context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (pluginusecase.LocalPluginDetail, error)
	// RestartNodePlugin restarts one node-local plugin process.
	RestartNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error)
	// UninstallNodePlugin disables and removes one node-local plugin process.
	UninstallNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) error
	// ListPluginBindings returns cluster-authoritative plugin-user bindings.
	ListPluginBindings(ctx context.Context, req managementusecase.PluginBindingListRequest) (managementusecase.PluginBindingListResponse, error)
	// BindPluginUser creates or updates a cluster-authoritative plugin-user binding.
	BindPluginUser(ctx context.Context, req managementusecase.PluginBindingMutationRequest) (managementusecase.PluginBindingMutationResponse, error)
	// UnbindPluginUser removes a cluster-authoritative plugin-user binding.
	UnbindPluginUser(ctx context.Context, req managementusecase.PluginBindingMutationRequest) error
}

// PermissionConfig binds a resource to allowed actions.
type PermissionConfig struct {
	// Resource is the protected manager resource name; use "*" to grant all manager resources.
	Resource string
	// Actions contains the allowed action codes.
	Actions []string
}

// UserConfig describes one static manager login user.
type UserConfig struct {
	// Username is the static login identity.
	Username string
	// Password is the static login secret.
	Password string
	// Permissions lists the resource permissions granted to the user.
	Permissions []PermissionConfig
}

// AuthConfig configures manager JWT authentication and permissions.
type AuthConfig struct {
	// On enables JWT login and permission enforcement.
	On bool
	// JWTSecret is the HMAC signing secret for manager tokens.
	JWTSecret string
	// JWTIssuer is the issuer claim used in manager tokens.
	JWTIssuer string
	// JWTExpire is the token lifetime used for new manager tokens.
	JWTExpire time.Duration
	// Users contains the configured static manager users.
	Users []UserConfig
}

// Options configures the manager HTTP server.
type Options struct {
	// ListenAddr is the manager server listen address.
	ListenAddr string
	// Auth configures manager JWT login and permissions.
	Auth AuthConfig
	// Management provides the manager read usecases.
	Management Management
	// Logger is the logger used by the manager server.
	Logger wklog.Logger
}

// Server serves the manager HTTP API.
type Server struct {
	mu         sync.RWMutex
	engine     *gin.Engine
	httpServer *http.Server
	listener   net.Listener
	listenAddr string
	addr       string
	management Management
	auth       authState
	logger     wklog.Logger
	started    bool
}

// New constructs a manager HTTP server.
func New(opts Options) *Server {
	if gin.Mode() != gin.ReleaseMode {
		gin.SetMode(gin.ReleaseMode)
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	engine := gin.New()
	engine.Use(openCORSMiddleware())
	srv := &Server{
		engine:     engine,
		listenAddr: opts.ListenAddr,
		management: opts.Management,
		auth:       newAuthState(opts.Auth),
		logger:     opts.Logger,
	}
	srv.registerRoutes()
	return srv
}

// Engine returns the underlying gin engine.
func (s *Server) Engine() *gin.Engine {
	if s == nil {
		return nil
	}
	return s.engine
}

// Addr returns the resolved manager listen address after Start.
func (s *Server) Addr() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// Start begins serving the manager HTTP API.
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
	engine := s.engine
	s.mu.Unlock()

	if listenAddr == "" {
		return ErrListenAddrRequired
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	httpServer := &http.Server{Handler: engine}

	s.mu.Lock()
	s.listener = ln
	s.httpServer = httpServer
	s.addr = ln.Addr().String()
	s.started = true
	s.mu.Unlock()

	go func() {
		_ = httpServer.Serve(ln)
	}()

	return nil
}

// Stop gracefully shuts the manager HTTP server down.
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
