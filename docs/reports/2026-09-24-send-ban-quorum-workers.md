# Quorum worker 诊断与发送禁令矩阵补充

基于 `c90d67e29`。上一轮 5000 频道 / 4500 SEND/s 的 60 秒预检仍失败；
8 个 Store append worker 等待 quorum，512 个 router 许可用尽。

## 单变量诊断

保持相同二进制、三节点、256 Hash Slots、10 物理 Slots、三副本、5000 频道、
4500 SEND/s、96 RPC worker、8-item replication envelope、存储配置、队列
上限及全部验收断言，仅临时将 fixture 的 Store append worker 从 8 改为
128（当前产品默认）。这是诊断，不是原 8-worker 资格通过；无论结果如何
均保留原始失败，并恢复夹具原值后再决定是否另行设计或修正配置。

三个可证伪假设：

1. 外层 8-worker blocking quorum 并发限制吞吐。只改变为 128 应显著减少
   Store append 排队；如果没有改善，则不足以解释瓶颈。
2. 复制 RPC admission/远端吞吐限制主导。增加外层 worker 应仅增加 RPC
   排队、拒绝或远端处理等待，不能消除积压。
3. 磁盘持久化/宿主机竞争主导。增加 worker 应无法降低整体 commit 时间，
   或只把等待转移到存储队列/物理提交。

原重放命令沿用上一轮完整 5000/4500/60s 诊断。临时差异及源码 SHA 记录于
`diagnostic-128-source.json`，绝不把改变了资源参数的结果归为原配置通过。

首次 128-worker 诊断仍失败：测量窗口仅 390.93 ms，1760 次 Send 调用，
115 条尚未完成，SENDACK/RECV P99 为 97/98 ms。Store append wait P99
从上一轮 2441.83 ms 降到 89.61 ms，submitter 为 95.82 ms，说明外层
worker 限制确实影响排队；但此次失败由 `ReasonNodeNotMatch` 提前终止，
两个窗口长度不同，不能据此计算稳定吞吐提升或宣称解决了持续负载问题。

服务器日志明确记录 `permission read admission busy`。权限服务全节点共享
16 个正在执行的信封许可；本机和远端都受限，远端可通过成功的 transport
RPC 返回业务 busy。旧报告只统计 transport admission/error，因而该次报告
两项均为零。它们不证明权限读取没有拒绝。亚秒窗口的周期 gauge/CPU 采样
也不足以证明峰值为零或宿主机容量充裕。

新增报告字段 `send_permission_admission_busy`，直接累计已有公开直方图
`wukongim_message_permission_duration_seconds_count{stage="admission",result="busy"}`
的窗口差值，成功/失败结果都携带该值；正式验收要求为零。分别先编写解析
和验收拒绝测试，观察缺字段编译失败及非零 busy 被错误接受，再补齐实现。
没有新增产品指标或变更准入上限。历史报告缺少此字段，不能补写成零。

同二进制、同 128-worker 参数的第二次重放验证了新观测：273.14 ms 后
再次因 `ReasonNodeNotMatch` 失败，1230 次 Send 调用、77 条 pending，
权限 admission busy 为 **15**，transport permission RPC error/admission
error 仍为 **0/0**。SENDACK/RECV P99 为 92/91 ms。该差异证明确实漏记
了业务准入失败，不能把修正后的报告当作容量问题已修复。两次临时 fixture
改动均自动恢复为 8 个 worker。

随后保持 128 worker、4500 SEND/s、60 秒目标及同一二进制，只将频道数
从 5000 减为 25，以缩小重放。该场景 34.02 秒结束：测量 7.79 秒、
35060 次 Send 调用后同样失败，权限 admission busy 为 3，transport RPC
error 为 0，pending 为 34，SENDACK/RECV P99 为 125/138 ms。因此 busy
并非只在 5000 频道上发生；当前已有较短的失败重放，但尚未证明它是最小
触发条件。上轮 8-worker/25 频道/10 秒通过也不能排除更长运行中的突发。
不扩大 16 个权限许可、不跳过 fresh barrier、不重试到成功来掩盖拒绝。

## 功能矩阵补充

在当前真实单节点/三节点用例中，先连接两个不同 DeviceID 的同 UID 设备，
对 frame 已声明的其他源频道类型 3–12 分别创建完整正向对照。禁令写入后，
每个接入节点的 HTTP 与两个既有设备均须返回禁言，MessageID/Seq 为零；
解禁后两个设备不重连直接成功。每个频道检查完整已提交历史与预期六条正向
消息精确一致，不能通过忽略分页或只看错误码宣称拒绝无副作用。

测试使用正确的 Agent 编码和 visitor 自身身份，保持各类原有业务权限。用例
失败会保留失败，不因种类难测而删减矩阵。该补充不替代插件、其他故障矩阵、
持续压力或三组各三次对照。

最终主流程通过，共 84.48 秒（单节点 20.66 秒，三节点 63.82 秒），
284 条操作观察；每种新增类型均核对恰好六条已提交正向对照。
保留两次夹具失败：Agent 的编码 ID 包含 `@`，通用建频道接口拒绝；改为
合法首次 SEND 建立后，普通用户消息同步又依赖该场景未建立的成员索引。
最终通过公开 Manager 历史接口验证所有类型，不改产品建频道或成员语义。
这证明拒绝没有新增持久化记录，尚未扩大为所有拒绝类型的投递侧证明。

本次变更限于黑盒用例、压力报告解析/验收及证据文档；不改变产品二进制。
直接相关压力 harness 测试与两场景 vet 通过。完整 F/R 验收缺口继续见
[验收核对](2026-09-24-send-ban-acceptance-audit.md)。

## 重放与产物

```sh
WK_E2E_BINARY=/path/to/recorded/wukongim \
WK_E2E_SEND_BAN_REPORT=/tmp/send-ban-matrix.json \
GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban \
  -run '^TestUserAndChannelSendBan$' -count=1 -timeout=5m -p=1 -v

GOWORK=off go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath -count=1
```

压力使用上一轮 5000/4500/60s 命令，128-worker 单变量临时改动、原值恢复
及 25 频道缩小诊断均有记录。全部 JSON、失败/通过日志、测试源码指纹、
实际二进制 SHA 与冻结上下文见
[产物清单](assets/send-ban-quorum-workers-20260924/manifest.json)。日志仅将
制表符展开、删除行尾空白并统一末尾换行；清单保留原始字节 SHA。
`matrix-manager.json` 未设置 revision/fingerprint 环境变量，保留原值为空，
由配套 `source.json` 关联本次工作副本，不伪称在随后提交上执行。
