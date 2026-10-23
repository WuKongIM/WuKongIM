# 方案 C：方法策略驱动的 RPC 流量模块

独立方案；未阅读 A/B。依据固定源 `103292a9e141414b6d1b876ea519cc90da11ecdf`（RPC 相关源与已合并 3768 相同）。本稿只设计，不修改源码、不运行 Go、产品、HTTP、DB 或新采样。采用 codebase-design 的 module / interface / seam / adapter / depth / locality 术语。

## 目标与证据边界

推荐把现有 `pkg/transport` 深化为一个拥有调度、预算、连接与终止语义的 module；不是在每个业务调用外再包一层 limiter。希望支持 foreground reads、foreground writes、bulk reads、maintenance 和协议流量，策略可配置，而普通 typed caller 无需知道 socket 数、priority、batch 等实现细节。业务合批、UID 去重、authority fences 和幂等恢复仍归原业务 module。

先前 5f 的 presence authority histogram 区间均值为 client 293.72ms、service wait 129.53ms、handler 113.18ms。它们是不同的一比三十二样本、包含排空，不能相减为某一次请求的耗时，也不证明共同根因。recovery64 唯一原规模试验同样未通过：约 2000.72 primary SEND/s，首个失败 periodic 在约35秒有20 firstfail/70017 sent；最终失败是 overall_first_attempt_rate。此结果拒绝把提高并发当作已验证的最终修复。

## 已有实现必须复用

| 已有事实 | 固定源码位置 | 设计影响 |
|---|---|---|
| 每 peer 连接池；cluster 默认16，`shardKey % PoolSize` 固定选 slot | `pkg/cluster/net/transport.go:14,148`；`pkg/transport/internal/peer/manager.go:88` | 不重造池；增加受预算约束的 pool group 和选池策略。 |
| 一个 connection 一个 reader/writer/pending table；四 lane DRR 权重8/6/4/1，共享 queued item/byte 上限 | `pkg/transport/internal/conn/conn.go:82`；`internal/sched/scheduler.go:86` | 单有高优先级不能抢占已经开始的大帧写；需要有限物理隔离及逻辑 class。 |
| ready frames 已批写；RPC/Bulk 可选 bounded coalescing，Control/Raft 到达可唤醒；cluster 等待默认0 | `internal/conn/conn.go:270–334`；`pkg/cluster/net/transport.go:15` | transport batching 与业务合批不同；保留前者，不新增通用业务合批框架。 |
| 每 service queued+executing retained bytes、可移除过期队列、并发 token、共享有界 executor | `internal/rpc/service.go:43,177`；`pkg/transport/server.go:83–129` | 保留真正所有者账本；在其上补 node/peer/class 汇总预算，不能把 queue depth 当执行数。 |
| negotiated budget/cancel、queued deadline；`CancelRunning=false` 保护已开始 mutation | `internal/conn/conn.go:186`；`internal/rpc/service.go:314–368` | 保留这些语义；把方法特征集中声明，不能由 caller 随意变更。 |
| request OwnedBuffer 按 backing allocation 计费；异步 reply 必须复制；已有 LocalNetwork adapter | `internal/core/types.go:314`；`internal/rpc/service.go:23,359`；`pkg/cluster/net/local.go` | 所有权已经存在；明确 reply lifetime，并补 response 在途预算。 |
| bounded observer drain、exact counter aggregation、一比三十二延迟采样、service wait/task/client 区分 | `internal/core/observer_aggregate.go:6,91`；`internal/app/observability.go:1068` | 不再造 observer worker；补缺失阶段和同一请求的 bounded sample 关联。 |

上述 `internal/...` 简写均相对 `pkg/transport/`，唯独明确写出的 `internal/app` 为仓库根路径。当前 cluster 静态 switch 已区分 Raft、Bulk、Control、RPC，也已有 read cancellation 白名单及服务默认期限；方案是把分散知识收拢到类型化注册表，不宣称现在没有这些能力。

## Interface：策略由注册者决定，调用者只拥有请求

以下是示意类型，表达契约，不是可直接提交的完整 Go 实现。保留 Client / Server 两个小 interface；生命周期 handle 只由 composition 使用，不流入 usecase。

```go
type TrafficClass uint8
const (
    Protocol TrafficClass = iota // existing ordered Raft/control traits remain distinct
    ForegroundRead
    ForegroundWrite
    BulkRead
    Maintenance
)

type MethodPolicy struct {
    Class     TrafficClass
    Execution ExecutionPolicy // CancelableRead or FinishStartedMutation
    Ordering  OrderingPolicy  // Unordered or existing OrderedProtocol
    Limits    MethodLimits    // request/reply bytes, queued/retained bytes, counts
    Deadline  DeadlinePolicy  // queue/execution ceilings; zero is never accidental infinity
}

type CallOptions struct {
    ReplyLimit uint32 // may reduce the method maximum; never raises it
    Affinity   uint64 // unordered methods only; ordered method's adapter owns its key
}

type Caller interface {
    Call(context.Context, PeerID, MethodID, OwnedBuffer, CallOptions) (OwnedReply, error)
    Notify(context.Context, PeerID, MethodID, OwnedBuffer) error
}

type Registrar interface {
    Handle(MethodID, MethodPolicy, Handler) error
}

type Lifetime interface {
    Stop(context.Context) StopReport // true Quiescent or explicit incomplete ownership
}
```

`OwnedBuffer` / `OwnedReply` 是所有权契约；复用现有 buffer 实现，常见 typed adapter 负责编码、解码和 Release，业务 caller 仍得到原 DTO。`CallError` 只通过 typed fields 区分 not-dispatched / outcome-unknown / completed-remote-error；不会从 error 文本推断。

方法 catalog 是启动前静态注册表，静态 ID/alias 和可取消性由代码声明。配置只能在代码允许的 profile 范围调整资源值；不能把 mutation 配成 CancelableRead，也不能把普通调用升级为 Protocol。新方法不必新增一个全局 service，注册后复用实现。未知、重复或不兼容 policy 在启动前失败。

若实施可配置 profile，沿用主配置 `wukongim.toml` 的 domain-grouped snake_case 与 `WK_` override，并同步 example/详细字段注释；不引入另一套配置文件。配置初期随 endpoint generation 固定，不做运行时热切换。

第一阶段不让 per-call options 改 class、priority、worker 数、重试语义或资源上限。调用者的 context 可以缩短期限；ReplyLimit 可以缩小回复预算。需要同一逻辑操作的 interactive 与 background 用法时，先由 typed adapter 注册两个明确 method binding/ID，复用同一 codec/handler，服务端能独立确定真实 policy。混合 read/mutation 的现有 service 在拆分前保守保留 mutation 语义，不能按未验证 caller flag 开启取消。这样初期无需偷偷扩展 wire priority 枚举或解析业务 payload。后续若确需动态 class hint，必须另做有能力协商、服务端白名单验证的协议扩展，不把它塞进本次最小实施。

使用例：

```go
// internal/access/node registers the exact typed read entry.
registry.Handle(MethodPresenceEndpoints, methods.PresenceForeground, endpointHandler)
registry.Handle(MethodPresenceEndpointsBulk, methods.PresenceBulk, endpointHandler)

// internal/infra/cluster chooses a fixed typed method, encoding stays here.
func (a *PresenceAdapter) Lookup(ctx context.Context, peer PeerID, request Request) (Result, error) {
    payload := encodeOwned(request)
    reply, err := a.rpc.Call(ctx, peer, MethodPresenceEndpoints, payload, CallOptions{})
    if err != nil { return Result{}, translateTypedError(err) }
    defer reply.Release()
    return decodeCopy(reply.Bytes()) // DTO remains valid after deferred Release
}
```

## Implementation：逻辑分舱与有限物理隔离

Method → logical class → pool group → peer slot → existing connection scheduler。建议只有 protocol、foreground、background 三类物理 pool group，logical class 区分前台读/写与后台工作；不是每 method 创建池。每 group 懒连接，预留与最大连接数都受 node 总连接预算约束，不能默认为每 peer 每 class 复制16个连接。具体池大小和权重由后续混合负载证据确定，当前不提出未经验证的性能数字。

复用 DRR 的 byte cost、batch 和 urgent wakeup；同一 foreground group 内使用 class 的最小保留份额、最大在途额度与有界公平调度，读、写都不能永久饥饿。Bulk 和 Maintenance 共享后台 physical group，但有各自预算和调度权重；maintenance 不能耗尽前台所有者或协议 reserve。允许借用闲置额度时只能借未保留部分，并保持归还规则，不靠任意全局优先级无限抢占。

Pool group 与 connection 上仍有硬帧/批字节上限。已开始的单帧写不可抢占；后台超大响应必须使用业务已有的有界分页/分块契约，不能仅提高 MaxFrameBodyBytes。TCP 对端、磁盘、网络带宽仍会共享，这种隔离不是全链路带宽保证。

Raft 保留原 sender 的稳定 stream→connection 映射及服务端串行执行，Append-before-Heartbeat 顺序不能因池化丢失。初期不改变现有串行 Raft 并发。池大小在 endpoint generation 内固定；重新连接交由现有 Raft 重传/恢复语义，transport 不暗中重试或重复 mutation。协议 class 的 reserve 也不能绕开总内存上限。

## 成本与所有权

预算分 node 总量、peer、class、method 四个维度；实际扣费覆盖 queued+executing request backing bytes、pending request 数、response 编码/排队/传输 backing bytes、connection/dial 数。所有 reservation 使用一个有明确 owner 的 lease，申请失败不保留部分额度，取消、错误、Stop 和成功都只归还一次。不能让“每连接64MiB × peer数 × pool数”变成未声明的 node 上限。只计算传输层能可靠拥有的 bytes/counts，不把 UID 或业务 facts 的猜测塞入通用成本模型。

`Call` 接管 request 后，业务不得再改写/复用 backing allocation；失败也明确由哪一方 Release，沿用现有实现的实际释放契约。reply 上限在方法注册时有保守 ceiling，调用者可收紧。小回复可以预留上界；大回复走已有分页/分块 port，在编码下一块前获取增量额度。服务端拒绝超限，不能先分配巨大结果再观察 bytes。typed handler 对业务结果的分配仍需自己的有界契约，transport 无法仅从返回的 `[]byte` 保证整个 handler heap。

## Deadline、结果与 Stop

queue deadline = caller 剩余预算与 method queue ceiling 的较小值；执行期限遵守 method execution ceiling。queue 中取消可证明未开始，并释放全部 owner。read handler 可跟随取消；mutation 一旦 started 继续受服务自身有界执行上下文保护，caller 超时仅停止等待。没有明确 not-dispatched 证据时，typed error 必须保留 outcome unknown，业务走原幂等/committed-proof 恢复，不能自动重放。当前 best-effort cancel 和 deadline 协商需继续兼容老 peer；不支持能力不等于远端已取消。

`Stop(ctx)` 先关闭 admission，取消并释放未开始队列，通知 cooperative reads，等待已开始 owner 和 reply 释放，再关连接与 observer。ctx 耗尽返回 StopIncomplete 及固定计数/remaining bytes；不能把当前100ms grace 返回误报为完全 quiescent。Go 无法强杀不合作 handler；未完成 owner 的依赖仍不能先关闭，composition 必须把 StopIncomplete 作为未完成清理或进程退出条件，不能隐藏到无人负责的后台 goroutine。此设计不声称一个短 timeout 能保证所有 started mutations 都已完成。

## 观测：一份调用样本贯穿阶段

已有 queue depth、retained bytes、inflight、admission 和 client/service histograms保留真实含义。新增缺失阶段：typed encode/decode、预算等待、peer acquire/dial、scheduler wait、socket write completion、remote service queue、executor/handler、response encode/queue/write、origin pending/response decode。每阶段记录固定 reason 的入/出计数、占用、deadline/cancel/overlimit；指标只用静态 method/class、direction、bounded peer 等固定维度，不用 UID/Channel/client message ID。

不是给每次调用开 goroutine 或全量 trace。复用 bounded observer drain，在 origin 只做一次有界采样决定；选中的调用携带固定大小采样信息，server 用相对 monotonic durations 回传，origin 汇成一个闭合样本；丢弃/不支持/缺阶段必须标 coverage unknown。跨节点时钟不能直接相减；不声称精确单向 network latency。client 总墙钟可扣 server residence 得 residual，但不能把不同阶段独立 histogram 的均值相减作因果。若需要新 wire timing envelope，应独立 capability/version协商；先补不需协议变化的本地阶段，再加受限诊断样本。默认不保留身份或 payload，低频诊断 ring 私有、有条数/字节/时间上限。

## Seam、adapters 与验证

依赖分类：调度/预算/策略校验为 in-process；cluster peer 是 remote-but-owned。生产 TCP 与现有 LocalNetwork 是真实两个 adapters；故障 TCP/loopback 可以替换网络行为，observability 作为现有非阻塞 adapter 注入。seam 留在 pkg/transport / pkg/cluster/net，内部 sched/peer/rpc 私有，不扩散给每个 caller。`internal/app` 只组合 lifetime/config，`internal/access/node` 声明 entry traits，`internal/infra/cluster` 仍映射 DTO/authority/error，usecase/runtime 不依赖 entry-specific frame、socket 或 concrete cluster constructors。single-node cluster 同样保留 authority 与 admission 契约。

先冻结失败矩阵再实现：多 peer 中一个慢 peer；大 bulk 回复与读写/协议同时运行；Raft 顺序与换连接；caller deadline 在 queue/dispatch/partial-write/handler 各处竞争；mutation unknown outcome；request/reply ownership只释放一次；node/peer/class/method预算分别满；receiver未读、半断连接；Stop中有已开始 mutation/不合作 handler；observer饱和/缺样本。测试跨 public seam，不写每个新 helper 的镜像测试；真实网络、阻塞和 elapsed-time 用 integration/process E2E。原2k population、expiry、首失败门禁必须继续独立运行，新的混合流量试验补充它，不能替换它。

## 取舍与实施顺序

C 的 depth 在于调用者只声明目标、方法和拥有的请求，复杂调度/回收/错误语义集中在一个 module；locality 优于在十几个业务包各放限额。代价是注册表和预算维度比最小接口多，错误配置和保留份额会损失利用率，物理隔离增加 socket/内存。因此只提供少数 typed policy profiles，启动时验证总预算，初期不做热更新、自适应权重、任意插件调度、自动重试或通用业务合批。

建议顺序：先收拢静态 catalog并补本地阶段观测，保持原行为；再引入 node/peer/class aggregate budget 与少数 pool groups；用混合负载和原2k门禁证明隔离有效；最后只在证据需要时加协商式闭合 timing samples。这个顺序不是当前 message_loss 的根因结论，也不授权新的测试、云采购、合并或发布。

## Source freeze

以下为本稿实际阅读的固定源；未引用其它设计稿。

| Path | SHA256 |
|---|---|
| `AGENTS.md` | `2220f49835b8dba20bafbc277854421222765be52e874fc6ee994bcd3ee0d746` |
| `CONTEXT.md` | `2c6c14ebb838dcfb498b6b6c130f0a500c21dcd3a49343bb467961921bd8168c` |
| `pkg/cluster/FLOW.md` | `0a7ba4b4094ba483f4b7bdc17a654496e7e1a3560349c3ddb4ae401f19633d36` |
| `internal/infra/cluster/FLOW.md` | `3ac522a7c276f2b5769df1fddd34d5785f02cb589500f3b9b38b219b821a2ed0` |
| `internal/usecase/presence/FLOW.md` | `f488255eef3f39a79ff2fd1afff662091fb7f8a59174f6c9fcaf941d1fe9b841` |
| `internal/app/FLOW.md` | `97552ae0d95e4957c4005b6c6af0a961414eb9f9dec93faf7e15d5a89b28405b` |
| `pkg/transport/config.go` | `e626d0dba425abd057caa17f711fd4e1e1fea75bb42fc814564e9d331de3d79f` |
| `pkg/transport/client.go` | `37eb6c62c720f19432b2df2e71eec676cdb3dafd7a073b5b745a77c3b39f1604` |
| `pkg/transport/server.go` | `595a6283946d6b8eb7a225f9c3fd87722c78cfdd3ed71d33e1c12d26c7edf19a` |
| `pkg/transport/internal/core/types.go` | `44b9b39ab50f1b82fd65a8650831bb560441b94512782aeb90d350e8391956d7` |
| `pkg/transport/internal/core/observer_aggregate.go` | `c01e278466ce980ba3137cc839ce4bb1c6ec631e26c91e5a4f382a736cc3200a` |
| `pkg/transport/internal/peer/manager.go` | `4f89ec54542db83e3d2cb88e585314ee098a5bf70ad053d1406672a105275af4` |
| `pkg/transport/internal/sched/scheduler.go` | `ae60b961a0e69dadf4a263a2371b7a57463c6b1aac219953de8c7626f316cb7a` |
| `pkg/transport/internal/conn/conn.go` | `4385f155f3e7305d51ae1215d1672fb1ab244545c3baf5d5eaef4c42385c8a51` |
| `pkg/transport/internal/rpc/service.go` | `d93fe20c2b6ee60bf7fcb9503042b8268ef90c828b821788330d01b74afa1172` |
| `pkg/cluster/net/transport.go` | `d0a3d07ecf1220069b9c374af65eaa4ee0b400312da6cfb730391c91be8b9be9` |
| `internal/app/observability.go` | `27eb7f43fc390a240988e56f92764929aee7432c594ef991d2515203718278b5` |

Skills: `/Users/tt/.agents/skills/codebase-design/{SKILL.md,DEEPENING.md,DESIGN-IT-TWICE.md}`。并行三种方案由 root 分配；本子任务只设计 C。
