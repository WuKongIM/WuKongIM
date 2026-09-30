# Send permission RPC 边界验证

本轮基于 `c91cc4a77`，对应设计 §7.4、§11 R3/R4。保持完整验收范围。

## 先固定失败场景

在编写隔离验证代码前固定这些风险与预期：

1. 4096 个不同用户事实跨 256 Hash Slots、16 物理 Slots，在同一远端节点
   必须成功分块；每项恰好返回一次，缺失事实与禁令事实不能错位。所有实际
   编码请求/响应均不超过 1 MiB，出站同时最多四个信封。
2. 五个目标节点的请求应并行执行，不能因节点聚合退化为串行；出站最多四个。
3. 一个节点信封包含 16 个 Slot 时，服务端同时最多四个屏障工作，所有工作
   在返回之前结束。每个 Slot 仍保留自己的屏障，不共享旧授权。
4. 一组已成功、另一组路由变化时，只刷新与重试失败组。若再次变动，最多
   总共两次尝试；成功组不重新读，也不能被失败组抹掉。
5. 重复响应索引、重复/不符的组 fence、有错误状态但夹带成功数据、缺失或
   多余结果、非法事实均不得成为成功。格式损坏不执行路由重试。
6. 真实占满 16 个接收准入后，第 17 个远端或本机信封都立即 busy，不等待
   无界队列；取消一个读取必须释放许可，可由新请求复用。最终无残留读者。

隔离的原因：这些测试需要精确拦住屏障并篡改线上同格式响应，以可靠检查
并发与坏响应，不依赖集群调度时序碰巧命中。使用真实 Store、JSON codec 和
元数据 snapshot；集群路由、RPC 传递与 barrier 是可控适配器，因此不能把结果
称为真实网络/Raft 压测。带 integration 标签，deadline 与调度等待不进入 unit 层。

## 执行结果

所有边界用例及 race 检查通过，未发现需要更改产品实现的缺陷。

- 4096 个不同用户事实覆盖 256 Hash Slots、16 个物理 Slots，聚合后分成
  10 个节点信封。输入逐项恰好一次，2048 个已禁言用户和 2048 个不存在用户
  的 Found/SendBan/Version 全部对齐。记录每个实际编码请求和响应字节。
- 请求/响应恰好 1 MiB 时接受，多一个字节时拒绝。使用合法 JSON 的尾部
  空白填充控制精确 wire 大小，不声称这代表典型业务 payload。
- 五个不同目标节点均恰好一次调用，四个请求可在同一 gate 上同时等待。
  单个节点信封的 16 个 Slot 则各执行一次屏障，峰值四个，不走本机 loopback。
- 路由变更的请求索引严格为 `[0,1]` 然后 `[1]`。成功组只读取一次；第二次
  再变化则保留成功结果并把失败组返回 stale_route，没有第三次调用。
- 七种坏响应均让整个不可信信封失败，不重试损坏结果，不返回半份成功事实。
- 16 个实际执行中的读者占满接收许可；远端和本机额外信封都立即 busy。
  取消首个后计数降至 15，新请求可进入。所有返回之后许可和维护读者均为零。

## 验证命令与范围

```sh
WK_SEND_PERMISSION_BOUNDARY_REPORT=/tmp/send-permission-boundaries.json GOWORK=off go test -tags=integration ./pkg/slot/proxy -run '^TestSendPermissionRPCBoundaries$' -count=1 -timeout=1m -v
WK_SEND_PERMISSION_BOUNDARY_REPORT=/tmp/send-permission-boundaries-race.json GOWORK=off go test -race -tags=integration ./pkg/slot/proxy -run '^TestSendPermissionRPCBoundaries$' -count=1 -timeout=1m -v
GOWORK=off go vet -tags=integration ./pkg/slot/proxy
```

实际运行 Go 1.25.11、darwin/arm64，使用独立工具链和缓存。测试报告保存
源码基线、工作副本指纹、开始/结束时间以及每个边界的计数。并发用事件 gate
控制，30ms 的负向窗口只验证没有额外 worker 提前进入，不作为性能指标。

[产物清单](assets/send-ban-rpc-boundaries-20260924/manifest.json) 保存最终与
race JSON、冻结上下文、源文件指纹和日志摘要。原始日志在忽略目录
`tmp/send-ban-validation-20260924-rpc-boundaries/`。没有修改生产读取行为、
没有降低吞吐/频道数或一致性门槛。真实网络饱和与跨节点并行耗时、迁移故障、
100k 场景和三组压力对照仍按[完整验收表](2026-09-24-send-ban-acceptance-audit.md)推进。
