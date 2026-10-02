# 发送就绪前缀批处理验证

基于 `97d1009c1`，继续处理原 5000 频道 / 4500 SEND/s 压力预检的队列满失败。
原始失败、旧版对照和等待栈保留在 [上一轮报告](2026-09-24-send-ban-pressure.md)。

## 修改前固定失败场景

1. 一个真实 gateway session 的 128 条已就绪消息，应按输入顺序交给同一个
   submitter batch；原实现实际产生 128 个单条 batch。
2. 即使不同频道按反向顺序完成，同会话的结果发布仍须按输入索引前进。
3. 冷目录 head 未就绪时，同会话后续普通消息及先就绪的目录都不能越过它；
   独立会话可以完成。屏障解除后连续就绪部分应一次提交。
4. hook 按输入顺序调用，hook 拒绝项不得进入追加批次，其拒绝结果仍占据原
   会话发布位置。已有缺失结果、权限拒绝、取消、hook 改写重新鉴权测试继续
   作为回归约束。
5. 修复不得提高网关队列、router worker、共享 admission 或存储并发上限。
   原完整压力形状、持久化、全部收发守恒与 30 分钟资格不变。

先运行三个新增确定性回归用例，观察原实现的单条分割失败，再修改生产代码。
该隔离测试能准确验证 usecase 批次边界；是否解决实际压力失败必须由同一真实
进程夹具重放证明。

## 变更与顺序边界

每轮收集每个会话的连续就绪前缀，在未就绪目录处停止；合并后按原输入索引
排序，hook 顺序和提交切片顺序保持不变。终态/拒绝项不阻挡已准备好的后续项。
仍由原 `finalize` 链按会话顺序发布 ACK 结果。追加层仍将相同 canonical
Channel 分为一个有序组，并在既有资源上限内处理独立频道；不改其重试、持久化
和复制语义。网关仍等待整个批次结束才派发该会话的下一个批次。

目录屏障、入口派发顺序、hook 顺序、同频道序列和 ACK 顺序分别验证；不同
频道没有共用消息序列。修复恢复批量提交这个边界，不以逐条等待一个独立频道
的持久化结果作为下一条已就绪命令进入同批次的条件。

## 验证状态

消息用例、追加路由、gateway adapter/core 的完整单元测试与完整 race 均通过，
定向 vet、WKProto helper 回归和 `flow-doc-contracts` 通过。真实三节点私聊与
群聊各发送 1200 条消息，按 TCP 原始 ACK、发送者原序号及接收序列验证通过
（总用时 57.64 秒）。同一二进制的用户/频道禁令单节点和三节点回归通过，
共 71.04 秒。完整目标仍未完成，不将这些结果称为 30 分钟/三组各
三次性能资格。


## 新的压力证据与尚缺部分

- 25 频道、4500 SEND/s、10 秒最小重放通过：SENDACK P99 130 ms、RECV P99
  136 ms、pending 为 0，全部 45000 条消息完成。它只证明最小症状修复。
- 原 5000 频道、4500 SEND/s、60 秒目标重放仍失败：约 1.944 秒、8747 次
  Send 调用后队列满断连。提交前 P99 降为 0.495 ms，权限 P99 8.41 ms；
  submitter P99 2454.68 ms，Store append wait P99 2441.83 ms，512 个共享
  router 许可占满，Store append 队列峰值 56.5%。没有放宽压力资格。
- 夹具固定 `WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=8`，失败栈中的 8 个
  worker 均等待 `runDurableRound`。当前 quorum commit 不走旧 store-append
  worker batch；这是下一轮验证的候选瓶颈，尚未证明扩大 pool 是正确修复。
  产品默认 128 不等于这个夹具的实际值；本轮不改变该值或队列上限。

## ACK 顺序观测修正

首次加强 E2E 时，直接按 `WKProtoClient.ReadSendAck` 的顺序断言失败。该 helper
为每个 future 启一个发布 goroutine，因而 bridge 顺序不是线上的顺序。保留
这次失败及旧版对照，不将它直接归为服务器乱序。新增可注入 Dialer 的夹具
构造器；场景在原 TCP read 上被动解码，最多保留 1 MiB 不完整帧和 1024 个
ACK，只保存序号/原因这些标量，既不重排也不改写网络字节。再用这条真实线序
证据检查 ACK，同时检查所有 futures 成功以及接收者看到的原消息顺序。


## 重放与产物

```sh
GOWORK=off go test ./internal/usecase/message ./internal/runtime/channelappend \
  ./internal/access/gateway ./pkg/gateway/core -count=1
WK_E2E_BINARY=/path/to/recorded/wukongim GOWORK=off go test -tags=e2e \
  ./test/e2e/message/chat_lifecycle \
  -run '^Test(Person|Group)ChannelCrossIngressBurstPreservesReceiveSequence$' \
  -count=1 -timeout=9m -p=1 -v
```

压力重放沿用上一轮报告的 5000/4500/60s 命令。缩小到 25 频道、10 秒仅为
最小诊断。源码与二进制 SHA、冻结上下文、完整结果和原始失败均在
[产物清单](assets/send-ban-prefix-20260924/manifest.json)。没有修改队列、worker
数量、复制数、durability 或最终验收门槛。
