# pkg/slot 边界

`pkg/slot` 是 Slot 维度的分布式元数据存储层。它只维护需要按 hash slot 分片、并通过 Slot Raft 达成一致的权威元数据状态。

## 只做

- `multiraft/`: 管理本节点多个 Slot Raft group，提供 open/bootstrap/close、step、propose、config change、leader transfer、compaction 和 status 等底层能力。
- `fsm/`: 解码并确定性 apply 元数据命令，校验 Slot/HashSlot 归属，用 `pkg/db/meta` 批量提交，负责 snapshot/restore。
- `proxy/`: 给上层提供分布式元数据 Store facade，按 key 路由到 Slot leader，提交写入命令，处理需要权威语义的 Slot RPC 读取。
- 元数据范围：User、Device、Channel、Subscriber、UserConversationState、CMDConversationState、ChannelRuntimeMeta、ChannelMigrationTask、PluginUserBinding 等集群共享状态。

## 不做

- 不决定 Slot 副本分配、扩缩容、rebalance、recover、hash-slot table 规划或节点生命周期；这些属于 controller/cluster。
- 不存储消息日志，不处理消息 append/fetch/replication、SendAck、投递、离线同步；这些属于 channel/message/delivery 相关包。
- 不做 HTTP、Gateway、管理 API、节点入口协议适配；入口放 `internal/access/*`。
- 不承载业务编排、权限规则、登录 token、发送规则、会话投影策略；业务放 `internal/usecase/*`。
- 不保存节点本地运行时状态，例如在线连接、mailbox、限流、插件进程状态；这些放 `internal/runtime/*`。
- 不做通用 RPC 聚合层；`proxy/` 只放直接查询或提交 Slot 元数据、且需要 Slot leader 权威语义的 RPC。

## 新增能力放哪

| 需求 | 落点 |
|------|------|
| 新增可复制元数据字段/表 | `pkg/db/meta` + `pkg/slot/fsm`，需要分布式 facade 时再加 `pkg/slot/proxy` |
| 新增业务流程 | `internal/usecase/*` |
| 新增节点本地运行时 | `internal/runtime/*` |
| 新增入口协议适配 | `internal/access/*` |
| 新增 Slot/节点调度策略 | controller/cluster 相关包 |
| 新增消息数据面能力 | channel/message/delivery 相关包 |

## 维护约束

- 修改流程时同步检查 `pkg/slot/FLOW.md`。
- 写路径必须使用稳定实体 key 计算 HashSlot；同一实体不能换 key 路由。
- 读路径必须明确是本地读还是 Slot leader 权威读；需要权威语义时不能退回本地全量扫描。
