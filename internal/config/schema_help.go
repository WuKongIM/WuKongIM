package config

// fieldHelp is the bilingual operator documentation for one public schema field.
type fieldHelp struct{ EN, ZH string }

// schemaHelp stays next to the schema so all configuration views share field meaning and constraints.
var schemaHelp = map[string]fieldHelp{
	"node.id": {
		EN: "Stable, non-zero node ID unique within the cluster; do not change it after data exists. Required at startup.",
		ZH: "节点在集群中的稳定、非零唯一 ID；创建数据后不要更改。启动必填。",
	},
	"node.data_dir": {
		EN: "Root directory for this node's durable data; place it on writable, backed-up storage. Required at startup.",
		ZH: "本节点持久化数据的根目录；应放在可写且受备份保护的磁盘上。启动必填。",
	},
	"cluster.listen_addr": {
		EN: "Listener for inter-node Cluster RPC; it may bind 0.0.0.0, but a wildcard must not be advertised to peers. Required at startup.",
		ZH: "节点间 Cluster RPC 的监听地址；可绑定 0.0.0.0，但不能把通配地址提供给远端节点连接。启动必填。",
	},
	"cluster.id": {
		EN: "Stable Controller cluster identity; required for seed joining. Static inventories and implicit single-node clusters derive it from node IDs when omitted.",
		ZH: "Controller 使用的稳定集群标识；种子加入时必填。静态清单或隐式单节点集群省略时会按节点 ID 派生。",
	},
	"cluster.start_timeout": {
		EN: "Maximum wait for cluster startup readiness gates, including committed Slot write probes; omitted or 0 uses 30s. Must be non-negative. Increase only when measured cold recovery needs more time; this does not skip quorum, routing, or placement checks or change steady-state request timeouts.",
		ZH: "集群启动就绪检查（包括 Slot 提交写入探测）的最长等待时间；省略或为 0 时使用 30s，不能为负数。仅在实测冷恢复需要更多时间时调整；不会跳过多数派、路由或放置检查，也不改变运行中的请求超时。",
	},
	"cluster.seeds": {
		EN: "Existing node addresses used to discover the cluster during dynamic joining; must be non-empty and cannot be combined with cluster.nodes.",
		ZH: "动态加入时用于发现集群的现有节点地址列表；不能为空，且不能与 cluster.nodes 同时使用。",
	},
	"cluster.advertise_addr": {
		EN: "Stable address stored in membership for peers to call this node; required with cluster.seeds and must be reachable by peers.",
		ZH: "动态加入时写入集群成员信息、供其他节点回连的稳定地址；使用 cluster.seeds 时必填。",
	},
	"cluster.join_token": {
		EN: "Shared credential that authenticates dynamic cluster joining; it must be non-empty whenever present and is required with cluster.seeds. Redacted in startup snapshots and diagnostics.",
		ZH: "动态加入集群的共享认证令牌；字段一旦出现就不能为空，使用 cluster.seeds 时必填。启动快照与诊断均脱敏。",
	},
	"cluster.nodes": {
		EN: "Static Controller voters with {id, addr} elements; a non-empty list must contain the current node.id, with non-zero unique IDs and non-empty addresses. It cannot be combined with cluster.seeds; an explicit empty list (including environment JSON null) currently falls back to an implicit single-node cluster and cannot disable clustering.",
		ZH: "静态 Controller 投票节点列表，元素为 {id, addr}；非空列表必须包含当前 node.id，id 非零且唯一，addr 非空。不能与 cluster.seeds 同时使用；显式空列表（环境变量 JSON null 同样）当前会回退为隐式单节点集群，不能用来禁用集群。",
	},
	"cluster.initial_slot_count": {
		EN: "Independent Slot Raft Groups written to persisted Controller state at first initialization; it cannot exceed cluster.hash_slot_count, and omitted or 0 uses 1. Existing clusters follow the persisted value: changing this setting does not resize Slots, and increasing it may block readiness. Do not change it after initialization.",
		ZH: "首次初始化时写入 Controller 持久状态的独立 Slot Raft Group 数，不得大于 cluster.hash_slot_count；省略或为 0 时使用 1。已有集群以持久值为准，修改不会调整 Slot 数，调大还可能阻塞节点就绪；初始化后不要更改。",
	},
	"cluster.hash_slot_count": {
		EN: "Stable hash-slot partitions that route keys across Slot Raft Groups; omitted or 0 uses 256. Do not change it after cluster initialization.",
		ZH: "把路由键分配到 Slot Raft Group 的稳定 Hash Slot 分区数；省略或为 0 时使用 256，集群初始化后不要更改。",
	},
	"cluster.slot_replica_n": {
		EN: "Voter replicas per Slot Raft Group when initializing Controller state; existing clusters follow persisted Controller state. 0 derives the static voter count but falls back to 1 during seed joining; an explicit static value cannot exceed the voter count.",
		ZH: "初始化 Controller 状态时每个 Slot Raft Group 的投票副本数；已有集群以 Controller 持久状态为准。0 在静态拓扑按投票节点数推导，种子加入时回退为 1；静态显式值不得超过投票节点数。",
	},
	"cluster.channel_replica_n": {
		EN: "Desired data replicas for newly created channels; 0 follows the local effective cluster.slot_replica_n, becoming 1 during seed joining rather than reading the persisted Controller replica count. Multi-replica clusters should set the same explicit value on every node and fit available nodes and failure domains.",
		ZH: "新建频道的期望数据副本数；0 跟随本地有效的 cluster.slot_replica_n，种子加入时也会成为 1，不会读取已有 Controller 的持久副本数。多副本集群应在所有节点显式配置一致值，并与可用节点和故障域匹配。",
	},
	"cluster.slot_tick_interval": {
		EN: "Local Slot Raft tick interval; defaults to 50ms and must be greater than 0.",
		ZH: "Slot Raft 本地 Tick 的时间间隔；默认 50ms，必须大于 0。",
	},
	"cluster.slot_election_tick": {
		EN: "Ticks waited before a Slot Raft election can start; defaults to 40 and must exceed the heartbeat tick.",
		ZH: "触发 Slot Raft 选举前等待的 Tick 数；默认 40，必须大于心跳 Tick。",
	},
	"cluster.slot_heartbeat_tick": {
		EN: "Slot Raft heartbeat interval in ticks; defaults to 2 and must be greater than 0.",
		ZH: "Slot Raft 发送心跳的 Tick 间隔；默认 2，必须大于 0。",
	},
	"cluster.slot_log_compaction_enabled": {
		EN: "Enables local Slot Raft snapshots and log compaction; enabled when omitted.",
		ZH: "是否启用本地 Slot Raft 快照与日志压缩；省略时启用。",
	},
	"cluster.slot_log_compaction_trigger_entries": {
		EN: "Applied entries since the last snapshot required before another compaction; defaults to 10000 and must be positive.",
		ZH: "相对上次快照新增多少条已应用日志后允许再次压缩；默认 10000，必须大于 0。",
	},
	"cluster.slot_log_compaction_check_interval": {
		EN: "Minimum interval between Slot Raft log-compaction checks; defaults to 30s and must be positive.",
		ZH: "检查 Slot Raft 日志是否需要压缩的最小间隔；默认 30s，必须大于 0。",
	},
	"cluster.channel_reactor_count": {
		EN: "Channel Reactor partitions on this node; 0 derives a CPU-aware runtime value.",
		ZH: "本节点的 Channel Reactor 分区数；0 使用 CPU 感知的运行时默认值。",
	},
	"cluster.channel_store_append_workers": {
		EN: "Maximum blocking Leader store-append workers; 0 keeps the Channel runtime default.",
		ZH: "阻塞式 Leader 存储追加的最大 Worker 数；0 使用 Channel 运行时默认值。",
	},
	"cluster.channel_store_append_batch_max_wait": {
		EN: "Maximum wait for coalescing Leader store-appends across channels; 0 keeps the Channel worker default.",
		ZH: "跨频道合并 Leader 存储追加任务的最长等待时间；0 使用 Channel Worker 默认值。",
	},
	"cluster.channel_store_apply_workers": {
		EN: "Maximum blocking Follower store-apply workers; 0 keeps the Channel runtime default.",
		ZH: "阻塞式 Follower 存储应用的最大 Worker 数；0 使用 Channel 运行时默认值。",
	},
	"cluster.channel_rpc_workers": {
		EN: "Maximum blocking Channel replication RPC workers; 0 uses 96.",
		ZH: "阻塞式 Channel 复制 RPC 的最大 Worker 数；0 使用默认值 96。",
	},
	"cluster.channel_rpc_batch_max_items": {
		EN: "Maximum same-target Pull or PullHint items coalesced into one RPC; 0 uses 8.",
		ZH: "一次发往同一目标的 Pull 或 PullHint RPC 最多合并项数；0 使用默认值 8。",
	},
	"cluster.max_channels": {
		EN: "Maximum loaded Channel Runtimes on this node; 0 leaves the count unlimited.",
		ZH: "本节点可同时加载的 Channel Runtime 上限；0 表示不设置上限。",
	},
	"cluster.channel_append_batch_max_records": {
		EN: "Queued record count that triggers a Channel store-append flush; 0 keeps the runtime default.",
		ZH: "排队记录数达到该值时触发 Channel 存储追加；0 使用运行时默认值。",
	},
	"cluster.channel_append_batch_max_wait": {
		EN: "Maximum age of the oldest queued Channel append before flushing; 0 keeps the runtime default.",
		ZH: "最早一条排队 Channel 追加在刷盘前可等待的最长时间；0 使用运行时默认值。",
	},
	"cluster.channel_append_batch_adaptive_flush": {
		EN: "Enables a shorter adaptive flush delay for low-traffic channels; disabled when omitted.",
		ZH: "是否让低流量频道使用更短的自适应刷盘等待；省略时关闭。",
	},
	"cluster.channel_append_batch_cold_max_wait": {
		EN: "Low-traffic channel delay used by adaptive flushing; 0 uses the normal batching window.",
		ZH: "启用自适应刷盘后，低流量频道的最长等待时间；0 沿用普通批处理窗口。",
	},
	"cluster.channel_follower_recovery_probe_interval": {
		EN: "Base interval for probing parked Followers for recovery; 0 keeps the Channel runtime default.",
		ZH: "暂停的 Follower 恢复探测基础间隔；0 使用 Channel 运行时默认值。",
	},
	"cluster.channel_follower_recovery_probe_jitter": {
		EN: "Random jitter window that spreads parked-Follower recovery probes; 0 keeps the runtime default.",
		ZH: "Follower 恢复探测的随机抖动窗口，用于分散集中探测；0 使用运行时默认值。",
	},
	"cluster.node_health_report_interval": {
		EN: "Interval for reporting compact node health to Controller; defaults to 5s and must be positive.",
		ZH: "本节点向 Controller 上报精简健康信息的间隔；默认 5s，必须大于 0。",
	},
	"cluster.node_health_report_ttl": {
		EN: "How long Controller trusts the latest node-health report; defaults to 30s and cannot be shorter than the report interval.",
		ZH: "Controller 信任最近一次节点健康报告的时长；默认 30s，不得小于上报间隔。",
	},
	"cluster.commit_coordinator_sync": {
		EN: "Durable-commit compatibility switch; omitted is equivalent to true, and an explicit value must also be true because WuKongIM does not allow durable sync to be disabled.",
		ZH: "持久提交同步兼容开关；省略等效于 true，显式值也只能为 true，WuKongIM 不允许关闭持久同步。",
	},
	"cluster.commit_coordinator_flush_window": {
		EN: "Maximum delay for grouping adjacent Channel durable commits; defaults to 500us, and an explicit value must be positive.",
		ZH: "合并相邻 Channel 持久提交请求的最长等待时间；默认 500us，显式值必须大于 0。",
	},
	"cluster.commit_coordinator_max_requests": {
		EN: "Maximum logical requests grouped into one physical commit; 0 applies no request-count limit.",
		ZH: "一次物理提交可合并的逻辑请求上限；0 表示不按请求数限制。",
	},
	"cluster.commit_coordinator_max_records": {
		EN: "Maximum message records grouped into one physical commit; 0 applies no record-count limit.",
		ZH: "一次物理提交可合并的消息记录上限；0 表示不按记录数限制。",
	},
	"cluster.commit_coordinator_max_bytes": {
		EN: "Maximum approximate payload bytes grouped into one physical commit; 0 applies no byte limit.",
		ZH: "一次物理提交可合并的近似负载字节上限；0 表示不按字节数限制。",
	},
	"cluster.commit_coordinator_shards": {
		EN: "Independent commit coordinators for the message database; 0 uses 1. Increase only after storage-specific load tests.",
		ZH: "消息数据库独立提交协调器数量；0 使用 1，增加前应针对存储进行压测。",
	},
	"channel_migration.enable": {
		EN: "Starts the background worker that advances Channel migrations and creates repair tasks; enabled when omitted.",
		ZH: "是否启动推进 Channel 迁移和创建修复任务的后台 Worker；省略时启用。",
	},
	"channel_migration.scan_interval": {
		EN: "Interval for scanning and advancing Channel migration work; omitted uses 1s, and an explicit value must be positive.",
		ZH: "扫描并推进 Channel 迁移工作的时间间隔；省略时使用 1s，显式值必须大于 0。",
	},
	"channel_migration.scan_limit": {
		EN: "Channel Runtime metadata rows read from one Slot page per scan; omitted uses 64, and an explicit value must be positive.",
		ZH: "每次扫描一个 Slot 页面时读取的 Channel Runtime 元数据上限；省略时使用 64，显式值必须大于 0。",
	},
	"channel_migration.max_pages_per_tick": {
		EN: "Maximum physical Slot pages scanned per worker tick; omitted uses 1, and an explicit value must be positive.",
		ZH: "每个 Worker Tick 最多扫描的物理 Slot 页数；省略时使用 1，显式值必须大于 0。",
	},
	"channel_migration.max_tasks_per_tick": {
		EN: "Maximum repair tasks created per scan; omitted uses 1, and an explicit value must be positive.",
		ZH: "每次扫描最多创建的修复任务数；省略时使用 1，显式值必须大于 0。",
	},
	"channel_migration.task_limit": {
		EN: "Maximum active migration tasks inspected by the executor per tick; omitted uses 1, and an explicit value must be positive.",
		ZH: "执行器每个 Tick 最多检查的活跃迁移任务数；省略时使用 1，显式值必须大于 0。",
	},
	"channel.message_retention_physical_gc_enable": {
		EN: "Enables background physical deletion of local messages beyond each channel's retention boundary; disabled when omitted.",
		ZH: "是否启用后台物理删除已超过频道保留边界的本地消息；省略时关闭。",
	},
	"channel.message_retention_scan_interval": {
		EN: "Interval for scanning one channel-catalog page for retention cleanup; 0 uses 1m.",
		ZH: "后台保留清理扫描一个频道目录页的间隔；0 使用默认值 1m。",
	},
	"channel.message_retention_channel_batch_size": {
		EN: "Maximum local channel-catalog entries processed per cleanup pass; 0 uses 128.",
		ZH: "每次保留清理最多处理的本地频道目录条目数；0 使用默认值 128。",
	},
	"channel.message_retention_max_trim_messages": {
		EN: "Maximum message rows deleted for one channel per cleanup attempt; 0 uses 1000.",
		ZH: "单个频道每次清理最多删除的消息行数；0 使用默认值 1000。",
	},
	"channel.message_retention_max_trim_bytes": {
		EN: "Maximum payload bytes deleted for one channel per cleanup attempt; 0 applies no byte limit.",
		ZH: "单个频道每次清理最多删除的负载字节数；0 表示不按字节数限制。",
	},
	"channel.large_group_subscriber_threshold": {
		EN: "Treats a channel as a large group when ordinary subscribers exceed this count; defaults to 500 and must be positive.",
		ZH: "普通订阅者数量超过该值时按大群组处理；默认 500，必须大于 0。",
	},
	"api.listen_addr": {
		EN: "Listener for the product HTTP API; an empty value disables this HTTP service.",
		ZH: "产品 HTTP API 的监听地址；留空则不启动该 HTTP 服务。",
	},
	"api.external_tcp_addr": {
		EN: "WKProto TCP address override published to capacity discovery and similar callers, in host:port form; empty derives from the first matching gateway.listeners entry and stays empty if none matches. It does not create a listener.",
		ZH: "向容量发现等调用方公布的 WKProto TCP 地址覆盖项，格式为 host:port；留空时从首个匹配的 gateway.listeners 推导，无匹配项则为空。它不会创建监听器。",
	},
	"api.external_ws_addr": {
		EN: "WebSocket URL override published to capacity discovery and similar callers; empty derives from the first matching gateway.listeners entry and stays empty if none matches. It does not create a listener and is redacted from diagnostic artifacts.",
		ZH: "向容量发现等调用方公布的 WebSocket URL 覆盖项；留空时从首个匹配的 gateway.listeners 推导，无匹配项则为空。它不会创建监听器，诊断制品会脱敏。",
	},
	"api.external_wss_addr": {
		EN: "Secure WebSocket URL override published to capacity discovery and similar callers; empty derives from the first matching gateway.listeners entry and stays empty if none matches. It does not create a listener and is redacted from diagnostic artifacts.",
		ZH: "向容量发现等调用方公布的安全 WebSocket URL 覆盖项；留空时从首个匹配的 gateway.listeners 推导，无匹配项则为空。它不会创建监听器，诊断制品会脱敏。",
	},
	"manager.listen_addr": {
		EN: "Listener for the Manager administration service; an empty value disables Manager.",
		ZH: "Manager 管理服务的监听地址；留空则不启动 Manager。",
	},
	"manager.auth_on": {
		EN: "Requires JWT login authentication for Manager routes; disabled when omitted. With false, Manager routes require no JWT, so do not expose an unauthenticated service to untrusted networks.",
		ZH: "是否要求 Manager 路由使用 JWT 登录认证；省略时关闭。false 时 Manager 路由不要求 JWT，请勿将未认证服务暴露到不可信网络。",
	},
	"manager.jwt_secret": {
		EN: "Secret used to sign and verify Manager JWTs; required when Manager is listening with authentication enabled. Redacted in startup snapshots and diagnostics.",
		ZH: "签发和验证 Manager JWT 的密钥；Manager 正在监听且启用认证时必填。启动快照与诊断均脱敏。",
	},
	"manager.jwt_issuer": {
		EN: "Issuer written to the Manager JWT iss claim.",
		ZH: "写入 Manager JWT iss 声明的签发者。",
	},
	"manager.jwt_expire": {
		EN: "Manager JWT lifetime; when Manager is listening with authentication enabled, omitted or explicit 0 uses 24h, while a negative value fails startup.",
		ZH: "Manager JWT 的有效期；Manager 正在监听且启用认证时，省略或显式 0 使用 24h，负值会导致启动失败。",
	},
	"manager.users": {
		EN: "Static Manager users with username, password, and permissions[{resource, actions}]; actions are r, w, or *. Required when Manager is listening with authentication enabled and fully redacted.",
		ZH: "可登录 Manager 的静态用户列表，元素含 username、password 和 permissions[{resource, actions}]；actions 只允许 r、w、*。Manager 正在监听且启用认证时不能为空，且整项脱敏。",
	},
	"bench.api_enable": {
		EN: "Exposes /bench/v1/* routes on the API listener for controlled benchmark environments; disabled when omitted and requires non-empty api.listen_addr.",
		ZH: "是否在 API 监听器上开放仅用于受控压测环境的 /bench/v1/* 接口；省略时关闭，生效还要求 api.listen_addr 非空。",
	},
	"bench.api_token": {
		EN: "Bench API bearer token; an empty value skips token validation, so set it whenever the API is remotely reachable. Redacted in snapshots and diagnostics.",
		ZH: "Bench API 的 Bearer Token；留空时不校验 Token，接口可被远程访问时必须设置。启动快照与诊断均脱敏。",
	},
	"bench.api_max_batch_size": {
		EN: "Maximum top-level records accepted by one Bench API mutation request; omitted uses 10000, while an explicit value at or below 0 removes this limit and is not recommended for remotely reachable environments.",
		ZH: "单次 Bench API 变更请求允许的顶层记录数上限；省略时使用 10000，显式小于等于 0 会取消该限制，不建议用于可远程访问的环境。",
	},
	"bench.api_max_payload_bytes": {
		EN: "Maximum JSON body bytes accepted by one Bench API mutation request; omitted uses 10485760 (10 MiB), while an explicit value at or below 0 removes this limit and is not recommended for remotely reachable environments.",
		ZH: "单次 Bench API 变更请求允许的 JSON 请求体字节上限；省略时使用 10485760（10 MiB），显式小于等于 0 会取消该限制，不建议用于可远程访问的环境。",
	},
	"observability.metrics_enable": {
		EN: "Enables runtime metric observers and exposes /metrics when an API listener exists; disabled when omitted. The HTTP metrics endpoint requires non-empty api.listen_addr.",
		ZH: "是否启用运行时指标观察器，并在 API 监听器存在时暴露 /metrics；省略时关闭。HTTP 指标端点要求 api.listen_addr 非空。",
	},
	"observability.debug_api_enable": {
		EN: "Exposes /debug diagnostics on the API listener; disabled when omitted and requires non-empty api.listen_addr. Never publish it directly to the internet.",
		ZH: "是否在 API 监听器上开放 /debug 诊断接口；省略时关闭，生效还要求 api.listen_addr 非空。不要直接暴露到公网。",
	},
	"prometheus.enable": {
		EN: "Starts a Prometheus child process managed by WuKongIM; disabled when omitted. Enabling also requires an API listener, metrics, and valid scrape targets; keep it disabled with external Prometheus.",
		ZH: "是否由 WuKongIM 启动并管理 Prometheus 子进程；省略时关闭。启用时还需开放 API 监听器、指标并提供有效抓取目标；使用外部 Prometheus 时保持关闭。",
	},
	"prometheus.query_base_url": {
		EN: "Base URL used by Manager to query an external Prometheus HTTP API; it must be an HTTP(S) URL with a host and no query or fragment. Empty may fall back to managed Prometheus. Redacted from diagnostics.",
		ZH: "Manager 查询外部 Prometheus HTTP API 的基地址；必须是含主机且不含查询或片段的 HTTP(S) URL。留空时可回退到托管实例。诊断制品脱敏。",
	},
	"prometheus.binary_path": {
		EN: "External Prometheus executable used by the managed process; empty uses the embedded binary.",
		ZH: "托管 Prometheus 使用的外部可执行文件路径；留空使用内嵌二进制。",
	},
	"prometheus.listen_addr": {
		EN: "Web listener for the managed Prometheus process, in host:port form; defaults to 127.0.0.1:9099.",
		ZH: "托管 Prometheus 的 Web 监听地址，格式为 host:port；默认 127.0.0.1:9099。",
	},
	"prometheus.data_dir": {
		EN: "Directory for generated Prometheus configuration and TSDB data; empty derives it from node.data_dir.",
		ZH: "托管 Prometheus 生成配置和保存 TSDB 数据的目录；留空时从 node.data_dir 推导。",
	},
	"prometheus.retention_time": {
		EN: "Time-based retention window for the managed Prometheus TSDB; omitted or 0 uses 360h.",
		ZH: "托管 Prometheus TSDB 的按时间保留窗口；省略或为 0 时使用 360h。",
	},
	"prometheus.retention_size": {
		EN: "Optional size-based retention limit for the managed Prometheus TSDB; empty applies no size limit.",
		ZH: "托管 Prometheus TSDB 的可选按容量保留上限；留空表示不设置容量上限。",
	},
	"prometheus.scrape_interval": {
		EN: "Interval for managed Prometheus to scrape WuKongIM metrics; omitted or 0 uses 15s.",
		ZH: "托管 Prometheus 抓取 WuKongIM 指标的间隔；省略或为 0 时使用 15s。",
	},
	"prometheus.scrape_targets": {
		EN: "Metrics targets for managed Prometheus as host:port values without a URL scheme and with ports from 1 to 65535; an empty list may be derived from the API listener.",
		ZH: "托管 Prometheus 的指标目标列表，格式为不含 URL Scheme 的 host:port，端口为 1 到 65535；空列表可从 API 监听地址推导。",
	},
	"top.api_enable": {
		EN: "Exposes the read-only /top/v1/snapshot endpoint on the API listener for wkcli top; disabled when omitted and requires non-empty api.listen_addr.",
		ZH: "是否在 API 监听器上开放供 wkcli top 使用的只读 /top/v1/snapshot 接口；省略时关闭，生效还要求 api.listen_addr 非空。",
	},
	"top.collect_interval": {
		EN: "Interval at which Top samples local runtime state; omitted or 0 uses 1s.",
		ZH: "Top 收集器采样本节点运行状态的间隔；省略或为 0 时使用 1s。",
	},
	"top.history_window": {
		EN: "In-memory sample window retained for Top queries; omitted or 0 uses 5m and must be at least twice the collection interval.",
		ZH: "Top 查询在内存中保留的历史采样窗口；省略或为 0 时使用 5m，且至少为采样间隔的两倍。",
	},
	"diagnostics.enable": {
		EN: "Captures bounded node-local diagnostic events; enabled when omitted.",
		ZH: "是否采集本节点的有界诊断事件；省略时启用。",
	},
	"diagnostics.buffer_size": {
		EN: "Maximum diagnostic events retained in memory; 0 uses 50000.",
		ZH: "内存中最多保留的诊断事件数；0 使用默认值 50000。",
	},
	"diagnostics.sample_rate": {
		EN: "Baseline keep probability for successful diagnostic events, from 0 to 1; omitted uses 0.01.",
		ZH: "成功诊断事件的基础保留概率，范围为 0 到 1；省略时使用 0.01。",
	},
	"diagnostics.slow_threshold_ms": {
		EN: "Duration threshold for keeping successful slow events, in milliseconds; omitted or 0 uses 500.",
		ZH: "无错误事件被视为慢事件并保留的耗时阈值，单位毫秒；省略或为 0 时使用 500。",
	},
	"diagnostics.error_sample_rate": {
		EN: "Keep probability for non-success diagnostic events, from 0 to 1; omitted uses 1.",
		ZH: "非成功诊断事件的保留概率，范围为 0 到 1；省略时使用 1。",
	},
	"diagnostics.deep_sample_rate": {
		EN: "Sampling probability for expensive Reactor or store detail, from 0 to 1; defaults to 0.",
		ZH: "生成高成本 Reactor 或存储细节的采样概率，范围为 0 到 1；默认 0。",
	},
	"diagnostics.deep_slow_threshold_ms": {
		EN: "Threshold for deep tracing slow Reactor or store stages, in milliseconds; omitted or 0 follows the normal slow threshold.",
		ZH: "为慢 Reactor 或存储阶段启用深度追踪的阈值，单位毫秒；省略或为 0 时跟随普通慢阈值。",
	},
	"diagnostics.deep_max_items_per_batch": {
		EN: "Maximum messages expanded into events by one deep-trace batch; 0 uses 16.",
		ZH: "一个深度追踪批次最多展开的消息数；0 使用默认值 16。",
	},
	"diagnostics.debug_matches": {
		EN: "Temporary high-priority sampling rules; an empty list means none. Each item needs at least one of uid, channel_key, client_msg_no, or trace_id; positive ttl_seconds is required to take effect and zero is silently ignored. sample_rate ranges from 0 to 1, where 0 retains nothing.",
		ZH: "临时高优先级采样规则列表；空列表表示无规则。每项至少提供 uid、channel_key、client_msg_no、trace_id 之一；ttl_seconds 必须为正才生效，零值会被静默忽略；sample_rate 范围为 0 到 1，0 不保留事件。",
	},
	"gateway.token_auth_on": {
		EN: "Requires each CONNECT token to exactly match the stored device token for the same UID and device_flag; defaults to true. Disable only for a controlled compatibility migration.",
		ZH: "是否要求 CONNECT Token 与相同 UID、device_flag 的已存设备 Token 完全匹配；省略时为 true。仅在受控兼容迁移期间关闭。",
	},
	"gateway.gnet_multicore": {
		EN: "Enables gnet's CPU-scaled multi-event-loop mode; enabled when omitted.",
		ZH: "是否启用 gnet 按 CPU 扩展的多 Event Loop 模式；省略时启用。",
	},
	"gateway.gnet_num_event_loop": {
		EN: "gnet event-loop count; 0 retains the WuKongIM baseline of 4.",
		ZH: "gnet Event Loop 数；0 保留 WuKongIM 基线值 4。",
	},
	"gateway.runtime_async_send_workers": {
		EN: "Maximum workers dispatching SEND frames asynchronously; a non-positive value uses 1000.",
		ZH: "异步分发 SEND 帧的最大 Worker 数；非正数使用默认值 1000。",
	},
	"gateway.runtime_async_send_queue_capacity": {
		EN: "Maximum queued asynchronous SEND frames; a non-positive value uses 131072, and a full queue rejects new SEND frames.",
		ZH: "异步 SEND 队列最多容纳的帧数；非正数使用 131072，队列满时拒绝新的 SEND 帧。",
	},
	"gateway.runtime_async_auth_workers": {
		EN: "Maximum workers processing CONNECT authentication asynchronously; a non-positive value uses 16.",
		ZH: "异步处理 CONNECT 认证的最大 Worker 数；非正数使用默认值 16。",
	},
	"gateway.runtime_async_auth_queue_capacity": {
		EN: "Maximum queued CONNECT authentications; a non-positive value uses 8192, and a full queue rejects new CONNECT authentication requests.",
		ZH: "异步 CONNECT 认证队列的最大容量；非正数使用 8192，队列满时拒绝新的 CONNECT 认证请求。",
	},
	"gateway.runtime_async_pool_release_timeout": {
		EN: "Maximum graceful-release wait for asynchronous worker pools during shutdown; a non-positive value uses 100ms.",
		ZH: "Gateway 停止时异步 Worker Pool 等待优雅释放的最长时间；非正数使用 100ms。",
	},
	"gateway.default_session_async_send_batch_max_wait": {
		EN: "Maximum wait for a SEND shard to collect adjacent frames; 0 uses 1ms, while a negative value normalizes to 0 and disables waiting.",
		ZH: "一个 SEND 分片为合并相邻帧最多等待的时间；0 使用默认值 1ms，负值会归一化为 0 并取消等待。",
	},
	"gateway.default_session_async_send_batch_max_records": {
		EN: "Maximum frames in one Gateway SEND micro-batch; 0 uses 128.",
		ZH: "一个 Gateway SEND 微批次最多包含的帧数；0 使用默认值 128。",
	},
	"gateway.default_session_async_send_batch_max_bytes": {
		EN: "Maximum payload bytes in one Gateway SEND micro-batch; 0 uses 524288.",
		ZH: "一个 Gateway SEND 微批次允许的最大负载字节数；0 使用默认值 524288。",
	},
	"gateway.listeners": {
		EN: "Client listener list: name, network, address, transport, and protocol are required; path and proxy_protocol_trusted_cidrs are optional; nonempty trusted CIDRs enable automatic direct/PROXY v1/v2 detection (5s, 4096-byte v2 cap), empty disables parsing; names and addresses must each be unique. Omitted opens WKProto TCP on 0.0.0.0:5100 and WSMux on 0.0.0.0:5200; an explicit empty list (including JSON null in the environment) disables Gateway.",
		ZH: "客户端监听器列表；name、network、address、transport、protocol 必填，path 和 proxy_protocol_trusted_cidrs 可选；配置可信 CIDR 后自动识别直连与 PROXY v1/v2（5 秒期限、v2 最大 4096 字节），空列表关闭解析；名称与地址必须各自唯一。省略时开放 0.0.0.0:5100 WKProto TCP 和 0.0.0.0:5200 WSMux；显式空列表（环境变量 JSON null 同样）不启动 Gateway。",
	},
	"gateway.send_timeout": {
		EN: "Maximum duration allowed for one message send initiated by Gateway; a non-positive value uses 5s.",
		ZH: "一次由 Gateway 发起的消息发送允许占用的最长时间；非正数使用默认值 5s。",
	},
	"message.person_whitelist_enabled": {
		EN: "Enforces receiver-side allowlist checks for person messages; disabled by default for compatibility.",
		ZH: "是否对个人消息执行接收方白名单检查；默认关闭以保持兼容行为。",
	},
	"message.cmd_channel_suffix": {
		EN: "Reserved command-channel suffix; omitted or empty uses ____cmd. Only ASCII letters, digits, underscores and hyphens are allowed; reserve it from business channel IDs. Must match on every node and requires restart. Choose before first use: changing it does not migrate stored command channels or UID bindings and can make pending commands inaccessible.",
		ZH: "命令频道的保留后缀，省略或留空使用 ____cmd。仅允许 ASCII 字母、数字、下划线和连字符；业务频道 ID 不得使用该后缀。集群所有节点必须一致，修改后重启。请在首次使用前确定；修改不会迁移已有命令频道或 UID 绑定，可能使待同步命令无法访问。",
	},
	"message.system_uid": {
		EN: "Primary system account UID used when trusted sends omit a sender; omitted or empty uses ____system. Every cluster node must use the same value.",
		ZH: "受信发送未提供发送者时使用的主系统账号 UID；省略或留空使用 ____system。集群内所有节点必须配置为同一值。",
	},
	"message.system_device_id": {
		EN: "Device ID for trusted system sessions; omitted or empty uses ____device, so an empty value cannot disable recognition. Such sessions may bypass channel-type send permissions after the send-ban check.",
		ZH: "受信任系统会话使用的设备 ID；省略或留空使用 ____device，不能通过空值禁用。此类会话通过发送禁用检查后可绕过特定频道类型的发送权限。",
	},
	"message.permission_cache_ttl": {
		EN: "Cache lifetime for permission, membership, and missing-channel reads; 0 disables caching.",
		ZH: "权限、成员关系和频道缺失读取的缓存时长；0 表示不缓存。",
	},
	"presence.activation_timeout": {
		EN: "Timeout for Gateway session route activation against the UID authority; 0 uses 3s.",
		ZH: "Gateway 会话向 UID 权威节点激活在线路由的超时时间；0 使用默认值 3s。",
	},
	"presence.touch_flush_interval": {
		EN: "Interval for flushing local connection activity to UID authorities; 0 uses 1s.",
		ZH: "本节点把连接活动批量刷新到 UID 权威节点的间隔；0 使用默认值 1s。",
	},
	"presence.touch_batch_size": {
		EN: "Maximum local touched routes processed per flush chunk; 0 uses 512.",
		ZH: "每个刷新分块最多处理的本地活动路由数；0 使用默认值 512。",
	},
	"presence.touch_max_routes_per_flush": {
		EN: "Maximum dirty routes processed across all chunks in one flush; omitted uses 65536, while an explicit value must be positive and at least presence.touch_batch_size.",
		ZH: "一次刷新跨所有分块最多处理的脏路由数；省略时使用 65536，显式值必须大于 0 且不小于 presence.touch_batch_size。",
	},
	"presence.route_ttl": {
		EN: "How long a UID authority keeps a route alive after its latest activity; 0 uses 90s.",
		ZH: "UID 权威节点自最近活动后保留在线路由的时长；0 使用默认值 90s。",
	},
	"channel_append.shard_count": {
		EN: "Shards used to look up Channel append authority state; 0 derives a CPU-aware default.",
		ZH: "频道追加权威状态查找的分片数；0 使用 CPU 感知的默认值。",
	},
	"channel_append.advance_pool_size": {
		EN: "Worker pool that advances Channel Append Writer state machines; 0 uses the runtime default 500.",
		ZH: "推进 Channel Append Writer 状态机的 Worker Pool 大小；0 使用运行时默认值 500。",
	},
	"channel_append.effect_pool_size": {
		EN: "Worker pool used by blocking appends and post-append recipient processing; 0 uses the runtime default 2000.",
		ZH: "阻塞追加调用与追加后收件人处理使用的 Worker Pool 大小；0 使用运行时默认值 2000。",
	},
	"channel_append.recipient_authority_dispatch_concurrency": {
		EN: "Deprecated compatibility input; canonical Online Delivery plans ignore this value.",
		ZH: "已弃用的兼容输入；当前在线投递流程会忽略此值。",
	},
	"delivery.enable": {
		EN: "Connects committed messages to the online-delivery runtime; enabled when omitted.",
		ZH: "是否把已提交消息接入在线投递运行时；省略时启用。",
	},
	"delivery.fanout_page_size": {
		EN: "Maximum subscriber UIDs read in one fanout page; 0 uses 512.",
		ZH: "一次扇出分页最多读取的订阅者 UID 数；0 使用默认值 512。",
	},
	"delivery.push_batch_size": {
		EN: "Maximum recipients in one exact-target lookup, delivery plan, or owner-node push chunk; 0 uses 512.",
		ZH: "一次精确目标查询、投递计划或 Owner 节点推送分块最多包含的收件人数；0 使用默认值 512。",
	},
	"delivery.pending_ack_ttl": {
		EN: "Lifetime used to remove stale pending acknowledgements during delivery activity; 0 uses 30s.",
		ZH: "投递活动期间清理过期待确认记录所使用的存活时间；0 使用默认值 30s。",
	},
	"delivery.pending_ack_max_per_session": {
		EN: "Maximum owner-local pending acknowledgements retained for one UID session; 0 uses 1024.",
		ZH: "一个 UID 会话可保留的本地待确认记录上限；0 使用默认值 1024。",
	},
	"delivery.event_queue_size": {
		EN: "Maximum recipient delivery plans waiting for asynchronous processing; 0 uses 1024.",
		ZH: "等待异步处理的收件人投递计划队列上限；0 使用默认值 1024。",
	},
	"delivery.recipient_worker_concurrency": {
		EN: "Maximum workers processing recipient delivery plans; 0 uses 320.",
		ZH: "并行处理收件人投递计划的 Worker 上限；0 使用默认值 320。",
	},
	"webhook.http_addr": {
		EN: "Target receiving JSON webhook POST requests; empty disables the webhook runtime. Redacted from diagnostic artifacts.",
		ZH: "接收 JSON Webhook POST 的目标地址；留空则不启动 Webhook Runtime。诊断制品脱敏。",
	},
	"webhook.focus_events": {
		EN: "Limits delivery to these names: msg.notify, msg.offline, or user.onlinestatus; an empty list selects all supported events.",
		ZH: "只投递这些事件名；支持 msg.notify、msg.offline、user.onlinestatus，空列表表示全部。",
	},
	"webhook.queue_size": {
		EN: "Maximum webhook events queued in memory before worker execution; 0 uses 1024.",
		ZH: "Worker 执行前可在内存中排队的 Webhook 事件上限；0 使用默认值 1024。",
	},
	"webhook.workers": {
		EN: "Maximum concurrent webhook sender workers; 0 uses 16.",
		ZH: "并发发送 Webhook 请求的 Worker 上限；0 使用默认值 16。",
	},
	"webhook.msg_notify_batch_max_items": {
		EN: "Maximum messages in one msg.notify webhook request; 0 uses 100.",
		ZH: "单个 msg.notify Webhook 请求最多携带的消息数；0 使用默认值 100。",
	},
	"webhook.msg_notify_batch_max_wait": {
		EN: "Maximum wait to collect adjacent msg.notify messages; 0 uses 500ms.",
		ZH: "msg.notify 为合并相邻消息最多等待的时间；0 使用默认值 500ms。",
	},
	"webhook.online_status_batch_max_items": {
		EN: "Maximum records in one user.onlinestatus request; 0 uses 512.",
		ZH: "单个 user.onlinestatus 请求最多携带的状态记录数；0 使用默认值 512。",
	},
	"webhook.online_status_batch_max_wait": {
		EN: "Maximum wait to collect adjacent user.onlinestatus records; 0 uses 2s.",
		ZH: "user.onlinestatus 为合并相邻记录最多等待的时间；0 使用默认值 2s。",
	},
	"webhook.offline_uid_batch_size": {
		EN: "Maximum offline UIDs in one msg.offline request; 0 uses 512.",
		ZH: "单个 msg.offline 请求最多携带的离线 UID 数；0 使用默认值 512。",
	},
	"webhook.request_timeout": {
		EN: "Timeout for one outbound webhook request attempt; 0 uses 5s.",
		ZH: "单次出站 Webhook 请求尝试的超时时间；0 使用默认值 5s。",
	},
	"webhook.retry_max_attempts": {
		EN: "Maximum attempts for an admitted webhook batch before it is dropped; 0 uses 3.",
		ZH: "一个已接纳 Webhook 批次在丢弃前的最多尝试次数；0 使用默认值 3。",
	},
	"webhook.before_send.enabled": {
		EN: "Independently enable synchronous admission; defaults to false.",
		ZH: "独立启用同步发送前回调；默认 false。",
	},
	"webhook.before_send.http_addr": {
		EN: "HTTP(S) callback endpoint, required when enabled and redacted in diagnostics.",
		ZH: "同步回调 HTTP(S) 地址；启用时必填，诊断输出脱敏。",
	},
	"webhook.before_send.timeout": {
		EN: "Callback deadline; zero uses 500ms, capped by the original send deadline.",
		ZH: "单次回调期限；0 使用 500ms，受原发送期限约束。",
	},
	"webhook.before_send.on_timeout": {
		EN: "Callback timeout policy: allow or deny; defaults to deny.",
		ZH: "回调超时策略：allow 或 deny；默认 deny。",
	},
	"webhook.before_send.on_error": {
		EN: "Callback error policy: allow or deny; defaults to deny and never overrides explicit rejection.",
		ZH: "调用错误策略：allow 或 deny；默认 deny，不覆盖明确拒绝。",
	},
	"webhook.before_send.max_in_flight": {
		EN: "Per-node concurrency limit; zero uses 256, saturation rejects immediately.",
		ZH: "每节点并发上限；0 使用 256，满载立即拒绝。",
	},
	"plugin.enable": {
		EN: "Enables node-local .wkp plugin processes and PersistAfter hooks; enabled when omitted.",
		ZH: "是否启用本节点的 .wkp 插件进程和 PersistAfter Hook；省略时启用。",
	},
	"plugin.dir": {
		EN: "Directory containing executable .wkp plugin files; when Plugin is enabled, empty derives it from node.data_dir.",
		ZH: "本节点存放可执行 .wkp 插件文件的目录；启用 Plugin 且留空时从 node.data_dir 推导。",
	},
	"plugin.socket_path": {
		EN: "Unix socket used for plugin host RPC; when Plugin is enabled, empty derives it from node.data_dir.",
		ZH: "插件 Host RPC 使用的 Unix Socket 路径；启用 Plugin 且留空时从 node.data_dir 推导。",
	},
	"plugin.sandbox_dir": {
		EN: "Root directory for each plugin's writable sandbox data; when Plugin is enabled, empty derives it from node.data_dir.",
		ZH: "每个插件可写沙箱数据的根目录；启用 Plugin 且留空时从 node.data_dir 推导。",
	},
	"plugin.state_dir": {
		EN: "Directory containing node-local desired plugin state; when Plugin is enabled, empty derives it from node.data_dir.",
		ZH: "保存本节点插件期望状态文件的目录；启用 Plugin 且留空时从 node.data_dir 推导。",
	},
	"plugin.timeout": {
		EN: "Timeout for plugin host RPC and graceful process shutdown; 0 uses 5s.",
		ZH: "插件 Host RPC 与进程优雅停止的超时时间；0 使用默认值 5s。",
	},
	"plugin.hot_reload": {
		EN: "Watches the plugin directory and reloads changed binaries; enabled when omitted.",
		ZH: "是否监视插件目录中的二进制变更并热重载；省略时启用。",
	},
	"plugin.fail_open": {
		EN: "Reserved for future Send hooks; omitted is false, but the current value has no effect and PersistAfter is always fail-open.",
		ZH: "为未来发送 Hook 保留的失败开放开关；省略时为 false，但当前值不影响行为，PersistAfter 始终失败开放。",
	},
	"plugin.persist_after_queue_size": {
		EN: "Maximum PersistAfter events queued in memory; 0 uses 1024.",
		ZH: "内存中最多排队的 PersistAfter 事件数；0 使用默认值 1024。",
	},
	"plugin.persist_after_workers": {
		EN: "Maximum concurrent PersistAfter hook workers; 0 uses 16.",
		ZH: "并发调用 PersistAfter Hook 的 Worker 上限；0 使用默认值 16。",
	},
	"log.level": {
		EN: "Minimum recorded level: debug, info, warn, or error; omitted uses info.",
		ZH: "记录的最低日志级别，可选 debug、info、warn、error；省略时使用 info。",
	},
	"log.dir": {
		EN: "Directory for rolling log files; empty uses ./logs.",
		ZH: "滚动日志文件的保存目录；留空时使用 ./logs。",
	},
	"log.max_size": {
		EN: "Maximum log-file size before rotation, in MB; values at or below 0 use 100.",
		ZH: "单个日志文件触发轮转前的最大大小，单位 MB；小于等于 0 时使用 100。",
	},
	"log.max_age": {
		EN: "Maximum days to retain rotated logs; values at or below 0 use 30.",
		ZH: "轮转日志文件的最长保留天数；小于等于 0 时使用 30。",
	},
	"log.max_backups": {
		EN: "Maximum rotated files retained per log; values at or below 0 use 10.",
		ZH: "每类日志最多保留的轮转文件数；小于等于 0 时使用 10。",
	},
	"log.compress": {
		EN: "Compresses rotated log files with gzip; enabled when omitted.",
		ZH: "是否使用 gzip 压缩轮转后的日志文件；省略时启用。",
	},
	"log.console": {
		EN: "Adds a standard-output log sink; enabled when omitted.",
		ZH: "是否额外把日志输出到标准输出；省略时启用。",
	},
	"log.format": {
		EN: "File encoder format; json emits structured JSON, while other values use console encoding. Omitted uses console.",
		ZH: "日志文件编码格式；json 输出结构化 JSON，其他值使用 Console 编码，省略时为 console。",
	},
}
