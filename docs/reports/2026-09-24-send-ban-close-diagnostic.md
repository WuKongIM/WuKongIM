# 持续运行的 gateway 关闭原因

状态：完整 30 分钟目标再次失败。相同 5000 频道、4500 SEND/s、128 append
workers，在 231.84 秒后 sender_read EOF；发起 1,043,260 次 SEND，pending
为 4758。没有并行编译、测试、其他压测或 CPU profile，也未触发磁盘保护。

本轮只让压力诊断收录产品已有的低基数 connection close reason 指标，
不改生产二进制、队列、发送负载或验收条件。准备与发送共 466.34 秒。
测试辅助方法通过（0.356 秒），预编译 harness 的源码差异和摘要已保存。

## 新证据

失败前清理尚未开始时，节点 3 的公开指标同时记录：

- `gateway_connection_closes_total{reason=async_dispatch_queue_full}` = 3。
- `runtime_pool_admission_total{component=gateway,pool=async_send,result=full}` = 3。

这确认本轮有服务端 SEND 准入满载导致的主动关闭，而非仅凭客户端 EOF
猜测关闭原因。它尚未证明积压最先由哪个存储、路由或调度环节触发。
三个节点另有 peer_closed=1，不能把这些不同关闭原因合并解释为过载。

权限 admission busy=0。提前结束窗口 SENDACK/RECV P99=1165/1034ms，
也已超过既定 1 秒门槛；扩大队列即使避免关闭，也不能据此认定延迟合格。
单节点堆峰值 377,986,704 bytes、compaction debt 峰值 527,090,286 bytes，
CPU 均值 411.11%。这些来自不完整窗口，不能升格为持续性能通过。

## 下一个单变量探针

源码与夹具配置给出：128 gateway workers、131072 总队列、512 ordering
shards、每片 256 个排队位置；会话按 session ID 固定落片。25 个发送连接
各约 180 SEND/s，一个不被消费的空分片约 1.42 秒可被一个发送者填满。
这些是源码推导，不是现场逐分片占用采样。

较大 gateway batch 必须等待该批所有 Channel 组结束后，同一会话才能
处理下一批；多个批次也可能占满共享 512-group 追加路由。下一步单独诊断
batch max records=32，总队列、分片数与每片容量保持不变，保留完整负载、
顺序、权限、持久化和原门槛。该探针尚不修改默认配置，也不替代 30 分钟
资格和三次 5% 同机对照。

已向用户询问目标性能验收机器的 CPU/内存/磁盘配置及已有测试机，以区分
产品缺陷与当前 Mac 同机三节点的容量限制；未购买或启动云资源。

[产物清单](assets/send-ban-close-diagnostic-20260924/manifest.json)包含
完整失败、关闭原因、冻结上下文、构建身份、磁盘保护及待验证假设。
