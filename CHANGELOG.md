# Changelog

WuKongIM release notes are maintained here. User-visible pull requests add
their entries under `Unreleased`; before a tag is created, release maintainers
move those entries into a version section named for that exact tag.

## [Unreleased]

### 🔧 Improvements / 改进

- Add text-message editing to the embedded Demo for sent messages in direct and group chats, with conflict/draft handling and live message/preview updates. / 内嵌 Demo 支持编辑本人已发送的单聊、群聊文本，处理冲突与草稿，并实时更新正文及会话摘要。

### 🐛 Bug Fixes / 问题修复

- Preserve a bounded rolling execution trace and aligned one-second counters from the original mixed SEND qualification when a latency cohort breaches 400 ms. / mixed SEND 原始验证出现延迟超标时，保留有限滚动执行 trace 与对齐的逐秒计数器。

- Preserve cancellation during benchmark metrics/profile response reads so normal workload shutdown can retain terminal evidence. / 保留压测指标及 profile 响应读取期间的取消信号，避免正常停止丢失终止证据。

- Retain an independent hot SENDACK latency profile with bounded I/O context when an earlier throughput alarm has already collected profiles. / 早期吞吐告警已采集 profile 后，仍为热点 SENDACK 延迟超标保留一次独立采集及有限 I/O 上下文。

- Retain failed 500-QPS warmup accounting and bounded failure categories, including mixed SEND pressure counters. / 保留 500 QPS 预热失败计数、有限错误分类及 mixed SEND 压力证据。

- Reuse 500-QPS performance results for documentation/Demo-only changes only after three matching clean main qualifications; unavailable or failed evidence keeps fresh checks mandatory. / 纯文档或 Demo 改动仅在三次 main 基线验证成功且性能输入一致时复用结果；证据缺失或失败时仍执行完整检查。

- Prevent large-group delivery from blocking unrelated Channels that previously shared a worker hash shard, while preserving per-Channel FIFO and bounded queues. / 避免大群投递阻塞原先哈希到同一 worker 的无关频道，保留频道内顺序与队列边界。

- Run messaging correctness independently of 500-QPS performance gates, and include arrival queueing, underload and dropped requests in sustained performance results. / 消息正确性与 500 QPS 性能门禁独立执行，持续压测结果包含请求排队、发压不足和丢弃。
- Preserve confirmed chat-lifecycle failures that terminate measurement early instead of obscuring them as incomplete-duration evidence. / 聊天生命周期测试提前终止时保留已证实的失败原因，避免被测量时长不足掩盖。

## [v3.0.0-beta.21] - 2026-09-19

### 🔧 Improvements / 改进

- Reuse unchanged durable retention state in bounded Channel storage caches, with invalidation around retention changes, truncation and restore/import. / Channel 有界存储缓存复用未变化的持久化保留状态，在保留变更、截断及恢复导入前后失效，减少会话查询的重复读取。

- Retain bounded host and per-second arrival evidence in the original conversation release gate, including rejected windows, without changing its load or acceptance policy. / 会话发布门禁保留原始运行中有界的主机与逐秒请求证据（包括失败窗口），不改变负载和验收标准。

- Overlap up to eight independent Slot message-edit read groups per query, retaining fresh quorum/apply barriers, aligned pages, cancellation and byte bounds. / 单次查询最多并行读取八组独立 Slot 消息编辑数据，保留新的多数派与应用屏障、分页对齐、取消和字节上限。


### 🐛 Bug Fixes / 问题修复

- Include the Controller restart recovery prepared in beta.19–20: verify legacy pruned WAL prefixes against node identity, snapshots, metadata and the complete committed suffix, then restore a newer snapshot before replay when materialized state lags. Real corruption remains fatal. / 包含 beta.19–20 中准备的 Controller 重启恢复修复：核验旧 WAL 清理前缀后的节点身份、快照、元数据及完整提交后缀；状态文件落后时先恢复较新的快照再重放。真实损坏仍拒绝启动。

### ⬆️ Upgrade Notes / 升级说明

- beta.17–20 did not complete publication. This release includes their message-edit functionality, query improvements and Controller recovery changes. Deploy matching server versions and follow the SDK edit/restore merge contract described below. / beta.17–20 未完成发布；本版本包含这些版本准备的消息编辑功能、查询优化和 Controller 恢复修复。各节点须部署一致版本，并遵循下文的 SDK 编辑与恢复合并协议。

- Back up every node's complete data directory before upgrading. Validated legacy WAL recovery is automatic; do not delete WAL or edit checksums. Once a new-format segment is written, rollback requires restoring the pre-upgrade data backup as well as the old image. / 升级前备份各节点完整数据目录。通过核验的旧 WAL 会自动恢复，无需删除日志或修改校验和。写入新格式日志段后，回退必须同时恢复升级前数据备份和旧镜像。

## [v3.0.0-beta.20] - 2026-09-19

### 🔧 Improvements / 改进

- Reuse bounded transport-observer state and delivery buffers, reducing query-time metrics overhead while preserving latest-state revisions and shutdown draining. / 运输层指标复用有界状态与投递缓冲区，降低查询期间的指标开销，同时保留最新状态版本及关闭排空语义。

- Retain bounded per-second timing and slow-arrival evidence in the fixed mixed-query diagnostic, without changing release workloads or thresholds. / 固定混合查询诊断保留有界的逐秒耗时与慢请求调度证据，不改变发布负载或门槛。

### 🐛 Bug Fixes / 问题修复

- Include the Controller WAL restart recovery introduced in beta.19: recover the legacy pruned-prefix CRC defect only after verifying snapshots, metadata, node identity and the complete committed suffix. Corrupted records still fail closed; new WAL segments require an upgraded runtime. / 包含 beta.19 的 Controller WAL 重启恢复修复：仅在快照、元数据、节点身份及完整提交后缀验证通过后恢复旧格式前缀清理造成的 CRC 断链；真实损坏仍拒绝启动，新日志段须由升级后的程序读取。

- Restore a newer Controller snapshot before committed-log replay when the materialized state file lags compaction, preserving restart recovery after readiness probes. / 状态文件落后于快照时，先恢复较新的 Controller 快照再重放提交日志，保证就绪探测与日志清理后的重启恢复。

## [v3.0.0-beta.19] - 2026-09-19

### 🔧 Improvements / 改进

- Avoid repeated sparse-index reads when a channel has no SyncOnce records, with bounded ownership, write/import invalidation and closed-storage checks. / 频道没有 SyncOnce 记录时避免重复读取稀疏索引，保留有界存储、写入与导入失效及存储关闭检查。

- Reuse owned message-page storage during history and conversation sync, reducing copying and allocations while preserving custom-reader isolation and bounded pagination. / 历史与会话同步复用已独立持有的消息页存储，减少复制及内存分配，保留自定义读取器隔离与有界分页。

- Avoid copying replica lists when query metadata batches only need a Slot number, preserving foreground lifecycle and observed-Leader checks. / 查询元数据批次仅需 Slot 编号时不再复制副本列表，保留前台生命周期与已观测 Leader 校验。

- Share one pinned database view within each bounded Slot message-update read batch, reducing per-channel snapshot work while preserving fresh read barriers and byte limits. / 每个有界 Slot 消息更新读取批次共用一个数据库快照，减少逐频道快照开销，保留新鲜读取屏障与字节上限。

- Read conversation lifecycle and runtime routing metadata together at the Slot owner, reducing duplicate query RPCs while keeping remote Leader validation and read deadlines. / 会话查询在 Slot 所属节点合并读取频道状态与运行路由元数据，减少重复 RPC，保留远端 Leader 校验与读取时限。

- Read only the disk frontier for persisted conversation previews and transfer already-owned message pages through local/remote Channel reads, avoiding unused checkpoint reads and duplicate page copies. / 持久化会话预览仅读取落盘尾序号，本地与跨节点频道读取直接转交已有独立所有权的消息页，减少无用检查点读取及重复复制。

- Reuse bounded decoded runtime metadata during conversation queries, with mutation/restore generation fences and independently owned replica lists. / 会话查询复用有容量限制的运行元数据解码结果，写入与恢复通过版本失效保证新鲜度，副本列表保持调用方独立所有权。

- Transfer independently decoded message payloads through conversation/history read adapters, avoiding redundant copies while preserving caller ownership after storage closure. / 会话与历史读取直接转交独立解码的消息正文，减少重复复制，保持存储关闭后的调用方所有权。

- Expose bounded read-stage timings for conversation list/sync responses, head hydration, and message-edit barrier/storage reads. / 增加会话列表与同步响应、摘要补齐、消息修改屏障及存储读取的固定维度耗时指标。

- Read the first surviving sparse ordinal directly for zero-floor conversation counts, avoiding a redundant predecessor seek while preserving retained-history and corruption checks. / 会话计数下界为零时直接读取首条存活稀疏索引，减少一次无效前驱查找，保留历史清理基线与损坏检查。

- Construct Channel message storage keys in one owned allocation, reducing repeated allocation during conversation preview reads while preserving the existing on-disk encoding. / 频道消息存储键使用一次独立分配构造，减少会话预览读取中的重复分配，保持原有存储编码兼容。

- Transfer retired Channel storage warm state without an extra large-struct allocation or interface copies, reducing persisted conversation-read lease churn while preserving independent leases and cache bounds. / 频道存储暖状态回收后直接转移，避免额外大结构分配和接口值复制，减少持久化会话读取的租约开销，同时保留独立租约与缓存容量限制。

### 🐛 Bug Fixes / 问题修复

- Restore a newer Controller snapshot before replaying the committed suffix when the materialized state file lags compaction, avoiding restart failures after readiness probes. / 状态文件落后于快照时先恢复较新的 Controller 快照再重放提交后缀，避免就绪探测与日志清理后重启失败。

- Keep Controller WAL recoverable after snapshot cleanup and restart. Upgrades recover the legacy pruned-prefix CRC defect only after snapshot, metadata, node-identity and complete committed-log verification; corrupted records still fail closed. Newly rotated WAL segments use independent checksums and require this version or newer to reopen. / 修复 Controller WAL 快照清理后重启报 CRC 错误；升级时仅在快照、元数据、节点身份及完整提交日志验证通过后兼容恢复旧格式断链，真实损坏仍拒绝启动。新轮转日志段采用独立校验，写入后须使用本版本或更新版本启动。

- Refresh Controller state on watch notifications and reject older snapshots after newer state is applied, avoiding obsolete startup task replay and routing/maintenance rollback during concurrent readiness probes. / Controller 通知触发当前状态读取，已应用较新状态后拒绝旧快照，避免重复执行过时启动任务，以及并发就绪探测导致的路由与维护状态回退。

- Preserve temporary dependency failures across Channel read RPC so connection loss during failover returns retryable unavailability for ordinary history, exact lookup and conversation queries. / 频道读取 RPC 保留临时依赖故障类型，故障切换中的连接中断在普通历史、精确查询和会话查询中返回可重试的不可用状态。

- Capture bounded system, scheduler/GC, physical commit and sampled replication counters in the first PR append gate, preserving evidence when it fails before mixed SEND starts. / 前置 PR append 门禁保留有界系统、调度/GC、物理提交及采样复制计数，覆盖混合 SEND 开始前即失败的窗口。

- Retain bounded CPU, disk, scheduler/GC, storage and replication counter snapshots from the original PR mixed SEND gate on success or failure, without profiling or changing its 400 ms limit. / PR 混合 SEND 原始门禁无论成功或失败均保留有界的 CPU、磁盘、调度/GC、存储和复制计数快照，不启用 profile，也不改变 400 ms 门槛。

- Add an opt-in fixed-host Linux SEND diagnostic that retains the original 400 ms verdict and collects bounded CPU, execution-trace, storage and replication evidence during measured traffic. / 增加可手动启用的固定 Linux 主机 SEND 诊断，保留原始 400 ms 判定，并采集正式压测期间有界的 CPU、执行 trace、存储和复制证据。

- Exclude setup and completed warmup from mixed SEND benchmark stage statistics, report measured sample counts, and preserve an unbounded histogram tail instead of reporting a false finite P99 upper bound. / 混合 SEND 基准的分段统计排除初始化和已完成的预热，显示正式窗口样本数，并避免将超出直方图范围的长尾误报为有限的 P99 上界。

- Decode all registered Slot commands in Raft log views, including read progress, ordinary/CMD memberships, channel updates, latest-message metadata, and directory/migration tasks; omit message bodies and distinguish unsupported inspection or versions from corrupt log data. / 槽位 Raft 日志支持展示全部已注册命令，涵盖已读进度、普通/CMD 成员关系、频道更新、最新消息元数据及目录/迁移任务；隐藏消息正文，并区分暂不支持展示或版本与日志数据损坏。

### ⬆️ Upgrade Notes / 升级说明

- Back up each node’s complete data directory before upgrading. Validated legacy Controller WAL prefixes are admitted automatically and recorded durably; no WAL deletion or manual checksum edits are needed. After this version writes a new WAL segment, use this version or newer; rollback requires restoring the pre-upgrade backup. / 升级前备份各节点完整数据目录。旧版 Controller WAL 前缀通过完整验证后自动恢复并持久化验证结果，无需删除 WAL 或手工修改校验和。新版本写入新日志段后须使用本版本或更新版本；回退须恢复升级前备份。

## [v3.0.0-beta.18] - 2026-09-15

### 🔧 Improvements / 改进

- Encode batched conversation heads and recent-message responses in one pre-sized buffer, reducing allocation and copying while preserving all supported wire formats and read consistency. / 会话摘要和最近消息的批量响应使用预分配缓冲区编码，减少内存分配与复制，保持协议兼容和读取一致性。

- Reduce runtime metadata decoding allocations by canonicalizing owned replica sets and decoding directly from verified row bytes; preserve corruption checks and caller-owned inputs. / 运行时元数据直接从已校验行解码并原地规范化自有副本集合，减少分配，同时保留损坏校验与调用方数据所有权。

### ⬆️ Upgrade Notes / 升级说明

- Includes the ordinary-message editing and EVENT integration changes prepared in beta.17, whose artifact publication was blocked by the release performance gate. Deploy matching server binaries and adopt the SDK edit/restore merge contract; CMD and stream messages remain non-editable. / 包含 beta.17 中准备的普通消息编辑与 EVENT 接入功能；beta.17 的产物发布被性能门禁阻断。请部署版本一致的服务端，并使用遵循编辑与恢复合并协议的 SDK；CMD、流消息仍不可编辑。

## [v3.0.0-beta.17] - 2026-09-15

### 🚀 New Features / 新功能

- Add ordinary durable-message payload editing with version/restore-epoch checks, idempotent retries, single-channel incremental sync, and opt-in online EVENT hints. History, exact lookup, `/conversation/list`, and `/conversation/sync` read the latest content without changing message order or unread counts; CMD and stream messages remain non-editable. New metadata requires matching server binaries; SDKs must adopt the edit/restore merge contract. / 新增普通持久化消息修改、版本与恢复代数校验、幂等重试、单频道增量同步及按连接协商的 EVENT 提示；历史、精确查询和两个会话接口均返回最新内容，不改变消息顺序或未读。CMD、流消息不允许修改；新元数据要求节点版本一致，SDK 需适配编辑与恢复合并协议。

### 🔧 Improvements / 改进

- Batch message-edit recipient Presence lookups by authoritative node and overlap up to four independent notifications during ready dispatch and durable repair, reducing RPC amplification and backlog while retaining durable progress, bounded queues, and recovery scans. / 消息编辑接收人的在线状态按权威节点批量查询，最多并行派发四条独立通知（覆盖就绪派发及持久化补偿），减少 RPC 放大与积压，保留持久化进度、有界队列和恢复扫描。

- Wake the existing message-edit notification worker after a successful commit through a bounded, coalescing identity queue, reducing cold-channel hint delay while retaining durable recovery scans and cluster-authoritative delivery. / 消息编辑提交成功后通过有界身份队列唤醒现有通知 worker，合并重复任务，缩短冷频道提示等待，同时保留持久化补偿扫描与集群权威投递。

- Coalesce concurrent readiness write probes and reuse successful proof for up to two seconds, reducing Slot Raft noop entries while retaining live Slot, placement and authority checks. / 合并并发就绪写入探针，并在两秒内复用成功证明，减少 Slot Raft 空操作条目，同时保留实时槽位、放置能力与路由权威检查。

- Turn the bilingual v2-to-v3 migration guide into a practical single-node cluster walkthrough, with advanced procedures in a separate reference. / 将中英文 v2 → v3 迁移教程改为单节点集群实战步骤，多节点、插件与异常处理独立为参考页。

### 🐛 Bug Fixes / 问题修复

- Add Grafana panels for persisted conversation-read admission, in-flight batches and shared limit, sampled occupancy, slot hold time, and batch size, restoring coverage of all six exported metrics. / Grafana 补齐会话持久化读取的准入速率、在途批次与共享上限、采样占用、执行占用时长和批次大小面板，恢复六项已导出指标的覆盖。

- Retry a message-edit notification once when its ready-dispatch wave budget expires, avoiding unnecessary cold-slot rediscovery delays while preserving bounded retries and durable recovery. / 消息编辑通知因就绪派发整轮预算耗尽而失败时保留一次有界重试，减少重新扫描冷槽位造成的额外等待，并保留持久化恢复。

- Preserve device identity when routing message-edit EVENT hints so opted-in real clients pass the final session fence and receive updates. / 消息修改 EVENT 提示路由保留完整设备身份，修复真实客户端已协商能力却因会话校验而收不到提示的问题。

- Return HTTP 503 with `code: unavailable` for temporary failures in ordinary history, exact message lookup and both conversation read APIs, preserving legacy error fields so clients can retry the same cursor without parsing error text. / 普通历史、消息精确查询和两个会话查询接口的临时故障统一返回 HTTP 503 与 `code: unavailable`，保留旧错误字段，客户端无需解析错误文本即可重试原游标。

- Inject the release version and commit into Docker server and CLI binaries so Manager node versions report the image release instead of `dev`. / Docker 服务端与 CLI 注入发布版本和提交信息，修复 Manager 节点版本始终显示 `dev` 的问题。

## [v3.0.0-beta.16] - 2026-09-13

### ⚠️ Breaking Changes / 破坏性变更

- Read `/conversation/sync` heads and recent messages from current-Leader persisted storage without activating Channel runtimes; retain legacy fields and cursor rules, and fail the entire request on read errors or response-budget exhaustion. / `/conversation/sync` 的摘要和最近消息改为读取当前 Leader 已落盘数据，不激活频道运行时；保留旧版字段和游标规则，读取失败或响应预算超限时整次请求失败。

- Read conversation-list previews from current-Leader persisted messages without activating Channel runtimes; failed SENDs may still appear. Any read error fails the whole page. Remove `/conversation/retry` and `unresolved`; retry the original list request and cursor. / 会话列表直接读取当前 Leader 已落盘消息，不激活频道运行时，发送失败的消息也可显示；任一读取错误则整页失败。删除 `/conversation/retry` 和 `unresolved`，失败后用原请求、原游标重试。

### 🔧 Improvements / 改进

- Avoid intermediate storage-decoding copies while preserving checksum validation and independently owned decoded messages and metadata. / 存储解码减少中间复制，保留校验和验证及消息、元数据结果的独立所有权。

- Reduce conversation batch allocations by sharing request-scoped authoritative metadata across local read descriptors; preserve wire formats and Leader fences. / 会话批量读取共享本次请求解析出的权威元数据，减少对象分配，保持通信格式及 Leader 校验。

- Add a bounded Linux AMD64 diagnostic workflow for mixed conversation reads, retaining host counters and separate driver/server profiles without changing release gates. / 新增 Linux AMD64 会话混合读取诊断工作流，保留主机指标及独立压测端、服务端剖析，不改变发布门禁。

- Extend release QPS gates with simultaneous conversation list/sync traffic and exact hidden-page checks. / 发布 QPS 门禁增加会话 list/sync 混合负载及隐藏会话准确分页校验。

- Sort bounded legacy membership metadata before limiting `/conversation/sync` preview reads to the requested page prefix; preserve variable-length Channel ID ordering, visibility and post-page filters. / `/conversation/sync` 先排序有界成员元数据，再按目标页读取摘要；保留不同长度频道 ID 的排序、可见性及分页后过滤规则。

- Reduce conversation preview read overhead by reading an exact persisted tail record directly, preserving missing-sequence fallback and read failures. / 会话预览按准确落盘序号直接读取尾消息，减少范围扫描开销，保留序号缺失时的回溯和读取失败语义。

- Expose bounded persisted-conversation read admission, occupancy and hold-time metrics to distinguish backpressure from routing latency. / 增加会话落盘读取准入、占用量和持有时长指标，用于区分读取背压与路由延迟。

- Reduce conversation unread-count CPU and allocations by sharing one bounded persisted-index iterator across both range boundaries. / 会话未读计数的两个范围边界共用一次有界落盘索引迭代，减少 CPU 和内存分配。

- Batch `/conversation/sync` membership and channel-state reads through current Slot leaders to reduce cross-node RPCs while preserving visibility and whole-request failures. / `/conversation/sync` 通过当前 Slot Leader 批量读取成员关系和频道状态，减少跨节点 RPC，保持可见性规则及整次请求失败语义。

- Add opt-in sustained conversation QPS diagnosis with per-node CPU/RPC metrics and separate execution traces. / 新增会话接口持续负载诊断，采集各节点 CPU、RPC 指标及独立执行跟踪。
- Reuse immutable message-storage keys within bounded caches and remove redundant legacy-sync payload copies to reduce conversation read allocations. / 有界复用消息存储 Key，并去除旧版会话同步的重复 Payload 复制，降低会话读取的内存分配。
- Reduce conversation preview allocations by reading only the newest record on ordinary tails while retaining bounded scans over internal records. / 会话预览优先只读取最新一条记录，减少无用消息解码和内存分配，内部消息仍采用有界回溯。
- Gate Docker and binary releases on fixed `/conversation/list` and `/conversation/sync` QPS/P99 and per-request allocation tests in single-node and three-node clusters, including complete responses and zero Channel activation. / Docker 与二进制发布新增会话接口 QPS/P99 与每请求分配量门禁，覆盖单节点和三节点集群，并验证响应完整及零频道激活。

- Auto-detect direct clients and PROXY protocol v1/v2 on TCP and WebSocket Gateway listeners by default, with bounded preface parsing and timeout. Optional trusted CIDRs restrict header sources; omitted or empty accepts unverified client address assertions from any peer. / Gateway TCP 和 WebSocket 默认自动识别直连与 PROXY v1/v2，并限制协议头大小与等待时间；可选可信 CIDR 限制协议头来源，省略或为空时接受任意对端声明的未验证客户端地址。

- Show the configured `guest` username and password on the Manager login page. / Manager 登录页在配置 guest 账号时展示其账号和密码。

- Clarify that Manager message deletion includes the selected message and all earlier messages in the same channel, with explicit bulk deletion confirmation. / 管理台消息删除明确标注“此条及更早消息”，并在批量删除确认中说明同频道、包含未展示历史消息的影响范围。

- Sample rejected WebSocket handshakes as informational diagnostics instead of listener-error stack traces; genuine listener faults remain errors. / WebSocket 握手拒绝改为限频 INFO 诊断，避免扫描请求刷出 ERROR 堆栈，真实监听器故障仍完整记录。

- Exclude normal Slot scheduling coalescing and rescheduling from Manager runtime admission errors; show observed zero errors as normal while preserving missing-data status. / Manager 运行时准入错误排除正常 Slot 调度合并及重新调度，有采集且错误为零时显示正常，无数据仍保持未知。

### 🐛 Bug Fixes / 问题修复

- Track plugin batch channel-owner initialization in the goroutine supervisor while preserving its 16-task concurrency bound and error cancellation. / 插件批量频道归属初始化接入 goroutine 统一托管，保留 16 并发上限和出错取消行为。

## [v3.0.0-beta.14] - 2026-09-12

### 🚀 New Features / 新功能

- Add bounded batch CMD discovery binding for stable source channels and exact request-scoped recipients, sharing one committed start boundary across the batch. / CMD 发现绑定支持有上限的源频道成员批次和精确临时接收者范围，一批共用已提交起始边界。

- Expose `cluster.start_timeout` / `WK_CLUSTER_START_TIMEOUT` for measured cold-recovery readiness budgets, retaining the 30s default and all write, quorum, and placement checks. / 支持配置冷恢复的启动就绪等待期限，默认仍为 30 秒，保留写入、多数派和放置检查。

- Support reviewed transitive duplicate resolution, archive independent v2 unread counters, and preserve absent conversation lists without deleting history; hidden memberships require matching cluster binaries. / 支持获准的去重替代链解析、原独立未读计数归档，以及保留历史访问权限的会话列表隐藏；隐藏成员状态要求集群使用配套程序。

- Support explicitly approved preservation of conversations beyond the original v2 list cap and exact, hash-bound recovery of conflicting conversation states from their original unique indexes. / 支持明确批准后保留原 v2 列表上限之外的会话，并按原行哈希绑定的唯一索引记录恢复冲突会话状态。

- Add exact-row, operator-approved v2 quarantine with immutable original archives, dependent-index proofs, and independently rebuilt omitted-position mappings. / 增加按原记录精确授权的 v2 异常隔离，保留完整原始归档，验证关联索引并独立重建被排除位置的映射。

- Support offline mapping of the exact original search plugin with verified history-rebuild seeds on every target; document the required runtime catch-up upgrade for leader changes. / 支持原搜索插件的精确离线映射，为全部目标生成经过验证的历史索引重建种子，并明确 Leader 变化所需的运行时增量补齐升级。

- Add read-only migration access for Pebble format 19 while preserving legacy source support, source locks, and business compatibility checks; document development-build and plugin checks before downtime. / 迁移读取器增加 Pebble 格式 19 只读支持，保留旧格式读取、源文件锁与业务兼容检查，并补充停机前的开发版本和插件核对说明。

### 🐛 Bug Fixes / 问题修复

- Refresh old empty-number conversation previews even when a legacy client cursor has advanced, preserving unread and visibility boundaries. / 旧客户端游标已前移时仍可刷新空编号历史会话摘要，保留未读和可见范围边界。

- Restore legacy previews for historical messages with empty client numbers using stable read-only aliases, preserving stored records and indexed lookup support. / 历史空客户端编号消息返回稳定的只读兼容编号，恢复会话摘要并支持索引查询，原始记录不改写。

- Restore generated client message numbers for HTTP sends that omit the field, and return the number with the send result so legacy clients can resolve conversation previews. / HTTP 发送省略客户端消息编号时恢复自动生成，并随发送结果返回，避免旧客户端会话摘要为空。

- Keep offline CMD synchronization available after a bound source Channel is disbanded; skip only confirmed terminal sources and retain explicit failures for unavailable reads. / 已绑定源频道解散后，离线 CMD 同步继续读取其他频道；仅跳过已确认的终止源，读取不可用仍明确报错。

- Keep CMD synchronization working when an explicitly bound source has not produced its first persistent command; unavailable reads remain errors. / 显式绑定的频道尚未产生首条持久 CMD 时，按空记录处理，避免阻断其他命令同步；读取不可用仍返回错误。

- Restore legacy `POST /messages` exact lookup by message ID, sequence, and client message number, using committed indexes with membership and retention checks.

- Fix group CMD delivery to use current source-group subscribers for persistent and transient commands, including after membership changes.

- Allow a connected plugin readiness handshake to use the remaining startup budget, avoiding repeated premature timeouts when the node is CPU throttled. / 插件就绪握手使用剩余启动等待期限，避免 CPU 限流时反复提前超时导致启动失败。

- Confirm retried sends against current Leader committed history before returning success; a local durable proposal alone no longer produces a successful send acknowledgment. / 重试发送必须由当前 Leader 的已提交历史确认，避免将仅本地落盘的提议误报为发送成功。

- Preserve committed channel migration task chains across Slot replay batch boundaries, preventing older Leader metadata from reappearing after restart. / 修复 Slot 日志重放批次边界导致后续频道迁移任务被误跳过，避免重启后恢复出旧 Leader 状态。

- Preserve the canonical Controller Raft 100ms tick through the runtime facade, avoiding an unintended 200ms election floor during cold recovery. / Controller 统一使用 100ms 默认 tick，避免上层覆盖后将选举等待下限缩短为 200ms，导致冷恢复期间频繁重新选举。

- Refresh older loaded channel authority during committed history and conversation reads, then require fresh quorum recovery before serving messages. / 历史和会话读取遇到旧频道运行状态时重新加载当前权威信息，完成多数派恢复后再返回数据。

- Preserve the failed channel migration task and phase in bounded worker diagnostics, including when a later tick deadline expires. / 频道迁移日志保留失败任务和阶段，避免后续超时覆盖原始错误。

- Keep healthy Slot replay ranges batched when conditional metadata conflicts occur, preserving Raft order and durable stale-entry watermarks while reducing synchronous commits. / Slot 日志回放遇到条件冲突时继续批量提交无冲突区间，保留 Raft 顺序及过期操作的持久化进度，减少同步落盘次数。

- Resolve plugin channel ownership from current Slot metadata after leader changes, preventing requests from repeatedly forwarding to a stopped node. / 频道 Leader 变化后，插件从当前 Slot 元数据解析所属节点，避免请求持续转发到已停止的节点。

- Exclude internal recovery and SyncOnce records from unread badges and set-unread boundaries; keep legacy unread-history pulls and deleted conversation visibility correct after failover. / 未读计数及设置未读边界排除内部恢复与 SyncOnce 记录，修复故障切换后旧客户端未读历史拉取和已删除会话的显示。

- Keep imported hidden conversations absent during cluster recovery; only a newer business message or explicit activation reveals them. / 集群恢复时保持迁入的隐藏会话不可见，仅新业务消息或明确激活后显示。

- Bind reviewed conversation replica choices and archive-only decisions to every original copy, including absence; preserve the full source archive. / 会话副本选择及仅归档决定绑定全部原副本和缺失状态，保留完整源归档。

- Prevent offline migration index joins from exhausting their read cache when write memtables grow. / 修复离线迁移写入内存表增长后耗尽读缓存、导致索引校验反复读取数据块的问题。

- Preserve original message Expire values through v2 migration, native appends, reads, restart, and replica recovery; bind lifetimes in versioned quorum proposals and reject lossy older RPC encodings. / v2 迁移及原生写入、读取、重启和副本恢复保留消息 Expire 原值，以版本化提案校验有效期，并拒绝会丢失该字段的旧 RPC 编码。

- Restore the legacy `/plugins/:plugin_no/*path` Product HTTP route through the existing plugin usecase, with bounded bodies/timeouts and maintenance fencing, so business backends can call search plugins after upgrading. / 恢复经现有插件用例处理的 Product HTTP 插件路由，限制请求体与调用时长并保留维护屏障，使业务后端升级后仍可调用搜索插件。

### 🔧 Improvements / 改进

- Reuse verified duplicate-chain suffixes within each offline migration pass and count terminals on disk, avoiding quadratic history lookups while retaining original edge proofs. / 离线迁移在每次重建内复用已核实的去重链后缀，以磁盘索引统计终点，避免重复遍历，保留完整原始替代证据。

- Overlap bounded authoritative permission checks during batch history and conversation synchronization to reduce reconnect latency for accounts with many conversations. / 会话及历史批量同步以有限并发检查权威权限，降低多会话账号的重连等待。

- Batch offline CMD reads across bound channels to reduce reconnect latency for users in many groups, preserving acknowledgement boundaries and complete-result failure handling. / 离线 CMD 按频道批量读取，降低多群用户重连耗时，保持确认边界和完整结果的错误处理。

- Batch plugin channel-owner lookups through current Slot authority, sharing metadata reads and bounding concurrent cold-channel initialization during global search. / 插件按当前 Slot 权威批量查询频道所属节点，合并元数据读取，并以有上限的并发处理冷频道初始化。

- Avoid per-row key-range allocations during Slot snapshot restore while retaining the same ownership and namespace checks. / Slot 快照恢复时避免逐行重复分配键范围，保持所属 hash slot 和命名空间校验不变。

- Report bounded channel-repair progress, blocked examples, and worker errors instead of silently discarding failed background ticks. / 以限频日志报告频道修复进度、阻塞示例和后台任务错误，避免静默丢弃失败信息。

- Reuse sealed migration preparation during export while rechecking source freshness and all exported bytes; compute invariant lookup-policy digests once per selection. Independent import and verification still rebuild original records. / 导出复用带摘要的迁移准备结果，重新核对来源和全部导出字节；会话选择仅计算一次固定策略摘要。导入和独立验证继续从原记录重建。

- Merge sorted source indexes during migration validation to avoid per-index point lookups, and report preparation stage durations without weakening missing, conflicting, or orphaned-index checks. / 迁移校验采用有序索引归并，减少逐条查询，并输出准备阶段耗时，保留缺失、错指与孤立索引检查。

- Distinguish existing-channel quorum service from new-channel placement readiness during three-node migration fault tests. / 区分三节点迁移故障演练中的已有频道多数派服务与新频道放置就绪条件。

### 📚 Documentation / 文档

- Align static documentation publication checks, navigation counts, and overview tests with the source-backed Product HTTP operation registry, including legacy message lookup. / 文档发布产物检查、导航数量及总览测试以源码对应的 Product HTTP 接口注册表为准，包含旧版消息查询接口。

- Correct migration terminology: 12 physical Slots own the 256 logical hash slots. / 修正迁移教程术语：12 个物理 Slot 承载 256 个逻辑 hash slot。

- Document explicit CMD binding and disconnected-recipient acceptance as prerequisites for v2 migration cutover. / 补充 v2 迁移切换前的 CMD 显式绑定适配和离线接收者验收要求。

- Clarify v2 migration timeout budgeting and safe preparation retries after interruption. / 补充 v2 迁移任务的超时预算与预检中断后的安全重试说明。

- Provide reusable v2 migration steps with an explicit reader baseline, release capability checks, generic plugin guidance and optional search-index rebuilding, host resource budgets, and separately captured stage logs. / v2 迁移教程提供通用步骤，明确来源读取基线与版本能力，补充通用插件指引、可选搜索索引重建和宿主机资源预算，并分别保存阶段日志。

- Clarify three-node Compose migration addresses, independent bind mounts, Slot counts, and validation before container startup. / 明确三节点 Compose 迁移的容器通信地址、独立绑定目录、Slot 数量和启动前校验要求。

- Document v2 migration configuration semantics for person whitelists, TCP PROXY protocol, and business data-source synchronization. / 补充 v2 迁移中单聊白名单、TCP PROXY 协议和业务数据源同步的配置差异。

- Document business-database sequence mapping during v2 migration, including deletion boundaries and message references that client cache resets cannot repair. / 补充 v2 迁移时业务数据库的序号映射要求，涵盖清理客户端缓存无法修复的删除边界与消息引用。

### ⬆️ Upgrade Notes / 升级说明

- Use matching beta.14 server and wkcli binaries on every target node for the new expiration, hidden-conversation, and indexed-RPC formats. Keep a complete cold backup and rehearse v2 migration before switching; a source-build verification does not replace validation of your own data. / 新的有效期、隐藏会话和索引 RPC 格式要求各目标节点使用配套的 beta.14 服务端与 wkcli。切换前保留完整冷备并演练 v2 迁移；源码构建验证不能替代对自身数据的校验。

## [v3.0.0-beta.13] - 2026-09-09

### 🔧 Improvements / 改进

- Add immutable DATA-FORMAT.json identity to fresh v3 node directories and migration outputs, reject unsupported formats before startup, and expose read-only `wkcli db info`; existing unmarked directories remain unregistered. / 全新 v3 节点目录和迁移产物增加不可变 DATA-FORMAT.json 版本标识，启动前拒绝不支持的格式，并提供只读 wkcli db info；已有无标识目录保持未登记。

- Allow v2 migration plans to omit `source_commit`, defaulting to the supported reader schema while retaining format checks and legacy plan/archive identity; operators no longer need to identify their deployed binary commit. / v2 迁移计划允许省略 source_commit，默认使用支持的读取规则并保留格式检查及旧计划、归档身份，部署者无需查询旧二进制提交号。

- Require automatic installed `wkcli` identity and offline functional acceptance on all four signed-package client distributions before a new release is complete. / 新版本签名包发布增加四种 Linux 系统的 wkcli 安装身份与离线功能自动验收，全部通过后才算交付完成。

### 📚 Documentation / 文档

- Simplify the bilingual v2 → v3 migration guide into four steps, with explicit multi-server file collection and distribution and expandable troubleshooting and data policies; use 12 logical Slots and explain source revision compatibility. / 将中英文 v2 → v3 迁移指南精简为四步，明确多服务器冷备汇总与目标分发，排障和数据策略按需展开，计划示例采用 12 个逻辑 Slot 并说明来源提交字段的兼容写法。

- Automatically check compatible Web EasySDK npm releases, verify the bilingual tutorial in Chromium, and propose reviewed documentation upgrades. / 自动检查兼容的 Web EasySDK npm 新版本，在 Chromium 验证中英文教程后创建文档升级 PR，交由维护者审核。

- Keep EasySDK tutorial versions and installation pins in one shared manifest so compatible upgrades synchronize bilingual instructions and download links. / EasySDK 教程版本及安装引用集中到统一清单，兼容升级可同步中英文安装说明和下载链接。

- Document released wkcli installation, paired server/CLI version checks, and installed-command examples in both languages; complete the migration tool navigation. / 补齐中英文 wkcli 发布版安装、服务端与工具版本核对、已安装命令示例及迁移工具导航。

### 🐛 Bug Fixes / 问题修复

- Fix dual-architecture Docker release verification and recovery by pulling each platform through its immutable child manifest. / 修复 Docker 双架构发布校验与恢复流程，按不可变子清单分别拉取各架构镜像。

## [v3.0.0-beta.12] - 2026-09-09

### 🔧 Improvements / 改进

- Consolidate operator tools into `wkcli bench`, `wkcli db`, and `wkcli migrate`; remove the separate `wkbench`, `wkdb`, and `wkmigrate` executables, migrate repository callers, and include version-matched `wkcli` in binary archives and native packages. External scripts must adopt the new subcommands. / 运维工具统一为 `wkcli bench/db/migrate`，移除旧独立程序并迁移仓库调用；二进制归档与原生安装包包含同版本 `wkcli`，外部脚本需切换新子命令。

- Align the complete Product HTTP reference with all 42 runtime routes, including event projection sync and the native send `header.red_dot` flag. / 将完整 Product HTTP 文档对齐全部 42 个运行时接口，补齐事件投影同步和发送请求的原生 `header.red_dot` 字段。

- Add a bilingual v2 → v3 offline migration guide covering plans, data policies, independent verification, client cutover, and rollback boundaries. / 新增中英文 v2 → v3 离线迁移指南，覆盖迁移计划、数据处理、独立校验、客户端切换与回退边界。

- Add an isolated Docker rehearsal for a delivered v2 migration package, with fresh targets, archive-only import/verification, exact import retry and bounded resource guards. / 增加迁移交付包的隔离 Docker 演练脚本，覆盖空目标、仅依赖归档的导入与校验、重复导入和资源保护。

- Restructure all eight EasySDK quickstarts and examples in Chinese and English around first-message integration, move historical validation into engineering reference, and pin Web installation to 2.0.5. / 重整八个平台的中英文 EasySDK 入门与示例，统一首次收发流程，将历史验证移入工程文档，并将 Web 安装版本更新为 2.0.5。

- Use current health, retain fair Channel repair scan progress, and activate authoritative cold replicas from consistent durable frontiers for migration probes after node loss; allow fenced leader recovery while keeping business writes closed. / 频道修复使用最新健康状态、保留公平扫描进度，并按权威元数据和一致持久化状态加载冷副本；写入栅栏内可完成新 leader 恢复，业务写入仍保持关闭。

- Document C# EasySDK application endpoint replacement and optional three-node reproduction, with server blockers tracked separately. / 补充 C# EasySDK 应用侧地址切换与三节点手动复现说明，单独记录服务端阻塞。

- Document Python EasySDK group examples and installed-package membership/permission acceptance, including the required server cache fix. / 补充 Python EasySDK 群聊示例、正式包成员与权限验收，以及所需的服务端缓存修复说明。

- Document Rust EasySDK released-package three-node messaging, permissions and same-endpoint ingress crash recovery acceptance. / 补充 Rust EasySDK 正式包三节点通信、权限与接入节点崩溃后原地址重连验收。

- Document Rust EasySDK weak-network and resource-boundary acceptance, including SEND ambiguity, backpressure, observer lag and repeated cleanup. / 补充 Rust EasySDK 弱网与资源边界验收，覆盖发送结果未知、背压、监听器落后和反复清理。

- Document C++ EasySDK released-package three-node WSS recovery acceptance and clarify uncertain SEND outcomes and same-endpoint reconnect behavior. / 补充 C++ EasySDK 正式包三节点 WSS 恢复验收，明确 SEND 结果不确定性与原地址重连边界。

- Make Manager JavaScript preload ordering reproducible while preserving stylesheet order, so rebuild verification is stable. / 固定 Manager JavaScript 预加载顺序并保留样式顺序，避免重建校验因依赖遍历顺序变化失败。

- Document independent Python EasySDK three-node WSS fault recovery and bounded stability acceptance. / 补充 Python EasySDK 三节点 WSS 故障恢复与限时稳定性验收说明。

- Document Rust EasySDK group messaging and released-package checks for member fanout, permissions, membership changes and reconnect. / 补充 Rust EasySDK 群聊用法及正式包成员投递、权限、成员变更与重连验收。

- Clarify the pending C++ EasySDK submission to the default vcpkg catalog and retain the working custom-registry setup in both languages. / 补充 C++ EasySDK 申请进入 vcpkg 默认目录的待收录状态，中英文接入说明继续保留可用的自定义 registry 配置。

- Document WuKongEasySDK-Python 0.1.0 PyPI installation, matching examples, and published-package verification. / 更新 WuKongEasySDK-Python 0.1.0 的 PyPI 安装、配套示例与正式包验证说明。

- Document Rust EasySDK 0.1.0 public-package WSS recovery acceptance and stabilize the ready-key scheduler regression test with deterministic synchronization. / 补充 Rust EasySDK 0.1.0 正式包 WSS 恢复验收，并使用确定性同步稳定调度器就绪 Key 回归测试。

- Publish Rust EasySDK 0.1.0 installation instructions for crates.io, with bilingual examples and separately identified package and WSS recovery verification. / 更新 Rust EasySDK 0.1.0 的 crates.io 正式包安装文档，提供双语示例并分别记录包校验与 WSS 恢复验收。

- Increase the initial logical Slot count in `wukongim init` and shipped single-node/three-node cluster configurations from 10 to 12. Hash slots remain 256; existing clusters retain their persisted Slot count. / `wukongim init` 及随仓库提供的单节点、三节点集群配置将初始逻辑 Slot 数从 10 调整为 12；Hash Slot 保持 256，已有集群继续使用持久化的 Slot 数。

- Improve Manager node-log troubleshooting with explicit keyword search, a navigable details drawer and copy feedback, reliable retry, and live scrolling that preserves the reading position. / 优化管理台节点日志排查：关键字提交查询、可切换事件的详情抽屉与复制反馈、可靠重试，以及保护阅读位置的实时跟随。

### 🐛 Bug Fixes / 问题修复

- Update the embedded Prometheus gRPC dependency to v1.83.2 to address CVE-2026-84445. / 将内嵌 Prometheus 的 gRPC 依赖升级至 v1.83.2，修复 CVE-2026-84445。

- Fix release archive validation to include the unified `wkcli` executable, and use the official Go module proxy in hosted image builds. / 修复发布归档校验遗漏统一 `wkcli` 的问题，并在托管镜像构建中使用官方 Go 模块代理。

- Fix Channel follower replacement stalling before the spare joins ISR: copy the committed log in bounded pages, refresh durable follower progress, and keep temporary catch-up lag retryable without reducing quorum. / 修复 Channel 副本替换在备用节点加入 ISR 前停滞的问题：分页同步已提交日志、刷新持久化进度，并保留暂时落后任务的重试能力，不降低仲裁要求。
- Migration verification accepts valid empty-sender messages while checking their existing exact client index; history continuation reads respect the remaining page demand. Recovery rechecks all observed tails, and timed-out migration tasks yield without skipping peers.

- Reconstruct online routes from current gateway owners after Slot authority changes, preventing an empty rebuilding directory from skipping acknowledged messages. / Slot 权威切换后按连接所属节点重建在线路由，避免空目录将已确认消息的在线接收者误判为离线。
- Recover consecutive Channel failures by proving compatible durable tails, copying missing immutable entries, and rechecking quorum without truncating acknowledged messages; rotate migration tasks so one waiting recovery cannot starve others. / 连续节点故障时验证日志前缀、补齐缺失记录并重新确认 quorum，保留已确认消息；轮转迁移任务，避免单个恢复任务阻塞其他频道。
- Synchronize benchmark RECVACK counter assertions with completed accounting under race instrumentation. / 修复 race 模式下 RECVACK 计数测试过早断言的问题。

- Restore online delivery for ordinary `no_persist=1, sync_once=0` HTTP and WKProto sends, returning a transient message ID with sequence zero and preserving the receive flag without writing message history. / 修复普通频道 `no_persist=1, sync_once=0` 的 HTTP 与 WKProto 消息成功返回却不在线投递的问题；分配瞬时消息 ID、保持序号为零并保留接收标志，不写入消息历史。

- Refresh group recipient versions from the current Slot leader before routed sends so membership changes through another ingress exclude removed users and include new members. / 群消息发送前从当前 Slot Leader 读取成员版本，修复跨入口变更成员后被移除用户仍收消息、新成员漏收的问题。

- Bundle Prometheus binaries built from pinned source with dependency security fixes in Linux amd64/arm64 Docker images so enabling the managed Prometheus process starts successfully and persists metrics on the existing data volume. / Docker 镜像为 Linux amd64/arm64 内嵌从固定源码构建并修复依赖漏洞的 Prometheus 二进制，修复开启内置进程后启动失败、容器反复重启的问题，指标随现有数据卷持久化。

- Preserve queued WebSocket payloads across subsequent reads, preventing corrupted JSON-RPC messages and unexpected client disconnections. / 修复 WebSocket 入队数据被后续读取覆盖导致的 JSON-RPC 消息损坏与客户端异常断开。

- WebSocket handshake rejection logs now include peer addresses, HTTP status, and bounded requested/expected paths for path mismatches without URL query parameters. Manager parses console log fields and shows the error and listener without expanding details. / 完善 WebSocket 握手拒绝日志，补充来源地址、HTTP 状态和路径不匹配时的实际/期望路径，不记录 URL 查询参数；Manager 支持解析 console 日志字段并直接显示错误原因和监听器。

- Fix JavaScript/Web quickstart reconnect history occasionally appearing empty while person membership is still being projected after SENDACK; the example backend now retries empty latest pages within its existing finite budget. / 修复 JavaScript/Web 示例重连时偶发空历史：后端在既有有限预算内等待最新空页的单聊成员投影。

- Read device credentials from the current Slot leader so newly written or rotated Tokens authenticate consistently through any ingress, including nodes outside the Slot replica set. This uses promoted internal RPC 87; upgrade all cluster nodes together before relying on cross-node authentication.
- Fix benchmark Token preparation to persist device credentials through the user use case before chat-lifecycle CONNECT, restoring authenticated three-node workloads without disabling Gateway authentication.
- Restore Manager browser smoke validation with Gateway Token authentication enabled by using all-node HTTP readiness before authenticated browser navigation.
- Restore the documentation browser acceptance runner and pinned Chromium dependency; validate BFF-issued credentials with Gateway Token authentication enabled, including bidirectional messaging and reconnect recovery.
- Fix transient person-channel command sends rejecting valid system senders or deriving recipients and client channel IDs with the command suffix. Preserve the command flag on delivered packets.

- 修复内部控制记录导致普通历史分页提前结束的问题：单次与批量同步、插件读取在有界扫描内按可见消息数量判断 `more`，整页控制记录不再遮住更早历史；聊天 Demo 沿用调用方页大小，并支持在没有滚动条时手动加载更早消息。

- 恢复后的旧 Channel Leader 若本机日志已进入更高任期，会按有效持久化证据刷新失效路由，避免首条发送误报日志冲突；真正的同任期冲突仍拒绝。

- 原生频道故障转移在每轮五秒预算内连续推进同一任务的有限步骤，并轮转读取积压任务，避免不可达目标或阶段间空等阻塞其他频道恢复；存储格式不变。

- 修复大量频道下健康报告过期后漏掉已扫描频道的及时故障切换：节点失去可用资格时重新开始有界扫描，状态不变时继续原游标，页数与任务预算不变。

- 原生频道恢复取得可用多数派及精确日志证明后不再等待暂停副本的探测超时；已到达的冲突／更长尾部仍参与校验，迟到响应保持有界回调所有权。

- 修复故障换主在写入隔离续期后误等待排空不可用旧主的问题：重新核验存活目标副本，避免阻塞后续频道修复。

- 修复副本不可达时重复发送可能永久占住频道待提交项的问题：本地确定冲突直接进入持久化幂等校验，保留原消息 ID 和序号，后续消息可继续写入。

- 原生频道换主恢复保留已观察到的尾部，对同链落后副本复制缺失的原提案后重新证明多数提交，支持三副本中一个 Leader 暂停时继续写入；本地恢复不覆盖已有记录，内容冲突仍阻断。

- 修复节点进程暂停后旧频道写入长期等待失联 Leader 的问题：转发尝试有界超时并刷新路由，仅对持久化且带原幂等键的消息重试；频道修复扫描跨轮次保留游标并轮转 Slot，避免后续频道长期得不到故障切换。

- Chat Demo 默认使用已有 Web Token 登录，不重写服务端凭据；凭据保存在当前标签页而非 URL，刷新或重新同步时重建消息/会话缓存。创建演示凭据须显式选择，退出会清除本标签页凭据。
- 修复 Chat Demo 向前翻页时文本和订单卡片显示错位；有未发送草稿时禁用重新同步，并保留明确的凭据错误提示。

- 修复原生恢复过程中的错误隔离：独立元数据请求不再继承同批请求的校验错误；副本尚未提升时 Leader 故障可撤销旧补建任务后重新选主；追加转发连接不可用返回可重试错误。

- 修复原生频道替换副本无法追平的问题：通过有界后台任务复制已提交历史，新副本不计入写入 quorum；修复恢复检查读取已加载副本的过期进度。

- `wkmigrate prepare` 遇到活动插件时继续执行独立的源业务副本检查，失败时输出已完成的检查证据并保持退出码 1；插件未兼容时仍不生成可转换、可导出的准备结果。

- 迁移候选同步 main 已有的空会话兼容修复：授权成员读取尚无消息的频道时返回空数组，保留成员限制和真实故障；补充全流消息排除后的 HTTP 同步、未读操作及重启追加验收。

- Recover cold quorum Channel leaders before history and conversation reads, including restarted empty replicas; return errors while recovery is unavailable instead of incomplete successful pages. Keep uncommitted messages invisible when reverse-reading a zero committed watermark. / 修复多节点重启后历史末条消息或会话遗漏：读取前完成原生 quorum 恢复，并阻止倒序读取暴露未提交消息。

- Fix read-only message catalog pagination across different-length channel keys so diagnostics and migration verification do not omit channels at page boundaries.
- Recover native cold Channel replicas for leader failover and allow quorum verification while a transfer write fence remains active; business writes remain fenced until cutover validation finishes.
- Preserve native `SyncOnce` in Channel RPC v8 so remote history reads keep recovery barriers and CMD records filtered; reject lossy legacy-codec fallbacks without changing storage.
- Preserve original message `red_dot` in the existing v3 stored flag and through ordinary send, history reads, replication and recovery; do not archive or clear it during v2 migration. Target nodes require Channel RPC v8 / Exchange v4 and a coordinated all-node upgrade.

- `wkmigrate authority` 识别旧 v2 原样保存配置版本的规则；仅在所有所属 Slot 副本的前后命令、完整配置字段及消息历史均匹配时，识别历史 Leader 指向自己的无成员变更标记。缺失证据仍阻止候选判定，不改写原库，也不放行未证实的状态。

- 迁移输出遵守现有 v3 消息存储与复制约束；不再引入迁移专用摘要格式或历史前缀恢复分支。无法完整保留的协议字段、非 1 起始历史及重复 ID 明确阻止迁移，不改写标识或额外丢弃数据。

- 迁移预检核对原插件绑定的主键与用户、插件双向业务索引，避免把原账号旧插件字段为空误判为无绑定；插件运行及配置兼容门槛仍保持。

- 迁移预检按原版语义保留零配置版本和非空 ID 的类型 0 源频道配置；仍按所属 Slot 核对副本一致性，不改写版本，也不放宽消息、会话和频道策略的身份检查。
- 迁移工具无损保留原 v2 会话及频道配置中的非 UTF-8 标识，按原字节关联、路由和校验，避免 JSON 替换字符合并不同身份；更新工具后须使用新的工作空间和归档。
- 迁移预检识别原 v2 独立写入的频道成员计数，并从原用户、设备解析个人权限频道；完整保留黑名单及原始计数，避免误报缺失频道身份或凭计数创建频道属性。无法解析的实际权限和空身份记录仍拒绝。
- 迁移配置比较保留原始历史离线统计作为证据，但不再将各节点的离线次数、离线时间差异误判为权威配置冲突；Leader、任期、副本及复制日志检查保持严格。
- 迁移预检按原版 v2 恢复语义处理空用户 ID 的会话缓存：完整归档并记录数量，不再误报为无效已读位置，也不创建空用户会话。

- 恢复原版 `/message/eventsync` 的持久事件投影读取，保留事件序号、分页及可见性过滤行为。

### 🚀 New Features / 新功能

- Show selected-node startup configuration as redacted TOML with effective defaults, full-document search/copy, and optional detailed Chinese/English comments; older nodes report unsupported TOML inspection explicitly. / 节点配置页改为脱敏 TOML，补齐生效默认值，支持全文搜索、复制和可选中英文详细说明，并明确提示旧节点不支持的情况。

- Add opt-in synchronous `msg.before_send` Webhooks for payload replacement or rejection, independent timeout/error policies, bounded concurrency, and business rejection codes 128–255 through SENDACK and HTTP. / 新增可选同步发送前 Webhook，支持内容修改、拒绝发送、独立超时及错误策略、有界并发和业务拒绝码透传。
- Add `message.cmd_channel_suffix` (`WK_MESSAGE_CMD_CHANNEL_SUFFIX`), defaulting to `____cmd`, consistently across command send, delivery, sync, plugins and Manager filtering. All nodes must agree; changing the suffix does not migrate existing command channels or bindings.

- `wkmigrate` 支持按捕获摘要、用户／频道身份和保留消息尾序号限定补齐缺失会话，将已核验历史设为已读；原生历史可见范围保持完整，未批准或证据变化的情况仍拒绝。

- `wkmigrate` 可显式接受经完整消息前缀、正式副本多数和已应用配置日志证明的源副本落后；保留当前 Leader 原历史并在归档重建时重新核验，默认仍阻断空 Leader、同任期换主及内容冲突。另可按显式恢复决定选取空 Leader 频道的一份完整正式多数副本，绑定捕获、完整诊断和消息摘要；未知异常仍阻断。

- `wkmigrate` 支持插件程序分块归档、离线重建、原生目录安装及独立文件校验；显式兼容规则仅接受已审计的 AI 示例 Linux/amd64 原程序、Receive 注册和统一配置。未知程序仍阻断，精确重试拒绝程序被替换或权限漂移。

- `wkmigrate` 可显式将原用户创建／更新时间完整留档，以适配没有对应字段的 v3 原生用户表；报告绑定每个节点的原值，归档重建重新核对，用户身份、插件绑定及设备凭据仍严格比较。

- Add explicit offline stream-message exclusion with source archives, continuous per-channel sequences, mapped read/delete boundaries, and native append/restart validation. / 离线迁移支持显式排除流消息，完整归档源行、连续编号频道消息并映射读删位置，验证原生追加与重启接续。

- `wkmigrate dedupe-plan` 报告 v3 新增按节点的消息字段影响统计，区分排除 CMD、去重后的保留消息与排除消息，并提供有界样本；不修改协议字段、不放宽兼容检查，需使用新的规划工作空间。

- 迁移计划新增 `plugin_configs`，可让指定插件统一采用某源节点的有效配置，同时保留各节点启用状态、时间戳和全部原配置。导入配置纳入批次完整性检查，离线校验从原始归档独立重算并拒绝缺失、篡改或多余配置；业务兼容检查仍保留。

- 新增插件迁移的单节点/三节点隔离验收，串联原绑定导入、默认程序扫描、节点配置、真实消息落库，并在两次完整重启后、发送新消息前核对全部已有消息和回复；发现逐节点配置会影响自动回复内容时，仍阻止业务兼容放行，支持对已批准的指定插件显式选择统一配置来源。

- 迁移工具支持把原 `PluginUser` 绑定按 UID 重新分配到 v3 原生表，并从源归档独立核对绑定字段、副本数量和双向查询；原纳秒时间保留在归档，目标沿用原生毫秒精度。插件程序、配置及业务行为仍须通过兼容验收。

- 迁移计划可显式指定 `plugin_nodes`，为每个目标选择配置来源，支持同一来源扩展到多个目标及缩容时明确选择来源；全部原配置留档，保持所选启用状态和时间戳。遗漏或重复目标、未知来源时拒绝，归档重建独立重算；该步骤不解除插件业务兼容检查。

- `wkmigrate diagnose` 的插件兼容问题新增各节点的配置、方法及原记录指纹，可识别节点配置差异且不输出配置值；诊断结果不代表插件已兼容，也不放宽迁移检查。

- 迁移工具可按原唯一索引与当前 Slot Leader 列表核对重复会话，保留已读、删除位置及列表版本，并检查去重前数量上限；空频道管理记录仅在无业务引用、正式副本及原配置日志一致时完整归档。两项均须显式启用，归档重建重新验证，默认仍严格拒绝。

- `wkmigrate` 新增显式 `metadata.device_lookup=v2_cold_start` 选项，按原版冷启动登录的 UID 索引保留最小设备 ID 的完整凭据；原始重复记录留档，归档重建重新选择并验证。默认拒绝重复凭据，不推断停机前缓存，也不放宽副本一致性或会话检查。

- `wkmigrate prepare` 从原始捕获的 Slot 配置命令和完整消息历史重建来源证明，支持有充分证据的补副本状态及历史 Leader 自指标记；归档携带命令并独立重验，缺失、改动或错位命令仍拒绝，不改变原库或 v3 放置与存储逻辑。

- `wkmigrate` 支持显式排除 CMD 消息、保留最新重复消息并压紧剩余序号；同步映射普通会话的已读/删除位置，省略旧 CMD 同步位置，生成可校验序号映射，支持 `export-map` 从归档重建。原始记录完整归档，跨频道冲突及不确定来源仍拒绝，不改变 v3 存储逻辑。

- 新增 `wkmigrate dedupe-plan`：按频道内原序号规划重复 MessageID 或发送者 ClientMsgNo 的最新记录保留，输出每条候选删除记录、保留依据及连续序号影响；源数据保持不变，跨频道身份冲突与相互淘汰的保留记录明确阻止规划通过。

- 新增 `wkmigrate authority` 专项核验：只读核对带迁移标记的源频道配置、已保留的 Slot 配置日志及逐序号副本消息，输出校验和绑定的分类证据；不清除迁移标记、不选择或修改业务数据，也不放宽迁移门槛。

- 新增 `wkmigrate diagnose` 全量源诊断：收集所有可读节点的兼容问题，以磁盘排序统计重复 ID/索引冲突，输出分节点数量、有限样例和带校验和的完整明细；扫描不完整时明确标记，诊断结果不能作为迁移通过凭据。

- `wkmigrate` 支持显式排除升级遗留的旧流分片及元数据，保留主消息及原序号；排除项完整归档并列出数量和校验摘要，默认仍拒绝，插件兼容检查不受影响。
- 新增 `wkmigrate prepare/export/import/verify` 离线迁移命令，读取未经升级的固定 v2 源版本，导入全新 v3 集群，并校验业务字段、消息索引、摘要链、初始化快照与副本记录数量。相同计划支持中断恢复；拒绝不兼容业务插件、关键源索引损坏和超出原生恢复预算的单条消息，不覆盖已有业务目标。大规模性能验收尚未完成。

### 📚 Documentation / 文档

- Update C# EasySDK documentation with published npm `easyjssdk 2.0.5` interoperability evidence for Node and Chromium/WSS.

- Document C#/Chromium WSS acceptance with certificate rejection controls and separately pinned JavaScript native-WebSocket recovery source. / 补充 C#/Chromium WSS 验收、证书拒绝检查与独立固定版本的 JS 原生 WebSocket 恢复修复记录。

- Document C++ SDK 0.1.0 prebuilt archives for Windows x64, macOS arm64 and Linux x64, including offline CMake integration, compatibility, checksums and upgrades. / 补充 C++ SDK 0.1.0 三平台预编译包的离线 CMake 接入、兼容要求、校验和升级说明。

- Add C#/JavaScript EasySDK interoperability evidence for public NuGet and candidate source, including pinned dependencies, recovery scenarios, and Node/ws transport scope. / 补充 C#/JavaScript EasySDK 正式包与源码互通验证记录，明确固定依赖、故障恢复场景和 Node/ws 传输范围。

- Add bilingual WuKongEasySDK-Python quickstarts, source installation, asyncio lifecycle guidance, and Python/JavaScript interoperability evidence. / 新增 WuKongEasySDK-Python 中英文接入教程、源码安装、asyncio 生命周期说明与 Python/JavaScript 互通验证记录。

- Document the C++ EasySDK vcpkg Git registry with automatic dependency installation and a minimal CMake consumer. / 新增 C++ EasySDK vcpkg Git registry 接入文档，支持自动安装依赖和最小 CMake 消费端示例。

- Add bilingual WuKongEasySDK-Rust quickstarts with pinned Git installation, Tokio lifecycle, bounded queues, and Rust/JavaScript interoperability examples. / 新增 WuKongEasySDK-Rust 中英文接入文档，覆盖固定 Git 版本安装、Tokio 生命周期、有界队列与 Rust/JavaScript 互通示例。

- Document the public WuKongEasySDK C# NuGet 1.0.0 installation and its independent package verification in both languages. / 更新 C# WuKongEasySDK 中英文文档，提供 NuGet 1.0.0 正式包安装与独立发布验证记录。

- Align the Manager and internal transport documentation inventories with the startup TOML route and RPC 88, restoring documentation publication checks. / 补齐启动 TOML 接口与 RPC 88 的文档清单，恢复文档发布检查。

- Add bilingual C# WuKongEasySDK integration, console example, async lifecycle, and pinned-source installation guidance for the new `WuKongEasySDK-CSharp` repository. / 新增 C# WuKongEasySDK 中英文接入文档，覆盖固定源码安装、控制台示例、异步生命周期和验证边界。
- Add bilingual WuKongEasySDK-CPP documentation for pinned C++17 source, CMake integration, WS/WSS, messaging, thread cleanup, and real C++/JS interoperability evidence. / 新增 WuKongEasySDK-CPP 中英文文档，覆盖固定 C++17 源码、CMake、WS/WSS、消息收发、线程清理与真实 C++/JS 互通凭据。

- 中英文 Docker 部署文档补充阿里云镜像地址，说明中国大陆用户如何在 `docker run` 和 Docker Compose 中切换仓库，并保持镜像版本同步。

- Add a directly runnable, standard-library Go before-send Webhook example with allow/replace/reject rules, bilingual setup instructions, and real-process send/history validation. / 新增可直接运行的 Go 发送前 Webhook 示例，支持放行、改写及拒绝，并附中英文接入说明和真实进程验证。

- Add a reproducible local three-node Webhook acceptance guide covering Token authentication, failure policies, overload isolation, recovery, and observable results. / 新增本机三节点 Webhook 验收指南，覆盖 Token 鉴权、失败策略、过载隔离、恢复及观测结果。

- Document synchronous before-send Webhook configuration, request/response contracts, failure policies, business rejection codes, and cluster behavior in Chinese and English. / 补充同步发送前 Webhook 的中英文接入、配置及协议文档。

<!--
Use only the non-empty categories that apply: `⚠️ Breaking Changes /
破坏性变更`, `🚀 New Features / 新功能`, `🐛 Bug Fixes / 问题修复`,
`🔧 Improvements / 改进`, `⬆️ Upgrade Notes / 升级说明`,
`🔒 Security / 安全`, `📚 Documentation / 文档`, and
`⚠️ Known Issues / 已知问题`. Prefix the selected category with `### `.

Every category must contain at least one "- " list entry. Release headings use
the exact form: ## [v3.0.0-beta.5] - 2026-09-01
-->

## [v3.0.0-beta.9] - 2026-09-07

### 🐛 Bug Fixes / 问题修复

- 修复 Demo 打开尚无消息的单聊时历史同步和未读清零返回 400 的问题：单条及批量同步返回空消息数组，缺失会话的未读操作幂等成功，不创建成员关系；群成员校验、已移除成员限制和真实存储/路由故障仍保持有效。
- 未配置客户端对外地址时，默认公网 `/route` 和 `/route/batch` 会用请求主机名补全 Gateway 的通配监听地址，修复标准 Docker 部署中 Demo 连接 `ws://0.0.0.0:5200` 失败的问题；显式地址、Linux 回环监听、内网查询和指定节点路由保持原有行为。
- `/message/send` 现在会在 `from_uid` 与兼容别名 `sender_uid` 均为空时使用系统账号，并支持通过 `message.system_uid` / `WK_MESSAGE_SYSTEM_UID` 配置该账号（默认 `____system`），恢复 Demo 命令消息的旧版兼容行为。

### 🔧 Improvements / 改进

- 内嵌聊天 Demo 新增中英文界面，根据浏览器语言偏好自动选择，未匹配支持语言时默认使用英文；可通过 `?lang=en` 或 `?lang=zh` 分享固定语言的体验入口。
- 频道消息同步、旧会话同步与插件读取共用分页规则，继续保持各入口原有的可见范围、返回顺序和错误语义。

- Manager 消息诊断页现在会在集群节点未启用 diagnostics 时显示 TOML 与环境变量配置指引、标出受影响节点并禁用无效的追踪操作，不再直接暴露内部错误文本。
- Manager 节点列表现在显示每个节点当前运行的 WuKongIM 程序版本，便于识别滚动升级期间的版本差异。

### 📚 Documentation / 文档

- 补充原版 v2 数据迁入指定三节点 v3 测试目录的部署验收报告，记录旧环境备份、真实 SDK 缓存重置、插件回复、完整重启和监控检查。

- Document the legacy stream-parent audit and the explicit decision to omit stream messages while preserving continuous sequences. / 记录旧流主消息语义核对及本次排除流消息、保持连续序号的明确范围。
- 中英文 README 现以业务开发者的首次接入为主线，提供 Linux 软件包安装、systemd 启动、SSH 转发与双用户收发步骤，并提供 Docker 部署指南入口，更新 SDK 选型入口，明确业务后端和 Product HTTP 认证边界；英文版采用英文 Demo 操作说明与真实截图。

- 中英文文档补齐 Web 双用户接入闭环，统一教程文本消息与 Manager 访问方式，并提供可下载的监控告警、压测配置及结果解读。

- 文档站“资源”菜单的聊天演示与 Manager 演示入口已切换到新的 HTTPS 域名。
- Product HTTP API 参考现在统一完整合同与窄 Profile 的信任等级和示例，补齐响应字段说明、可执行条件 Schema、节点本地与分阶段写入语义，并明确 HTTP 200 后仍需检查的业务结果。
- Docker 部署文档补充配置文件权限要求，明确官方镜像的 `10001:10001` 非 root 身份、推荐的 `0640` 权限，以及仅 root 可读导致容器启动失败的诊断信息。
- 文档站的当前发布版本现在从根 Changelog 的最新版本标题构建期注入，并在不可变二进制发布成功后自动部署；后续发布无需再手工同步中英文 Linux、Docker 示例及其测试。

## [v3.0.0-beta.8] - 2026-09-04

### ⚠️ Breaking Changes / 破坏性变更

- Gateway 现在默认启用 CONNECT Token 鉴权，并按 UID 与设备类别精确校验 `/user/token` 持久化的设备凭据；升级前必须先为客户端准备 Token，或仅在受控兼容迁移期间设置 `gateway.token_auth_on=false`。

### 🐛 Bug Fixes / 问题修复

- 频道消息同步现在会在应用成员可见性下限时保留“最新一页”的零边界语义，消息超过单页时不再误返回最早一页。

### 📚 Documentation / 文档

- 项目根目录恢复 Apache License 2.0 正文，并将中英文 README 的许可证声明链接到仓库内文件。
- Product HTTP API 文档现已逐一对齐 41 个运行时路由的请求 DTO 与校验语义，为所有查询和 JSON 请求参数补充中英文解释，并修正空白字符串与 `null` 的兼容约束。
- 服务端部署与运维文档改为面向初学者的精简实操指南，快速开始统一使用 Linux 软件包与 systemd，并修复已移除页面的链接。
- 中英文 Linux 部署与配置参考现已将 `v3.0.0-beta.7` 作为签名 Preview 软件源版本，并以 `sudo wukongim init` 作为默认初始化入口，同时保留显式路径兼容命令；Docker 部署示例也同步固定到同一版本。

## [v3.0.0-beta.7] - 2026-09-03

### 🚀 New Features / 新功能

- 原生 Linux 安装新增 `sudo wukongim init` 快捷入口，默认安全生成 `/etc/wukongim/wukongim.toml`，同时保留原有显式路径命令用于兼容和自定义位置。
- 签名 Linux Preview 软件源新增 `wukongim-archive-keyring` 与 `wukongim-release` 引导包，首次配置后可直接通过 APT 或 DNF/YUM 安装和升级 WuKongIM，并由包管理器自动接收后续公开签名证书更新。

### 🐛 Bug Fixes / 问题修复

- 文档 CDN 现在仅对静态导出的页面 RSC `index.txt` 从缓存键删除 `_rsc`，并在构建期验证每个已发布路由都有独立静态载荷；章节跳转不再因一次性 RSC 查询值反复触发高延迟回源，同时保留图片及其他查询参数的缓存隔离。
- 文档 Pages 自定义域名迁移与回滚现在会先暂存已验证产物，在域名绑定变化后立即重新部署，并以绕过 CDN 的根路径、双语首页、深层页面及搜索真实 GET 作为内容就绪门禁；证书批准或 API `204` 不再被误当作站点可用。
- Manager 精确查询频道运行时元数据时，现在会按频道 Leader 路由最大消息序号读取；非 Slot 副本节点不再因本地缺少元数据而误报 404。
- 多副本频道 Leader 重启后，最近会话重试会在持久化提交 checkpoint 落后于本地日志时按当前元数据激活冷 runtime 并恢复 quorum 高水位，不再把仍存在的会话返回为空。
- 多副本频道的消息拉取现在会在持久化 checkpoint 落后时使用活动 Leader 已确认的提交高水位，发送成功后的首条消息可立即出现在频道与最近会话同步结果中。
- 文档 CDN 证书检查现在根据显式公网路由模式和多个公共解析器的直接 CNAME 答案决定是否验证阿里云边缘证书，不再将供应商 `DomainCnameStatus` 误当作公网切流证明。
- 文档 CDN 证书检查现在正确接受“已安装证书且暂无需续期”以及强制初始化时“尚未安装证书”的布尔结果，同时仍会拒绝缺失或类型错误的状态字段。
- 文档 CDN 证书轮换现在兼容阿里云已启用的手动上传证书省略 `Status` 字段的响应，同时仍会拒绝免费或未知类型的空状态以及尚未生效的证书状态。
- 文档 CDN 的 ACME 账户初始化现在使用固定生产端点和已审阅条款的账户专用流程，不再因合法邮箱或 Let's Encrypt 省略可选联系人字段而失败，也不会在初始化账户时误发起证书申请。
- WKProto 编解码器现在会安全处理空输入并拒绝未知帧类型编码，避免畸形输入触发越界崩溃或静默产出空报文。
- 插件热重载监视器现在在启动后立即停止时保持完成信号的稳定引用，避免并发清理将其置空后引发 `close of nil channel` 崩溃。
- Issue Agent 验证器现在会在判定工作目录越界前规范化 checkout 根路径，避免 macOS 上 `/var` 与 `/private/var` 别名导致合法子目录被误拒。
- JSON-RPC 解码器现在会将未知通知方法统一归类为 `ErrUnknownMethod`，与未知请求的错误分类保持一致，便于调用方稳定识别协议错误。
- JSON-RPC subscribe/unsubscribe 请求现在会在协议适配层转换为带正确 action 的 `SUB` 帧，并将可用的 `SUBACK` 关联到原请求；当前 Product Gateway 仍未发布 `SUB` 入站能力。
- 权限元数据批量读取现在会在进入 Slot 代理前拒绝未知读取类型，并保持其余合法结果的原始对齐，避免无效类型被下游结果覆盖或污染整批授权证据。
- Controller Slot 副本迁移与 Leader 转移在运行时未启动时现在统一 fail closed 为 `ErrNotStarted`，不再根据空状态返回误导性的业务校验错误。
- Slot FSM 现在会为确定性的过期元数据提案持久化已应用水位，节点重启后不再重复回放已经判定为无操作的 Raft 日志。
- 元数据存储关闭后清理终态频道迁移任务现在返回关闭错误，不再因访问已释放的底层数据库而触发空指针崩溃。
- 消息恢复后缀替换现在会在写入前校验保留边界的 Proposal 与 Entry 身份一致性，检测到损坏时拒绝替换并保留原有后缀。
- 元数据备份与恢复现在拒绝夹带运行时或迁移状态、跨注册 span 乱序及重复键的快照，并在完整性预检失败时保留目标端原有数据。
- 完整备份发布现在会在写入任何仓库对象前校验全部 256 个 Hash Slot 的完成进度，避免不完整任务提前绑定空仓库或留下发布副作用。
- Controller Raft 启动时若物化状态文件丢失且无可用快照，现在会从保留 WAL 重建；若日志已压缩且快照数据缺失则拒绝启动，避免以空状态继续运行。
- 集群节点停止时现在会撤销路由、Slot 与频道就绪状态，并阻止在途控制快照在停止后重新发布就绪，避免停机窗口暴露错误健康状态。
- 应用启动失败回滚现在会先关闭已开放的 Prometheus、Manager 与 API 入口，再停止备份调度运行时，避免回滚窗口继续接受新的管理请求。
- 阿里云 Lease 盘点、主机创建与身份移除现在会拒绝子资源角色冲突、跨实例磁盘响应及无法由 SDK 错误码证明已删除的身份资源。
- 阿里云仿真账户 Bootstrap 现在会安全处理官方 SDK 错误响应，并仅依据结构化错误码判断资源不存在，避免错误路径崩溃或把普通服务与传输故障误判为已删除。
- 阿里云只读权限探针现在兼容官方 SDK 的两种结构化 403 错误类型，合法 RAM 拒绝不再被误判为探针失败。
- 消息备份流回放现在会拒绝 `log_start_offset` 超过提交高水位的非法 checkpoint，避免校验和合法但语义损坏的快照进入恢复流程。
- 云部署离线文件适配器现在会拒绝负数读取与清单上限，并正确处理最大整数读取边界，避免非法参数触发 panic 或把已有文件静默读成空内容。
- Slot 代理现在会沿包装错误链识别“Slot 不存在”，避免上游附加上下文后被误分类为“暂无 Leader”并返回错误的 RPC 路由状态。
- WKDB bundle 导出现在会拒绝无法表示为 `int64` 的无符号 inspect 字段，并保留非法频道目录状态的 `ErrValidation` 分类，避免损坏或类型异常的数据被静默回绕或失去可识别的验证错误。
- Cloud View 现在会按原始文件大小严格拒绝超过 256 KiB 的配置和超过 64 KiB 的运行状态，即使超量部分仅为尾随空白，也无法再绕过文件上限。
- Cloud Simulation 现在会在创建任何付费资源前校验完整的 Run Locator 参数，并按原始输入大小拒绝超过 64/128 KiB 的请求与阿里云配置，避免无效命令留下资源或通过尾随空白绕过上限。
- Cloud Host 在线与离线安装现在都会在执行任何主机副作用前拒绝无效的远程根目录前缀，避免参数错误导致部分安装状态残留。
- Cloud Bundle 现在会按完整输入大小拒绝超过 128 KiB 的部署 spec，合法 JSON 后追加尾随空白也无法再绕过上限。
- Gateway 会话的 `LoadOrStoreValue` 现在保留已存储的 `nil`，并在并发初始化保留热键时只允许一个调用方取得写入权，避免会话状态被后续竞争者覆盖。
- Cloud Analysis Bridge 现在会拒绝固定 PEM 证书前的非空白数据，避免额外内容被 PEM 解析器静默忽略。
- Review Agent GitHub 适配器现在保留取消与超时错误身份，并严格限制写操作响应体大小，避免上层误判重试语义或读取过量响应数据。
- Cloud Analysis 诊断结果现在会拒绝 NaN 和无穷大置信度，确保分类一直满足 `[0,1]` 约束。
- `wkcli bench` 现在会拒绝无法安全表示的超大 payload 尺寸，并将帮助与错误文本写入命令注入的对应输出流。
- Raft 日志现在会将 `leader lost` 归类为 `leader_change` 事件，避免 Leader 丢失信号被误计入普通日志。

### 🔧 Improvements / 改进

- 文档静态构建现在生成并校验固定域名、排序唯一且有数量上限的 RSC `index.txt` URL 清单，为后续精确刷新设计提供可审计输入；当前发布流程仍只刷新原有四个 URL，不新增预热、权限或 TTL 变更。
- 原生 Linux 包 CI 现在会在 Ubuntu 24.04、Debian 12、Rocky Linux 9 与 AlmaLinux 9 的真实 systemd 环境中验证配置初始化、健康检查、显式启停/重启、活动卸载、状态保留及重装不自动激活。
- 文档发布新增默认关闭的阿里云 CDN 定点刷新与 Let's Encrypt DNS-01 边缘证书轮换支持；两条路径使用独立的 GitHub OIDC 角色，不在仓库保存长期阿里云凭据，且在完成外部配置和切流前不会改变现有 GitHub Pages 服务。
- Chat Lifecycle 正式演练启动器、正式收尾器和通用 Cloud Lease 回收扫描现在仅在存在 transition、handoff、付费资源生产者或云端库存期间启用；取得完整空闲与零库存证明后会自动停用，并在下一次 transition、停止请求、精确清理或付费 Acquire 前安全恢复；完整 Artifact 盘点会重试短暂的 GitHub API 分页错误。

### 🔒 Security / 安全

- 二进制发布的手动恢复现在必须从目标版本的精确 tag ref 启动，并同时绑定事件提交与 Workflow 提交；从 `main` 为其他 tag 生成无法被软件源信任的 provenance 会在构建前失败。

### 📚 Documentation / 文档

- Linux 服务端部署文档将软件源安装收敛为统一的 `curl -fsSL https://packages.githubim.com/repo | sudo sh` 添加源入口，再显式更新索引并按包名安装；首次添加后不再手工下载特定版本的 WuKongIM deb/rpm。
- Linux 软件源引导脚本明确只添加签名软件源，不会更新索引、安装 WuKongIM 或启动服务；文档继续保留首次 HTTPS 信任边界、底层 APT/RPM 引导包和固定主密钥指纹说明。
- Linux 服务端部署文档现提供 `v3.0.0-beta.6` 签名 Preview 软件源的 APT 与 DNF/YUM 安装流程，并在写入专用 keyring 前固定核对仓库主密钥指纹。
- 中英文 v3 文档站现由仓库内 GitHub Pages 工作流执行完整静态验收后发布到 `docs.githubim.com`，发布产物与通过验收的 `docs-site/out` 保持一致。
- WuKongEasySDK 中英文文档现固定 Web `2.0.4`、Android `1.0.5`、iOS `1.1.1` 与 Flutter `1.1.0` 正式包，并补充四端 example 与正式包的独立验收回执及可复现流程。
- Docker 服务端部署现在提供精简的 `docker run` 与 Docker Compose 两种流程，共用最小 `wukongim.toml`、持久数据卷和完整配置参考；中英文教程删除远程一键安装脚本及其自动版本解析说明，保持两步完成启动和就绪验证。
- 服务端配置文档新增独立的中英文常用配置页，以表格解释最常用的 10 个配置项；配置参考改写为可搜索的逐字段手册，为全部公开 TOML 与 `WK_*` 配置补充用途，并标明关键默认值、`0` 值、互斥、敏感和迁移说明。
- 文档站资源菜单移除官网链接，并使用聊天演示与 Manager 演示当前可用的 HTTP 地址。
- 中英文公共文档新增可访问的 Mermaid 架构与流程图，精简产品、指南和部署导航，并将已撤下的 Kubernetes 页面重定向到受支持的部署入口。

## [v3.0.0-beta.6] - 2026-09-01

### 🐛 Bug Fixes / 问题修复

- 服务端二进制现在内置 IANA 时区数据库，官方最小 Docker 运行时可在 Manager 中保存 `Asia/Shanghai` 等非 UTC 备份计划，不再因运行镜像缺少 `zoneinfo` 返回无效请求。
- 二进制发布恢复流程不再使用 Actions `GITHUB_TOKEN` 无权访问的仓库管理接口，并可从精确工作流提交取得旧标签缺失的 Release Notes 解析器；不可变 Release 设置改由管理员在发布前外部核验，流程仍在发布后强制验证 Release 已封存为不可变。

### 🔧 Improvements / 改进

- GitHub Release 正文现由人工维护的 Changelog 生成，并在二进制文件与三个 Docker 镜像仓库的版本身份、摘要和平台验证完成后再公开发布。

### 📚 Documentation / 文档

- Docker 服务端部署文档已切换到三仓库同摘要的 `v3.0.0-beta.5` 非 root 镜像，并同步更新预发布风险说明。

## [v3.0.0-beta.5] - 2026-09-01

### 🚀 New Features / 新功能

- GitHub Release 新增未签名的 Linux amd64 DEB/RPM 安装包，并与四个平台的压缩包共用校验和与构建来源证明；软件源发布仍保持关闭。

### 🔧 Improvements / 改进

- Chat Lifecycle 演练收尾定时器现在仅在付费演练或待清理 handoff 存在期间启用，取得全局空闲与零库存证明后自动停用，避免仓库空闲时持续产生 GitHub Actions 运行。
- Chat Lifecycle handoff 发现现在可安全穷尽最多 20,000 个保留 Artifact，仓库中超过 5,000 个无关 Artifact 时不再阻塞空闲定时器停用。
- 官方 Docker 镜像新增 `/readyz` 健康检查和 `SIGTERM` 优雅停止契约，并显著缩小 Docker 构建上下文。

### ⬆️ Upgrade Notes / 升级说明

- 官方 Docker 镜像默认改为 UID/GID `10001:10001` 非 root 用户；命名卷可直接使用，自定义宿主机绑定目录需在升级前授予该 UID/GID 写权限。

### 🔒 Security / 安全

- Docker 运行时升级并固定到受支持的 Alpine 3.24.1 摘要，构建基础镜像同步固定摘要，镜像内 Go 安全相关依赖完成升级。
- Docker 发布流程现在会分别扫描 amd64 和 arm64 候选镜像；发现 Critical 或 High 漏洞时阻止发布，恢复发布也会重新扫描现有规范摘要。

### 📚 Documentation / 文档

- 中英文 Docker 服务端部署文档改为 Compose 优先的可验证单节点集群流程，补充固定镜像、随机凭据、端口保护、持久化、健康检查、日常运维和 `docker run` 备用路径。
