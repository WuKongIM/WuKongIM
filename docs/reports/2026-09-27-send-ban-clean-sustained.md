# 磁盘清理后的 Docker 持续验证

**原定 5000 频道、4500 SEND/s、30 分钟测试仍失败：发送 503.723 秒后 EOF。**
释放磁盘已恢复测试条件，但不构成产品修复，也没有完成 R2 持续资格。

本轮 `clean-sustained-01` 使用已有 Linux arm64 产品二进制和补齐失败
metrics 的 harness，8 CPU/6 GiB、256 Hash Slots、10 个物理 Slots、
三副本、append workers 128、gateway workers 128/queue 131072/batch 32
保持不变。使用原容器，不继承 diagnostic 容器的 profile、driver、stall、
storage-history 或 replication 诊断开关。只有既有测试采样、失败抓取及
外部 15 秒资源保护，另记录共享 Docker accounting。

冷预热 168.482 秒通过。发送第 1–4、6–8 分钟 pending 为 0；第 5
分钟 pending 为 11，下一分钟归零。第 8 分钟 SENDACK/RECV 均为
2160001。之后在 2266756 次 SEND 调用、pending 6566 时 EOF；测试
总计 694.530 秒，SENDACK P50/P95/P99 为 10/91/575 ms。

失败累计快照中，节点 2 的 `async_dispatch_queue_full` 关闭计数为 2；
权限 admission busy 窗口增量为 0，Channel admission full 与 transport
RPC rejected 也均为 0。这是持续发送调度积压路径，与 fresh 参考版
约 28.5 秒的等待名额立即拒绝分开记录。

主机最低可用磁盘 10.977 GiB，高于 8 GiB 保护线，runner 没有保护
停止。cgroup 最大 memory.current 为 4.748 GiB，所有资源样本的
memory max/oom/oom_kill 事件均为 0，容器没有 OOMKilled。临近结束
主机可用空间出现明显下降，但仅凭主机全局空间变化不能归因于某个
文件或证明 I/O 饱和。测试正常清理后仅剩保活 sleep，磁盘回到约
24 GiB；没有自动重启测试。

39 个共享 Docker 样本全部成功，只观察到本测试容器和既有 buildkit
容器；这不是对全部宿主机活动的独占证明。失败 goroutine 抓取输入
分别 88686/123495/100435 字节，均未截断。三个节点均可见 WAL 的
Fdatasync 栈，节点 1 还有两个 L4 compaction sync、节点 2 一个 L5
compaction sync；没有 memTableWriteStall。这些是顺序、失败后的
快照，不能确定 sync 持续多久、属于哪个 DB，或解释哪次等待先导致
调度积压。后续需用有界 I/O 时序证据定位，不能据此放宽持久化保证。

本轮没有产品代码变更，不把运行更久当成修复通过，也不抹去之前的
277.717 秒 EOF、磁盘保护中断和 fresh 参考失败。R2/R6 仍未通过。

原始日志以 JSON 无损保存，命令、资源记录、共享 accounting、失败
指标、进程清理结果、二进制摘要及运行入口见
[产物清单](assets/send-ban-prime-metrics-20260927/clean-sustained-01/manifest.json)。
全部 9 个产物摘要已核验，归档日志与原始输出一致。
