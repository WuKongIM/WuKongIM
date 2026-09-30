# quorum 提交的重复缓存保留

状态：已移除 durable-quorum 提交后的 legacy Pull payload 缓存副本；
确定性回归、reactor 全包、定向 race 和单/三节点发送 E2E 均通过。
原规模四分钟压力复验再次 EOF 失败，不能给出内存恢复或持续资格通过结论。
此前 EOF 的完整因果链仍未证实，本改动不声明已修复该断连。

## 证据与修改

上一轮 5000 频道、4500 SEND/s 的四分钟运行因单节点堆峰值超过 512 MiB
失败。90 秒 heap 样本在节点 2 的 reactor recentRecordCache append/clone
累计保留约 63.95 MiB；该快照不是峰值，也不是无界泄漏的证据。

先写 `TestQuorumCommitDoesNotRetainLegacyPullPayloads`，通过真实 reactor
worker completion 路径复现提交后留下 payload 副本。失败日志保留。
随后仅删除 `afterSuccessfulQuorumCommit` 对该缓存的填充及其无用参数。
quorum runtime 继续负责复制、修复和重试证据，legacy append 缓存不变。
兼容 Pull 的缓存未命中仍走持久化读取，不改变成功提交的 HW/序号。

私有缓存所有权需要这一小范围确定性断言；capture quorum port 只模拟
提交收据，不能据此声称持久化正确。真实进程 E2E 另验历史、恢复和投递。

## 已完成验证

- 修复前新增测试失败，修复后 reactor 全包通过（2.03 秒）。
- Quorum、RecentRecord、LeaderPull 定向 race 通过；保留 macOS 链接器
  LC_DYSYMTAB 警告，未将其当作测试失败。
- 新二进制 `TestRejectedSendHasNoDelivery` 通过（45.51 秒），70 条观察。
- `TestUserAndChannelSendBan` 单/三节点通过（91.04 秒），284 条主观察，
  独立禁令组合和完整历史保留在 JSON 中。
- FLOW 命名检查：81 compliant、0 invalid、10 个既有行数建议警告。

生产源码为 `2fae8d552` 加本报告记录的最小工作副本差异；不是假称测试发生
在后续干净提交上。生产二进制 SHA256、预编译 E2E harness 的独立身份、
源码 patch、修复前失败及修复后日志见 assets 中的 source/plan 文件。

## 原规模复验

使用相同压力 harness、5000 频道、4500 SEND/s、128 append workers、
四分钟时长和 90 秒后的有界 profile。上限、Raft 一致性、持久化和零拒绝
要求均不变。计时窗口不并行运行编译、其他测试或其他压力任务。
四分钟即使通过也仅是诊断，不能替代 30 分钟持续资格或三次 5% 对照。
EOF、瞬时积压与 quorum receipt 保留仍是不同的待验证因素。

## 本次压力结果

窗口在 162.10 秒提前结束，729,469 次 SEND 调用，pending=6546，
错误为 sender_read EOF。提前结束窗口 P50/P95/P99=65/130/250ms，
RECV P99=232ms、CPU 均值 405.76%，权限 busy/error 均为零，路由峰值
512/512。单节点堆峰值 349,352,552 bytes；运行更短，不能与上一轮完整
四分钟峰值直接作百分比改善对比。总运行 392.14 秒，无磁盘保护中断。

90 秒 node-2 in-use heap 样本为 165.90 MiB，quorumLog.remember 约
46.02 MiB；旧 recentRecordCache append/clone 已不再出现在该样本中。
这与确定性回归共同支持重复缓存已移除，不证明所有内存或 EOF 问题恢复。

失败快照中节点 1、2 的 gateway async_send full 各为 1，三个节点的
队列深度分别为 2032/1768/2028；追加 writer 深度为 51/72/57。
当前公开采样只有聚合队列，没有区分 global/shard/mailbox 拒绝来源，
也没有收集已有的 connection close reason 指标。下一步先补齐这些证据，
不能仅凭事后计数断定每一次 EOF 的全部原因或扩大队列冒充容量优化。

[产物清单](assets/send-ban-quorum-cache-20260924/manifest.json)保留修复前
失败、修复后功能结果、失败压力窗口、原始 profile 和源码身份。30 分钟
资格仍失败，5% 重复对照仍未验证。
