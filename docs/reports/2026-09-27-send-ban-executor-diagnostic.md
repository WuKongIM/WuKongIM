# 持续失败的复制与提交执行栈

**本轮确认节点 3 出现 Pebble memtable 堆积导致的写停顿，尚未确认
flush 变慢的原因，也没有修复持续资格。** 同一正常生产二进制、同一
5000 频道/4500 SEND/s/30 分钟目标在发送 170.560 秒、767,523 次调用后
再次 sender_read EOF。总耗时 410.774 秒，磁盘保护未触发。

## 变化与验证范围

只扩充 E2E 失败栈过滤器，加入独立的 Channel replication、MessageDB
及 commit/engine worker，将固定输出上限从 64 KiB 改为 256 KiB。
单节点输入仍最多 2 MiB，三个节点共用三秒采集预算。没有修改生产代码、
默认配置或验收条件；本轮无并行编译、其他负载、测试或 CPU/heap profile。
goroutine profile 在失败后采集，既有 driver/stall 探针与上一轮相同。

新 harness 编译通过；本轮真实 E2E 负载失败，不能报告为 E2E 通过。
诊断输出包含三个节点、约 58 KB，没有达到过滤输出上限。当前工具没有
保存原始响应大小，不能证明单节点输入没有被 2 MiB 上限截断；这里的
结论基于实际捕获到的正向证据，不用缺失某种栈证明该工作不存在。

## 断连前后观察

断连前最后三轮，25 条连接的最早未确认消息都对应节点 3 的 active
Leader，序号均未前进，LEO/HW 均为 153/153、epoch=1。公开查询每节点
耗时 0.23–0.55ms。三轮 SENDACK pending 从 3938 增至 5063、6188，
最早年龄从 928ms 增至 1178ms、1428ms。全窗口 682 份快照、12 次节点
查询、零查询失败。失败报告的 pending=6460 来自稍后、不同语义的计数。

失败后的执行栈显示：

- 节点 3：一个 commit coordinator 停在 Pebble
  `maybeInduceWriteStall` 的 `db.go:2644`；本机模块缓存及二进制依赖
  都对应 `github.com/cockroachdb/pebble/v2 v2.1.4`。该行是 memtable
  队列达到停写条件后等待 flush 的分支，**不是** L0 read amplification
  停写分支；依赖源文件摘要与定位保存于产物中。
- 同节点有 23 个本机 quorum worker、32 个副本 RPC handler 停在
  MessageDB 提交的 `Coordinator.submitResult`，85 个 Channel worker
  等待 quorum 结果。另一个 coordinator 在等待新请求。
- 节点 1/2 各有 16 个 peer exchange 调用等待传输结果，各自两个
  coordinator 在等待新请求。该栈不直接暴露远端节点身份，不能仅据此
  把每一个 RPC 都指定为发往节点 3。

公开关闭计数只在节点 2 出现一次 `async_dispatch_queue_full`；三个节点
各有一次 peer_closed，后者不归类为过载。permission busy=0，追加路由
峰值 33.20%，SENDACK P99=312ms。最近十秒 driver 每 100ms 最多 452
次调用、单连接最多 19 次，最大调度滞后 2.18ms、socket Write 0.64ms。

这些时序与栈支持“一个节点的存储写停顿，经跨节点追加和连接内等待
放大为 gateway 队列满”的解释。profile 比断连前快照晚，通用 coordinator
标签也不标识物理 DB 实例，故不能声称已经建立逐请求、全阶段因果追踪。

## 下一步

不能通过增加 memtable 停写阈值或放松 durability 冒充修复。需要继续查看
Pebble 独立 flush/compaction 执行栈及已有阶段指标，区分磁盘同步、压缩
和后台调度。当前过滤器仍未保留没有 WuKongIM 调用栈的 Pebble 后台
goroutine；已有 `wukongim_channelv2_replication_stage_duration_seconds`
也包含本机排队/存储和远端交换细分，但目前 soak 产物没有保留这些桶。

完整结果、源码/二进制摘要、冻结上下文、公开配置、栈及可重复分析脚本
见[产物清单](assets/send-ban-executor-diagnostic-20260927/manifest.json)。
R2 持续资格仍失败，R6 的 5% 门槛仍未验证；没有发布。
