# 精确56c Presence real-transport cancellation 最小回归方案

本方案仅只读设计。当前事实是 Service13/14 的已开始读取与修改都使用服务执行 context；取消 caller 不会自动取消这些执行。它能解释一种可能的资源寿命差，却未证明本次 message_loss 由此造成。这里建议先写刻画现状的回归，再依据实际证据决定是否改变读取语义；不能把全部混合服务改为 CancelRunning=true。

## 已核实边界与已有覆盖

- pkg/cluster/net/transport.go:322-357：Service13 PresenceAuthority 和14 PresenceOwner 都没有进入 CancelRunning=true 白名单。队列默认5s、执行默认30s；二者均混合读取和修改。生产 client 开启 RequestBudgets (:146-153)。
- pkg/transport/internal/rpc/service.go:314-350：执行前重新检查原 request context；一旦开始，CancelRunning=false 选择服务根 context，再包执行30s。资源直到 handler 返回才释放；不会强杀不合作的处理器。
- pkg/transport/server.go:276-305：原 request context 仍跟踪 caller 生命周期；取消后终端 response callback 可直接放弃回复。service handler 返回与原 caller 收到成功不是同一个边界。
- 实际服务13 op8 在 node/presence_rpc.go:461-488 进入 target-batch authority；service14 op9/10 在 :547-552 进入 OwnerRoutes 端口。服务13 register/commit/abort/unregister/touch 及14 apply-action7 共用这些混合服务。
- infra/cluster/presence_recovery.go:105 对每个 owner read 包250ms；delivery/runtime.go:17,505 对整个计划包5s。op8 client 自己不另加250ms。以下最小测试不宣称覆盖整个 Delivery Runtime 或实际 Registry 的锁等待。
- 已有真实 TCP 测试 pkg/transport/request_budget_integration_test.go:208-265 验证排队取消、释放队列容量、未执行；:267-307 验证 opts.CancelRunning=true 的读响应 deadline、false 的 mutation 继续；:309-327 验证可取消读的 disconnect。前者大量同步屏障，后者使用50ms真实deadline。存在 pkg/cluster/net/transport_performance_test.go:24-50 的服务策略静态测试，但其表没有 Presence13/14。现有 presence RPC 测试主要直接调 Adapter/假 RPC，未把真实混合服务 policy 与实际 op 编码连起来。

## 最小 fixture：一个入口 integration 文件，零生产 hook

候选位置为 internal/access/node/presence_transport_cancellation_integration_test.go，带 integration build tag。仅设计，尚未创建。用公开 clusternet.NewTransportServer 监听127.0.0.1:0，真实注册13/14到公开 node.Adapter；使用 node.NewClient 进行真实 op 编码/解码。Authority/OwnerRoutes/Owner 是现有公开窄端口的受控测试实现，保持合法 target、对齐结果、静态 owner/boot 身份。只在这些端口边界加入 entered、inspect、release、finished 同步 channel。

fixture 将 Service.Concurrency/QueueSize 显式缩成1/1便于确定排队次序，保留实际 CancelRunning 选择、5s队列与30s服务 timeout；这不模拟生产128并发耗尽，也不更改512/256/4恢复限制。主用 op8 与 op10；op9可以加入同一读取表作兼容性额外行。修改保护至少覆盖同服务13 RegisterRoute 和14 ApplyRouteAction，副作用计数在release后原子增加一次。

底层用公开 transport.Client（RequestBudgets=true、PoolSize=1、固定peer/shard），通过只有 CallRPC 方法的测试 shim 接入 node.NewClient。它执行与 cluster/net 相同的真实 budget/cancel 协议，但不覆盖 cluster/net 的错误包装；断言使用 errors.Is 的公开取消/timeout类型。用公开 Dialer 计数实际TCP建立，要求整例只有一代连接，禁止 ClosePeer/重连来制造进展；否则取消栅栏证据无效。默认连接队列远大于本例最多三项，观察拒绝/丢事件即判 fixture 无效。

## 无长 sleep 的远端取消栅栏

Caller Cancel 返回仅证明本地 Call 结束，不能证明远端已处理 cancel frame。可利用现有公开 capability 协议建立一个仅用于测试的同步栅栏：

1. 等受控端口 entered，保证业务请求已完成协商且开始执行；触发 caller cancel，等待该 Call 返回预期取消。
2. 在同一公开 transport.Client、同peer/shard和同连接上，以 PriorityControl 调用 wire.CapabilityServiceID，payload严格等于 wire.CapabilityRequestBudgets，并等待原样ACK。
3. Conn.Call 在返回取消前把 RPCCancel 以 PriorityControl 入队（conn.go:210-221）；同lane append/FIFO 在 sched/scheduler.go:162-166,416-425。PoolSize1固定到同slot（peer/manager.go:82-105）。服务端 readLoop 同步dispatch（conn.go:258-266），CancelInbound 在 server.go:243-245 同步调用，capability在 :254-262同步处理。因此在无拒绝、无重连的 fixture 下，后置同lane探针ACK意味着前置cancel已被处理。
4. ACK后触发 inspect；受控端口将 ctx.Err()/deadline种类写到缓冲结果channel。读取当前expected是ctx.Err()==nil，仍受服务30s budget约束。随后明确release并join handler/后继RPC；不靠“等一段时间仍没退出”推断存活。

该栅栏不新增协议或生产观察字段；重复capability请求由现有server分支允许。若协商/探针失败、发生连接更代或队列拒绝，先判 fixture_invalid，不能把失败解释成presence取消语义。

## 最小顺序及断言

A. 响应成功：entered→release→Call完整解码成功→再Cancel。成功结果保持有效，后一条同连接请求返回自己的不同静态结果；不期望已完成调用回溯变成取消。

B. 已开始读取：entered→Cancel→caller取消→远端取消栅栏ACK→inspect→release。当前56c的op8/op10端口ctx仍未取消，尚占一个执行slot。可在release前提交B并等其唯一admission事件；release后B才进入并成功。最终资源/执行次数归零、各请求结果对应正确。不把占用解释成四个recovery cohorts或生产饱和。

C. 已开始修改：与B相同，但端口在release后提交一个静态副作用。ctx仍有效、副作用恰好一次；caller仍只得到取消，不能把caller取消当作未提交。将整个13或14错误设为CancelRunning=true会在此用例暴露语义回归。

D. 排队取消：A占住执行slot；提交B（读取或修改），通过限量原始Observer的 service_admission ok +本例唯一wire字节数确认B已入队。Cancel B，等取消栅栏和 service_queue expired；在A仍阻塞时提交C，要求C获得刚释放的队列位置。再release A，join A/C，最终B端口执行次数与副作用均为0。读取/修改的排队取消预期相同；取消后异步移除必须等待事件，不能只等caller返回。

E. owner真实250ms deadline：entered后不release，等待真实 context.WithTimeout(250ms) 的caller终端，然后取消栅栏/inspect/release；接受公开DeadlineExceeded/ErrTimeout族，当前服务14ctx仍不因callerdeadline取消。这里等待真实timer，不使用sleep，也不设紧的毫秒性能断言。Service13计划5s预算由源码绑定；主用显式取消且父deadline为5s，避免每例都耗5s。若需逐点验证5s timer，可单独加一例真实5s deadline，其结果不能由缩短timer冒充。

所有同步等待只用有限测试guard（例如每屏障3s，实际5s例guard另设10s），guard超时是失败，不自动重试。清理总是先释放所有受控端口、join所有调用，再Stop client/server。记录最大revision的queue/inflight/retained归零事件；Observer有丢失则资源证明不完整。不要读取Prometheus的1/32 duration histogram来决定单请求屏障。

## 响应竞态和候选成功标准

取消已经完成后再放行回复，以及成功已经返回后再取消，是两个可确定的边界。不要把release和cancel同时抛入调度器后断言必须某一方胜出；Conn.Call select可合法观察任一先就绪结果。若以后增加同时发生的竞态例，只能断言恰好一个公开终端、无跨请求错配、无重复修改、资源最终释放。

以上是当前policy的刻画：B/E读取“继续执行”应通过。如果未来明确决定读取应停止，读取例才改成“远端取消栅栏后ctx取消且释放资源”；同服务修改例仍必须继续。当前实现会在新的读取期望下失败，这是所需的red，不代表它已经是原始消息过期根因。不得据此直接更改整服务CancelRunning、关闭连接或加入重试。

## 有限回归产物

未来执行若获授权，输出一份小JSON，仅含source/tree、service/op枚举、phase顺序、caller结果族、ctx检查结果、执行/副作用计数、dial代数、三项资源末值和cleanup完成布尔；不含UID/Channel、payload、地址或任意错误文本。每例不超过32条阶段事件，总例数固定。保留第一失败，不以重跑选绿。此设计及失败矩阵现在均未实现、未运行。
