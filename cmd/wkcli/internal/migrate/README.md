# wkcli migrate

Offline migration from unmodified WuKongIM `v2.2.5-20260422`
(`a888f89533d0e7d1b2030e06504ca97f1ad891d4`) into a fresh native v3 cluster.
The source server does not need an upgrade. Linux and macOS source locking is supported.
The reader also supports Pebble format 19 through an explicit read-only engine
adapter; all original schema, index, authority, and plugin checks still apply.
This does not certify arbitrary older or modified server builds. Released
`v3.0.0-beta.13` binaries do not contain this adapter.

See the [operator runbook](../../../../docs/superpowers/runbooks/v2-to-v3-migration.md)
and [final acceptance report](../../../../docs/superpowers/reports/2026-09-08-v2-migration-final-acceptance.md)
for the tested source, explicit policies, cache reset procedure, and validation limits.
Offline verification does not switch production traffic.

`source_commit` is optional in the plan. Omission or an empty value selects the
pinned reader schema above; it does not detect or certify the source binary
revision. Explicit different revisions are rejected. Actual format and business
compatibility checks still run. Normalization precedes plan hashing, so legacy
explicit plans and omitted-field plans retain the same workspace/archive identity.
Tools released before this change still require the explicit field.

For a fresh rehearsal using delivered Linux binaries, see the
[isolated package rehearsal](../../../../scripts/migration/README.md). Its dry run
validates inputs and mount isolation before starting the full offline pipeline.

Optional `plugin_nodes` in the plan explicitly chooses one source for every
target node, for example `[{"source_node":1001,"target_node":1},
{"source_node":1002,"target_node":2},{"source_node":1003,"target_node":3}]`.
Preparation derives node-local native desired settings and a capture-bound
`plugin_settings` report. Assignment preserves distinct config values, enable
state and timestamps; raw registration fields remain available in the private
workspace and original archive. Every target must appear exactly once; unknown
sources and repeated or missing targets fail before capture. A source may supply
several targets, allowing expansion or contraction with an explicit configuration
choice. Every original node is still captured, including nodes not selected to
supply target settings. This does not certify plugin runtime compatibility or
deploy executables. User bindings
have a separate native metadata path. Existing plugin business-mapping blockers
remain active.

An explicit `plugin_configs` policy can choose one source's effective configuration
for a named plugin on every mapped target, for example
`[{"plugin_no":"wk.plugin.ai-example","source_node":1001}]`. Only that plugin's
config is replaced; every node retains its own enabled state and timestamps.
All original node configurations and the selected source row provenance remain
in private migration evidence. The plugin must exist on every mapped source;
unknown/repeated choices or a missing registration fail. Unlisted plugins keep
the node mapping's configuration. Changing this policy changes the plan digest;
use fresh workspaces and archives.

Import writes desired settings through the native store at `<data_dir>/plugin-state`
before sealing the generation fingerprint. Exact retries succeed; changed files
are rejected. Offline `verify` independently recomputes expected settings from raw
archived Plugin rows and the plan, checking configurations, flags, timestamps,
missing/extra files and per-target counts. Its `plugin_settings` report has its own
source-bound digest. It does not use converted settings as expected values and
does not install plugin programs or authorize target startup.

```sh
GOWORK=off go build -o ./bin/wkcli ./cmd/wkcli
./bin/wkcli --help
```

Use `diagnose` first for a source-wide census of the implemented compatibility
checks. It emits bounded JSON category summaries and a checksummed JSONL file
containing every finding, with exact disk joins rather than an LRU sample.
Counts are physical per-node findings (or explicitly named duplicate groups),
not distinct business messages across replicas. Identifiers use JSON strings.
An unreadable source is marked incomplete while other nodes are still scanned.
No target directory or prepare/conversion seal is created. Exit code 1 still
emits the report when blocked/incomplete; exit 0 means only that the listed
checks found no issues. Neither result certifies migration or cutover.

```sh
./bin/wkcli migrate diagnose --plan /srv/wkmigrate/plan.json --workspace /srv/wkmigrate/diagnostic-workspace > /srv/wkmigrate/diagnostic.json
```

Keep this workspace separate from `prepare`: its identity deliberately prevents
reuse by export/import. The report lists checks and unverified downstream joins,
authority selection, pending-conversation recovery, API/SDK and runtime behavior.
Raw diagnostics count and check all original main Message rows. Message-policy
omissions are evaluated separately by `dedupe-plan` and authoritative conversion;
raw compatibility findings alone do not account for those exclusions.

Use `authority` to investigate current source channel configurations with
nonzero `MigrateFrom` or `MigrateTo`. It reads each stopped node twice under
source locks and requires identical file digests across passes. Retained Slot
configuration commands are decoded without replay; selected-channel message
comparisons include every original stored field and sequence. Scratch storage
contains digests instead of payload copies, and differences are streamed.

```sh
./bin/wkcli migrate authority --plan /srv/wkmigrate/plan.json --workspace /srv/wkmigrate/authority-workspace > /srv/wkmigrate/authority.json
```

The checksummed JSONL contains configuration history, every differing message
sequence, and one `channel` classification per distinct marked owner. Classes
are `consistent_formal_replicas`, `learner_lag_only`, `conflict`, and
`insufficient_evidence`. A candidate is diagnostic only: this command does not
write selected records or a PREPARED seal. `prepare` independently rebuilds
transition proofs from captured raw commands and rows; unproven markers still
fail. Missing retained config commands remain insufficient evidence, even
when message copies agree. Historical client ACKs are not certified: original
v2 initializes channel applied progress from the last stored message on restart.

Report version 3 uses a fresh `:authority-v3` workspace identity. Configuration
copies expose `version_rule`: `slot_log_index` or `original_encoded_payload`.
The latter requires exact command-payload/row equality under the historical v2
apply rule; no timestamp heuristic rewrites ConfVersion. A
`historical_self_leader_noop` additionally requires each owner-Slot replica's
retained predecessor to match every field except ConfVersion/MigrateFrom/MigrateTo,
with unchanged full membership, no learners, unchanged Leader/term, identical
commands and complete matching message histories. Other same-node markers still
fail. Neither classification certifies a successful prepare or cutover.
Unmarked channels and message-event projections are outside this focused audit.
Use a separate workspace; exit 1 still emits completed unresolved/incomplete
reports. Exit 0 certifies only the stated snapshot comparisons, never cutover.

When `prepare` fails after source checks have started, stdout may contain a
`status: "blocked"` preflight with partial evidence; stderr contains the error and
the process exits 1. `selection.replica_comparison_complete` covers only raw
business replica comparison. Unmapped active plugin registrations are counted in
`selection.plugin_business_rows` without hiding later source inconsistencies.
Any plugin compatibility blocker leaves the selection digest empty, prevents
conversion/export, and publishes no PREPARED checkpoint.

Executable files additionally require a code-defined compatibility profile.
`selection.plugin_artifact_compatibility_pending` counts files without one;
even a descriptor-only old registration cannot authorize an unknown program.

The installation phases are `prepare`, `export`, `import`, and `verify`. Every
command takes an immutable `--plan` and a `--workspace`; export/import/verify
also require `--archive`.
Create each target data directory’s parent beforehand, but leave `data_dir` itself
absent. Import creates that directory exclusively; an unrelated existing directory
is refused. For `data_dir=/srv/migration-targets/node1`, create only
`/srv/migration-targets` before import.

Stop all source writes and processes before preparation. Import all target
nodes before startup; run independent offline verification before first startup.
Use a v3 binary built from the same implementation as the migration tool.

Read the [operator runbook](../../../../docs/superpowers/runbooks/v2-to-v3-migration.md)
for the plan schema, commands, restart boundaries, supported mappings and cutover.
The [local acceptance report](../../../../docs/superpowers/reports/2026-09-06-v2-to-v3-migration.md)
records functional coverage and the deferred 100 GiB / four-hour performance target.

Historical Stream/StreamMeta tables left by older v2 upgrades fail by default.
An operator may explicitly set `exclusions.legacy_stream_storage: true` in the
JSON plan to keep those two tables only in the checksummed source archive.
This flag alone retains parent Message rows and their fields. The separate
`messages.exclude_streams` policy below omits stream messages and associated
event projections; neither flag authorizes plugin exclusions. Excluded
physical row counts and a source-row digest appear in prepare/archive/verify
reports. Changing this rule requires a new workspace, archive and empty targets.

Original member-count-only ChannelInfo rows are derived administrative data;
they remain in the archive without creating channel bodies. Personal permission
owners can be resolved from original account/device UID identities. Actual
allowlist/denylist members still require matching operational indexes and source
replica agreement. Partial identities remain errors and are never mistaken for derived counters. The named empty-channel proof below is the only archival exception.

Original Conversation UID/channel IDs and ChannelClusterConfig channel IDs can
contain non-UTF-8 bytes. Internal state uses a versioned, lossless string encoding
for identity joins, native conversion and independent comparisons. Operational
indexes, shard placement and replica equality still must match the original bytes.
Use a fresh workspace and archive after updating the tool; old intermediate
encodings are rejected. Unproved empty IDs, zero-type business rows and unverified plugin
compatibility remain errors. Native reopen and snapshot recovery are covered;
this does not establish public API/SDK compatibility for opaque identifiers.

Original `PluginUser` bindings are selected from source Slot 0 after both original
lookup indexes and formal replicas agree. Import redistributes them by UID through
v3's existing `plugin_binding` table. UID/plugin identities remain unchanged;
native timestamps use milliseconds while physical source IDs and exact nanoseconds
remain archived. Independent verification checks fields, UID lookups, bounded
plugin-to-user index pages, replica counts and bootstrap snapshots. This does not
install plugins or certify their runtime behavior: global methods/configuration
and the obsolete `User.PluginNo` column retain their compatibility gates.

Original source ChannelClusterConfig rows may have ConfVersion=0 or, with a
nonempty ID, ChannelType=0. The pinned source stores and reloads these values.
They remain exact source control identities: the owner Slot and replica
comparison establish authority, and v3 placement is generated independently.
Conflicting replicas, missing version bytes, unknown leaders and zero terms
still fail. This does not permit zero-type messages, policies or conversations.

Target message data uses the existing v3 proposal version 1 and unmodified
message keys, indexes and append validation. Original seconds map to
ServerTimestampMS. RedDot is preserved in its existing stored bit; build the
tool and all target nodes from the same version with the general RedDot fix
(Channel RPC v8 / Exchange v4). Message fields those paths cannot retain
(including nonzero sync_once/expire or a nonempty StreamNo/topic),
nonpositive timestamps, retained histories starting after sequence 1 and duplicate
IDs block conversion. No fallback rewrites IDs, fills missing history or expands
the authorized Stream/StreamMeta exclusion. An empty-ID source row also fails.

The earlier full-protocol/retained-prefix implementation has been withdrawn.
Its previous acceptance results do not qualify this implementation. Compatible
synthetic records cover installation and native proposal transfer; the three-node
empty-replica recovery acceptance currently fails on the restored v3 runtime.
See the [current boundary and evidence report](../../../../docs/superpowers/reports/2026-09-06-v2-migration-existing-v3-contract.md).

The [diagnostic/control report](../../../../docs/superpowers/reports/2026-09-06-v2-migration-diagnostic-control.md)
separates source incompatibilities from reproducible cold-history read failures
on the pristine pre-migration v3 revision. Passing migration unit tests does not
mean those native recovery acceptance cases passed.

The [marked-channel authority report](../../../../docs/superpowers/reports/2026-09-07-v2-migration-source-authority.md) records the real backup classification, source candidates and the remaining historical configuration ambiguity.

## 重复消息规划

```sh
wkcli migrate dedupe-plan --plan /absolute/plan.json --workspace /absolute/dedupe-workspace
```

该命令只读扫描停机来源，用独立工作空间生成 SHA-256 绑定的 JSONL 清单。每个节点分别判断：MessageID 相同，或频道 ID/类型、发送者及非空 ClientMsgNo 均相同，只保留频道内原 MessageSeq 最大的记录。发送者或 ClientMsgNo 为空时，不参与 ClientMsgNo 幂等去重，遵循原生 v3 规则；不同节点的物理副本不互相删除。跨频道 MessageID 没有可比较的频道序号，或某个保留候选又被另一条规则淘汰时，报告 unresolved。

报告包含重复组、每条候选删除记录与保留记录的全部源字段摘要、受影响频道、需要重排序号的存活记录数量，以及旧流主消息的保留影响。明细不包含消息正文或凭据。退出码 0 只表示规划扫描完整且无未决冲突；它不表示可导入。

报告 v5 的每个节点另有 `protocol_impact`：分别统计候选保留消息和候选排除
消息的不兼容字段，另以 `candidate_stream_drops` 统计流消息排除，每个字段最多提供三条不含正文的保留样本。CMD、流消息、重复项
不会被重复算作剩余字段阻断；同一消息的多个字段分别计数，消息总数只计一次。
统计发生在每节点候选选择后，不代表权威副本选择完成，也不会清除 RedDot、
StreamNo 或其他字段。JSONL 含节点汇总并纳入摘要。v5 使用新的
`:dedupe-v5` 工作空间身份，旧规划工作空间必须另存，不能原地升级。

prepare/import 现在支持下述显式消息策略。未配置时保持原有严格行为。不能把诊断工作空间用于 prepare，规划通过也不代表可切换；原始记录仍完整归档。

真实备份的 [248 条候选删除及序号影响报告](../../../../docs/superpowers/reports/2026-09-07-v2-migration-message-dedupe.md)。

## 排除 CMD 和流消息、去重及序号映射

在迁移计划中显式配置：

```json
{
  "messages": {
    "keep_latest_duplicates": true,
    "exclude_cmd": true,
    "exclude_streams": true,
    "compact_sequences": true
  },
  "exclusions": {
    "legacy_stream_storage": true
  }
}
```

这是计划的附加片段；其余源节点和目标集群字段仍必须完整填写。修改策略会改变计划身份，需使用新工作空间和新归档。

工具先验证原始索引及权威副本，再处理选出的消息：排除 CMD 和流消息，然后对其余消息按同频道原序号保留重复键的最新记录，按剩余顺序从 1 编号。CMD 判断遵循固定 v2 版本的 SyncOnce、默认 `____cmd` 命令频道和 `systemcmdonline` 保留频道；不读取正文猜测业务类型。旧 CMD 会话/同步位置省略，普通会话、权限和未被排除的消息事件投影仍迁移。改变过原版 CMD 路由的自定义部署需要单独核对。

流消息按原 `Setting` 的 `1<<1` 标志或非空 `StreamNo` 识别，不根据正文猜测。流消息及其明确关联的频道 + ClientMsgNo 事件投影/游标只归档；同一事件身份还关联保留消息时拒绝转换，避免误删。两张旧流表仍由 `exclusions.legacy_stream_storage` 控制。CMD 与流标志重叠的消息归入 CMD 计数，流排除计数不重复计算。流消息先退出重复候选组，因此较新的流消息不会挤掉普通消息。

保留消息按原顺序连续编号为 `1…N`，原生 v3 首条新消息分配 `N+1`。全流频道保留频道/成员等业务元数据，消息尾部和读删位置归零，首条新消息为 1；不导入空号占位消息，不改变 v3 存储逻辑。重启和后续追加同样必须通过连续序号校验。

每条普通会话的已读和删除位置映射为：原位置之前（含当前位置）仍保留的消息条数。例如旧 seq 1 被删除、旧 seq 2 保留并改为新 seq 1，旧已读位置 1 映射为 0，不能直接跳到重复消息的保留位置 1。高于旧尾部的位置映射到新尾部；0 保持 0。原本就存在的序号缺口或与持久尾部不一致仍拒绝，不利用压号掩盖源缺失。

保留消息的 ID、ClientMsgNo 和正文保持原值；分配器基线仍覆盖被排除的旧 ID。两种重复规则的删除集合取并集；跨频道 MessageID 或保留候选又被淘汰时拒绝。原 MessageID 索引仍须指向该频道最大原序号，只允许已证明的重复写入形态，不能忽略错误索引。

成功 prepare 输出 `sequence_mapping`，包含文件路径、SHA-256、行数及 selected source 摘要。映射文件每行记录原频道、原消息序号、原 ID、源节点/摘要、目标序号、旧位置的映射边界和删除原因。被删除消息的 target_seq 为 0，不能当作存活消息；boundary_seq 才是旧同步/已读位置的映射。身份按原字节无损编码，特殊非 UTF-8 身份须使用迁移状态编解码规则。

归档转移到目标机器后，可以重新导出映射，无需挂载源库：

```sh
wkcli migrate export-map --plan /absolute/plan.json \
  --workspace /absolute/map-workspace --archive /absolute/source-archive
```

客户端还必须按迁移代次重建或映射本地消息缓存与旧游标，避免将旧序号当作新序号；该工作不能由数据库导入自动完成。工具不会宣称客户端处理已经通过，也不会自动切换。`verify` 从原始归档所选记录独立重建映射，不信任转换侧映射或成功计数，并检查目标中没有额外 CMD/旧重复消息及游标误映射。

新策略及真实备份结果见 [CMD 排除与序号转换报告](../../../../docs/superpowers/reports/2026-09-07-v2-migration-cmd-and-sequences.md)。

Preparation now captures raw original Slot config commands with node/shard
identity, ordered counts and hashes. The archive contains those bytes, so
`prepare` and archive reconstruction rerun the configuration/history proof
before selecting marked-channel records. A fresh `authority_digest` binds the
proof to the entire capture. Only proved replica supplementation and the exact
historical self-Leader no-op are supported. Markers and original versions remain
unchanged in selected source records; target placement uses the v3 plan.
Use a fresh preparation workspace for this capture format. Older archives lack
command evidence and cannot certify a marked source. Missing, altered or
misplaced captured commands fail before business selection. `authority` reports
are diagnostic outputs and cannot be supplied as approval files.

## 重复设备凭据

原版 v2 冷启动时，`GetDevice(uid, flag)` 从 UID 二级索引按设备 ID
升序查找第一个匹配设备。热缓存可能由另一条较晚写入的记录覆盖；停机后的
磁盘不能还原此前缓存。默认仍拒绝重复设备，消息的“保留最新”策略不适用于凭据。

运营方明确选择保留 v2 冷启动后的登录行为时，可在计划中加入：

```json
{"metadata": {"device_lookup": "v2_cold_start"}}
```

工具验证全部原始业务索引，按该规则选择一条完整设备记录，保留其 Token 和
DeviceLevel，不混合字段或接受多组 Token。选中记录仍须在所属 Slot 的正式副本上
一致。所有原始设备记录均留在归档，`selection.metadata` 记录物理重复组数、
被遮蔽行数和校验摘要；`PrepareArchive` 从原始行和索引重新选择，离线 `verify`
对比原生 v3 中的凭据。改变该选项须使用新计划工作空间及归档。

此选项不能证明登录行为与停机前缓存一致，也不解决重复会话、空身份、插件或
消息协议兼容问题。现场证据及剩余限制见
[元数据检查报告](../../../../docs/superpowers/reports/2026-09-07-v2-migration-metadata-lookups.md)。


## 重复会话与空频道记录

本次现场已选择设备冷启动规则。会话与空频道证明可分别显式启用：

```json
{
  "metadata": {
    "device_lookup": "v2_cold_start",
    "conversation_lookup": "v2_active_slot",
    "conversation_list_limit": 1000,
    "archive_empty_channels": true,
    "archive_user_timestamps": true
  }
}
```

`conversation_list_limit` 必须填写原部署的 `conversation.userMaxCount`，不能
为通过预检而调大。若业务方明确决定保留列表范围之外的有效持久化会话，可显式启用
`metadata.preserve_all_conversations=true`；原上限保持不变，报告记录超限用户数与最大物理行数。
工具跟随原唯一索引确定已读、删除及未读状态，要求所属 Slot
的全部正式副本一致。当前 Slot Leader 的普通会话列表按 UpdatedAt 选择原记录，
保留旧列表 version；若同时间的不同状态无法区分，或列表与唯一索引状态不同，
默认拒绝转换。经业务方逐组确认后，可在 `metadata.conversation_recoveries`
绑定 `node_id`、`logical_key`（`IdentityKey(uid, channelID, channelType)`）、
`indexed_sha256`（唯一索引原主记录的 `json.Marshal(Row)` SHA256）与 `rows_sha256`
（组内按物理 ID 升序，逐行原记录 SHA256 后加换行，再计算 SHA256），选择完整的原索引记录。
原行变更、绑定不符或未实际使用的决定均会拒绝；所有正式副本仍须通过状态比对。
CMD 类型遵循原按物理 ID 扫描覆盖的读取规则；批准排除 CMD 后不会
写入目标 CMD 会话。检查列表上限时计算去重前物理行，以及原停机缓存中实际需要
恢复的新普通会话。其余原行无损归档，不恢复为额外会话。消息去重、CMD 排除和
序号压紧仍由 `messages` 独立控制；已读和删除位置使用同一获准映射独立校验。

`archive_empty_channels` 只允许 ChannelId 空、ChannelType 0 的 ChannelInfo /
ChannelClusterConfig 管理记录对。必须无业务标志、成员计数和任何已支持结构中的
业务引用，且记录及保留配置命令在 Slot 0 正式副本上一致，最后一条已应用命令与
当前完整配置吻合。缺失、损坏、迁移未完成或无法解释的引用都会阻止完成。
所有原始记录和命令均保留在归档；`selection.empty_channels` 绑定完整捕获摘要与
证明摘要。归档重建重新证明，不接受外部报告代替源证据。独立计数记录及真正的
黑名单仍按原规则处理；此选项不能归档业务插件或绕过消息兼容检查。

`archive_user_timestamps` 仅将 User.CreatedAt / UpdatedAt 作为旧管理数据
完整留档。v3 原生用户表没有对应字段；工具不选择一个时间覆盖其他副本，也不
新增目标字段。启用后，用户副本比较仅排除这两个字段，其他字段仍必须一致。
`selection.archived_user_timestamps` 记录捕获的用户物理行数、实际时间字段数
及绑定节点身份和全部原行的 SHA256；归档重建重新计算。默认仍严格比较时间，
更改此选项必须使用新工作空间。此选项不能放行用户插件或设备凭据冲突。

## 正式消息副本的严格前缀差异

默认要求频道全部正式副本逐条一致。停机备份中若有副本仅缺少末尾消息，可在
新计划顶层显式设置 `"history": {"leader_quorum_prefixes": true}`。
工具仍只使用当前配置 Leader 的完整原行，并且同时要求：完整历史至少存在于
正式副本多数；其余副本逐字段摘要完全相同且是严格前缀；无中间缺洞、任期倒退、
未来任期、尾部错配或成员外历史；所属 Slot 配置及已应用进度一致，当前配置与
最后已应用命令完全吻合；保留命令中无删除、成员变更、未完成迁移或同任期换主。
无法确认时仍阻断。此规则不选择最长 follower，不改变源 Leader 或任何原消息，
不绕过业务元数据比较，也不证明历史客户端 ACK。

`selection.history_prefixes` 记录涉及频道、通过和未解决数量，并绑定所有原始
证据报告摘要。prepare 每次从捕获行重建证明；导出保留全部源副本，断开源访问
后的归档重建重新核对相同规则，不接受旧诊断或工作空间中的缓存证明代替验证。
源选择完成后，流消息、CMD、去重和序号映射仍由原 `messages` 策略处理。
更改历史策略需要新的计划工作空间。当前 Leader 为空而 follower 有历史的情况
不属于此规则，不能用这个开关解除阻断。

## 指定空 Leader 频道的历史恢复

业务方明确决定恢复已核验历史时，可在 `history.recoveries` 中列出具体频道：

```json
{
  "history": {
    "leader_quorum_prefixes": true,
    "recoveries": [
      {
        "owner_hash": "原始频道哈希的十进制字符串",
        "identity_sha256": "原始频道身份的64位小写SHA256",
        "capture_digest": "完整源捕获的64位小写SHA256",
        "proof_digest": "重建诊断报告的64位小写SHA256",
        "source_node": "已确认完整源节点的十进制字符串",
        "messages": 2,
        "history_sha256": "该节点完整原消息历史的64位小写SHA256"
      }
    ]
  }
}
```

示例中的标识和摘要是占位说明，必须替换成已审阅的原始诊断值。最多 1024 个
不同频道。指定节点必须是当前正式成员，至少正式多数持有相同完整历史，当前
Leader 必须完全没有消息及尾记录。工具重新核对完整捕获、配置命令和全部消息，
任何摘要、条数或副本选择漂移都会拒绝；未知异常、删除、字段冲突、中间缺洞、
任期倒退和未应用配置不会因为填写恢复决定而被忽略。

只从指定节点读取整份原历史，不合并节点、不更改原 Leader，也不修改 v3 存储
逻辑。原诊断保留 `unresolved` 和历史原因；selection 将恢复数单列为
`history_prefixes.recovered`，并把显式决定纳入摘要。归档重建重新执行全部检查，
未应用到任何频道的决定也会拒绝。恢复不是对旧消息已获客户端 ACK 的认定。

## 插件原程序与明确兼容范围

计划可增加 `plugin_artifacts`，每个源节点的每份原程序分别填写
`source_node`、`plugin_no`、绝对 `path`、精确 `bytes` 和小写 `sha256`。
源文件必须是普通可执行文件；捕获不运行或修改它。程序按 1 MiB 分块保存，
最多 1024 个源文件、每文件最多 512 MiB、最多 65536 个目标分配。原文件权限
也保存在捕获描述中。导出归档包含全部源程序分块；导入及验证只读归档，
不需要原数据库或程序路径仍然可访问。

当前唯一实现的 `profile` 是 `wk-ai-example-receive-linux-amd64-v1`，只接受
已审计的 `wk.plugin.ai-example` 程序：11856443 字节，SHA256 为
`671b3436d1a8d765371077009b1dfd6dec4528a1ce9cdc0dbebe2cfddc5b3224`。
每个原节点必须只注册这一插件，版本必须是 0.0.1、方法只有 Receive、优先级 1，
配置只有字符串 `name`；每个原节点都必须提供同一程序和该 profile，并通过
`plugin_configs` 显式统一配置。其他程序、方法、竞争插件或未映射绑定仍拒绝。
不填写 profile 只能得到捕获证据，不能完成 prepare/export/import。

目标应运行 Linux/amd64。工具按 `plugin_nodes` 选择程序来源，写入目标的
`plugins/<plugin_no>.wkp`，权限为 0500，再发布整代数据的完成标记。断点重试
可以续写本代保留的临时文件，不覆盖不同的已发布程序；缺失、额外文件、符号链接、
字节或权限差异均拒绝。`verify` 重新核对原归档字节、计划分配及每个目标的实际
文件，不依赖导入器的计数。具体现场目标仍须完成启动、Receive 和重启验收，
离线兼容报告不等于 `cutover_ready`。详见
[程序迁移报告](../../../../docs/superpowers/reports/2026-09-08-v2-migration-plugin-artifacts.md)。


## 已批准的缺失会话

订阅成员有保留历史但所有原副本都没有会话／待恢复意图时，默认阻断。
业务方可以批准仅对已核验的用户和频道补齐一个已读会话。在现有 `metadata`
配置内增加 `missing_conversations` 数组；每项包含：

- `capture_digest`：完整源捕获的 SHA256。
- `uid_sha256`：原 UID 字节的 SHA256。
- `channel_sha256`：`migration.IdentityKey(channelID, uint8(channelType))` 的 SHA256。
- `retained_tail`：排除和去重、压号后已核验的频道尾序号，必须大于 0。

摘要必须为 64 位小写十六进制；最多 1024 项，同一用户／频道不能重复。
工具重新检查所有原节点，任何原会话／待恢复意图、捕获变化、尾序号变化、未使用
决定或其他未批准的缺失会话都阻断。该决定写入计划和选择摘要，并在归档重建时
重新验证，不能套用到另一份源数据。

目标使用原生 `JoinSeq=1`、`ReadSeq=retained_tail`、`DeletedToSeq=0`，不虚构
原会话 ID 或时间。该会话会新出现在聊天列表，全部保留历史仍可读取，初始未读
为 0；下一条新消息按原生规则计算。独立校验从原始业务行重新推导预期值，检查
所有目标副本的已读位置和可见边界，不依赖转换器生成的会话行。

### Exact conversation replica decisions

`metadata.conversation_replicas` requires the original `v2_active_slot` lookup
policy and at most 1024 distinct logical groups. Each entry contains
`logical_key`, `source_node_id`, `copies_sha256`, and optional `archive_only`.
A retained group chooses one existing complete original record; an archive-only
group requires `archive_only=true` and `source_node_id=0`. The operator must
review the original states and whether restoring an isolated copy is appropriate.
No implicit majority selection or single-copy union is performed.

Build `copies_sha256` from the prepared metadata candidates after original
lookup reduction. Visit every formal Slot replica in ascending node-ID order.
For a present candidate, append `nodeID candidateSHA originalSHA\n`, with single
spaces and a real newline. `candidateSHA` hashes the exact `MarshalState`
bytes at `candidate/metadata/<20-digit-node>/Conversation/<logical_key>`;
`originalSHA` hashes the exact captured original row referenced by `source_key`.
For an absent candidate append `nodeID absent\n`. Hash the concatenated bytes.
Changed candidate/row bytes, changed absence, an absent chosen source, an already
agreeing group, or an unused decision fail. Original rows remain in the archive;
import and verify independently reconstruct these decisions. An archive-only
group with a pending recovery intent fails for a separate source decision.

### Sealed preparation for export

Successful preparation publishes `archive_seal` with version 1, a report SHA256,
a length-framed digest of all `source/`, `catalog/`, `selected/`, and
`plugin-artifacts/` rows, and their count. The source digest includes quarantined
originals. Export rechecks the stopped source and plugin bytes, validates the
report, and compares the exported rows against this seal before publishing
`COMPLETE`. It does not repeat semantic preparation. This is an export integrity
checkpoint, not independent target verification. Import and verify still rebuild
all checks from original archive rows in separate workspaces. Old preparation
workspaces without a seal require a fresh workspace with matching tools.
