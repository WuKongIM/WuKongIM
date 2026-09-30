# Pebble flush 与复制阶段诊断

**持续资格仍未通过。** 正常生产二进制未变；扩充 E2E 诊断后，第二次
完整规模尝试在发送 213.145 秒、959,151 次调用后发生 sender_read EOF。
失败后的节点 3 flush 栈位于数据库目录同步，commit coordinator 再次
等待 memtable 写停顿条件解除。这是存储 I/O 排查的正向证据，尚不是
目录同步耗时测量或整条故障链的逐请求证明。

## 变更与运行边界

只修改 E2E 诊断：保留 Pebble 独立后台栈，显式记录每节点 HTTP 状态、
输入大小及截断状态，并保留已有前后边界抓取中的复制阶段直方图。
单节点输入上限 2 MiB，过滤输出共 256 KiB；每个指标边界最多 4096 行。
新增指标输出由 `WK_E2E_MEDIUM_RECIPIENT_REPLICATION_DIAGNOSTICS=1`
开启，不增加 SEND 窗口中的 HTTP 请求。无生产代码、持久化策略、默认
参数或验收条件变更。

harness 编译通过。真实三节点正向对照（25 频道、500 SEND/s、10 秒）
完成 5000 条消息、零 pending，前后指标 528/576 行，无采集失败或溢出。
完整规模采用 3 个进程、256 Hash Slots、10 个物理 Slots、3 副本、
5000 频道、4500 SEND/s、30 分钟目标；gateway 128 workers、队列
131072、batch 32，append workers 128。运行期间无并行编译、测试、
其他本任务压力或 CPU/heap profile；不能据此排除机器上其他进程 I/O。

两次完整规模尝试均保留：

1. 第一次在 cold prime 第 1081 条消息收到 HTTP 408，未进入测量窗口，
   总耗时 106.30 秒；没有吞掉该失败，也没有在 harness 中增加广泛重试。
2. 同一二进制与参数的新集群重跑，prime 约 172.578 秒，随后测量窗口
   在 213.145 秒失败，总耗时 448.286 秒。配置核对通过，磁盘保护未触发。

## 断连与后台栈

断连前最后三轮，25 条连接的最早未确认消息序号均未前进，全部对应
节点 3 的 active Leader，LEO/HW=191/191。pending ACK 从 3593 增至
4716、5845，最早年龄从 836ms 增至 1086ms、1336ms。全窗口记录
852 份快照、54 次节点查询、零查询失败。稍后的失败消息计数 pending
为 6490，两者时间与口径不同。SENDACK P99=884ms，permission busy=0。
公开关闭计数：节点 2/3 分别有 2/1 次 async_dispatch_queue_full；各节点
另有一次 peer_closed。最后十秒 driver 每 100ms 最多 453 次调用、
单连接最多 19 次，最大调度滞后 23.31ms、socket Write 11.10ms。

三个 profile 均 HTTP 200，输入分别为 113636、113175、123253 字节，
输入没有截断；保存的过滤输出约 129 KB，没有输出截断标记。节点 3：

- commit coordinator 停在 Pebble v2.1.4 `db.go:2644` 的 memtable
  写停顿分支。
- `pebble=flush, output-level=L0` 的后台栈位于
  `compactAndWrite -> provider.Sync -> vfsSync -> diskHealthCheckingDir.Sync
  -> os.File.Sync -> internal/poll.FD.Fsync`。
- L5、L6 compaction 栈均进入 `syscall.Write`。

对应依赖源码表明，flush 完成 SST 输出后需同步对象目录；Go 1.25.11
Darwin 的 File.Sync 尝试 F_FULLFSYNC，ENOTSUP 时可回退到 fsync。
该栈本身无法区分这两个系统调用分支。目录同步不能由 BytesPerSync
参数替代，也不能为通过测试而删除。源文件摘要保存在 flush-findings.json。

这些观察支持优先排查存储 I/O。profile 在失败后采集，通用数据库标签
不能把每个后台 goroutine 精确关联到同一物理 DB；单次栈也不能给出
同步阻塞时长，不能据此断言磁盘带宽耗尽或排除调度、其他进程影响。

## 复制阶段指标与下一步

前后各 528 行指标完整，无溢出；离线分析验证三个节点计数未回退、桶
单调、+Inf 与 count 一致。运行时每 32 次采样一次完成操作。节点 3
本机 quorum 存储 P99 约 90.83ms、P99.9 约 402ms，远端 foreground
exchange P99 约 92.68ms。本机 queue P99 约 9.77ms。这些是直方图
插值，尚未完成的停顿不会进入最终耗时，不能用这些分位数否定写阻塞。
边界抓取也不是各节点同时发生，不能把阶段分位数相加成请求分位数。

下一步应在相同负载下有界记录磁盘 I/O 与存储停顿的时间关系，或在
既有专用测试环境做相同二进制和持久化条件的对照。先区分单机三进程
共享存储的限制与产品内写放大，再决定生产修复；本轮没有提高队列/
memtable 阈值，没有降低同步或副本保证。R2 的 30 分钟资格与 R6 的
三次完整配对/5% 门槛仍未完成。

完整失败尝试、正向对照、源码/二进制摘要、冻结上下文、配置、原始
指标与可重复分析脚本见[产物清单](assets/send-ban-flush-diagnostic-20260927/manifest.json)。
