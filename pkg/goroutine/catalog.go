package goroutine

import "fmt"

// Module is a stable, low-cardinality owner of first-party goroutines.
type Module string

const (
	ModuleApp           Module = "app"
	ModuleAPI           Module = "api"
	ModuleManager       Module = "manager"
	ModuleGateway       Module = "gateway"
	ModuleTransport     Module = "transport"
	ModuleCluster       Module = "cluster"
	ModuleController    Module = "controller"
	ModuleSlot          Module = "slot"
	ModuleChannel       Module = "channel"
	ModuleDatabase      Module = "database"
	ModulePresence      Module = "presence"
	ModuleChannelAppend Module = "channelappend"
	ModuleDelivery      Module = "delivery"
	ModuleConversation  Module = "conversation"
	ModuleWebhook       Module = "webhook"
	ModulePlugin        Module = "plugin"
	ModuleBackup        Module = "backup"
	ModuleObservability Module = "observability"
)

// TaskKind describes the lifecycle and accounting semantics of one task.
type TaskKind string

const (
	TaskKindSingleton TaskKind = "singleton"
	TaskKindFixed     TaskKind = "fixed"
	TaskKindDynamic   TaskKind = "dynamic"
	TaskKindPool      TaskKind = "pool"
	TaskKindBurst     TaskKind = "burst"
)

// PanicPolicy determines whether a recovered panic is isolated or rethrown.
type PanicPolicy string

const (
	PanicPolicyRecover PanicPolicy = "recover"
	PanicPolicyRepanic PanicPolicy = "repanic"
)

// TaskID is a stable module/task identity from the compiled catalog.
type TaskID string

// TaskSpec describes one stable managed task.
type TaskSpec struct {
	// ID is the globally unique module/task identifier.
	ID TaskID
	// Module owns the task lifecycle.
	Module Module
	// Name is the stable task name within Module.
	Name string
	// Kind determines how task health and capacity are interpreted.
	Kind TaskKind
	// PanicPolicy determines whether a panic is isolated or crashes the node.
	PanicPolicy PanicPolicy
	// Expected is the normal live count for singleton or fixed tasks.
	Expected int
	// Required marks an expected task that must be live once the node is ready.
	Required bool
}

const (
	TaskAppDetachedWorkqueue               TaskID = "app/detached_workqueue"
	TaskAppTopCollector                    TaskID = "app/top_collector"
	TaskAppPresenceTouch                   TaskID = "app/presence_touch"
	TaskAppSeedJoin                        TaskID = "app/seed_join"
	TaskAppConversationFlush               TaskID = "app/conversation_flush"
	TaskAppConversationRoute               TaskID = "app/conversation_route"
	TaskAppConversationDrain               TaskID = "app/conversation_drain"
	TaskAppTaskAudit                       TaskID = "app/task_audit"
	TaskAppPrometheusWait                  TaskID = "app/prometheus_wait"
	TaskAppDeliveryMetadata                TaskID = "app/delivery_metadata"
	TaskAPIHTTPServe                       TaskID = "api/http_serve"
	TaskManagerHTTPServe                   TaskID = "manager/http_serve"
	TaskManagerSnapshotFanout              TaskID = "manager/goroutine_snapshot_fanout"
	TaskManagerDiagnosticsFanout           TaskID = "manager/diagnostics_fanout"
	TaskManagerLatestMessagesFanout        TaskID = "manager/latest_messages_fanout"
	TaskGatewayAsyncDispatch               TaskID = "gateway/async_dispatch"
	TaskGatewayAsyncAuth                   TaskID = "gateway/async_auth"
	TaskGatewayIdleMonitor                 TaskID = "gateway/idle_monitor"
	TaskGatewayTransportActor              TaskID = "gateway/transport_actor"
	TaskGatewayTransportServe              TaskID = "gateway/transport_serve"
	TaskDeliveryManagerAsync               TaskID = "delivery/manager_async"
	TaskWebhookNotify                      TaskID = "webhook/notify"
	TaskWebhookOnline                      TaskID = "webhook/online"
	TaskWebhookOffline                     TaskID = "webhook/offline"
	TaskTransportConnRead                  TaskID = "transport/conn_read"
	TaskTransportConnWrite                 TaskID = "transport/conn_write"
	TaskTransportRPCService                TaskID = "transport/rpc_service"
	TaskTransportRPCExecutor               TaskID = "transport/rpc_executor"
	TaskTransportRPCExecutorRelease        TaskID = "transport/rpc_executor_release"
	TaskTransportClientObserver            TaskID = "transport/client_observer"
	TaskTransportServerObserver            TaskID = "transport/server_observer"
	TaskTransportAccept                    TaskID = "transport/accept"
	TaskTransportConnectionCleanup         TaskID = "transport/connection_cleanup"
	TaskClusterNodeControlWatch            TaskID = "cluster/node_control_watch"
	TaskClusterRuntimeControlWatch         TaskID = "cluster/runtime_control_watch"
	TaskClusterHealthReport                TaskID = "cluster/health_report"
	TaskClusterTaskReconcile               TaskID = "cluster/task_reconcile"
	TaskClusterPreferredLeader             TaskID = "cluster/preferred_leader"
	TaskClusterChannelTick                 TaskID = "cluster/channel_tick"
	TaskClusterChannelRetention            TaskID = "cluster/channel_retention"
	TaskClusterChannelMigration            TaskID = "cluster/channel_migration"
	TaskClusterSlotLeaderRefresh           TaskID = "cluster/slot_leader_refresh"
	TaskClusterConversationTouch           TaskID = "cluster/conversation_touch"
	TaskClusterRaftTransport               TaskID = "cluster/raft_transport"
	TaskClusterObserveLoop                 TaskID = "cluster/observe_loop"
	TaskControllerRaftRun                  TaskID = "controller/raft_run"
	TaskControllerRaftApply                TaskID = "controller/raft_apply_scheduler"
	TaskControllerRefresh                  TaskID = "controller/refresh_loop"
	TaskSlotRaftWorker                     TaskID = "slot/raft_worker"
	TaskSlotRaftTicker                     TaskID = "slot/raft_ticker"
	TaskSlotRaftApplyWorker                TaskID = "slot/raft_apply_worker"
	TaskSlotConditionWaiter                TaskID = "slot/condition_waiter"
	TaskChannelReactor                     TaskID = "channel/reactor"
	TaskChannelReactorClose                TaskID = "channel/reactor_close"
	TaskChannelStoreClose                  TaskID = "channel/store_close"
	TaskChannelTaskCancellation            TaskID = "channel/task_cancellation"
	TaskChannelWorkerPool                  TaskID = "channel/worker_pool"
	TaskDatabaseRaftWriteWorker            TaskID = "database/raft_write_worker"
	TaskDatabaseRaftSnapshotGC             TaskID = "database/raft_snapshot_gc"
	TaskDatabaseLatestMigration            TaskID = "database/latest_migration"
	TaskDatabaseBackupStream               TaskID = "database/backup_stream"
	TaskDatabaseCommitCoordinator          TaskID = "database/commit_coordinator"
	TaskPresenceBatchResolve               TaskID = "presence/batch_resolve"
	TaskConversationBatchRead              TaskID = "conversation/batch_read"
	TaskChannelAppendRouter                TaskID = "channelappend/router"
	TaskChannelAppendPoolRelease           TaskID = "channelappend/pool_release"
	TaskChannelAppendRecipientResolve      TaskID = "channelappend/recipient_resolve"
	TaskChannelAppendAdvanceScheduler      TaskID = "channelappend/advance_scheduler"
	TaskChannelAppendDeliveryFanout        TaskID = "channelappend/delivery_fanout"
	TaskChannelAppendMetrics               TaskID = "channelappend/metrics"
	TaskChannelAppendWriterAdvance         TaskID = "channelappend/writer_advance"
	TaskChannelAppendWorkerPool            TaskID = "channelappend/worker_pool"
	TaskChannelAppendStopDrain             TaskID = "channelappend/stop_drain"
	TaskChannelAppendPostCommitRetry       TaskID = "channelappend/post_commit_retry"
	TaskDeliveryRetryWorker                TaskID = "delivery/retry_worker"
	TaskDeliveryRetryDoneWait              TaskID = "delivery/retry_done_wait"
	TaskChannelAppendDeliveryWorker        TaskID = "channelappend/delivery_worker"
	TaskChannelAppendDeliveryDoneWait      TaskID = "channelappend/delivery_done_wait"
	TaskChannelAppendDeliveryAdmissionWait TaskID = "channelappend/delivery_admission_wait"
	TaskPluginHookWorker                   TaskID = "plugin/hook_worker"
	TaskPluginHookFinalize                 TaskID = "plugin/hook_finalize"
	TaskPluginLifecycleClose               TaskID = "plugin/lifecycle_close"
	TaskPluginWatcher                      TaskID = "plugin/watcher"
	TaskPluginProcessWait                  TaskID = "plugin/process_wait"
	TaskPluginStopCallback                 TaskID = "plugin/stop_callback"
	TaskBackupCoordinator                  TaskID = "backup/coordinator"
	TaskBackupPartitionWorker              TaskID = "backup/partition_worker"
	TaskBackupPartitionProducer            TaskID = "backup/partition_producer"
	TaskBackupRestoreCoordinator           TaskID = "backup/restore_coordinator"
	TaskBackupRestoreWorker                TaskID = "backup/restore_worker"
	TaskBackupRestoreProducer              TaskID = "backup/restore_producer"
	TaskObservabilityDashboard             TaskID = "observability/dashboard"
)

var defaultTaskCatalog = []TaskSpec{
	{ID: TaskAppDetachedWorkqueue, Module: ModuleApp, Name: "detached_workqueue", Kind: TaskKindPool, PanicPolicy: PanicPolicyRecover},
	{ID: TaskAppTopCollector, Module: ModuleApp, Name: "top_collector", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskAppPresenceTouch, Module: ModuleApp, Name: "presence_touch", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic, Expected: 2},
	{ID: TaskAppSeedJoin, Module: ModuleApp, Name: "seed_join", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskAppConversationFlush, Module: ModuleApp, Name: "conversation_flush", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskAppConversationRoute, Module: ModuleApp, Name: "conversation_route", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskAppConversationDrain, Module: ModuleApp, Name: "conversation_drain", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskAppTaskAudit, Module: ModuleApp, Name: "task_audit", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskAppPrometheusWait, Module: ModuleApp, Name: "prometheus_wait", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRecover, Expected: 1},
	{ID: TaskAppDeliveryMetadata, Module: ModuleApp, Name: "delivery_metadata", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskAPIHTTPServe, Module: ModuleAPI, Name: "http_serve", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskManagerHTTPServe, Module: ModuleManager, Name: "http_serve", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskManagerSnapshotFanout, Module: ModuleManager, Name: "goroutine_snapshot_fanout", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskManagerDiagnosticsFanout, Module: ModuleManager, Name: "diagnostics_fanout", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskManagerLatestMessagesFanout, Module: ModuleManager, Name: "latest_messages_fanout", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskGatewayAsyncDispatch, Module: ModuleGateway, Name: "async_dispatch", Kind: TaskKindPool, PanicPolicy: PanicPolicyRecover},
	{ID: TaskGatewayAsyncAuth, Module: ModuleGateway, Name: "async_auth", Kind: TaskKindPool, PanicPolicy: PanicPolicyRecover},
	{ID: TaskGatewayIdleMonitor, Module: ModuleGateway, Name: "idle_monitor", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskGatewayTransportActor, Module: ModuleGateway, Name: "transport_actor", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskGatewayTransportServe, Module: ModuleGateway, Name: "transport_serve", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskDeliveryManagerAsync, Module: ModuleDelivery, Name: "manager_async", Kind: TaskKindPool, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskWebhookNotify, Module: ModuleWebhook, Name: "notify", Kind: TaskKindPool, PanicPolicy: PanicPolicyRecover},
	{ID: TaskWebhookOnline, Module: ModuleWebhook, Name: "online", Kind: TaskKindPool, PanicPolicy: PanicPolicyRecover},
	{ID: TaskWebhookOffline, Module: ModuleWebhook, Name: "offline", Kind: TaskKindPool, PanicPolicy: PanicPolicyRecover},
	{ID: TaskTransportConnRead, Module: ModuleTransport, Name: "conn_read", Kind: TaskKindDynamic, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskTransportConnWrite, Module: ModuleTransport, Name: "conn_write", Kind: TaskKindDynamic, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskTransportRPCService, Module: ModuleTransport, Name: "rpc_service", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskTransportRPCExecutor, Module: ModuleTransport, Name: "rpc_executor", Kind: TaskKindPool, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskTransportRPCExecutorRelease, Module: ModuleTransport, Name: "rpc_executor_release", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskTransportClientObserver, Module: ModuleTransport, Name: "client_observer", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskTransportServerObserver, Module: ModuleTransport, Name: "server_observer", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskTransportAccept, Module: ModuleTransport, Name: "accept", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1, Required: true},
	{ID: TaskTransportConnectionCleanup, Module: ModuleTransport, Name: "connection_cleanup", Kind: TaskKindDynamic, PanicPolicy: PanicPolicyRecover},
	{ID: TaskClusterNodeControlWatch, Module: ModuleCluster, Name: "node_control_watch", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1, Required: true},
	{ID: TaskClusterRuntimeControlWatch, Module: ModuleCluster, Name: "runtime_control_watch", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1, Required: true},
	{ID: TaskClusterHealthReport, Module: ModuleCluster, Name: "health_report", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskClusterTaskReconcile, Module: ModuleCluster, Name: "task_reconcile", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskClusterPreferredLeader, Module: ModuleCluster, Name: "preferred_leader", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskClusterChannelTick, Module: ModuleCluster, Name: "channel_tick", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskClusterChannelRetention, Module: ModuleCluster, Name: "channel_retention", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskClusterChannelMigration, Module: ModuleCluster, Name: "channel_migration", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskClusterSlotLeaderRefresh, Module: ModuleCluster, Name: "slot_leader_refresh", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskClusterConversationTouch, Module: ModuleCluster, Name: "conversation_touch", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskClusterRaftTransport, Module: ModuleCluster, Name: "raft_transport", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskClusterObserveLoop, Module: ModuleCluster, Name: "observe_loop", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskControllerRaftRun, Module: ModuleController, Name: "raft_run", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskControllerRaftApply, Module: ModuleController, Name: "raft_apply_scheduler", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskControllerRefresh, Module: ModuleController, Name: "refresh_loop", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskSlotRaftWorker, Module: ModuleSlot, Name: "raft_worker", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskSlotRaftTicker, Module: ModuleSlot, Name: "raft_ticker", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskSlotRaftApplyWorker, Module: ModuleSlot, Name: "raft_apply_worker", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskSlotConditionWaiter, Module: ModuleSlot, Name: "condition_waiter", Kind: TaskKindDynamic, PanicPolicy: PanicPolicyRecover},
	{ID: TaskChannelReactor, Module: ModuleChannel, Name: "reactor", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskChannelReactorClose, Module: ModuleChannel, Name: "reactor_close", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskChannelStoreClose, Module: ModuleChannel, Name: "store_close", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskChannelTaskCancellation, Module: ModuleChannel, Name: "task_cancellation", Kind: TaskKindDynamic, PanicPolicy: PanicPolicyRecover},
	{ID: TaskChannelWorkerPool, Module: ModuleChannel, Name: "worker_pool", Kind: TaskKindPool, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskDatabaseRaftWriteWorker, Module: ModuleDatabase, Name: "raft_write_worker", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskDatabaseRaftSnapshotGC, Module: ModuleDatabase, Name: "raft_snapshot_gc", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskDatabaseLatestMigration, Module: ModuleDatabase, Name: "latest_migration", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskDatabaseBackupStream, Module: ModuleDatabase, Name: "backup_stream", Kind: TaskKindDynamic, PanicPolicy: PanicPolicyRecover},
	{ID: TaskDatabaseCommitCoordinator, Module: ModuleDatabase, Name: "commit_coordinator", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskPresenceBatchResolve, Module: ModulePresence, Name: "batch_resolve", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskConversationBatchRead, Module: ModuleConversation, Name: "batch_read", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskChannelAppendRouter, Module: ModuleChannelAppend, Name: "router", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskChannelAppendPoolRelease, Module: ModuleChannelAppend, Name: "pool_release", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskChannelAppendRecipientResolve, Module: ModuleChannelAppend, Name: "recipient_resolve", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskChannelAppendAdvanceScheduler, Module: ModuleChannelAppend, Name: "advance_scheduler", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskChannelAppendDeliveryFanout, Module: ModuleChannelAppend, Name: "delivery_fanout", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskChannelAppendMetrics, Module: ModuleChannelAppend, Name: "metrics", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskChannelAppendWriterAdvance, Module: ModuleChannelAppend, Name: "writer_advance", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskChannelAppendWorkerPool, Module: ModuleChannelAppend, Name: "worker_pool", Kind: TaskKindPool, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskChannelAppendStopDrain, Module: ModuleChannelAppend, Name: "stop_drain", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskChannelAppendPostCommitRetry, Module: ModuleChannelAppend, Name: "post_commit_retry", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskDeliveryRetryWorker, Module: ModuleDelivery, Name: "retry_worker", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskDeliveryRetryDoneWait, Module: ModuleDelivery, Name: "retry_done_wait", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskChannelAppendDeliveryWorker, Module: ModuleChannelAppend, Name: "delivery_worker", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskChannelAppendDeliveryDoneWait, Module: ModuleChannelAppend, Name: "delivery_done_wait", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskChannelAppendDeliveryAdmissionWait, Module: ModuleChannelAppend, Name: "delivery_admission_wait", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskPluginHookWorker, Module: ModulePlugin, Name: "hook_worker", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRepanic},
	{ID: TaskPluginHookFinalize, Module: ModulePlugin, Name: "hook_finalize", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskPluginLifecycleClose, Module: ModulePlugin, Name: "lifecycle_close", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskPluginWatcher, Module: ModulePlugin, Name: "watcher", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskPluginProcessWait, Module: ModulePlugin, Name: "process_wait", Kind: TaskKindDynamic, PanicPolicy: PanicPolicyRecover},
	{ID: TaskPluginStopCallback, Module: ModulePlugin, Name: "stop_callback", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskBackupCoordinator, Module: ModuleBackup, Name: "coordinator", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskBackupPartitionWorker, Module: ModuleBackup, Name: "partition_worker", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRecover},
	{ID: TaskBackupPartitionProducer, Module: ModuleBackup, Name: "partition_producer", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskBackupRestoreCoordinator, Module: ModuleBackup, Name: "restore_coordinator", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
	{ID: TaskBackupRestoreWorker, Module: ModuleBackup, Name: "restore_worker", Kind: TaskKindFixed, PanicPolicy: PanicPolicyRecover},
	{ID: TaskBackupRestoreProducer, Module: ModuleBackup, Name: "restore_producer", Kind: TaskKindBurst, PanicPolicy: PanicPolicyRecover},
	{ID: TaskObservabilityDashboard, Module: ModuleObservability, Name: "dashboard", Kind: TaskKindSingleton, PanicPolicy: PanicPolicyRepanic, Expected: 1},
}

func buildCatalog(specs []TaskSpec) (map[TaskID]TaskSpec, error) {
	catalog := make(map[TaskID]TaskSpec, len(specs))
	for _, spec := range specs {
		if spec.ID == "" || spec.Module == "" || spec.Name == "" || spec.Kind == "" || spec.PanicPolicy == "" {
			return nil, fmt.Errorf("goroutine: incomplete task spec: %+v", spec)
		}
		if TaskID(string(spec.Module)+"/"+spec.Name) != spec.ID {
			return nil, fmt.Errorf("goroutine: task %q does not match module/name", spec.ID)
		}
		if _, exists := catalog[spec.ID]; exists {
			return nil, fmt.Errorf("goroutine: duplicate task %q", spec.ID)
		}
		if spec.Required && spec.Expected <= 0 {
			return nil, fmt.Errorf("goroutine: required task %q must declare a positive expected count", spec.ID)
		}
		catalog[spec.ID] = spec
	}
	return catalog, nil
}
