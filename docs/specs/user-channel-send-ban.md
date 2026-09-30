# 用户与频道发送禁令设计

状态：核心实现及主要功能/故障验证已完成，见[实施与验证记录](../reports/2026-09-24-send-ban-implementation.md)。真实单/三节点 backup/restore 已通过；真实插件进程及受控在途边界已补充验证（见[插件报告](../reports/2026-09-24-send-ban-plugin.md)）；完整压力和剩余故障矩阵验收仍待完成，不能据此宣称可发布。

关联：[Issue #973](https://github.com/WuKongIM/WuKongIM/issues/973)。代码基线：`64f73d99b3b0cb8960d40825f76053f5a0000dbd`。

本方案采用用户确认的两个独立维度：用户表的 `send_ban` 限制发送者；频道表的 `send_ban` 限制实际目标频道。旧数据迁移、历史字段回填、旧版混部和旧版接口语义兼容不在本次范围内。所有部署均为集群，默认 256 Hash Slots；单节点集群使用相同权限与持久化路径。

## 1. 目标与非目标

目标：

- 一次用户禁言写入影响该用户所有设备、所有接入节点和所有应用消息发送入口，不枚举频道或在线连接。
- 对私聊规范频道 A@B 设置频道禁言，同时限制 A→B、B→A；对群频道设置后限制群内所有发送者。
- 禁言和解禁在权威写入成功后，对随后开始的权限判定生效，不依赖 TTL、广播或重新登录。
- 以批量去重、按目标节点聚合、节点内按 Slot 读取减少跨节点权限查询 RPC，并明确 Raft 一致性读取成本。
- 保留现有模块分工：消息用例拥有权限规则，Slot/存储只提供原始事实与原子变更。

非目标：封号/禁止登录、踢下线、阻止接收或历史读取、群内单成员禁言、定时禁言、撤回已提交消息、跨用户/跨频道原子事务、全新权限服务、订阅广播一致性协议。

## 2. 设计时核实的基线实现

| 位置 | 当前行为 | 设计影响 |
| --- | --- | --- |
| `internal/usecase/message/permission.go` | 读取 `(FromUID, person)` 的 `Channel.SendBan`；系统 UID 和请求级发送有提前返回 | 改为显式用户事实；共同禁令必须先于原有豁免 |
| `internal/usecase/message/permission_batch.go` | 已对相同权限事实去重，群聊和私聊有批量计划 | 扩充共同读计划，避免为用户检查另起一轮 RPC |
| `internal/usecase/message/app.go` | 非零 `PermissionCacheTTL` 会关闭现有批量事实读取 | 禁令权威批量读取必须与这项缓存解耦 |
| `pkg/slot/proxy/permission_batch_rpc.go` | 最多 4096 个事实、4 个 Slot worker；一次请求对应一个物理 Slot | 改进传输聚合维度为目标节点；保留每个 Slot 独立权威校验 |
| 同上 | 普通权限读取没有执行与消息编辑读取相同的 ReadIndex/apply barrier | 单纯“发给 Leader”不足以证明隔离旧 Leader 不会返回旧状态 |
| `pkg/slot/proxy/message_update.go` | 已有 ReadIndex + durable apply + snapshot 的读取模式 | 复用这个模式，不复用其业务 DTO 或跨请求成功证明 |
| `pkg/cluster/node_meta.go:GetUserMetadata` | 根据路由定位 Hash Slot 后直接读取本机数据库 | 不能直接作为用户禁言的权威查询 |
| `pkg/db/meta/table_user.go` | User 仅有 UID、Token、DeviceFlag、DeviceLevel | 增加用户发送策略字段，并提供不包含 Token 的查询投影 |
| `internal/usecase/channel/app.go:UpdateInfo` | 以整条业务标志写入覆盖频道记录 | 禁言必须用原子字段修改；其他写入也不能意外重置禁言 |

此前运行过现有 `send_permission` E2E，证明旧实现可通过个人频道记录拒绝发送；它不能证明本文的新模型、解禁时效或多节点 RPC 目标。

## 3. 领域规则

| User A.SendBan | Channel A@B.SendBan | A→B | B→A（B 未被禁言） |
| --- | --- | --- | --- |
| 0 | 0 | 继续其他权限检查 | 继续其他权限检查 |
| 1 | 0 | 拒绝 | 继续其他权限检查 |
| 0 | 1 | 拒绝 | 拒绝 |
| 1 | 1 | 拒绝 | 拒绝 |

解禁只是解除对应维度限制，不保证发送一定成功；成员资格、黑白名单、频道终态等仍可能拒绝。

统一准入规则：合法请求、发送者未被禁言、实际源频道未终结且未禁止发送、通过适用的其他权限、通过发送前 hooks，然后进入 append。

### 3.1 频道身份

- 普通私聊：使用现有 person codec 将双方身份规范化为唯一频道，A@B 只是说明性写法，不能手工按字符串拼接或排序实现新规则。
- 管理频道禁令必须明确指定规范频道 ID；个人接收者 UID 不再表示全局用户禁令。
- CMD 消息继承源频道禁令，先移除配置的 CMD 后缀再检查；不能通过派生 CMD 频道绕过。
- NoPersist、SyncOnce、持久化消息均执行同一禁令检查。
- 请求级 `subscribers` 发送没有持久源频道，只检查实际 `FromUID` 的用户禁令，并保留此模式的独立投递语义；它不自动等价于向每个 A@B 频道发送。只有可信业务后端能调用该服务端能力，不能作为客户端绕过频道禁令的通用代发入口。若产品要求禁止任何 A→B 联系，应另建用户关系策略，不能宣称频道禁令覆盖无频道投递。

### 3.2 系统身份与错误优先级

新用户禁令和频道禁令不受 SystemUID、SystemDeviceID、插件来源或 SkipPluginHooks 豁免。原有系统身份对普通成员/黑白名单检查的豁免继续保留；管理禁言/解禁不是消息 SEND，不受禁令阻挡。PING、RECVACK 等协议控制帧也不受影响。

HTTP 省略 `from_uid` 时仍按既有配置解析为系统 UID，该 UID 也进入新的禁令判定。通过插件改变发送身份或目标若被允许，必须重新规划授权；hook 不得复用原身份的授权结果。

建议稳定判定顺序：请求错误 → 发送者禁言 → 源频道 Disband → 源频道 SendBan → 原有 Ban/成员/黑白名单等规则。基础设施错误发生在当前必须判定的事实时返回可重试错误，不假装未禁言；已证明用户禁言时无需因其他无关事实读取失败改变拒绝原因。

插件 `/message/send` 的旧 `SendResp` 没有 reason 字段。消息用例拒绝时，插件用例返回带内部原因的类型化错误，入口映射为 RPC error，正文 `message send rejected: reason=25`；不能返回成功状态和零 MessageId。成功响应格式保持不变。

两种禁令均复用 wire `ReasonSendBan`（25），同步更新文档描述为“发送被禁止”，避免增加客户端协议枚举。服务端诊断通过固定维度 `scope=user|channel` 区分，HTTP 禁令查询提供准确状态。

`Channel.Ban` 保留为现有兼容拒绝条件，其适用类型、豁免及 `ReasonBan` 不在本次扩展；`SendBan` 是所有源频道类型统一执行的发送开关。两者为 OR 拒绝关系，不互相赋值；清除 SendBan 不清除 Ban。这个范围限制避免将存量 Ban 重新解释为未经实现的“禁止登录/读取”。

## 4. 存储与写入

### 4.1 数据结构

| 实体 | 字段 | 约束 |
| --- | --- | --- |
| User | 新增 `SendBan int64` | 仅 0/1；属于 UID 路由键 |
| User | 新增 `SendBanVersion uint64` | 该用户发送开关的版本，初始 0，实际状态变化才递增 |
| Channel | 保留 `SendBan int64` | 仅 0/1；属于实际频道路由键 |
| Channel | 新增 `SendBanVersion uint64` | 独立于成员版本、目录状态和频道运行时 epoch |

版本用于管理并发修改和诊断，不是远端缓存可用性凭证，也不是 append 授权令牌。达到 uint64 上限时拒绝修改，不能回绕。对外管理 API 的 JSON 版本使用十进制字符串；内部 Go 间的强类型 RPC 保持 uint64 精度。

继续使用用户表与频道表，不建立第二份 ban registry。沿用现有主键、表 ID 与 256 Hash Slots 路由，用户与频道不强制共置；否则一个用户跨多个频道会产生多份用户状态。

### 4.2 原子操作

在 Slot FSM 中新增窄命令：`SetUserSendBan(uid, value, expectedVersion?)`、`SetChannelSendBan(channelKey, value, expectedVersion?)`。通过现有 proposal 路由到 owner，单条命令在 apply 时读取并修改目标字段，保留其他所有字段；不要在入口先 Get 再整行 Upsert。

所有能写 User/Channel 的创建、全量更新、Token 更新、Manager 编辑、目录初始化和离线导入路径必须审计：在线业务变更不得以零值意外清除禁令或回退版本。有意修改禁令必须调用同一原子语义；禁止遗漏字段被解释成解除禁令。

| 场景 | 结果 |
| --- | --- |
| 对缺失 UID 禁言 | 原子创建最小 User 记录（不创建 Token/设备），写入 true，版本 1；支持先封禁后首次登录 |
| 对缺失 UID 解禁/查询 | false、版本 0；不创建无意义记录 |
| 对缺失群/其他频道禁言 | 返回 not_found，不创建没有完整配置的业务频道 |
| 对缺失规范私聊频道禁言 | 允许原子创建仅业务元数据记录，版本 1；不创建 Channel runtime、不增加成员、不生成会话，阻止第一次发消息 |
| 对缺失私聊频道解禁/查询 | false、版本 0，不创建记录 |
| 已 Disband 的频道 | 拒绝修改 SendBan，保持不可逆终态 |
| 重复设置同一值 | 返回当前状态和版本，不增长版本；允许优化为不产生无效写入，但不能牺牲权威性 |
| expected_version 不匹配 | conflict，不修改；即使值相同也按 CAS 契约返回冲突 |

已存在用户可能仅有禁言元数据，Token 初始化必须保留它；并发 CreateUser 的已存在结果是可处理情况。删除用户若未来开放，必须明确会删除用户禁令，不能把临时删记录当解禁工具。

无 expected_version 的请求是按 Raft apply 顺序最后写入生效。网络超时可能已经提交，不能承诺 exactly-once。管理客户端超时后先权威查询，再带版本提交；自动化控制端使用 CAS 防止旧重试覆盖较新的反向操作。本版不增加持久 request_id 去重表。

### 4.3 格式范围

用户确认无需旧数据处理，因此不设计旧频道到用户的迁移、双读、双写或后台回填。实施按新格式、匹配版本集群部署，不支持旧新二进制混部或回滚读取新格式。

但仍须分配新的格式能力/格式版本并在启动时明确拒绝不支持的数据目录，不能让尾部字段变化表现为随机损坏。保留永久 ID；同步覆盖 FSM 编解码、快照、备份恢复、wkcli inspect/export/import 和结构校验。测试使用全新数据目录，禁止开发脚本自动删除既有目录。更改 durable 格式的具体版本号由实现时的 `pkg/dataformat` 目录分配。

## 5. 管理接口

新增以下窄接口，由现有服务端管理访问边界保护，不向普通 SDK 开放；不会因为能提供 `uid` 就授权用户修改他人权限。

| 接口 | 请求/作用 |
| --- | --- |
| `POST /user/send_ban` | `{uid, send_ban: 0或1, expected_version?: "N"}` |
| `GET /user/send_ban?uid=...` | 权威读取用户状态 |
| `POST /channel/send_ban` | `{channel_id, channel_type, send_ban: 0或1, expected_version?: "N"}` |
| `GET /channel/send_ban?channel_id=...&channel_type=...` | 权威读取源频道状态 |

字段缺失与零值必须可区分，非法 UID/频道身份、CMD 派生 ID、非 0/1 值在提案前拒绝。频道接口只接受实际源频道；私聊使用 codec 可验证的规范身份。接口不接受客户端提供的 Slot、Leader 或路由键。

成功响应沿用服务端 JSON envelope，data 至少包含实体身份、`send_ban`、`send_ban_version`；频道响应附带实际规范频道身份。状态分类明确：400 invalid_request、404 channel_not_found、409 version_conflict/channel_disbanded、503 temporarily_unavailable。错误不返回内部地址和任意底层错误文本。

旧 `/channel`、`/channel/info` 中的 send_ban 若继续暴露，语义统一为实际频道开关，字段缺省必须保留；显式传入则纳入同一原子更新。不保留 `(uid, person)` 表示用户禁言的特殊语义。涉及 send_ban 的 Manager 写入也统一。不能只增加新接口却保留旧整行覆盖漏洞。

单实体接口先交付，不新增无上限批量管理接口；未来批量仅按 Slot 分组、返回逐项结果，不承诺跨 Slot 原子性。

## 6. 权限事实规划：一轮读取

扩展消息用例现有 `PermissionRead`，增加 `UserSendPolicy` kind 和只含 `Found/SendBan/Version` 的结果。Channel 结果包含 SendBan 与版本，不返回 Token、设备或完整用户记录。

每次现有 SendBatch 的权限阶段：

1. 校验并规范化所有发送身份/源频道；保留输入索引与单项 deadline。
2. 每个唯一发送者产生一个用户事实；每个唯一源频道产生一个频道事实。
3. 合并既有成员、黑白名单、陌生人等原始事实；私聊的接收者策略与实际 A@B 频道策略是两件事，不能误合并。
4. 按 `(kind, authoritative entity key, channel_type, membership uid)` 去重，一次交给 infra 批量读取。
5. 结果按索引回填，在消息用例中按固定顺序评估；原有 hooks、person-directory、append 顺序保持。

单条发送也是一个只有一条消息的计划，不能落回“先查用户 RPC，再查频道 RPC，再查成员 RPC”。系统身份也必须产生两个禁令事实，只能省略它本来获豁免的成员类事实。请求级发送只产生用户事实。

批量读取是同一阶段的一轮并行分发，不能宣称它一定只需一次网络往返：超过并发上限、分块、ReadIndex 等都会增加耗时。不同用户/频道所属 Slot 之间没有全局一致快照，也不需要创建分布式事务。

不在权限阶段访问群成员完整列表；10 万成员群只做发送者相关点查询和名单是否为空的查询。禁言操作及其查询复杂度与群成员数无关。

## 7. RPC 聚合与一致性

### 7.1 从 Slot 聚合到节点聚合

infra 将去重事实解析为路由：用户事实用 UID，频道事实用实际源频道的既有 key，成员派生列表保留现有 namespace 与路由。黑白名单派生 key 未必与父频道同 Slot，不能假定共置。

根据一个不可变路由快照，先构造每个物理 Slot 的子请求，再将同一目标 LeaderNodeID 的子请求装进一个节点请求。请求中的每个 Slot 仍携带自己的 Hash Slot 映射及当前可获得的完整路由 fence（SlotID、Leader、Term、ConfigEpoch、RouteRevision）；**只在网络信封层合并，不把同节点多个 Slot 当成一个 Raft 组。**

新节点批量 RPC 使用单独注册的 typed service/明确的新 codec；不得改变现有 conversation metadata 读取对老 codec 的解释。复用既有集群 transport 和服务注册，不创建第二套连接或路由表。已核实 `Node.RPCService` 是 node-scoped，现有 Slot 参数被忽略，实际委托 `CallRPC`；因此按节点装入多个 Slot 子请求不需要改变 transport。新服务经 `Store.RegisterRPCHandlers` / `Node.RegisterRPC` 注册，外层调用不提供 Slot 授权，全部权威校验依据各子请求重算。服务只读取本机当前权威 Slot，不递归转发。

概念信封为 `Request{format, groups:[{slot_fence, indexed_reads}]}` 与 `Reply{format, groups:[{slot_fence, status, indexed_results}]}`；子请求/结果 index 仅在当前信封内有效，不能由客户端选取路由。不同 Group 的 leader hints 是重路由提示，接入方仍须重新读取自身权威路由，不能直接信任远端地址。

本机 Leader 的子请求直接调用相同读取实现，无 loopback RPC；本机仅是 follower/非副本时必须远程读取。

接收端每个 Slot 子请求独立返回 success、stale_route、no_leader、unavailable 或事实级 not_found。一个 Slot 失败不能抹掉同节点其他 Slot 的已验证成功结果。响应须校验完整索引、唯一性、数量、类型与 fence；缺失、重复、矛盾项为错误。

### 7.2 每个 Slot 一次 fresh barrier

每个 Slot 子批次：

1. 验证节点仍是该 Slot 的实际 Leader，所有 key 属于该 Slot，未处于 restore/maintenance 禁止区间。
2. 建立 fresh safe ReadIndex，并等待其 commit index 已 durable apply；生产路径不允许静默降级成本机副本读。
3. 建立该 Slot 子批次的只读数据库 snapshot，一次读取其所有 user/channel/member 事实。
4. 返回前重新检查 authority fence 与映射；变化则该子批次重路由/失败，不输出旧成功。

既有 Leader 本机缓存可在满足 fresh barrier 且与 snapshot/apply generation 一致时作为解码优化；首版可直接读 snapshot，避免跨批次缓存正确性复杂度。

不同 Slot 的 barrier 相互独立。并发只读请求若要合并 barrier，必须在 barrier 发起之前封闭 cohort；新加入的请求不能使用发起于它之前的旧 barrier。首版仅做一个请求/Slot 内共享，不另建跨请求合并调度器。

### 7.3 生效承诺

禁言接口成功意味着对应 Slot 的变更已 quorum commit 并 durable apply。之后开始的新权限批次会建立新的权威读取屏障，所以能看到已完成变更。解禁同理。

与变更重叠、或在禁言成功前已完成权限读取的发送可能提交；已提交消息继续交付。本版不承诺“禁言响应返回后绝对没有任何在途消息落盘”，这需要跨用户 Slot、频道 Slot、Channel append 的撤销/排空屏障，会显著放大协调成本。

一次批次内复用只覆盖批次预先纳入的消息；不能把之后到达的消息追加进已授权批次。内部路由读取重试只重试失败的事实组；上层重新发起 SEND 则重新授权，不跨请求复用已允许结果。

### 7.4 有界执行

初始实现上限如下，均为验收起点而非实测最优值：

| 项目 | 初始上限/规则 |
| --- | --- |
| 单权限事实批次 | 沿用 4096 个唯一事实；超出按原始消息边界切分，保持顺序 |
| 节点请求/响应 | 各最多 1 MiB，编码前按条目与字节预估分块，解码前验证 |
| 出站节点请求 | 每个计划最多 4 个同时执行；超出分波 |
| 接收端 Slot 工作 | 每个请求最多 4 个 Slot worker；再受节点级共享 admission 限制 |
| 节点级请求 admission | 最多 64 个在执行（每信封最多 4 个 Slot worker，全节点最多 256 个），另最多 1024 个等待且未解码字节合计不超过 16 MiB；等待上限 2 s 或更早的调用方取消/截止时间；满载/等待超限 typed busy |
| Deadline | 不晚于原 SEND 剩余预算；不同取消/预算 cohort 不让一个最短 deadline 毒化全部消息 |
| 重路由 | 最多 1 次刷新并重试失败 Slot 子组；沿用剩余预算；不扫描所有 peers |
| 生命周期 | worker 受现有 goroutine registry 和停止流程管理；所有读取 join，及时释放 snapshot |

4096 是一个分块上限而非无限制总请求许可，入口仍受既有 SendBatch 大小/负载限制；大请求不能分块后绕过总体限制。每个节点也要有 bytes/inflight admission，不能只靠单请求 worker 数约束全局压力。

初版执行满载立即拒绝，在真实集群的短时突发中造成 busy；因此增加上述有界等待。等待发生在远端解码之前，每个信封仍受 1 MiB 限制；本机与远端共享预算，不为等待请求新增重试或复用权限结果。取得许可后才开始新的 barrier/snapshot，等待期间的策略修改不能被旧事实覆盖。500 SEND/s 的并发突发验证后，等待上限调整为 2 s，并以 1024 个请求和 16 MiB 未解码字节共同限制等待；它是最大准入等待预算，不是 SENDACK P99 目标。能否满足持续负载仍须实测，不以队列吸收替代容量验收。

2026-09-30 的三节点 500 SEND/s CI 在预热期间出现权限读取排队拒绝及 SEND 超时；当 ReadIndex 平均往返约 30 ms 时，16 个独立信封的执行预算不足。受控回归中，64 个独立调用各等待 100 ms 新鲜屏障，原预算使本机与远端路径各 32 个调用超出 300 ms 截止时间；执行上限调整为 64 后全部完成。执行仍有固定的节点级上限（64 个信封、至多 256 个 Slot worker）；每个请求和响应各限 1 MiB，原 1024 个等待位置、16 MiB 未解码等待字节、2 s 等待上限及取消规则不变。持续容量以原 500 SEND/s CI 场景验收；跨调用方读取合并仍由 #977 跟踪。

缺失用户仅在权威读取确认 not_found 后视为未禁言；已验证缺失私聊频道可按既有建频道流程继续。超时、旧路由、解码失败和缺少结果均不能当作缺失。

## 8. RPC 成本与优化取舍

定义一个未分块、未重试的权限批次：F 为唯一事实数，S 为它们涉及的物理 Slot 数，R 为涉及的远端 Leader 节点数，S_remote 为远端 Slot 数。

| 方案 | 权限业务查询的跨节点 RPC | 关键性质 |
| --- | --- | --- |
| 对每条消息逐项查询 | 随消息数 × 所需事实数增长 | 串行轮次多，不采用 |
| 当前批量路径 | 约 S_remote 次 | 同节点不同 Slot 仍分别调用 |
| 本方案 | R 次；超过字节/条目上限则按节点分块增加 | 同节点多个 Slot 共用一个传输信封 |

例如一个批次涉及 20 个 Slot，其 Leader 都落在两个远端节点：当前通常是 20 个权限查询 RPC，本方案是 2 个节点批量 RPC。单条消息的用户与频道若在同一远端节点，可放在一个请求；分属两个远端节点就是两个并行请求；都在本机 Leader 时权限查询远端 RPC 为零。

这不是“全部集群流量降为 2 次”：Raft ReadIndex quorum 通信仍按 Slot 发生，消息 append/复制、首次 person-directory 等流量也另算。当前权限读取没有等价的 fresh barrier，所以新方案加强正确性后，**总网络消息量可能高于当前弱读取路径**，不能仅凭外层 RPC 数声称总性能提高。必须与具有同等一致性保证的逐 Slot 基线比较，并同时报告对当前生产基线的变化。

用户查询替换掉原来的 `(FromUID, person)` 频道查询，普通路径不会因为加用户表就天然多一轮串行读取。频道 SendBan 随本来就需要的源频道元数据一起返回；去掉重复的 terminal/channel flag 查询。已有 person-directory 使用的权威频道事实可以按现有同批次约束复用，但不把 permission result 当作跨阶段永久路由证明。

不采用以下方案：

- 把每个用户禁令复制到全部群：写放大与群数相关，漏掉未来新群，还需跨 Slot 收敛。
- 在所有接入节点缓存允许状态并仅广播失效：分区、丢广播、重启可能导致继续放行，无法满足生效契约。
- TTL 缓存 ban=true：虽然不多放行，但会延迟解禁；同样不符合双向及时生效。
- 为 ban 建一个中心查询服务或集中 Slot：增加热点、故障域和额外 RPC。
- 每条消息单独查 User RPC：破坏已有批量优势。
- 首版上权限租约：需租约撤销/到期、节点成员与分区处理，写入延迟和实现复杂度显著增加。

`message.permission_cache_ttl` 保留用于原有可容忍陈旧的辅助权限事实，不能覆盖 User SendBan、Channel SendBan、Disband，也不能关闭这些事实的批量路径。缓存未命中的辅助事实与强制权威事实一起装入本轮请求。若同一频道行已权威读到，应直接使用新鲜行，不能再用缓存行覆盖。配置说明明确该参数不影响禁令时效。

## 9. 模块与接口分工

| 模块 | 改动 |
| --- | --- |
| `internal/access/api` | 四个禁令接口、严格 DTO、错误映射；修正旧频道 DTO 的缺省字段语义 |
| `internal/usecase/user` | 设置/读取用户禁令策略，使用窄存储端口 |
| `internal/usecase/channel` | 设置/读取实际源频道禁令、身份验证、缺失/终态策略 |
| `internal/usecase/message` | 统一事实规划、用户/频道禁令判定、系统豁免位置、缓存隔离、原因优先级 |
| `internal/infra/cluster` | 映射 DTO、连接 user/channel policy 与 Slot 端口；不承接业务判断 |
| `pkg/cluster` / `pkg/slot/proxy` | 权威路由、节点聚合 RPC、Slot 子批次 fences/barrier/重试/限额 |
| `pkg/slot/fsm` / `pkg/db/meta` | 两种原子命令、字段/版本、snapshot 事实读取、所有写入保留约束 |
| `internal/app` | 注入端口及共享 admission/观察器，保留唯一 composition root |
| `pkg/channel` / gateway core | 不新增业务禁令规则；继续接收已准入工作 |

不新建聚合 UserService 或通用权限 DSL。消息用例使用已有事实批量端口的扩展，不通过用户用例逐项调用远端读取；业务规则保持在 message 模块内。批次基础设施既能处理 UID-owned 也能处理 Channel-owned key，不再将 User 伪装成 Channel DTO。

## 10. 可观测性

实现提供 `wukongim_message_permission_counts_total{kind}`、`wukongim_message_permission_duration_seconds{stage,result}`、`wukongim_message_permission_inflight` 和 `wukongim_message_send_ban_rejections_total{scope}`。继续使用已有 transport/Raft、Go 与 SEND 阶段指标进行对照：

- 拒绝计数：`scope=user|channel`；错误：`unavailable|stale_route|busy|invalid`。
- 每批输入消息数、唯一用户数、唯一频道数、去重前后事实数、节点信封数、Slot 子组数、请求/响应字节。
- 阶段耗时：plan、route、rpc、barrier、snapshot、evaluate；barrier 单列，避免 RPC 聚合掩盖 quorum 成本。
- 查询 RPC/消息、Raft 消息/消息、节点 inflight/拒绝数、CPU、分配、GC 与 SEND P99。

新增动态指标标签不放 UID、频道、Slot、节点 ID 或 request_id；沿用指标库已有的固定 node_id/node_name 节点标签。数量用直方图/计数，具体业务身份仅在受控定向诊断中使用。管理审计记录操作者、目标、旧新值、版本、结果；不记录 Token 或 payload。旧值由原子 apply 的内部结果返回，禁止先查询再拼接审计。Manager 使用已认证用户名；后端 API 没有已认证的人类操作者时明确记录 unknown 和真实 socket peer。提交错误记录 outcome_unknown 并省略旧新状态证明。持久化审计系统不是本次前置依赖。

## 11. 测试与性能验收

先建立失败场景和黑盒 E2E，再实现。使用新数据目录，单节点与三节点集群均为 256 Hash Slots；通过真实 HTTP/WKProto/公开观测验证，输出可重复的 JSON 结果，不以内存 fake 的通过替代集群验收。

功能矩阵：

1. 用户禁言覆盖私聊、多个群、其他源频道类型、多个设备、WKProto、HTTP、插件、NoPersist、SyncOnce/CMD、请求级 subscribers；未禁言用户不受影响。
2. A@B 禁言同时阻止双向发送，A@C 不受影响；群禁言不影响其他群；用户/频道独立解禁组合正确。
3. SystemUID/SystemDevice、SkipPluginHooks 无法绕过新增禁令；登录、接收、历史读取与协议 ACK 仍工作。
4. 已连接用户不重连即可在成功写入后的新请求观察到禁言与解禁；permission_cache_ttl 非零也成立；预热的 allow/ban/缺失缓存均不能覆盖结果。
5. 从节点 1 写入、节点 2/3 立即发送；覆盖 owner、follower、非副本接入、Leader 切换、隔离旧 Leader、重启、restore、缺失/重复 RPC 结果。
6. 写入超时后查证；CAS 冲突；Token 更新、频道元数据/成员更新不重置禁令；新 UID 与新私聊频道可预先禁言。
7. 用户禁言已明确时，其他 Slot 的无关错误不覆盖该拒绝；依赖事实不可用时不会发出成功 SENDACK、不会新增已提交消息。
8. 在途消息按契约允许完成；确定在成功修改之后发起的新 SEND 不得放行；拒绝结果须验证无新的持久化记录/投递，不能仅检查错误码。

RPC 与性能矩阵：

- 相同 UID/相同频道，多个 UID/同一群，相同 UID/多个频道，大量不同 UID/频道，单条与现有 gateway 实际批量分布。
- 10 万成员群中禁言和发送检查无全群扫描；包含 5000 个活跃频道与 4500 SEND/s 的参考压力档位，实际机器能力不足时如实报告，不以缩小规模冒充通过。
- 构造已知 leader placement，验证无分块/重试时查询信封数等于涉及远端节点数；同节点多 Slot 合并、本机 Leader 不走 loopback、不同节点并行。
- 验证 4096 facts/1 MiB 分块、4 worker、节点 admission、超载 backpressure、最短 deadline 隔离、路由变化只重试失败子组。
- 三组同机对照：当前基线；逐 Slot 聚合 + 同样 fresh barrier；本方案节点聚合 + fresh barrier。报告所有组的吞吐、SEND P50/P95/P99、查询 RPC、Raft 流量/字节、CPU、分配和 GC，不能只报告 RPC 数。
- 性能门槛初值：在同等 fresh barrier 的基线下，节点聚合的 P99/CPU 不应恶化超过 5%，查询 RPC 遵守上述计数公式；固定负载重复至少三次。若噪声/资源不足，标记未验证。若 barrier 成本使当前产品 SLO 不达标，停止发布，先依据测量优化，不能恢复陈旧读来换取通过。

预期 E2E 产物包含 commit、拓扑、配置、场景种子、工作负载、操作与 ACK 时间线、结果、受控身份、RPC/Raft 计数、延迟及资源指标；保留有界诊断与重跑命令。代码实施时按 E2E 目录规则新增 `send_ban` 场景和 AGENTS/catalog，并跑直接相关既有 send_permission/no_persist/terminal_disband 及新场景。若必须单独测试 codec/FSM 故障，先写失败清单和测试再写实现。

## 12. 交付顺序

1. 固化本契约、失败场景和 E2E 预期；分配格式/命令/RPC 标识，列出所有 User/Channel 写入方。
2. 实现字段、原子命令、窄读写接口、快照/导入导出；先证明并发写不覆盖禁令。
3. 实现 UID + Channel 统一事实计划、Slot fresh barrier 与节点聚合 RPC；完成路由/fence/容量校验。
4. 接入所有发送入口、修正豁免与缓存、管理接口和 app wiring；更新客户端原因说明、配置示例/说明、适用 FLOW、PROJECT_KNOWLEDGE、CHANGELOG。
5. 完成功能/故障 E2E 和三组性能对照，输出报告；指标不达标则继续优化，不宣称已完成可发布功能。

实施采用目录格式版本 2、Slot 命令 67（禁令）与 68（频道字段变更）、节点权限 RPC 91；十万成员建群的成员索引改用命令 69，按物理 Slot 有界合并，保留两提案并发和所有权/迁移/版本语义。普通更新与显式禁令变更区分。发送事实读取在整个 barrier/snapshot/fence 阶段持有维护准入，避免 restore 从中间切入。同预算但独立取消的消息共享封闭读取，逐条使用原 Context 判定取消。

## 13. 决策摘要

采用两个独立且持久化的发送开关，所有应用 SEND 共用一次事实规划；通过请求内去重和目标节点聚合减少业务查询 RPC，通过 Slot fresh ReadIndex/apply/snapshot 保证读取权威性。第一版不做跨请求权限租约或失效广播。旧数据不处理，但明确新格式与同版本部署边界。承诺新准入请求及时生效，不承诺撤销已获准的在途发送。
