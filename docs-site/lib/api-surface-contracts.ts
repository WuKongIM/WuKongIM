export type APIAudience =
  | 'public-integration'
  | 'operator'
  | 'cluster-internal'
  | 'agent-internal';

export type APIStability = 'stable' | 'beta' | 'unstable' | 'reserved';
export type HTTPMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'ANY' | 'WS';

export interface HTTPSurface {
  method: HTTPMethod;
  path: string;
  audience: APIAudience;
  authentication: string;
  condition: string;
  stability: APIStability;
}

const noBuiltInAuth = 'No built-in authentication; isolate the listener at the network boundary.';
const benchBearer =
  'Exact Bearer bench.api_token when configured; otherwise no built-in authentication.';

/** Non-product routes mounted on the Product HTTP listener. */
export const basicOperationsHTTP: readonly HTTPSurface[] = [
  {
    method: 'GET',
    path: '/healthz',
    audience: 'operator',
    authentication: noBuiltInAuth,
    condition: 'Always registered.',
    stability: 'stable',
  },
  {
    method: 'GET',
    path: '/readyz',
    audience: 'operator',
    authentication: noBuiltInAuth,
    condition: 'Always registered; returns 503 when the readiness provider is not ready.',
    stability: 'stable',
  },
  {
    method: 'ANY',
    path: '/metrics',
    audience: 'operator',
    authentication: noBuiltInAuth,
    condition: 'Registered only when a Prometheus handler is configured.',
    stability: 'stable',
  },
  {
    method: 'GET',
    path: '/top/v1/snapshot',
    audience: 'operator',
    authentication: noBuiltInAuth,
    condition: 'Always registered; returns 404 when the snapshot provider is absent.',
    stability: 'unstable',
  },
];

/** Debug routes. Every entry also requires observability.debug_api_enable. */
export const debugHTTP: readonly HTTPSurface[] = [
  ['GET', '/debug/config', 'A bounded config provider must also be configured.'],
  ['GET', '/debug/cluster', 'A bounded cluster provider must also be configured.'],
  ['GET', '/debug/diagnostics/trace/:trace_id', 'A diagnostics store must also be configured.'],
  ['GET', '/debug/diagnostics/message', 'A diagnostics store must also be configured.'],
  ['GET', '/debug/diagnostics/events', 'A diagnostics store must also be configured.'],
  ['GET', '/debug/goroutines', 'Always registered while the Debug API is enabled.'],
  ['GET', '/debug/goroutines/summary', 'Always registered; returns 404 without a registry provider.'],
  ['ANY', '/debug/pprof', 'Always registered while the Debug API is enabled.'],
  ['ANY', '/debug/pprof/*name', 'Always registered while the Debug API is enabled.'],
].map(([method, path, detail]) => ({
  method: method as HTTPMethod,
  path,
  audience: 'operator' as const,
  authentication: benchBearer,
  condition: `Requires observability.debug_api_enable. ${detail}`,
  stability: 'unstable' as const,
}));

/** Benchmark-only routes. Every entry also requires bench.api_enable. */
export const benchHTTP: readonly HTTPSurface[] = [
  ['GET', '/bench/v1/capabilities', 'Registered with the benchmark API.'],
  ['GET', '/bench/v1/capacity-target', 'Registered with the benchmark API.'],
  ['GET', '/bench/v1/snapshot', 'Registered with the benchmark API.'],
  ['GET', '/bench/v1/presence/snapshot', 'Registered with the benchmark API.'],
  [
    'POST',
    '/bench/v1/terminal-fence/prepare',
    'Also requires a terminal-fence controller and a non-empty bench.api_token.',
  ],
  ['GET', '/bench/v1/channel-runtime/snapshot', 'Registered with the benchmark API.'],
  ['POST', '/bench/v1/channel-runtime/probe', 'Registered with the benchmark API.'],
  ['POST', '/bench/v1/channel-runtime/evict', 'Registered with the benchmark API.'],
  ['POST', '/bench/v1/users/tokens', 'Registered with the benchmark API.'],
  ['POST', '/bench/v1/channels', 'Registered with the benchmark API.'],
  ['POST', '/bench/v1/channels/subscribers', 'Registered with the benchmark API.'],
  ['POST', '/bench/v1/channels/subscribers/remove', 'Registered with the benchmark API.'],
].map(([method, path, detail]) => ({
  method: method as HTTPMethod,
  path,
  audience: 'agent-internal' as const,
  authentication: benchBearer,
  condition: `Requires bench.api_enable. ${detail}`,
  stability: 'unstable' as const,
}));

export type ManagerAuthOffBehavior =
  | 'unguarded'
  | 'fail-closed'
  | 'unavailable'
  | 'dedicated-bearer';

export interface ManagerRoute {
  method: HTTPMethod;
  path: string;
  group: string;
  permission: string;
  authOff: ManagerAuthOffBehavior;
}

export interface ManagerRouteGroup {
  id: string;
  permission: string;
  authOff: ManagerAuthOffBehavior;
  routes: readonly ManagerRoute[];
}

function managerGroup(
  id: string,
  permission: string,
  authOff: ManagerAuthOffBehavior,
  routes: readonly (readonly [HTTPMethod, string])[],
): ManagerRouteGroup {
  return {
    id,
    permission,
    authOff,
    routes: routes.map(([method, path]) => ({ method, path, group: id, permission, authOff })),
  };
}

/** Exact Manager registration inventory; this is an operator-private contract, not Product HTTP. */
export const managerRouteGroups: readonly ManagerRouteGroup[] = [
  managerGroup('operations-mcp-endpoint', 'dedicated MCP credential', 'dedicated-bearer', [
    ['ANY', '/mcp'],
  ]),
  managerGroup('login', 'fixed-user login', 'unavailable', [['POST', '/manager/login']]),
  managerGroup('permissions', 'cluster.permission:r', 'unguarded', [
    ['GET', '/manager/permissions'],
  ]),
  managerGroup('mcp-read', 'cluster.mcp:r', 'fail-closed', [
    ['GET', '/manager/mcp'],
    ['GET', '/manager/mcp/audits'],
  ]),
  managerGroup('mcp-write', 'cluster.mcp:w', 'fail-closed', [
    ['POST', '/manager/mcp/tokens'],
    ['DELETE', '/manager/mcp/tokens/:credential_id'],
    ['PUT', '/manager/mcp/owner'],
    ['POST', '/manager/mcp/start'],
    ['POST', '/manager/mcp/stop'],
  ]),
  managerGroup('node-read', 'cluster.node:r', 'unguarded', [
    ['GET', '/manager/nodes'],
    ['GET', '/manager/nodes/:node_id'],
    ['GET', '/manager/nodes/:node_id/config'],
    ['GET', '/manager/runtime/workqueues'],
    ['GET', '/manager/realtime-monitor'],
    ['GET', '/manager/nodes/:node_id/onboarding/status'],
    ['GET', '/manager/nodes/:node_id/scale-in/status'],
    ['GET', '/manager/nodes/:node_id/diagnostics'],
  ]),
  managerGroup('node-write', 'cluster.node:w', 'unguarded', [
    ['POST', '/manager/nodes/join'],
    ['POST', '/manager/nodes/:node_id/activate'],
    ['POST', '/manager/nodes/:node_id/onboarding/plan'],
    ['POST', '/manager/nodes/:node_id/onboarding/start'],
    ['POST', '/manager/nodes/:node_id/onboarding/advance'],
    ['POST', '/manager/nodes/:node_id/slot-move-out/plan'],
    ['POST', '/manager/nodes/:node_id/slot-move-out/advance'],
    ['POST', '/manager/nodes/:node_id/scale-in/plan'],
    ['POST', '/manager/nodes/:node_id/scale-in/start'],
    ['POST', '/manager/nodes/:node_id/scale-in/drain'],
    ['POST', '/manager/nodes/:node_id/scale-in/remove'],
    ['POST', '/manager/nodes/:node_id/scale-in/advance'],
  ]),
  managerGroup('slot-read', 'cluster.slot:r', 'unguarded', [
    ['GET', '/manager/slots'],
    ['GET', '/manager/slots/:slot_id/logs'],
    ['POST', '/manager/slots/leader-transfer-plan'],
  ]),
  managerGroup('slot-write', 'cluster.slot:w', 'unguarded', [
    ['POST', '/manager/nodes/:node_id/slots/:slot_id/compact'],
    ['POST', '/manager/slots/leader-transfer-batch'],
    ['POST', '/manager/slots/:slot_id/leader-transfer'],
  ]),
  managerGroup('controller-read', 'cluster.controller:r', 'unguarded', [
    ['GET', '/manager/controller/logs'],
    ['GET', '/manager/controller/tasks'],
    ['GET', '/manager/controller/tasks/:task_id'],
    ['GET', '/manager/controller/task-audits'],
    ['GET', '/manager/controller/task-audits/:task_id/events'],
    ['GET', '/manager/nodes/:node_id/controller-raft'],
  ]),
  managerGroup('controller-write', 'cluster.controller:w', 'unguarded', [
    ['POST', '/manager/nodes/:node_id/controller-raft/compact'],
    ['POST', '/manager/nodes/:node_id/controller-voter/promote'],
    ['POST', '/manager/controller-raft/compact'],
  ]),
  managerGroup('diagnostics-read', 'cluster.diagnostics:r', 'unguarded', [
    ['GET', '/manager/diagnostics/trace/:trace_id'],
    ['GET', '/manager/diagnostics/message'],
    ['GET', '/manager/diagnostics/events'],
    ['GET', '/manager/diagnostics/tracking-rules'],
  ]),
  managerGroup('diagnostics-write', 'cluster.diagnostics:w', 'unguarded', [
    ['POST', '/manager/diagnostics/tracking-rules'],
    ['DELETE', '/manager/diagnostics/tracking-rules/:rule_id'],
  ]),
  managerGroup('application-log-read', 'cluster.log:r', 'unguarded', [
    ['GET', '/manager/app-logs/sources'],
    ['GET', '/manager/app-logs'],
    ['GET', '/manager/app-logs/stream'],
  ]),
  managerGroup('database-read', 'cluster.db:r', 'unguarded', [
    ['GET', '/manager/db/inspect/tables'],
    ['GET', '/manager/db/inspect/tables/:domain/:table'],
    ['POST', '/manager/db/inspect/query'],
  ]),
  managerGroup('channel-read', 'cluster.channel:r', 'unguarded', [
    ['GET', '/manager/channel-runtime-meta'],
    ['GET', '/manager/channels'],
    ['GET', '/manager/channels/:channel_type/:channel_id'],
    ['GET', '/manager/channels/:channel_type/:channel_id/subscribers'],
    ['GET', '/manager/channels/:channel_type/:channel_id/allowlist'],
    ['GET', '/manager/channels/:channel_type/:channel_id/denylist'],
    ['GET', '/manager/conversations'],
    ['GET', '/manager/messages'],
  ]),
  managerGroup('connection-read', 'cluster.connection:r', 'unguarded', [
    ['GET', '/manager/connections'],
    ['GET', '/manager/connections/:session_id'],
  ]),
  managerGroup('webhook-read', 'cluster.webhook:r', 'unguarded', [
    ['GET', '/manager/webhooks/config'],
  ]),
  managerGroup('plugin-read', 'cluster.plugin:r', 'unguarded', [
    ['GET', '/manager/nodes/:node_id/plugins'],
    ['GET', '/manager/nodes/:node_id/plugins/:plugin_no'],
    ['GET', '/manager/plugin-bindings'],
  ]),
  managerGroup('plugin-write', 'cluster.plugin:w', 'unguarded', [
    ['POST', '/manager/plugin-bindings'],
    ['DELETE', '/manager/plugin-bindings'],
    ['PUT', '/manager/nodes/:node_id/plugins/:plugin_no/config'],
    ['POST', '/manager/nodes/:node_id/plugins/:plugin_no/restart'],
    ['DELETE', '/manager/nodes/:node_id/plugins/:plugin_no'],
  ]),
  managerGroup('channel-write', 'cluster.channel:w', 'unguarded', [
    ['POST', '/manager/messages/retention'],
    ['POST', '/manager/channels'],
    ['PATCH', '/manager/channels/:channel_type/:channel_id'],
    ['POST', '/manager/channels/:channel_type/:channel_id/subscribers/add'],
    ['POST', '/manager/channels/:channel_type/:channel_id/subscribers/remove'],
    ['POST', '/manager/channels/:channel_type/:channel_id/allowlist/add'],
    ['POST', '/manager/channels/:channel_type/:channel_id/allowlist/remove'],
    ['POST', '/manager/channels/:channel_type/:channel_id/denylist/add'],
    ['POST', '/manager/channels/:channel_type/:channel_id/denylist/remove'],
    ['POST', '/manager/channel-migrations/leader-transfer'],
    ['POST', '/manager/channel-migrations/replica-replace'],
    ['POST', '/manager/channel-migrations/:task_id/abort'],
  ]),
  managerGroup('migration-read', 'cluster.channel:r', 'unguarded', [
    ['GET', '/manager/channel-migrations/active'],
    ['GET', '/manager/channel-migrations/:task_id'],
  ]),
  managerGroup('user-read', 'cluster.user:r', 'unguarded', [
    ['GET', '/manager/users'],
    ['GET', '/manager/users/:uid'],
    ['GET', '/manager/system-users'],
  ]),
  managerGroup('user-write', 'cluster.user:w', 'unguarded', [
    ['POST', '/manager/users/:uid/kick'],
    ['POST', '/manager/users/:uid/token/reset'],
    ['POST', '/manager/system-users/add'],
    ['POST', '/manager/system-users/remove'],
  ]),
  managerGroup('backup-read', 'cluster.backup:r', 'unguarded', [
    ['GET', '/manager/backups'],
    ['GET', '/manager/backups/archives/:archive_id'],
  ]),
  managerGroup('backup-write', 'cluster.backup:w', 'fail-closed', [
    ['PUT', '/manager/backups/plan'],
    ['POST', '/manager/backups/repository/test'],
    ['POST', '/manager/backups/jobs'],
    ['POST', '/manager/backups/jobs/:job_id/cancel'],
    ['POST', '/manager/backups/archives/:archive_id/verify'],
    ['PUT', '/manager/backups/archives/:archive_id/hold'],
    ['DELETE', '/manager/backups/archives/:archive_id'],
  ]),
  managerGroup('restore-write', 'exact cluster.restore:w', 'fail-closed', [
    ['POST', '/manager/backups/archives/:archive_id/restore'],
    ['POST', '/manager/backups/restores/:job_id/cancel'],
  ]),
];

export const managerRoutes: readonly ManagerRoute[] = managerRouteGroups.flatMap(
  (group) => group.routes,
);

export const managerSecurityBoundary = {
  cors:
    'All Manager HTTP routes except /mcp reflect the caller Origin (or use *) and allow credentials-bearing headers; isolate the listener.',
  authOn:
    'When manager.auth_on=true, HS256 Manager JWT and the route permission above are enforced.',
  authOff:
    'When manager.auth_on=false, ordinary read and write permission middleware is absent. Only backup writes, restore, and MCP administration fail closed; /mcp keeps its own credential boundary.',
} as const;

export interface NodeTransportService {
  id: number;
  symbol: string;
  name: string;
  kind: 'rpc' | 'message';
  stability: APIStability;
}

function nodeService(
  id: number,
  symbol: string,
  name: string,
  kind: 'rpc' | 'message' = 'rpc',
  stability: APIStability = 'unstable',
): NodeTransportService {
  return { id, symbol, name, kind, stability };
}

/** Shared cluster transport catalog from pkg/cluster/net/ids.go. */
export const nodeTransportServices: readonly NodeTransportService[] = [
  nodeService(1, 'RPCSlotForwardPropose', 'slot_forward_propose'),
  nodeService(2, 'RPCChannelPull', 'channel_pull'),
  nodeService(3, 'RPCChannelAck', 'channel_ack'),
  nodeService(4, 'RPCChannelPullHint', 'channel_pull_hint'),
  nodeService(5, 'RPCChannelNotify', 'channel_notify'),
  nodeService(6, 'RPCControlStateSync', 'control_state_sync'),
  nodeService(7, 'RPCControlReportNode', 'control_report_node'),
  nodeService(8, 'RPCControlReportSlots', 'control_report_slots'),
  nodeService(9, 'RPCChannelAppend', 'channel_append'),
  nodeService(10, 'RPCChannelAppendBatch', 'channel_append_batch'),
  nodeService(11, 'RPCControlRaft', 'control_raft'),
  nodeService(12, 'RPCControlTaskResult', 'control_task_result'),
  nodeService(13, 'RPCPresenceAuthority', 'presence_authority'),
  nodeService(14, 'RPCPresenceOwner', 'presence_owner'),
  nodeService(15, 'RPCDeliveryPush', 'delivery_push'),
  nodeService(16, 'RPCDeliveryFanout', 'delivery_fanout', 'rpc', 'reserved'),
  nodeService(17, 'RPCChannelPullBatch', 'channel_pull_batch'),
  nodeService(18, 'RPCChannelPullHintBatch', 'channel_pull_hint_batch'),
  nodeService(19, 'RPCChannelLastVisible', 'channel_last_visible'),
  nodeService(20, 'RPCReservedConversationDirectory', 'reserved_conversation', 'rpc', 'reserved'),
  nodeService(21, 'RPCChannelAuthoritySend', 'channel_authority_send'),
  nodeService(22, 'RPCManagerConnection', 'manager_connection'),
  nodeService(23, 'RPCManagerLogs', 'manager_logs'),
  nodeService(24, 'RPCManagerControllerRaft', 'manager_controller_raft'),
  nodeService(25, 'RPCManagerSlotRaft', 'manager_slot_raft'),
  nodeService(26, 'RPCManagerChannels', 'manager_channels'),
  nodeService(27, 'RPCManagerDBInspect', 'manager_db_inspect'),
  nodeService(28, 'RPCManagerAppLogs', 'manager_app_logs'),
  nodeService(29, 'RPCManagerDiagnostics', 'manager_diagnostics'),
  nodeService(30, 'RPCManagerPlugins', 'manager_plugins'),
  nodeService(31, 'RPCPluginBindingScan', 'plugin_binding_scan'),
  nodeService(32, 'MsgSlotRaft', 'msg_slot_raft', 'message'),
  nodeService(33, 'MsgSlotRaftBatch', 'msg_slot_raft_batch', 'message'),
  nodeService(64, 'RPCControlWrite', 'control_write'),
  nodeService(65, 'RPCManagerMessageRetention', 'manager_message_retention'),
  nodeService(66, 'RPCNodeLifecycle', 'node_lifecycle'),
  nodeService(67, 'RPCSlotStatus', 'slot_status'),
  nodeService(68, 'RPCManagerTaskAudit', 'manager_task_audit'),
  nodeService(69, 'RPCChannelMigrationMeta', 'channel_migration_meta'),
  nodeService(70, 'RPCMessageEventAppend', 'message_event_append'),
  nodeService(71, 'RPCManagerNodeConfig', 'manager_node_config'),
  nodeService(72, 'RPCManagerLatestMessages', 'manager_latest_messages'),
  nodeService(73, 'RPCScheduledBackupMessages', 'scheduled_backup_messages'),
  nodeService(74, 'RPCScheduledBackupSlot', 'scheduled_backup_slot'),
  nodeService(75, 'RPCScheduledBackupRepositoryProbe', 'scheduled_backup_probe'),
  nodeService(76, 'RPCScheduledBackupRestore', 'scheduled_backup_restore'),
  nodeService(77, 'RPCOpsMCP', 'operations_mcp'),
  nodeService(78, 'RPCManagerGoroutines', 'manager_goroutines'),
  nodeService(79, 'RPCSlotSubscriberMetadata', 'slot_subscriber_metadata'),
  nodeService(80, 'RPCSlotChannelMetadata', 'slot_channel_metadata'),
  nodeService(81, 'RPCChannelConversationHeads', 'channel_conversation_heads'),
  nodeService(82, 'RPCChannelCommittedReads', 'channel_committed_reads'),
  nodeService(83, 'RPCSlotUserMembership', 'slot_user_membership'),
  nodeService(84, 'RPCSlotRuntimeMetadata', 'slot_runtime_metadata'),
  nodeService(85, 'RPCSlotPermissionMetadataBatch', 'slot_permission_metadata_batch'),
  nodeService(86, 'RPCChannelQuorumExchange', 'channel_quorum_exchange'),
];

export const nodeTransportBoundary =
  'Cluster-internal TCP transport has no per-call bearer or TLS identity. Join alone checks the shared join token; every other service trusts cluster routing and network isolation.';

export const nodeTransportCatalogDebt = [
  'The default Slot proxy uses promoted catalog IDs 79, 80, 83, 84, and 85.',
  'The exported generic Slot proxy additionally declares private IDs 4, 47, and 53. ID 4 overlaps RPCChannelPullHint and is deliberately excluded from default composition.',
] as const;

export const operationsMCPTools = [
  'cluster_health',
  'node_inspect',
  'slot_inspect',
  'channel_runtime_inspect',
  'controller_tasks_query',
  'metrics_query_range',
  'logs_search',
  'logs_context',
  'diagnostics_query',
  'config_read_redacted',
  'backup_inspect',
  'pprof_analyze',
] as const;

export const cloudAnalysisMCPTools = [
  'run_inspect',
  'cluster_snapshot',
  'workload_inspect',
  'metrics_query_range',
  'logs_search',
  'logs_context',
  'diagnostics_query',
  'task_audits_query',
  'trace_start',
  'trace_query',
  'profile_capture',
  'profile_top',
  'profile_list',
  'config_read_redacted',
] as const;

export const reviewCheckMCPTools = ['check_list', 'check_result', 'check_run'] as const;

export const issueAgentCLICommands = [
  'reconcile-github',
  'recover-task',
  'build-context',
  'capture-candidate',
  'verify-candidate',
  'mint-app-token',
  'publish-candidate',
] as const;

export const reviewAgentCLICommands = [
  'normalize-review-result',
  'reconcile-github',
  'recover-review',
  'build-context',
  'verify-baseline',
  'validate-review-result',
  'validate-explanation',
  'append-state',
  'publish-review',
] as const;

export const reviewCheckSelectorCommands = [
  'go-format',
  'go-mod-tidy',
  'web',
  'demo',
  'docs',
  'docs-integration',
  'three-node',
] as const;

export const cloudAnalysisHTTP: readonly HTTPSurface[] = [
  {
    method: 'POST',
    path: '/mcp',
    audience: 'agent-internal',
    authentication: 'Non-renewable, run-scoped Analysis Bearer token; cross-origin requests rejected.',
    condition: 'Available only for one exact live Simulation Run.',
    stability: 'unstable',
  },
  {
    method: 'GET',
    path: '/healthz',
    audience: 'agent-internal',
    authentication: 'No built-in authentication.',
    condition: 'Analysis gateway process health and bounded run metadata.',
    stability: 'unstable',
  },
  {
    method: 'GET',
    path: '/self-check',
    audience: 'agent-internal',
    authentication: 'No built-in authentication.',
    condition: 'Checks bounded dependencies and returns failure names only.',
    stability: 'unstable',
  },
  {
    method: 'POST',
    path: '/analysis/token',
    audience: 'agent-internal',
    authentication: 'GitHub OIDC Bearer token.',
    condition: 'Optional issuer for one non-renewable Analysis Token.',
    stability: 'unstable',
  },
];

export const cloudViewSurfaces: readonly HTTPSurface[] = [
  {
    method: 'ANY',
    path: '/cloud-view/status',
    audience: 'operator',
    authentication: noBuiltInAuth,
    condition: 'Cloud View catch-all dispatcher; returns no-store viewer state.',
    stability: 'unstable',
  },
  {
    method: 'ANY',
    path: '/prometheus[/...]',
    audience: 'operator',
    authentication: noBuiltInAuth,
    condition: 'Redirects /prometheus and proxies the Prometheus subtree.',
    stability: 'unstable',
  },
  {
    method: 'WS',
    path: '<any WebSocket upgrade>',
    audience: 'public-integration',
    authentication: 'Cloud View adds no authentication; the upstream Gateway authenticates WKProto.',
    condition: 'Proxied to a healthy node Gateway.',
    stability: 'unstable',
  },
  {
    method: 'ANY',
    path: '/demo|/route|/user|/channel|/tmpchannel|/message|/conversation|/conversations|/streammessage',
    audience: 'public-integration',
    authentication: 'Cloud View adds no authentication; upstream Product HTTP boundaries remain.',
    condition: 'Known Product prefixes proxy to a healthy node API; every other path proxies Manager.',
    stability: 'unstable',
  },
];

export const pluginHostRPCPaths = [
  '/plugin/start',
  '/close',
  '/message/send',
  '/channel/messages',
  '/cluster/config',
  '/cluster/channels/belongNode',
  '/conversation/channels',
  '/plugin/httpForward',
] as const;

export const webhookEvents = ['msg.notify', 'msg.offline', 'user.onlinestatus'] as const;

export const benchmarkWorkerHTTP = [
  'GET /healthz',
  'GET /v1/info',
  'POST /v1/assign',
  'POST /v1/phase/prepare',
  'POST /v1/phase/connect',
  'POST /v1/phase/warmup',
  'POST /v1/phase/run',
  'POST /v1/phase/cooldown',
  'POST /v1/prepare/channels',
  'POST /v1/terminal-cut',
  'POST /v1/stop',
  'GET /v1/status',
  'GET /v1/metrics',
  'GET /v1/report',
] as const;

export const chatLifecycleWorkerHTTP = [
  'GET /healthz',
  'GET /v1/info',
  'POST /v1/chat-lifecycle/assign',
  'POST /v1/chat-lifecycle/start',
  'GET /v1/chat-lifecycle/status',
  'GET /v1/chat-lifecycle/snapshot',
  'POST /v1/chat-lifecycle/checkpoint',
  'POST /v1/chat-lifecycle/rate',
  'POST /v1/chat-lifecycle/grant',
  'POST /v1/chat-lifecycle/lifecycle-candidates',
  'POST /v1/chat-lifecycle/lifecycle-reheat',
  'POST /v1/chat-lifecycle/stop',
] as const;
