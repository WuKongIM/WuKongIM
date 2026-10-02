# 发送禁令压力验收推进

基线 `74908ffb5`。保持三节点、256 Hash Slots、10 物理 Slots、5000 个自然
哈希频道、4500 SEND/s 与原定 30 分钟资格，不用缩小规模替代验收。

## 首次预检

60 秒诊断配置在 5000 频道完成创建与预热后、连接发送者时失败：
`ReasonAuthFail`。夹具的 WKProto 客户端没有 Token，而默认认证开启。
因此测量窗口尚未开始，没有吞吐/延迟结论。先用 25 频道的最小诊断复现
同一认证失败，再显式关闭该夹具的 Token 登录认证；它不验证 SDK 认证。
发送权限、用户与频道禁令不因此豁免。正式压力规模不变。

## 修改前固定观测失败场景

1. 同一传输写批次混合 Raft/RPC/Control/Bulk 时分别累计各 lane 的 payload
   字节，旧总量仍相同；单帧、接收路径和协商请求均保留 lane。
2. 失败或已取消的未发送 frame 不计入成功字节；观察 drain 聚合后总字节
   不因采样或多个 lane 混合而丢失、重复。
3. 指标只含固定 direction/priority 标签，不引入 UID/频道/节点对等基数。
   Raft lane 在 cluster 服务分类中只能属于 Slot/Controller Raft。
4. 采样必须在每个节点、窗口前后都有 Raft lane 指标才给出数值；旧版本、
   部分节点缺失、重复或 counter 回退都保留 null。仅累加 outbound，不能
   将请求和接收各计一次。数据是 transport payload bytes，明确不含 wire
   header、TCP/IP、TLS 与网卡重传开销，不能叫完整线上字节数。
5. 完整规模预检和最终三组/三次验收记录确切源码、配置与原始结果。短诊断
   通过也不能使 30 分钟或 5% P99/CPU 门槛变为通过。


## 观测实现与验证

新增固定 10 条 `wukongim_transport_lane_payload_bytes_total{direction,priority}`
series（2 个方向 × 5 个固定 lane），沿单帧、混合写批次、接收与 observer
聚合路径保留 Priority。已有 transport 总字节指标保持原口径，不改 wire
协议、队列容量或发送权限。计数只表示成功 frame 的 payload，不是完整网卡
流量。失败写与发送前取消不增加此计数。

采样器在两个窗口边界按节点 ID 检查恰好一个 outbound Raft series；缺失、
重复、节点集合变化或任一节点 counter 回退均保留 null。先运行缺失 lane
及采样器边界失败用例，再实现。完整 app/metrics/transport 单元测试、定向
race、vet、压力夹具测试及 `flow-doc-contracts` 通过；Mac race 链接器的
既有 LC_DYSYMTAB warning 保留在日志中。

## 压力预检结果（失败）

三节点、256 Hash Slots、10 物理 Slots、5000 频道、4500 SEND/s、60 秒目标
的预检，在测量约 1.823 秒、完成 8205 次客户端 Send 调用时出现 EOF。这里的
Send 调用次数不是已提交或已收 ACK 的消息数。服务端明确记录
`gateway: async send dispatch queue is full`。待完成消息 6548，SENDACK P99
1516 ms；权限阶段 P99 4.96 ms，提交前阶段 P99 2046 ms，追加提交阶段 P99
83.77 ms。权限 RPC 错误和 admission 错误均为零。观测到的 outbound Raft
payload 增量 175670 bytes；完整原始失败 JSON 与等待栈一并保留。

同版本 25 频道、10 秒目标的最小诊断也在约 1.77 秒失败；加强逐节点采样后
再次运行在约 1.73 秒失败，取得 97579 Raft payload bytes。它验证了观测链路，
**没有通过压力验收**。其他任务可能同时占用宿主机，因此这些短窗口的 CPU、
延迟或字节数字不能用于 5% 对照结论。

## 旧实现最小对照

重新从干净的 `64f73d99b3b0cb8960d40825f76053f5a0000dbd` 构建基线，
使用同一最终夹具、25 频道、4500 SEND/s、10 秒目标重放，也出现相同队列满
断连。构建前后该 checkout 的 `git status --porcelain` 均为空，Go/module 文件与提交
逐项一致；但重建二进制仍记录 `vcs.modified=true`，保留这个来源差异，不声称
它具有 clean VCS stamp。旧二进制诊断另存。这个对照说明症状不是新增禁令独有，不能据此排除新增权限
检查的额外成本；正式三组、三次、完整窗口对照仍须完成。

## 后续定位方向

按优先级保留三个可证伪假设：

1. 同一会话的批次在持久化完成前不能推进下一项：如果它主导积压，等待栈应
   落在逐条追加，且批次大小增加只放大提交前等待；在保持顺序的有界批处理
   实现下，同样原始负载应显著减少这部分等待。
2. hook 路径带来额外等待：如果主导，应能在 hook 或绑定查询栈看到积压，并
   在只改变该路径的诊断中消失。
3. 磁盘或宿主机竞争：如果主导，应与 physical commit/资源采样同步恶化，且
   在空闲宿主机重放时缓解。

当前栈存在 `SendBatchEach -> submitSendBatchLane -> Router.sendSingle`，
分别等待本机追加与远端 `ForwardSendBatch`。实现每轮只选择同一会话链的
一个 head，同步等待结果后才推进下一项，支持第一个假设。采样中没有 hook
等待栈不代表完全排除第二个假设。尚未修改调度：不能为提高吞吐而破坏同一
会话的提交顺序、ACK 顺序、冷目录屏障或有界资源要求。

## 重放

Go 1.25.11；使用来源记录中的二进制和该工作副本的 E2E 夹具：

```sh
WK_E2E_BINARY=/path/to/recorded/wukongim \
WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK=1 \
WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION=60s \
WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS=5000 \
WK_E2E_MEDIUM_RECIPIENT_QPS=4500 \
GOWORK=off go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath \
  -run '^TestCloudMediumPermissionSoak$' -count=1 -timeout=8m -p=1 -v
```

最小诊断仅把 duration 改为 10s、channels 改为 25。最终资格仍须恢复 30m、
5000 频道及三组各三次同机对照。没有运行完成的资格结果，R2/R5/R6 仍未通过。

[产物清单](assets/send-ban-pressure-20260924/manifest.json) 保存完整失败日志、
提取的失败 JSON、源码和二进制摘要、冻结上下文以及成功/失败验证日志。
