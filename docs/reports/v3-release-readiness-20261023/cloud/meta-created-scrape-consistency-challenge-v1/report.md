# 单一假设静态反证：meta-created scrape 一致性

**在精确 d349 的实际生产 parser 链中，这个 global/per-Slot 撕裂假设不成立。** `MetaCreatedTotal` 不是另一个独立采样的全局 Prometheus counter；它是 parser 从同一批 per-Slot 样本直接累加出的兼容汇总。因此单次 Gather 即使不是全局时点快照，也不能仅由并发 Inc 让这个汇总与其自己的 per-Slot sum 不相等。

- producer 只有一个 `slot_id/result` CounterVec（`pkg/metrics/channel_runtime.go:194–197`），每次 `ObserveMetaCreate` 只 Inc 其中一个 counter（`:531–536`）。client_golang 固定 v1.16.0 的 `counter.go:144–151` 用 atomic.Add/Load 读写整数；`vec.go:345–353` 的 RLock 保护枚举，`registry.go:411–457,632` 分别调用 Collect/Write，不承诺所有 series 同时采样。
- `Registry.Handler` 只 Gather 一次（`pkg/metrics/registry.go:106–130`）；新名称 alias 从已收集 DTO clone（`:133–165`），不会再次取值。target 只识别原 `channelv2_meta_created_total`（`internal/bench/target/client.go:1251–1254`），不会叠加 alias。
- parser 每次创建新局部 snapshot（`:1131–1134`）。同一条唯一样本在 `:1328–1340` 同时写入 slot 的 uint64 值并累加 `MetaCreatedTotal[result]`；重复键拒绝。`:1420–1430` 要求精确整数且 ≤2^53，`:1337–1338` 在汇总溢出前拒绝，因此接受的汇总不存在浮点舍入差。缺少的 slot 在两种表示中都为0。
- `MetaCreateAccounting.Checkpoint` 确实在 `internal/bench/chatlifecycle/lifecycle_proof.go:1027–1028` 拒绝 scalar/sum 不一致；但在上述 parser 输出中此等式由构造成立。`production_meta.go:138–144` 会把非 product 的 preview 错误立即返回，这不改变前提反证。

没有识别到能在**未改动合法生产 pipeline**中对此假设产生确定性 RED 的 seam。直接伪造不一致 MetricsSnapshot 能触发 accounting guard，却绕过真实 parser，不能作为 Inc/Gather 问题的复现。若以后需要确认性回归，可在现有 parser / 内存 RoundTripper seam 输入明确混合时点但规范的 series，验证两表示仍一致；不需要为已反驳前提增加锁。这里只讨论测试设计，未写或运行任何测试。

一般意义上的跨 series/节点快照并非全局原子切面，仍应避免把它当作同时刻业务真相；这是一条解释边界，不能升级成这里的 HarnessInvalid 原因。实际失败 round 和 terminal worker vectors 未保存，六份附近 scrape 的 synthetic 重放通过也不能证明原 checkpoint 成功。本次仅反驳该特定 scalar/sum 竞争假设；实际 leaf 与投递根因仍未知。源码/工作树保持 clean，无编译、服务、请求、采样、云或指标修改。
