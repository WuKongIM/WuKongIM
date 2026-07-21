package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	rpcStatusOK                      = "ok"
	rpcStatusNotLeader               = "not_leader"
	rpcStatusStaleRoute              = "stale_route"
	rpcStatusRouteNotReady           = "route_not_ready"
	rpcStatusContextCanceled         = "context_canceled"
	rpcStatusContextDeadlineExceeded = "context_deadline_exceeded"
	rpcStatusNotFound                = "not_found"
	rpcStatusInvalidArgument         = "invalid_argument"
	rpcStatusRejected                = "rejected"

	presenceOpRegisterRoute      = "register_route"
	presenceOpCommitRoute        = "commit_route"
	presenceOpAbortRoute         = "abort_route"
	presenceOpUnregisterRoute    = "unregister_route"
	presenceOpEndpointsByUID     = "endpoints_by_uid"
	presenceOpEndpointsByTargets = "endpoints_by_targets"
	presenceOpTouchRoutes        = "touch_routes"
	presenceOpApplyRouteAction   = "apply_route_action"
)

// PresenceAuthorityRPCServiceID is the cluster RPC service for UID route authority calls.
const PresenceAuthorityRPCServiceID uint8 = clusternet.RPCPresenceAuthority

// PresenceOwnerRPCServiceID is the cluster RPC service for owner-node actions.
const PresenceOwnerRPCServiceID uint8 = clusternet.RPCPresenceOwner

// PresenceAuthority is the authority-side route API exposed over node RPC.
type PresenceAuthority interface {
	RegisterRoute(context.Context, presence.RouteTarget, presence.Route) (presence.RegisterResult, error)
	CommitRoute(context.Context, presence.RouteTarget, string) error
	AbortRoute(context.Context, presence.RouteTarget, string) error
	UnregisterRoute(context.Context, presence.RouteTarget, presence.RouteIdentity, uint64) error
	EndpointsByUID(context.Context, presence.RouteTarget, string) ([]presence.Route, error)
	TouchRoutes(context.Context, presence.RouteTarget, []presence.Route) error
}

// PresenceBatchAuthority optionally resolves multiple UIDs under one exact authority fence.
type PresenceBatchAuthority interface {
	EndpointsByUIDs(context.Context, presence.RouteTarget, []string) ([]presence.Route, error)
}

// PresenceOwner applies authority-requested actions to owner-local sessions.
type PresenceOwner interface {
	ApplyRouteAction(context.Context, presence.RouteAction) error
}

// DeliveryOwnerPush accepts owner-node delivery batches over node RPC.
type DeliveryOwnerPush interface {
	Push(context.Context, runtimedelivery.PushCommand) (runtimedelivery.PushResult, error)
}

// DeliveryFanoutRunner accepts authority-node fanout tasks over node RPC.
type DeliveryFanoutRunner interface {
	RunTask(context.Context, runtimedelivery.FanoutTask) error
}

// ConversationAuthority handles UID-owned conversation active cache and durable hide requests.
type ConversationAuthority interface {
	AdmitPatches(context.Context, conversationusecase.RouteTarget, []conversationusecase.ActivePatch) error
	// AdmitActiveBatch admits one already-routed channelappend active batch at the target authority.
	AdmitActiveBatch(context.Context, conversationusecase.RouteTarget, conversationactive.ActiveBatch) error
	// HideConversationsForTarget applies exact hide mutations at one fenced UID authority target.
	HideConversationsForTarget(context.Context, conversationusecase.RouteTarget, []metadb.ConversationDelete) error
	ListConversationActiveViewForTarget(context.Context, conversationusecase.RouteTarget, metadb.ConversationKind, string, metadb.ConversationActiveCursor, int) (conversationusecase.ActiveViewPage, error)
	DrainAuthority(context.Context, conversationusecase.RouteTarget) (string, error)
}

// ManagerConnectionReader handles owner-local manager connection inventory requests.
type ManagerConnectionReader interface {
	ListConnections(context.Context, managementusecase.ListConnectionsRequest) ([]managementusecase.Connection, error)
	GetConnection(context.Context, managementusecase.GetConnectionRequest) (managementusecase.ConnectionDetail, error)
	NodeRuntimeSummary(context.Context, uint64) (managementusecase.NodeRuntimeSummary, error)
	SetNodeDrainMode(context.Context, managementusecase.SetNodeDrainModeRequest) (managementusecase.SetNodeDrainModeResponse, error)
}

// ManagerLogReader handles node-local manager distributed log page requests.
type ManagerLogReader interface {
	ControllerLogEntries(context.Context, managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error)
	SlotLogEntries(context.Context, managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error)
}

// ManagerControllerRaftOperator handles node-local manager Controller Raft operations.
type ManagerControllerRaftOperator interface {
	ControllerRaftStatus(context.Context, uint64) (managementusecase.ControllerRaftStatus, error)
	CompactControllerRaftLog(context.Context, uint64) (managementusecase.ControllerRaftCompactionResult, error)
}

// ManagerSlotRaftOperator handles node-local manager Slot Raft operations.
type ManagerSlotRaftOperator interface {
	SlotRaftStatus(context.Context, uint64, uint32) (managementusecase.SlotNodeLogStatus, error)
	CompactSlotRaftLog(context.Context, uint64, uint32) (managementusecase.SlotRaftCompactionResult, error)
}

// ManagerChannelReader handles node-local manager channel list requests.
type ManagerChannelReader interface {
	ListBusinessChannels(context.Context, managementusecase.ListBusinessChannelsRequest) (managementusecase.ListBusinessChannelsResponse, error)
}

// ManagerMessageRetentionOperator handles node-local manager message retention requests.
type ManagerMessageRetentionOperator interface {
	AdvanceMessageRetention(context.Context, managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error)
}

// ManagerDBInspectReader handles node-local manager DB inspect requests.
type ManagerDBInspectReader interface {
	QueryDBInspect(context.Context, managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error)
}

// ManagerAppLogReader handles selected-node ordinary application log requests.
type ManagerAppLogReader interface {
	ApplicationLogSources(context.Context, managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error)
	ApplicationLogEntries(context.Context, managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error)
}

// ManagerNodeConfigReader handles selected-node effective startup config requests.
type ManagerNodeConfigReader interface {
	// NodeConfigSnapshot returns one selected node's allowlisted effective startup config.
	NodeConfigSnapshot(context.Context, uint64) (managementusecase.NodeConfigSnapshot, error)
}

// ManagerDiagnostics handles node-local diagnostics queries and tracking rules.
type ManagerDiagnostics interface {
	QueryDiagnostics(ctx context.Context, query diagnostics.Query) diagnostics.QueryResult
	AddDiagnosticsTrackingRule(ctx context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
	ListDiagnosticsTrackingRules(ctx context.Context) ([]diagnostics.TrackingRule, error)
	DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) error
}

// ManagerTaskAuditReader handles node-local Controller task audit reads.
type ManagerTaskAuditReader interface {
	// ListControllerTaskAudits returns retained Controller task histories.
	ListControllerTaskAudits(context.Context, managementusecase.ControllerTaskAuditListRequest) (managementusecase.ControllerTaskAuditListResponse, error)
	// ControllerTaskAuditEvents returns retained events for one Controller task.
	ControllerTaskAuditEvents(context.Context, string) (managementusecase.ControllerTaskAuditEventsResponse, error)
}

// ManagerPluginReader handles node-local manager plugin inventory requests.
type ManagerPluginReader interface {
	// ListNodePlugins returns this node's plugin inventory projection.
	ListNodePlugins(context.Context, uint64) (managementusecase.NodePluginList, error)
	// GetNodePlugin returns one plugin detail from this node.
	GetNodePlugin(context.Context, uint64, string) (managementusecase.Plugin, error)
	// UpdateNodePluginConfig persists desired config for one plugin on this node.
	UpdateNodePluginConfig(context.Context, uint64, string, json.RawMessage) (managementusecase.Plugin, error)
	// RestartNodePlugin restarts one plugin process on this node.
	RestartNodePlugin(context.Context, uint64, string) (managementusecase.Plugin, error)
	// UninstallNodePlugin disables and removes one plugin process on this node.
	UninstallNodePlugin(context.Context, uint64, string) error
}

// NodeLifecycleManager handles validated seed-join lifecycle requests.
type NodeLifecycleManager interface {
	JoinNode(context.Context, managementusecase.JoinNodeRequest) (managementusecase.JoinNodeResponse, error)
}

// NodeReadinessProvider reports app-local startup readiness for a joining node.
type NodeReadinessProvider interface {
	NodeReadiness(context.Context, NodeReadinessRequest) (NodeReadinessResponse, error)
}

// ControllerVoterReadinessProvider reports target readiness before Controller voter preparation.
type ControllerVoterReadinessProvider interface {
	ControllerVoterReadiness(context.Context, ControllerVoterReadinessRequest) (ControllerVoterReadinessResponse, error)
}

// ControllerVoterPreparer prepares a target node for Controller Raft voter promotion.
type ControllerVoterPreparer interface {
	PrepareControllerVoter(context.Context, PrepareControllerVoterRequest) (PrepareControllerVoterResponse, error)
}

// PluginHTTPRouter invokes one node-local plugin HTTP route.
type PluginHTTPRouter interface {
	Route(context.Context, string, *pluginproto.HttpRequest) (*pluginproto.HttpResponse, error)
}

// ManagerLatestMessageReader reads one node-local newest-message page.
type ManagerLatestMessageReader interface {
	ListLocalLatestMessages(context.Context, uint64, int) (managementusecase.ListMessagesResponse, error)
}

// BackupMessageShardCapturer uploads one bounded local committed-message shard.
type BackupMessageShardCapturer interface {
	CaptureMessageShard(context.Context, runtimebackup.CaptureRequest, runtimebackup.MessageShard) (runtimebackup.MessageShardCapture, error)
}

// BackupPartitionCapturer captures one logical hash-slot partition locally.
type BackupPartitionCapturer interface {
	CaptureBackupPartition(context.Context, runtimebackup.CaptureRequest) (backupusecase.PartitionReport, error)
}

// BackupRestoreTargetInspector checks this node's durable semantic storage.
type BackupRestoreTargetInspector interface {
	InspectLocalRestoreTarget(context.Context) (clusterpkg.RestoreTargetLocalState, error)
}

// BackupRestorePartitionInstaller installs one authenticated partition locally.
type BackupRestorePartitionInstaller interface {
	InstallPartition(context.Context, backupusecase.RestorePlan, uint16) (backupusecase.RestorePartition, error)
}

// BackupRestorePartitionVerifier checks restored durable Channel boundaries locally.
type BackupRestorePartitionVerifier interface {
	VerifyLocalRestorePartition(context.Context, uint16, string, []clusterpkg.RestoreVerifyBoundary) error
}

// Options configures the internal node RPC adapter.
type Options struct {
	// Authority handles UID route authority requests after payload decoding.
	Authority PresenceAuthority
	// Owner handles owner-local session conflict actions after payload decoding.
	Owner PresenceOwner
	// Delivery handles owner-local delivery push batches after payload decoding.
	Delivery DeliveryOwnerPush
	// DeliveryFanout handles authority-node delivery fanout tasks after payload decoding.
	DeliveryFanout DeliveryFanoutRunner
	// ConversationAuthority handles UID conversation authority cache requests after payload decoding.
	ConversationAuthority ConversationAuthority
	// ManagerConnections handles owner-local manager connection inventory requests.
	ManagerConnections ManagerConnectionReader
	// ManagerLogs handles node-local manager distributed log page requests.
	ManagerLogs ManagerLogReader
	// ManagerControllerRaft handles node-local manager Controller Raft operations.
	ManagerControllerRaft ManagerControllerRaftOperator
	// ManagerSlotRaft handles node-local manager Slot Raft operations.
	ManagerSlotRaft ManagerSlotRaftOperator
	// ManagerChannels handles node-local manager channel list requests.
	ManagerChannels ManagerChannelReader
	// ManagerMessageRetention handles manager message retention requests on the channel leader.
	ManagerMessageRetention ManagerMessageRetentionOperator
	// ManagerLatestMessages handles node-local latest-message index reads.
	ManagerLatestMessages ManagerLatestMessageReader
	// ManagerDBInspect handles node-local manager DB inspect requests.
	ManagerDBInspect ManagerDBInspectReader
	// ManagerAppLogs handles selected-node ordinary application log requests.
	ManagerAppLogs ManagerAppLogReader
	// ManagerNodeConfig handles selected-node effective startup config requests.
	ManagerNodeConfig ManagerNodeConfigReader
	// ManagerDiagnostics handles node-local diagnostics query and tracking requests.
	ManagerDiagnostics ManagerDiagnostics
	// ManagerTaskAudit handles node-local Controller task audit reads.
	ManagerTaskAudit ManagerTaskAuditReader
	// ManagerPlugins handles node-local plugin inventory requests.
	ManagerPlugins ManagerPluginReader
	// NodeLifecycle handles validated seed-join lifecycle requests.
	NodeLifecycle NodeLifecycleManager
	// NodeReadiness reports app-local readiness for startup probes.
	NodeReadiness NodeReadinessProvider
	// ControllerVoterReadiness reports app-local readiness for Controller voter preparation.
	ControllerVoterReadiness ControllerVoterReadinessProvider
	// ControllerVoterPreparer prepares this node for Controller voter promotion.
	ControllerVoterPreparer ControllerVoterPreparer
	// NodeLifecycleClusterID is the cluster identity accepted by lifecycle RPCs.
	NodeLifecycleClusterID string
	// NodeLifecycleJoinToken is the shared token accepted by JoinNode RPCs.
	NodeLifecycleJoinToken string
	// PluginHTTPRoutes handles node-local plugin HTTP route requests.
	PluginHTTPRoutes PluginHTTPRouter
	// BackupMessages captures bounded committed-message shards on this node.
	BackupMessages BackupMessageShardCapturer
	// BackupPartitions captures logical hash-slot partitions on this node.
	BackupPartitions BackupPartitionCapturer
	// BackupRestoreTarget inspects local semantic storage during explicit restore mode.
	BackupRestoreTarget BackupRestoreTargetInspector
	// BackupRestoreInstaller installs authenticated recovery partitions locally.
	BackupRestoreInstaller BackupRestorePartitionInstaller
	// BackupRestoreVerifier validates authenticated recovery boundaries locally.
	BackupRestoreVerifier BackupRestorePartitionVerifier
	// Logger records node RPC adapter failures that are converted into statuses.
	Logger wklog.Logger
}

// Adapter decodes node RPC payloads and forwards them to local authority ports.
type Adapter struct {
	// authority owns business decisions; Adapter only performs RPC adaptation.
	authority PresenceAuthority
	// owner mutates only owner-local real session state.
	owner PresenceOwner
	// delivery pushes messages into owner-local delivery sessions.
	delivery DeliveryOwnerPush
	// deliveryFanout runs subscriber fanout tasks for this authority node.
	deliveryFanout DeliveryFanoutRunner
	// conversation owns UID conversation active cache decisions.
	conversation ConversationAuthority
	// managerConnections reads owner-local connection inventory for manager pages.
	managerConnections ManagerConnectionReader
	// managerLogs reads node-local distributed logs for manager pages.
	managerLogs ManagerLogReader
	// managerControllerRaft runs node-local Controller Raft status and compaction operations.
	managerControllerRaft ManagerControllerRaftOperator
	// managerSlotRaft runs node-local Slot Raft compaction operations.
	managerSlotRaft ManagerSlotRaftOperator
	// managerChannels reads node-local channel metadata for manager pages.
	managerChannels ManagerChannelReader
	// managerMessageRetention advances message retention boundaries on the channel leader.
	managerMessageRetention ManagerMessageRetentionOperator
	// managerLatestMessages reads the node-local latest-message index.
	managerLatestMessages ManagerLatestMessageReader
	// managerDBInspect reads node-local DB inspect results for manager pages.
	managerDBInspect ManagerDBInspectReader
	// managerAppLogs reads selected-node ordinary application logs for manager pages.
	managerAppLogs ManagerAppLogReader
	// managerNodeConfig reads selected-node effective startup config for manager pages.
	managerNodeConfig ManagerNodeConfigReader
	// managerDiagnostics queries diagnostics events and mutates temporary tracking rules.
	managerDiagnostics ManagerDiagnostics
	// managerTaskAudit reads retained Controller task audit history.
	managerTaskAudit ManagerTaskAuditReader
	// managerPlugins reads node-local plugin inventory for manager pages.
	managerPlugins ManagerPluginReader
	// nodeLifecycle submits validated seed joins through the management usecase.
	nodeLifecycle NodeLifecycleManager
	// nodeReadiness reports local startup readiness to seed nodes.
	nodeReadiness NodeReadinessProvider
	// controllerVoterReadiness reports local readiness before Controller voter preparation.
	controllerVoterReadiness ControllerVoterReadinessProvider
	// controllerVoterPreparer prepares this node for Controller voter promotion.
	controllerVoterPreparer ControllerVoterPreparer
	// nodeLifecycleClusterID is the cluster identity accepted by lifecycle RPCs.
	nodeLifecycleClusterID string
	// nodeLifecycleJoinToken is the shared token accepted by JoinNode RPCs.
	nodeLifecycleJoinToken string
	// pluginHTTPRoutes invokes node-local plugin HTTP route hooks.
	pluginHTTPRoutes PluginHTTPRouter
	// backupMessages uploads local committed-message shards.
	backupMessages BackupMessageShardCapturer
	// backupPartitions captures local Slot-leader logical partitions.
	backupPartitions BackupPartitionCapturer
	// backupRestoreTarget checks this node's semantic storage emptiness.
	backupRestoreTarget BackupRestoreTargetInspector
	// backupRestoreInstaller installs authenticated recovery partitions locally.
	backupRestoreInstaller BackupRestorePartitionInstaller
	// backupRestoreVerifier validates restored durable boundaries locally.
	backupRestoreVerifier BackupRestorePartitionVerifier
	// logger records adapter decode errors and rejected local operations.
	logger wklog.Logger
}

// New creates a node RPC adapter.
func New(opts Options) *Adapter {
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &Adapter{
		authority:                opts.Authority,
		owner:                    opts.Owner,
		delivery:                 opts.Delivery,
		deliveryFanout:           opts.DeliveryFanout,
		conversation:             opts.ConversationAuthority,
		managerConnections:       opts.ManagerConnections,
		managerLogs:              opts.ManagerLogs,
		managerControllerRaft:    opts.ManagerControllerRaft,
		managerSlotRaft:          opts.ManagerSlotRaft,
		managerChannels:          opts.ManagerChannels,
		managerMessageRetention:  opts.ManagerMessageRetention,
		managerLatestMessages:    opts.ManagerLatestMessages,
		managerDBInspect:         opts.ManagerDBInspect,
		managerAppLogs:           opts.ManagerAppLogs,
		managerNodeConfig:        opts.ManagerNodeConfig,
		managerDiagnostics:       opts.ManagerDiagnostics,
		managerTaskAudit:         opts.ManagerTaskAudit,
		managerPlugins:           opts.ManagerPlugins,
		nodeLifecycle:            opts.NodeLifecycle,
		nodeReadiness:            opts.NodeReadiness,
		controllerVoterReadiness: opts.ControllerVoterReadiness,
		controllerVoterPreparer:  opts.ControllerVoterPreparer,
		nodeLifecycleClusterID:   opts.NodeLifecycleClusterID,
		nodeLifecycleJoinToken:   opts.NodeLifecycleJoinToken,
		pluginHTTPRoutes:         opts.PluginHTTPRoutes,
		backupMessages:           opts.BackupMessages,
		backupPartitions:         opts.BackupPartitions,
		backupRestoreTarget:      opts.BackupRestoreTarget,
		backupRestoreInstaller:   opts.BackupRestoreInstaller,
		backupRestoreVerifier:    opts.BackupRestoreVerifier,
		logger:                   opts.Logger,
	}
}

// HandlePresenceAuthorityRPC handles one encoded presence authority RPC payload.
func (a *Adapter) HandlePresenceAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodePresenceRPCRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("presence authority rpc decode failed",
			wklog.Event("internal.access.node.presence_authority_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.authority == nil {
		if req.Op == presenceOpEndpointsByTargets {
			results := make([]presenceRPCEndpointLookupResult, len(req.EndpointGroups))
			for i := range results {
				results[i].Status = rpcStatusRejected
			}
			return encodePresenceEndpointsByTargetsResponseBinary(results)
		}
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusRejected})
	}

	switch req.Op {
	case presenceOpRegisterRoute:
		result, err := a.authority.RegisterRoute(ctx, req.Target, req.Route)
		if err != nil {
			status := presenceRPCStatusForError(err)
			a.logPresenceAuthorityError(req, status, err)
			return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
		}
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusOK, Register: result})
	case presenceOpCommitRoute:
		err := a.authority.CommitRoute(ctx, req.Target, req.PendingToken)
		status := presenceRPCStatusForError(err)
		a.logPresenceAuthorityError(req, status, err)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
	case presenceOpAbortRoute:
		err := a.authority.AbortRoute(ctx, req.Target, req.PendingToken)
		status := presenceRPCStatusForError(err)
		a.logPresenceAuthorityError(req, status, err)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
	case presenceOpUnregisterRoute:
		err := a.authority.UnregisterRoute(ctx, req.Target, req.Identity, req.OwnerSeq)
		status := presenceRPCStatusForError(err)
		a.logPresenceAuthorityError(req, status, err)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
	case presenceOpEndpointsByUID:
		routes, err := a.authority.EndpointsByUID(ctx, req.Target, req.UID)
		if err != nil {
			status := presenceRPCStatusForError(err)
			a.logPresenceAuthorityError(req, status, err)
			return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
		}
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusOK, Endpoints: routes})
	case presenceOpEndpointsByTargets:
		return a.handlePresenceEndpointsByTargets(ctx, req.EndpointGroups)
	case presenceOpTouchRoutes:
		err := a.authority.TouchRoutes(ctx, req.Target, req.Routes)
		status := presenceRPCStatusForError(err)
		a.logPresenceAuthorityError(req, status, err)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
	default:
		err := fmt.Errorf("internal/access/node: unknown presence rpc op %q", req.Op)
		a.rpcLogger().Warn("presence authority rpc unknown operation",
			wklog.Event("internal.access.node.presence_authority_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

func (a *Adapter) handlePresenceEndpointsByTargets(ctx context.Context, groups []presence.EndpointLookupGroup) ([]byte, error) {
	results := make([]presenceRPCEndpointLookupResult, len(groups))
	batchAuthority, hasBatchAuthority := a.authority.(PresenceBatchAuthority)
	for i, group := range groups {
		var routes []presence.Route
		var err error
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		} else if hasBatchAuthority {
			routes, err = batchAuthority.EndpointsByUIDs(ctx, group.Target, group.UIDs)
		} else {
			for _, uid := range group.UIDs {
				var uidRoutes []presence.Route
				uidRoutes, err = a.authority.EndpointsByUID(ctx, group.Target, uid)
				if err != nil {
					break
				}
				routes = append(routes, uidRoutes...)
			}
		}
		results[i].Status = presenceRPCStatusForError(err)
		if err == nil {
			results[i].Routes = routes
		}
	}
	return encodePresenceEndpointsByTargetsResponseBinary(results)
}

// HandlePresenceOwnerRPC handles one encoded presence owner-action RPC payload.
func (a *Adapter) HandlePresenceOwnerRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodePresenceRPCRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("presence owner rpc decode failed",
			wklog.Event("internal.access.node.presence_owner_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.owner == nil {
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusRejected})
	}
	if req.Op != presenceOpApplyRouteAction {
		err := fmt.Errorf("internal/access/node: unknown presence owner rpc op %q", req.Op)
		a.rpcLogger().Warn("presence owner rpc unknown operation",
			wklog.Event("internal.access.node.presence_owner_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
	err = a.owner.ApplyRouteAction(ctx, req.Action)
	status := presenceRPCStatusForError(err)
	if status == rpcStatusRejected {
		fields := []wklog.Field{
			wklog.Event("internal.access.node.presence_owner_rejected"),
			wklog.String("op", req.Op),
			wklog.String("status", status),
			wklog.UID(req.Action.UID),
			wklog.Uint64("ownerNodeID", req.Action.OwnerNodeID),
			wklog.Uint64("ownerBootID", req.Action.OwnerBootID),
			wklog.SessionID(req.Action.SessionID),
			wklog.String("action", req.Action.Kind),
			wklog.Error(err),
		}
		a.rpcLogger().Warn("presence owner rpc rejected", fields...)
	}
	return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
}

func (a *Adapter) rpcLogger() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger.Named("rpc")
}

func (a *Adapter) logPresenceAuthorityError(req presenceRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	fields := []wklog.Field{
		wklog.Event("internal.access.node.presence_authority_rejected"),
		wklog.String("op", req.Op),
		wklog.String("status", status),
		wklog.Int("hashSlot", int(req.Target.HashSlot)),
		wklog.Uint64("slotID", uint64(req.Target.SlotID)),
		wklog.LeaderNodeID(req.Target.LeaderNodeID),
		wklog.Uint64("routeRevision", req.Target.RouteRevision),
		wklog.Uint64("authorityEpoch", req.Target.AuthorityEpoch),
		wklog.UID(req.UID),
		wklog.Int("routes", len(req.Routes)),
		wklog.Error(err),
	}
	if req.Route.UID != "" {
		fields = append(fields, wklog.UID(req.Route.UID), wklog.SessionID(req.Route.SessionID))
	}
	a.rpcLogger().Warn("presence authority rpc rejected", fields...)
}

// PresenceRPCNode sends raw RPC payloads to another internal node.
type PresenceRPCNode interface {
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// Client forwards presence authority calls to the target leader node.
type Client struct {
	// node is the raw cluster RPC transport used by this adapter.
	node PresenceRPCNode
}

// NewClient creates a presence authority RPC client.
func NewClient(node PresenceRPCNode) *Client {
	return &Client{node: node}
}

// RegisterRoute registers one owner route on the target authority node.
func (c *Client) RegisterRoute(ctx context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpRegisterRoute, Target: target, Route: route})
	if err != nil {
		return presence.RegisterResult{}, err
	}
	if err := presenceRPCErrorForStatus(resp.Status); err != nil {
		return presence.RegisterResult{}, err
	}
	return resp.Register, nil
}

// CommitRoute promotes one pending authority route on the target node.
func (c *Client) CommitRoute(ctx context.Context, target presence.RouteTarget, token string) error {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpCommitRoute, Target: target, PendingToken: token})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}

// AbortRoute drops one pending authority route on the target node.
func (c *Client) AbortRoute(ctx context.Context, target presence.RouteTarget, token string) error {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpAbortRoute, Target: target, PendingToken: token})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}

// UnregisterRoute tombstones one exact owner route on the target node.
func (c *Client) UnregisterRoute(ctx context.Context, target presence.RouteTarget, identity presence.RouteIdentity, ownerSeq uint64) error {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpUnregisterRoute, Target: target, Identity: identity, OwnerSeq: ownerSeq})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}

// EndpointsByUID reads active authority routes for one UID from the target node.
func (c *Client) EndpointsByUID(ctx context.Context, target presence.RouteTarget, uid string) ([]presence.Route, error) {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpEndpointsByUID, Target: target, UID: uid})
	if err != nil {
		return nil, err
	}
	if err := presenceRPCErrorForStatus(resp.Status); err != nil {
		return nil, err
	}
	return resp.Endpoints, nil
}

// EndpointsByTargets resolves exact-target UID groups on one destination node.
func (c *Client) EndpointsByTargets(ctx context.Context, nodeID uint64, groups []presence.EndpointLookupGroup) ([]presence.EndpointLookupResult, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	if nodeID == 0 {
		return nil, fmt.Errorf("internal/access/node: presence endpoint destination node is zero")
	}
	for i, group := range groups {
		if group.Target.LeaderNodeID != nodeID {
			return nil, fmt.Errorf("internal/access/node: presence endpoint group %d leader %d does not match destination %d", i, group.Target.LeaderNodeID, nodeID)
		}
	}
	if c == nil || c.node == nil {
		return nil, fmt.Errorf("internal/access/node: presence rpc client not configured")
	}
	body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{
		Op:             presenceOpEndpointsByTargets,
		EndpointGroups: groups,
	})
	if err != nil {
		return nil, err
	}
	responseBody, err := c.node.CallRPC(ctx, nodeID, PresenceAuthorityRPCServiceID, body)
	if err != nil {
		if isPresenceEndpointsByTargetsUnsupported(err) {
			return c.endpointsByTargetsLegacy(ctx, groups), nil
		}
		return nil, err
	}
	wireResults, err := decodePresenceEndpointsByTargetsResponseBinary(responseBody)
	if err != nil {
		return nil, err
	}
	if len(wireResults) != len(groups) {
		return nil, fmt.Errorf("internal/access/node: presence endpoint result count %d does not match group count %d", len(wireResults), len(groups))
	}
	results := make([]presence.EndpointLookupResult, len(wireResults))
	for i, result := range wireResults {
		results[i] = presence.EndpointLookupResult{
			Routes: result.Routes,
			Err:    presenceRPCErrorForStatus(result.Status),
		}
	}
	return results, nil
}

func (c *Client) endpointsByTargetsLegacy(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
	results := make([]presence.EndpointLookupResult, len(groups))
	for i, group := range groups {
		for _, uid := range group.UIDs {
			if uid == "" {
				continue
			}
			routes, err := c.EndpointsByUID(ctx, group.Target, uid)
			if err != nil {
				results[i].Err = err
				break
			}
			results[i].Routes = append(results[i].Routes, routes...)
		}
	}
	return results
}

func isPresenceEndpointsByTargetsUnsupported(err error) bool {
	if err == nil {
		return false
	}
	var remoteErr transport.RemoteError
	if !errors.As(err, &remoteErr) {
		return false
	}
	return remoteErr.Code == "remote_error" &&
		remoteErr.Message == fmt.Sprintf("internal/access/node: unknown presence op id %d", presenceOpEndpointsByTargetsID)
}

// TouchRoutes refreshes owner routes on the target authority node.
func (c *Client) TouchRoutes(ctx context.Context, target presence.RouteTarget, routes []presence.Route) error {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpTouchRoutes, Target: target, Routes: routes})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}

// ApplyRouteAction applies one conflict action on the owner node.
func (c *Client) ApplyRouteAction(ctx context.Context, ownerNodeID uint64, action presence.RouteAction) error {
	resp, err := c.callService(ctx, ownerNodeID, PresenceOwnerRPCServiceID, presenceRPCRequest{Op: presenceOpApplyRouteAction, Action: action})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}

func (c *Client) call(ctx context.Context, target presence.RouteTarget, req presenceRPCRequest) (presenceRPCResponse, error) {
	return c.callService(ctx, target.LeaderNodeID, PresenceAuthorityRPCServiceID, req)
}

func (c *Client) callService(ctx context.Context, nodeID uint64, serviceID uint8, req presenceRPCRequest) (presenceRPCResponse, error) {
	if c == nil || c.node == nil {
		return presenceRPCResponse{}, fmt.Errorf("internal/access/node: presence rpc client not configured")
	}
	body, err := encodePresenceRPCRequestBinary(req)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, serviceID, body)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	return decodePresenceRPCResponse(respBody)
}

func presenceRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, authoritypresence.ErrNotLeader):
		return rpcStatusNotLeader
	case errors.Is(err, authoritypresence.ErrStaleRoute):
		return rpcStatusStaleRoute
	case errors.Is(err, authoritypresence.ErrRouteNotReady):
		return rpcStatusRouteNotReady
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	default:
		return rpcStatusRejected
	}
}

func presenceRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusNotLeader:
		return authoritypresence.ErrNotLeader
	case rpcStatusStaleRoute:
		return authoritypresence.ErrStaleRoute
	case rpcStatusRouteNotReady:
		return authoritypresence.ErrRouteNotReady
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusRejected:
		return fmt.Errorf("internal/access/node: presence rpc rejected")
	default:
		return fmt.Errorf("internal/access/node: unknown presence rpc status %q", status)
	}
}
