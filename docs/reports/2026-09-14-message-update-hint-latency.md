# 消息编辑提示时延优化

## 结论与范围

冷频道的提示等待主要来自任务发现：原 worker 每 200 ms 选择最多 4 个
Hash Slot；256 个槽在没有活跃任务时需约 12.8 秒遍历一轮，存在活跃任务时
还要分出扫描配额。编辑提交没有主动唤醒调度。

在相同 SDK、相同单节点集群及 256 个 Hash Slot 下，增加有界提交后唤醒后，
双客户端流程观察到的等待从秒级降到约 0.2 秒。这里只优化在线通知的任务发现，
不改 CMD、编辑接口、持久化格式、历史查询、未读和会话排序语义。

## 实现

- 成功 CAS/幂等响应只把返回的消息身份、序号和版本交给现有 worker；
  请求不等待投递，不携带正文、不枚举群成员、不新建 goroutine。
- FIFO 最多 1024 个消息身份；同一消息合并到较新版本，频道 ID 超过 1024 字节
  不进入快速队列。队列满、停机、派发失败都由原持久化 pending 扫描兜底。
- 快速波次最多 8 次访问，每次最多 4 页订阅者，超时 1 秒；大群未完成任务放回队尾。
  已到期的后台扫描优先于下一波快速处理，原扫描频率和扫描预算没有提高。
- 两条调度路径共用权威读取、订阅者分页和 previous-UID CAS；非 Leader 接口节点
  也通过集群路由读写。并发补偿可能重复发提示，SDK 仍按幂等提示合并。
- 停机与恢复会等待 worker 退出并清除内存队列；持久化任务仍是恢复依据。

## 实测

SDK 固定为 `WuKongIMJSSDK` 提交 `3165552`；服务端基线为 `c1db384a8`，
即已修复设备身份投递、但尚无提交后唤醒的代码。候选在其基础上只增加本次优化。
Node.js 22、Chromium、Apple M4，本机环回 BFF，自建单节点集群，256 Hash Slots。

| 样本 | SDK 双实例流程 | 双 Chromium 页面流程 |
| --- | ---: | ---: |
| 上次完整基线 | 3934 ms | 11877 ms |
| 本次独立基线复现 | 3930 ms | 未运行 |
| 候选第 1 次 | 204 ms | 210 ms |
| 候选第 2 次 | 153 ms | 205 ms |
| 最终调度代码复测 | 170 ms | 205 ms |

以上沿用测试脚本的 `onlineHintLatencyMs` / `browserHintLatencyMs` 字段，但测量
终点包含另一端正文已更新，不是纯网络 EVENT 时延。SDK 双实例流程还包括一次
故意丢失写回执后的幂等重试；浏览器流程包括点击与页面等待。每次均要求收到真实
`message_updated` EVENT，并验证正文、预览、未读保持、冲突、离线恢复和两个会话接口。
1 秒是本机比较用的通过预算，不是生产 SLA；这些样本不能推导 p95/p99。

队列合并微基准：`BenchmarkCommittedCoalescing` 为 **43.92 ns/op、0 B/op、
0 allocs/op**。这是同一目标更新的入队成本，不代表分布式派发吞吐量。
本次不使用 CPU pprof 归因等待：同一代码路径的旧/新端到端时序与冷槽断言已经
区分轮询等待和处理耗时；CPU 采样不能直接量化等待中的轮询周期。

## 验证与复现

- 同一 `TestMessageUpdateSingleNodeClusterHTTPFlow` 在基线失败：
  `committed hint exceeded the 2s integration budget`；候选通过。
  该断言检查提交后的独立 pending 行已被完成删除，读取仍经过权威屏障。
- 单元测试覆盖仅成功提交才调度、使用幂等返回版本、无正文有界队列、合并、
  大群波次让出、失败交回补偿；原 100000 人群分页测试继续通过。
- race 测试覆盖并发入队/停机、重启补偿、持续入队时后台扫描仍能执行。
- 确定性调度测试覆盖提交唤醒与扫描时钟同时就绪：处理扫描不能吞掉唯一唤醒；
  即使一次扫描超过时钟周期，快速任务也能在下一次扫描前执行一波。
- 三节点集群只保留非 Leader 接口节点的 worker：节点 1 接收编辑、节点 2
  作为 Slot Leader，远端 pending 行在预算内完成，证明快速路径没有绕过集群权威。
- 类型边界及 `internal/app` 单元测试通过；`flow-doc-contracts` 通过。
  FLOW 索引同步更新，并修正基线已有的 `pkg/cluster` 行数漂移；已有长度警告保留。

```bash
GOWORK=off go test ./internal/runtime/messageupdates ./internal/usecase/message ./internal/app -count=1
GOWORK=off go test -race -tags=integration ./internal/runtime/messageupdates -count=1
GOWORK=off go test -tags=integration ./internal/app -run '^TestMessageUpdate(HintFromNonLeaderAPI|SingleNodeClusterHTTPFlow)$' -count=1
GOWORK=off go test ./internal/runtime/messageupdates -run '^$' -bench BenchmarkCommittedCoalescing -benchmem
```

浏览器比较复用 SDK 的 `tests/message-editing.integration.cjs`，分别给
`WK_EDIT_SERVER_BIN` 指定基线与候选二进制，`WK_EDIT_PLAYWRIGHT` 指向已安装的
`@playwright/test`。读取输出 JSON，断言上述两个等待值均不超过 1000 ms。
本次未购买云资源；还没有验证生产高并发、网络拥塞、完整 100000 人在线群的提示
分布或长时间稳定性。队列溢出和失败补偿路径仍可能经历原有扫描等待。

## 固定规则来源

源版本 `c1db384a8` 的适用文件 SHA-256：

| 文件 | SHA-256 |
| --- | --- |
| `AGENTS.md` | `c6eae7244b1c660be4b2a62cbdc1efa5c26c35e34972e994df7e6ae073fc9d0b` |
| `internal/app/FLOW.md` | `2300ec9f09d8247b48d978a17638545ceacab9abca39abd1d3250b6e1b6d983c` |
| `internal/usecase/message/FLOW.md` | `89742aed3469f18b344ebf314c1b2ef87e9513b7261582d8f2d04b6c46e2d82d` |
