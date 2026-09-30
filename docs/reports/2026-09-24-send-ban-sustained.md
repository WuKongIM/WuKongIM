# send_ban 30 分钟持续资格首次尝试

状态：**失败**。保持 5000 活跃频道、4500 SEND/s、30 分钟目标，三节点、
256 Hash Slots、10 物理 Slots、三副本、128 append workers；未缩小规模，
未修改一致性、持久化或零拒绝门槛。生产二进制与 1ba4a6352 输入相同。

## 已观察结果

总运行 493.38 秒（包含冷准备和清理）；实际发送阶段 232.38 秒后在
sender_read 收到 EOF，已发起 1,045,692 次 SEND，pending=5702。
实际注入速率 4500.003/s，阶段 P50/P95/P99=61/121/813 ms，RECV P99
为 732 ms。进程持续运行，没有 panic 证据；测试主动失败并正常清理。
本轮没有触发 4 GiB 磁盘保护，最后磁盘采样仍约 12.6 GiB 可用。

权限 RPC 973,397 次，RPC 错误、transport 准入错误、权限 admission busy
均为零。成员变更为零，投递处理错误为零。CPU 均值 411.43%（100% 为一个
逻辑 CPU），分配 197,678,440,320 bytes，GC 972 次。Raft 信封 3,682,268，
outbound payload 226,519,748 bytes；这些累计量来自提前结束窗口。

追加路由并发峰值达到 512/512；Store append workers 峰值 100%，其队列
比例峰值 0.689；compaction debt 峰值约 549 MB。失败时公开 runtime 快照
记录节点 1 的 gateway async_send full=2，节点 2 的 append writer 深度 806。
等待栈包含大量 append future 和路由等待。源码中 SEND 准入失败会关闭
受影响会话；这支持积压触发队列满关闭的解释，但保留的应用日志尾部没有
最早关闭事件，不能仅凭事后计数证明每一次断连的完整因果链。

## 下一步诊断

已先固定三个可证伪假设：存储/compaction 造成追加等待；会话 SEND 队列
因等待关闭；独立接收/心跳超时。需用同负载的有界重现和 CPU/等待证据
区分，不能把扩大队列、丢失持久化或恢复陈旧权限读取当成修复。
尚未修改生产代码，30 分钟资格仍失败，5% 重复对照仍未验证。

## 复验与产物

```sh
WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK=1 WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION=30m WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS=5000 WK_E2E_MEDIUM_RECIPIENT_QPS=4500 GOWORK=off go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath -run '^TestCloudMediumPermissionSoak$' -count=1 -timeout=40m -p=1 -v
```

本次使用预编译 harness，身份、路径和生产输入摘要见 plan.json。
运行期间只进行轻量源码/文档编辑，没有编译、其他 E2E 或并行压力任务。
启动前只清理本任务独立 Go 构建缓存以释放磁盘，原用户文件未改动。
[清单](assets/send-ban-sustained-20260924/manifest.json)保存计划、冻结上下文、
原始失败结果、完整有界日志、runtime 快照、磁盘采样和假设列表。
