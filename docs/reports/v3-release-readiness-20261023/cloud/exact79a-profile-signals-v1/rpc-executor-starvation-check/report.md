# RPC执行池饥饿假说：有限检查

**共享executor存在；“固定总128池被presence占满”的模型不符合源码。** 79a中，一个`transport.Server`只有一个ants Executor；`Handle`注册每个service时把总容量增至Σ该service的Concurrency，同时每个service各有独立tokens和队列（`server.go:93–118`、`service.go:150`）。默认presence authority/owner、node permissions各128；quorum exchange512；每种ordered Slot/Controller Raft为1。没有owner/control专属物理executor；Raft/control只是不同发送优先级。

调用链为：presence authority handler → cold `recoverPage` lane → `RecoverRoutesByTargets` → 非本node owner的`ReadOwnerRoutes*` RPC。下游owner handler使用独立service token/queue，但同共享executor；Slot/permission/quorum请求也如此。返回的RPC response由`Conn.readLoop:251 → handleRPCResponse`直接投递pending channel，不再申请handler池。

每service默认queue4096/64MiB、retained128MiB；入队超界立即busy。ants Invoke为nonblocking，满时立即busy；service pump保留请求，以名义 **10µs** timer重试至队列deadline（默认5s），不是1ms。执行默认30s，混合服务未启用running caller cancellation；deadline不等于请求立刻释放。

冻结node2切面（非GOR同一时刻）：

| service | first queued / retained | terminal queued / retained |
| --- | ---: | ---: |
| presence authority | 207 /335 | 0 /0 |
| presence owner | 0 /0 | 0 /0 |
| node permissions | 0 /57 | 0 /0 |
| quorum exchange | 0 /2 | 0 /0 |
| Slot Raft batch | 0 /0 | 0 /0 |

335−207=128与authority自身token上限相符，但retained含ownership尾部，不宜当精确瞬时handler数。GOR另见127个authority RPC handler等lane、1个该handler等owner RPC，支持**该service自身**达到128；owner-read远端位置、212个barrier等待均不建立全局executor耗尽链。

Σ并发额度使presence的128个正常执行额度不能单独消耗所有其他service预算，且first cut owner/quorum未排队，削弱“共享固定小池导致循环饥饿”假说；**这不证明实际总pool充裕或所有饥饿不存在**。这些冻结派生未保留全局executor busy/capacity/waiting/rejected gauges；无远端owner同刻快照、提交重试历时或因果request join，不能判死锁。可证伪条件是：下游自己的tokens仍可用却长期无法进入executor、全局busy持续到capacity且有submit重试/拒绝；仅authority队列满不足以支持该链。

本检查只读源码和已有派生，原17项manifest不变；未新解profile、重采、构建、测试或启动负载。
