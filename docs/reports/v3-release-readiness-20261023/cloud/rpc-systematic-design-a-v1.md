# 方案 A：把现有 RPC 做深，调用面保持三种操作

状态：独立静态设计，未实现、未运行或改动负载。读取候选103292a9e；下列 transport/net 源文件与基线3768ad3ed相同。64恢复并发仅是诊断候选，不是本方案的结论。

## Module / Interface / Seam

Module 是现有 `pkg/transport` 请求生命周期，以及 `pkg/cluster/net` 对固定 service 的策略映射；不新增业务总对象。Interface 的数据面仍为三种已有操作：

```go
caller.Call(ctx, peer, service, payload) // 一次请求、一次结果，无自动业务重试
sender.Send(ctx, peer, service, payload) // 入队成功，不代表远端执行或持久化
server.Handle(service, handler, opts)   // 注册唯一执行语义与资源策略
```

cluster/net 的 Register/Handler Adapter 保持现有形式；Owned/Shard 兼容变体、Start/Stop 生命周期继续存在，不要求调用者改写。配额组合、排队、连接选择不增加到每次 Call 的参数。Interface 必须说明：截止时间包括准入/排队；取消未开始的请求；已开始 mutation 的独立执行与 ambiguous outcome；Send 的语义；payload ownership 和 typed busy/canceled/timeout/stopped。远端错误不能自动证明“未提交”。

Seam 沿现有 Caller/Sender/Handler；实现内部另有字节流 Dial/Accept Seam。分类依据固定 service 注册表；混合读写 service 默认保留现有不可取消执行语义。若要对 owner-read 等具体 operation 开启运行中取消，必须由 access Adapter 对已验证编码给出闭合语义，不能相信 caller 的“read-only”声明或按整个混合 service 一刀切。原4096回应、fresh Slot barrier、完整 authority fences 仍在原业务 Implementation，RPC不能替代它们。

## 已有能力与真正缺口

- `internal/sched/scheduler.go:86–113` 已有字节感知四lane加权调度（8:6:4:1）；连接队列、帧批处理、ready无空闲延迟复用，不再造一套。
- `rpc/service.go:165–215,247–375` 已有 service count/queued bytes/retained request bytes、FIFO、queue deadline、独立mutation/可取消read、释放后归还 ownership。`core/observer.go` 的有界非阻塞 drain 保留。
- `transport/server.go:101–116` 的共享 executor 容量随注册 service 并发求和；这不是独立的全节点资源预算。现有 per-service/per-connection 限制也不自动形成跨peer总界限。
- `peer/manager.go:83–90` 以 shardKey modulo pool选连接；cluster/net固定service映射不等于物理隔离。当前同peer默认16连接。字节优先级不能抢占已经阻塞的大写操作。
- `cluster/net/local.go:36–59` 直接调用 Handler，绕过这些策略；它可验证业务适配，不能拿它的通过冒充真实调度/取消/内存验收。

## Implementation 隐藏什么

1. **单一准入与ownership账本。** 在已有请求生命周期中联合计算 global / peer / class / service 的 item和owned-byte额度；共享一次权利，不叠加四个相互持有的阻塞信号量。复制/排队前取得预算，取消、拒绝、响应完成和Stop各只有一次归还。保留原service硬上限，先不填未经量测的新数值。请求、响应与执行中retained backing要明确计费；不能用128MiB UID额度宣称限制RPC堆或RSS。输入解码前的大frame、多连接、响应分配是必须覆盖的计费阶段。
2. **就绪请求公平调度。** 复用已有service queue与executor，只替换跨service/peer的dispatch选择；闭合class先分额、class内peer轮转，保留FIFO/age，避免hotpeer占满全部执行位置。Raft沿原稳定sender连接和现有串行执行顺序，不借此并行化。控制/响应/取消的进度容量纳入同一总预算并有保留份额；嵌套RPC不能形成“父请求持全局票、子请求无票”的死锁。CPU先以active execution slots/实测成本指导权重，不把walltime当CPU硬配额，也不新增自适应控制器。
3. **有限连接隔离与一次deadline。** 在原pool内显式划分Raft/control、foregroundRPC、bulk的连接归属，保持总连接上界；停止依赖service取模碰巧隔离。保留现有budget/cancel协商与旧peer显式兼容。queue等待消耗原ctx，执行仅采用已注册语义；不得重置5秒或自动重发已开始/不确定mutation。handler返回前即使caller离开仍保留应有账本，不制造提前free。

以上能力在一个生命周期实现，业务调用者不负责“先拿全局票再拿peer票再建ctx”。不移除业务自己的语义约束：恢复keys/UIDbytes、4096routes、权限freshness、ACK序列化仍各归原Module；重复的纯运输额度待等价证明后才删除，不能把所有gate一并取消。

## Dependency / Adapter

依赖是 remote but owned。生产 Adapter 使用现有TCP transport；受控 Adapter 使用同一client/server core和codec，在Dial/Accept处注入可阻塞/取消/断连的net.Conn。已存在ClientConfig.Dialer、testkit.BlockingConn、真实TCP Harness；Accept当前直接net.Listen，可提取一个很窄的内部listener注入点，供net.Pipe/内存listener走同一调度。它不是新外部业务Interface，也不将另一个策略实现塞入LocalNetwork。真实三节点验收仍不可被内存Adapter代替。

## Trade-offs

Depth高：调用者保留Call/Send，所有运输资源和失败所有权集中修改。代价是shared账本/dispatcher可能自身成为锁热点，必须分区和证明公平，而不是增加每call观测锁；保留现有observer drain。连接隔离会改变旧pool使用率并需精确Raft顺序回归。混合service细粒度取消需要真实编码分类，首阶段宁可保留独立执行语义，不能“默认read”提速。该设计解决运输排队和资源责任，不宣称解决owner扫描、Slot/存储等待或所有message_loss。

## 三项可证伪验收（先测试后实现）

1. **同一Seam的资源/取消矩阵：** real TCP与controlled Adapter均运行；hotpeer+large-response饱和时冷peer/控制类仍在原deadline完成；精确开始前取消零handler，开始后mutation只执行一次且不因caller取消回滚/重试；读取消最终join；Stop后计费owner/队列/执行均零。超额、慢响应、断连、嵌套Call同时覆盖；任何无限增长/重复释放/饥饿即失败。
2. **协议不变：** 固定sender的Raft Append→Heartbeat顺序、fresh Slot barrier/authority fence、4096route拒绝、混合operation取消与ambiguous mutation无重试，在原入口跑真实字节/网络fixture；仅增加调度吞吐而破坏任一条即失败。保留必要codec/protocol负例，不机械删除旧测试；通过新公共Seam覆盖后再移除真正重复的浅测试。
3. **原规模资格：** 固定原10k在线、2k groups/100k very-large分布、2000 SEND/s、120s与全部原gates，观察first-attempt/ACK/RECV/loss/p99、各class队列/retained bytes/CPU，必须完整终态及清理。先冻结候选与基线、同环境无采样主跑；诊断另行有界。具体资源额度和公平目标在实现前依据上述fixture测量冻结；不发明100k RPC/s承诺，不以Mac机制通过代替正式资格。

## 精确只读绑定
- `AGENTS.md` SHA256 `2220f49835b8dba20bafbc277854421222765be52e874fc6ee994bcd3ee0d746`
- `CONTEXT.md` SHA256 `2c6c14ebb838dcfb498b6b6c130f0a500c21dcd3a49343bb467961921bd8168c`
- `pkg/cluster/FLOW.md` SHA256 `0a7ba4b4094ba483f4b7bdc17a654496e7e1a3560349c3ddb4ae401f19633d36`
- `pkg/transport/server.go` SHA256 `595a6283946d6b8eb7a225f9c3fd87722c78cfdd3ed71d33e1c12d26c7edf19a`
- `pkg/transport/client.go` SHA256 `37eb6c62c720f19432b2df2e71eec676cdb3dafd7a073b5b745a77c3b39f1604`
- `pkg/transport/config.go` SHA256 `e626d0dba425abd057caa17f711fd4e1e1fea75bb42fc814564e9d331de3d79f`
- `pkg/transport/internal/core/types.go` SHA256 `44b9b39ab50f1b82fd65a8650831bb560441b94512782aeb90d350e8391956d7`
- `pkg/transport/internal/core/observer.go` SHA256 `85408802e60f8c886693ba396334cd79b798f94410791017f9d302eae75d266b`
- `pkg/transport/internal/rpc/service.go` SHA256 `d93fe20c2b6ee60bf7fcb9503042b8268ef90c828b821788330d01b74afa1172`
- `pkg/transport/internal/rpc/service_queue.go` SHA256 `b3da821e17fecfd4f1331dca820474940fa9668ca1aa175d673ce136196a70f2`
- `pkg/transport/internal/sched/scheduler.go` SHA256 `ae60b961a0e69dadf4a263a2371b7a57463c6b1aac219953de8c7626f316cb7a`
- `pkg/transport/internal/peer/manager.go` SHA256 `4f89ec54542db83e3d2cb88e585314ee098a5bf70ad053d1406672a105275af4`
- `pkg/cluster/net/transport.go` SHA256 `d0a3d07ecf1220069b9c374af65eaa4ee0b400312da6cfb730391c91be8b9be9`
- `pkg/cluster/net/local.go` SHA256 `b2f9bce1889003e1f647c7927493c47ef6fbe76091490a8e2f78238479c4a08b`

未发现 pkg/transport/FLOW.md、pkg/cluster/net/FLOW.md；已读适用 scope:subtree 的 pkg/cluster/FLOW.md。无源码修改、无测试/build/HTTP/DB/采样。
