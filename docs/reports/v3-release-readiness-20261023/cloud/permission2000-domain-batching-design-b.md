# 方案 B：领域感知的权威读取 Module 与异步流水线

这是独立设计，未阅读其它方案。依据 exact `103292a9e141414b6d1b876ea519cc90da11ecdf`。64 并发候选仍只是诊断：空载 32 个 cold 2-UID 调用的 64 RPC/2.88ms，以及预打包的 2 RPC/1.035ms，支持“合批可减少 RPC”，不证明 owner handler 慢，也不足以解释真实负载下的五秒等待。quota/authority 队列仍是待证伪假设。

## Interface：普通 caller 不负责批次协议

保留两个深 Module，不增加一个同时管理消息、presence、transport 的全局 aggregate。

```go
// Module 1: typed metadata facts; pkg/slot/proxy owns the implementation.
type PermissionFacts interface {
    ReadSendPermissionMetadataBatch(context.Context, []FactRead) []FactResult
}

// Module 2: typed presence proof resolution; usecase/presence owns semantics.
type PresenceRoutes interface {
    EndpointsByTargets(context.Context, []EndpointLookupGroup) []EndpointLookupResult
}
```

这里的 Interface 包括：一输入一结果，原顺序与重复项乘数不变；每个 caller 保留原 ctx；busy/cancel/stale/incomplete 保持显式；空结果只有完整证明才是 offline；既有请求、UID、字节及路由数上限有效。普通 caller 不学 Flush、batch ID、proof token、peer、RPC 重试或共享执行 deadline。

```go
facts := factsReader.ReadSendPermissionMetadataBatch(ctx, plan.Facts)
decision := policy.Evaluate(plan, facts) // business rules remain in usecase/message
if !decision.Allowed { return decision.Reason }
// Online Delivery still resolves exact current recipient targets through its port.
routes := presenceReader.EndpointsByTargets(ctx, targets)
```

比较过的另一种 Interface 是让每个 SEND 创建 BatchSession、手动 Submit/Flush/Await；它把封口、取消和响应配额重新推给所有 caller，Depth 较低，应舍弃。默认同步 Interface 可以保留，Implementation 在内部使用有界 pending requests、future 和分阶段完成。

## Seam 与 Implementation 隐藏什么

- metadata Module 隐藏 fact 去重、Slot/leader 分组、跨 caller 收集、fresh barrier、snapshot、映射/term 复核和索引展开；策略顺序、User Send Ban/Channel Send Ban 与 hooks 留在 message usecase。
- presence Module 隐藏跨 caller 的新 UID 集合、已有 UID flights、owner roster、分 owner 页、证明安装和重新读取 Directory；infra Adapter 翻译现有精确 RouteTarget，不能成为另一套业务规则。
- 两者各自拥有 admission/owned/executing/completed-but-joining 账本。执行宽度、call/key/byte/response 配额独立，不由并发数派生内存预算。UID 在受理时克隆；网络 body、结果页与 waiter 各自有明确 owner 和释放点。
- coalescing 尽量利用已到达的 preparation work，截止于既定短收集上限或最早活跃 caller 的预算；不增加每次 SEND 固定延迟，不把 5ms 当所有读取必付的税。公平调度保留大 caller 的进展，避免一个大页占住全部小请求。

## 两种证明生命周期不能合成一个“缓存”

**Permission**：cohort 必须在发起 fresh ReadIndex/apply 之前封口；迟到 caller 进入下一轮。只可共享此次证明之下的 facts，不能用完成的权限结果当 TTL 缓存。maintenance、mapping、leader/term 和 snapshot generation 继续复核。

**Presence**：新 UID 可在发 owner read 前组成页；已经存在的同 UID/五字段分布式身份 flight 可继续按原契约加入。完成只发布状态并安装 Directory proof，follower 再读当前 Directory；不把旧 owner 网络响应当新 caller 的 routes cache。authority/fence、owner boot、membership 完整性及已有 Directory invalidation/eviction 契约保留。失败与部分 owner evidence 不能证明 offline。

取消只移除该 caller 的兴趣，不取消仍被其它 caller 使用的执行，也不延长任何 caller 的原预算。最后一个 caller 离开后取消本地持有的工作；credits 保留到本地 Adapter 工作 join，Close 先封 admission 再 join。网络取消不等于对端已停：接收端保留自己的 request/response ownership 到实际完成，不伪造远端 join 证明。

## 4096 冲突是上线前置条件

把两个本来各可返回 4096 routes 的调用合并后，再以 cohort 总 4096 返回错误或截断，是可用性回归；把总上限直接乘 caller 数也不可接受。

完整跨 caller packing 需要一个**版本化、按原 call 隔离的执行协议**：每个执行 subround/工作结果页仍最多 4096 routes，原 call 的累计结果仍最多 4096。达到 aggregate 上限时，未完成 call 继续进入下一有界 subround；不能把该情况映射为原 call 的 route-not-ready，也不能丢弃已完成 sibling。continuation 必须有严格的 owner/boot、原 call ordinal、target fence、输入位置与逐 call route ledger；状态和结果页都有独立上限与原 ctx，关闭/取消清理。不是任意 transport failure 后的重试。

实现必须证明：一个原 call 横跨 subround 后仍保有原来允许的 4096，而不是每个 subround 获得新的 4096；所有 owner 证明齐全且最终 fence 验证完成才发布该 call。不得假设所有 target 是一个原子快照，也不得偷偷减弱当前读契约。未完成这套协议与测试前，旧格式只做不会超出原结果预算的保守合并/同 UID 去重；**不启用任意 disjoint-call 合并**。这是本方案最大的成本，不是一个通用 batcher 就能解决的问题。

## 异步 pipeline 与 SEND backpressure

先让同一 preparation 批次一次提交 facts，再统一等待结果，避免逐 SEND 串行 RPC。presence 冷证明在最终 hook 输出身份/target 已确定后，可作为受控预取与允许后的 append 重叠，但预取不授权 Online Delivery：提交后仍从当前 Directory 验证并形成原 delivery plan；append 失败只取消本 caller 的预取兴趣。不得缓存一份提交前 routes 直接投递。

真正释放 RPC executor worker 需要接收端 typed RPC Adapter 能“受理后延迟完成”。仅将读取挪到另一个 goroutine、让原 handler 等结果，不算异步设计。可增加窄的 opaque deferred-handler contract：enqueue 即释放执行 slot，但 accepted-request、payload、response credits 一直持有到 terminal completion；一次完成，取消和 shutdown 都 join。transport 只处理 opaque payload/ownership，不解析 UID、facts、ban 或 target。若这一能力未实现，则明确当前只减少请求放大，不能声称消除了 handler occupancy。

backpressure 向现有 SEND mailbox/preparation admission 传播：无读取 credit 时不继续从有界 accepted work 拉取并堆到第二个队列；busy 仍计入原验收失败，不靠重试藏住。已 durable append 的消息不能因后续 presence pressure 被当作未接受回滚；保持现有 Online Delivery 的完成/失败语义和原样本门禁。无需 Gateway 理解 UID policy，也不预先同时锁住两个 Module 的全部配额。

新版本只读 proof RPC 应独立注册并合作取消；保留现有 mixed authority/owner RPC 的 mutation 语义，不能直接把整个 mixed 路径改为 CancelRunning。opaque transport 不负责判断操作属于读还是写。

## Dependencies 与 Adapter

纯进程内依赖是 canonical key、ordinal、去重、proof/credit ledger。自有远端是 Slot metadata 与 owner-route reads，属于 **remote but owned**，在内部 Seam 使用注入的 typed port；app 是构造与生命周期 owner。

例如 OwnerPages port 提供对 owner/target 页的 Begin/Complete/Cancel（或等价内部 future）语义。生产 TCP Adapter 用固定 codec/capability、ctx 和 bounded owned buffers；Controlled Adapter 能 hold/reorder/fail exact page 并报告闭合阶段。两种 Adapter 经过同一 Module Interface 验证，真实 TCP 套件覆盖 codec、取消与接收端 admission。不能继续由 constructor 偷建不可替换的远端 Client，也不需要为本方案 mock 一个无关的第三方依赖。

## 可证伪门禁与迁移

1. 公共 Interface + 两种 Adapter：受控 32×2-UID 同轮聚合，证明去重与每个必要 owner/authority round 的 RPC 数减少；不能无视 placement 宣称整个三节点永远只有 2 RPC。caller 得到的 routes/facts 与独立执行对照完全一致。
2. 结果边界：两个各自合法的 4096-route calls 超过 aggregate 一页时，经有界 subround 均完成；一个 4097-route caller 只使自己失败；混合 stale/missing/success 保持原 identity alignment 和重复项。
3. fresh barrier hold 后加入 caller、期间 policy mutation、authority/owner boot/membership 切换，均不能借旧证明放行；同 UID follower cancel 不伤 leader，last cancel/Close 释放 payload、UID、response、request credits，且 held work 真实 join。
4. 受控下游迟滞同时验证 quota 与 RPC 往返闭合阶段、fair progress、有限队列/RSS/allocations；满载不得出现 admitted work 静默丢失或无限 goroutine。并用真正 TCP 重现相同失败条件。
5. 同一 exact binary、原三节点/原 2k distribution、原 ctx 和全部原门禁，无 profile 正式对照；RPC 数减少本身不算通过。首次失败、Online Delivery sample、内存和队列仍保留。64 诊断不能变成最终资格证据。

新增格式通过明确 capability/codec version 迁移；旧 peer 使用现有逐 call 路径，只有显式 unsupported 才选择兼容 Adapter，timeout/busy 不触发协议降级。先迁移现有 permission Module 与 presence caller 的稳定 Interface，再启用通过上述边界测试的 owner packing；移除被新公共 Interface 覆盖的浅层实现测试，保留真实兼容契约测试。没有公共 Interface 变化时普通调用者不需要同步重写。

此方案收益是把“每 SEND 自己筹备网络读取”的复杂度集中成两个有清晰证明生命周期的深 Module；代价是 shared-operation ownership、4096-safe 分段协议及接收端延迟完成。它比调高并发更接近放大问题，但需要上述实证，不能预先承诺修复真实五秒 timeout。

## 指令与源冻结

- `AGENTS.md` @ `103292a9e141414b6d1b876ea519cc90da11ecdf`: SHA256 `2220f49835b8dba20bafbc277854421222765be52e874fc6ee994bcd3ee0d746`
- `CONTEXT.md` @ `103292a9e141414b6d1b876ea519cc90da11ecdf`: SHA256 `2c6c14ebb838dcfb498b6b6c130f0a500c21dcd3a49343bb467961921bd8168c`
- `pkg/slot/FLOW.md` @ `103292a9e141414b6d1b876ea519cc90da11ecdf`: SHA256 `4b1f17c5f088e5d3454ba707eec9fc4f70ec830062a0bc830f117f298679ac07`
- `internal/usecase/message/FLOW.md` @ `103292a9e141414b6d1b876ea519cc90da11ecdf`: SHA256 `e02fa0e23332e80174eb4ba4ee9cd4ed078a311d8f436ece35fb3b4f1f6f9348`
- `internal/usecase/presence/FLOW.md` @ `103292a9e141414b6d1b876ea519cc90da11ecdf`: SHA256 `f488255eef3f39a79ff2fd1afff662091fb7f8a59174f6c9fcaf941d1fe9b841`
- `internal/infra/cluster/FLOW.md` @ `103292a9e141414b6d1b876ea519cc90da11ecdf`: SHA256 `3ac522a7c276f2b5769df1fddd34d5785f02cb589500f3b9b38b219b821a2ed0`
- `internal/app/FLOW.md` @ `103292a9e141414b6d1b876ea519cc90da11ecdf`: SHA256 `97552ae0d95e4957c4005b6c6af0a961414eb9f9dec93faf7e15d5a89b28405b`
- codebase-design `SKILL.md`: SHA256 `2c20617f87ec8af6a434859f381b2f061a69b530444e74eb39e78bb016a6d1e2`
- codebase-design `DEEPENING.md`: SHA256 `f3dd099ce99289bd213914d8ee3e2429b78309c3957ca4583f7659551b1d53c1`
- codebase-design `DESIGN-IT-TWICE.md`: SHA256 `8e740bf98446dbd4dfdc132ac4346d9a7eedaf93de6a495889171cf7f99f16bd`

此支是 root 并行 Design-It-Twice 的独立方案 B；没有读取其它方案，也没有再派生占用其余设计槽。未执行 Go/build/产品/HTTP/DB/profile，未修改产品。
