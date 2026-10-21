# 迁移全重启失败：独立只读错误分类审查

源码固定 `9a4900dc67ac674791edba11ea9a719cd91ec570`。本次未编辑仓库、未运行测试/构建/容器，也未调用云或 CI。源码与规则逐项 Git blob/SHA 核对在 `source-freeze.json`。以下是代码证据支持的修复边界，不是对原失败每个 voter 的已观测归因。

## 当前事实与未知

原始 JSONL 的 1→3 用例在导入后、协议追加后，三个节点分别读到 3/4 行；全重启后节点1的 `/channel/messagesync` 返回400：`internal/message: append failed: channel replication: durable quorum unavailable`（第26行）。三个进程仍存活、三个 `/readyz` 返回200；其余四拓扑通过。诊断尝试读取的 app/error 文件不存在，因此没有记录 barrier 具体 voter 的状态、原始错误或调用栈。有限原件摘录与完整输入摘要在 `observations.json`。

这可排除“这次400就是连接被拒绝/进程已退出”；不能排除通道尚未恢复、存储失败或无效证明。此前自然 CI 的连接拒绝是另一观测，不能以本次400替代其根因。全局 ready 不等同单个通道已取得恢复 HW（`pkg/channel/reactor/FLOW.md`、`pkg/cluster/FLOW.md`）。

## 实际错误路径

- `quorum_log.go:99–108` 只给 `errRecoveryProbeIncomplete`、`errRecoveryQuorumUnavailable` 补公共 `ErrNotReady`。第191行起的 current-term barrier 失败直接返回原错误，尚未发布 ready/HW。
- `recovery_barrier.go:53–58` 调用真实 durable round；该轮在 `quorum_round_state.go:213–268` 验每个独立 voter，但耗尽后统一返回 `errDurableQuorumUnavailable`。本地非冲突存储错误、远端冲突、无效响应、失联都可到这里；目前没有保留完成项 `err`。本地显式 Conflict 特例直接返回 `ErrLogConflict`。
- `reactor/quorum_runtime.go:118–139` 将 install 失败原样完成 waiter，保持 `CommitReady=false`。`internal/infra/cluster/message_reader.go:65–75` 经 `mapAppendError`，未知 sentinel 在 `error_map.go:43` 包成 `ErrAppendFailed`。`channel_messagesync.go:70` 仅已知暂态走503，否则旧400。现有 HTTP 路径没有错误：丢失分类发生在它之前。

## 能区分什么，不能区分什么

| 已有证据 | 具体实现 | 恢复分类边界 |
|---|---|---|
| 真实本地 Durable + 足够独立 Durable/AlreadyDurable | round 248–254；peer 586–589 核 exact LastOffset/proof | 成功；不能要求所有 voter 都成功 |
| follower 明确 NeedFrom | dispatcher 90–95；peer 590–591 校验范围；memory 389–397 在 expected base 高于 LEO 时产生 | 有类型的缺口证据；可作为有限恢复暂态候选，不是持久票 |
| 本地非暂态 I/O、InvalidConfig 或 Conflict | runtime 445–469、store_adapter 279、round 231–234 | 不得被其他远端暂态盖成 NotReady；保留原始因果身份 |
| remote Conflict / Unknown / malformed proof | dispatcher 101–117；peer 555–565/615–623 | 未证实暂态；不靠错误字符串或 repair 已调度判断 |
| remote Backpressured | dispatcher 106–110 | **当前不是严格的“只有饱和”证明**：exchange_server 343–350 把所有 DefinitelyNotWritten+非空Err（除三类 fence）编码为它 |
| 任意 repair task 已调度 | dispatcher 77–80、101–116、130以后 | 各类失败都会调度，不能作为可重试依据 |

第二层信息丢失并非假设：`store_adapter.go:279` 对 Factory 打开失败生成 DefinitelyNotWritten+原错误；`store_adapter.go:686–695` 对 InvalidConfig 也生成 DefinitelyNotWritten；`store/channel_adapter.go:308–317` 编码/输入失败同样可能该 outcome。上述值进入 `exchange_server.mapMutationResult` 可被标为 Backpressured。只在 round 把全部错误 `errors.Join` 给上层，会令一个暂态原因使混合永久失败也被 `readavailability.Unavailable` 命中。

## 建议最窄修复

1. 只在原 durable-round / current-term-barrier 边界携带有界失败事实：本地是否持久、失败 voter 的已验证类别，以及须保留的本地/永久 cause。既有 distinct-voter 和 MaxVoters 边界即可，无新重试框架。
2. barrier 仅在本地已持久且失败集合被证明属于闭集暂态时取得 ErrNotReady；永久/无效/未知分类保持失败，取消保持取消。保守首切片可只承认经过校验的 NeedFrom；不要因为任何一个暂态票存在就判整轮暂态。普通 Commit 的 outcome/幂等语义保持不变。
3. 若本切片需要涵盖 remote Backpressured，应先把现有编码收窄到真实 typed admission 原因；未知 DefinitelyNotWritten 可复用既有 OutcomeUnknown 而不是新增 wire 状态。否则不能声称永久远端错误不被升格。StaleFence 已是既有临时权威错误，但也需保留混合失败优先级。
4. 暂不把所有网络/未知错误加入新闭集；确需增加时在仍有具体 typed transport cause 的边界区分 malformed proof / storage / panic。不要改 HTTP 文案匹配、扩大 E2E 接受400、增加业务重试次数或降低 ready/HW 证明。

这些建议修复分类能力，不证明当前 Mac 首失败恰属闭集中的哪一种。若尚未捕捉本次具体 cause，应报告为“分类缺口已复现，原失败 voter cause 未知”，避免把测试注入当现场原因。

## 最小先行回归矩阵

诊断作者的真实 Install + 三个真实 MemoryStore + 旧 authority 一条已提交记录，随后增加 term/fence 触发 barrier，是正确 seam。旧 `TestQuorumLogInstallPreservesRecoveryFailureIdentity` 以相同 authority 恢复，其六分支不覆盖本次 barrier。

1. 正常旧→新 authority：真实 barrier 完成，旧业务记录身份不变，正确 HW/LEO；清故障后恢复只产生正确 deterministic barrier。
2. 本地 durable、两个远端明确 NeedFrom（真实合法 follower/offset）：失败不发布 ready/HW；故障清除后同 authority Install 成功。
3. 真实 wire Backpressured 对照；另用 DefinitelyNotWritten+InvalidConfig/任意 storage cause 经过原 mapMutationResult，揭示并锁住不能伪装暂态的编码边界。
4. 本地 Sync 原始 storage 错误、local Conflict；分别断言原身份、不含 NotReady、不能成功读取/追加。
5. 两远端 Conflict 与 malformed proof/Unknown：不含 NotReady，不发布 frontier。
6. 混合票（NeedFrom/Backpressured 与 Conflict/Unknown），以及本地永久失败+远端暂态：不因单个暂态错误被上层任一 errors.Is 命中而误判。
7. caller cancellation、重复/迟到 voter 回调：保持取消和原 distinct-voter/一次完成门槛，不以新分类改成功标准；已有测试可复用。
8. 原独立 local/read-RPC/API typed503 与永久400 回归，证明无新全文/substring 匹配；最后仍需真实五拓扑 E2E，而此只读报告不宣称测试已通过。

本次只读取证没有引入修改。Fixture v2 的三个既存改动保留，未触碰；规则导航覆盖列在冻结清单，FLOW 均具有合法 scope。
