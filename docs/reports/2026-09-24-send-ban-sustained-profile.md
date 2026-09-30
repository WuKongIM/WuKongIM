# 持续失败的同负载 profile 诊断

状态：四分钟诊断**失败**，但没有复现上轮 EOF。保持 5000 频道、4500
SEND/s、128 append workers，完整发送接收 1,080,000 条，pending=0，
权限 admission busy=0。SENDACK P99=900ms、RECV P99=802ms；失败项是
单节点堆峰值 667,843,200 bytes，超过既定 536,870,912 bytes 上限。
本次没有放宽上限，不替代前一轮失败的 30 分钟资格。

## 观测与证据限制

窗口实际 240.37 秒，CPU 均值 417.33%，分配 204,220,654,360 bytes，
GC 995 次。追加路由峰值仍达到 512/512。三个节点在发送 90 秒后各采集
2 秒 CPU profile、堆 profile 和有界等待栈；因此 CPU 样本不代表整段负载，
堆快照也不是随后观测到的峰值。没有并行编译、其他 E2E 或压力任务。

节点 2 的 CPU 样本主要落在 syscall 和调度/等待，没有单个权限业务函数
占主导；不能仅凭 CPU profile 证明 I/O 的具体瓶颈。该节点当时 in-use
heap 约 228.77 MiB，其中 quorumLog.remember 43.02 MiB，近期消息缓存
append/clone 累计 63.95 MiB。近期缓存是有界的逐频道缓存，不是已证明的
无界泄漏；大量活跃频道会放大它的总保留。

代码检查发现 afterSuccessfulQuorumCommit 仍追加 reactor recentRecords，
而 durable quorum 模式的复制/repair 已由独立 quorum owner 负责，旧 follower
Pull/Ack 热路径被禁用。下一步先固定回归失败场景，验证能否避免该模式的
重复 payload 保留，同时保留旧模式缓存及兼容 Pull 的持久化回退。
当前仅是有证据的修复候选，尚未修改实现，也未宣称 EOF 根因或容量已修复。

## 复验与产物

使用相同的预编译 harness 和生产二进制，固定 `SOAK_DURATION=4m`，
保留 `GROUP_CHANNELS=5000` 和 `QPS=4500`，设置
`WK_E2E_MEDIUM_RECIPIENT_PROFILE_DIR` 到新目录。完整命令和磁盘保护见
run-profile.py，最终结果和构建身份见 plan.json、result.json。

[清单](assets/send-ban-sustained-profile-20260924/manifest.json)保留原始
CPU/heap protobuf、pprof top、90 秒等待栈、完整结果、日志与冻结上下文。
日志仅做空白规范化，原摘要保留；protobuf 未修改。
