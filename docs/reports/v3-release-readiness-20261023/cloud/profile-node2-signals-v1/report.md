# c253 本地 node2 CPU 与固定信号的离线结果

**先读来源纠正及 `stamp-semantics-clarification/report.md`：本轮不是已验证的 79a 候选复现，也不是 Linux 性能验收。** 产品 c253 与 CLI e36e 都内嵌 Go1.25.11 / a804f4a / modified=false，启动与源码分析上下文声明79a。a804仅是VCS stamp，不能单凭它判断实际编译源；root报告CLI有独立79a构建证据，产品prebuilt完整来源链仍待审计。两个 revision 有普通消息产品实现差异，不能声明整体等价。`provenance-addendum/report.md` 独立保留完整纠正与33个解释文件的逐SHA等价范围；主机 `go version` 的1.25.0不代表产品编译版本。

本目录只使用已经终止、清理的单次诊断材料。未运行新服务、负载、SSH、云查询或主动 profile；没有修复/性能通过结论。诊断技能走有限只读证据路线，未构建新复现循环。最终因果分类按 root 要求暂缓；条件性假设及最小未来验证见 `provisional-hypotheses.json`。

## 窗口与原判

原报告 measured wall 为上海04:09:30.192341–04:09:40.898746，单调 elapsed=10.706257667秒。sent=logical ACK=12001，attempt12802，firstfail173，rejected185，retry801，RECV=RECVACK14618；sample102/delivered100/expired2，terminal retry exhaustion0。原判 `product_failure / overall_first_attempt_rate`、exit7 原样保留，不能把原判标签当上游根因证明。

整窗SEND/ACK约1120.93/s、attempt1195.75/s，包含失败停止与完成阶段；前5.042267秒周期切面 sent10006（约1984.42/s）、ACK7932（约1573.10/s）、firstfail/rejected3、expired0。它不支持“生成器最大只能1121QPS”。10000用户完成CONNECT/full-sync，harness failures/command saturation/underdelivery/transportRejected均0，三worker最终gen1/snapshot2，10000 generation_stop，drain未超时；因此此本地二进制场景并非 harness-invalid。上述有效性不补足候选版本来源错误。

## CPU：可以描述的样本与不能证明的等待

保留CPU40474B、SHA d3c5e46713de6add18641f2f4f4a0672cbac6db16322d555cdb0236cf32dcd23。已有top/-cum派生的SHA与receipt已独核；未重复跑pprof。仅解码gzip protobuf头的时间/周期字段，未输出其sample、strings或标签：实际窗口UTC20:09:30.437701000–20:09:40.607191708，duration10.169490708秒，约比measured wall起点晚245.360ms、比final wall早291.554ms。Helper父进程launch不是CPU采样起点；实际SEND admission关闭时刻未知，CPU可包含drain。

4.34秒CPU样本约相当于一核的42.68%，**不是主机利用率、两核GOMAX饱和度或等待占比**。保留top60中：

| 栈/叶子 | 样本权重 | 解释边界 |
| --- | ---: | --- |
| transport conn.writeLoop cumulative | 1.29s /29.72% | 下含writev1.28s，不能相加计两次 |
| writev→syscall.syscallX | 1.28s /29.49% | transport发送路径样本，未按RPC服务拆分 |
| transport readLoop cumulative | 0.38s /8.76% | 读路径样本，不等同网络故障 |
| runtime.madvise flat | 0.36s /8.29% | 内存运行时开销 |
| gcDrain cumulative | 0.35s /8.06% | 与其他运行时/系统调用栈可能重叠 |
| Pebble LogWriter.flushLoop cumulative | 0.22s /5.07% | 不能据此量化磁盘等待时长 |
| PresenceRecoveryOwners.RecoverRoutesByTargets cumulative | 0.05s /1.15% | 低CPU不能排除等待/串行化 |

`runtime/pprof.Do`、goroutine Registry高cumulative是包裹调用栈，不能作为额外独立热点。CPU profile不记录未运行时的完整等待时间；不能凭syscall样本称“已证实I/O瓶颈”，也不能凭总CPU低排除排队。GOR缺失、heap未请求，capture整体partial/valid=false。原始HTTP状态/curl退出原因未保留，不能判定GOR缺失究竟是超限、超时还是其他具体原因。fault-time goroutine、mutex、block、trace均缺失。

## 权限与投递：机制支持和关联缺口

服务日志与终态 `cohort_busy` 分节点均为110/69/10，共189；每条日志均固定映射 `permission read admission busy` → route_not_ready/backpressured。匹配源码路径在a804和79a逐SHA相同。该分支在新权限读取执行前拒绝：owned calls1024、owned bytes16MiB或active cohorts64中任一达到边界即可能发生。**采样node3确为64/64 active cohorts，calls334/1024、bytes1424736/16777216**，支持当时实际触及active-cohort边界；不能据单次切面推断另外179次拒绝的具体配额。first切面node1/2为41/38 cohorts，终态三者归零。

客户端只记录185个rejected、173个firstfail，服务端189条：无逐包身份/数值reason/失败耗时，不能硬join189到185或两个expired。聚合错误与源路径支持前置拒绝机制，不证明这些未接纳请求本身后来成为已接纳样本的丢失。

两次异步保留指标之间，permission `barrier`均值node1/2/3为209.116/283.174/242.767ms，snapshot仅0.01258/0.05162/0.01233ms。源码 `ReadSlotBarrier` 覆盖fresh quorum ReadIndex、worker/control排队与durable apply完成；这些均值**不是每次拒绝耗时，也不能继续拆为磁盘/CPU/网络延迟**。所有RPC采样权重合并后，presence authority约417.85ms、node permissions333.88ms、Slot propose333.96ms、channel quorum100.98ms、delivery push6.65ms；采样为固定比例、窗口为异步cut差，不能当精确端到端网络RTT。

投递不完整日志232=presence231+owner_push1，均durable timeout；终态RPC timeout counters为presence authority291、presence owner action4、delivery push1。这些是独立统计层，不能自动互为消息数或与2 expired关联。此轴未重新查询持久history，也未新增身份级因果证据。

## 冷 presence 页假说的源码边界

a804/79a相同的 `internal/usecase/presence/recovery.go` 将冷页按HashSlot%32选择lane，按固定升序取得并持有至恢复/安装完成；页上限512UID/256targets。证明已就绪的组绕过冷恢复。内层 `PresenceRecoveryOwners` 有节点级4个admission，按当前owner依次读取、每个child最多250ms，完成后复核membership与authority；不是给每HashSlot另发一次RPC，也不是32个owner RPC并行。

多lane页在等待owner读时可能阻挡其他冷页，是可证伪的convoy假说；此代码固定锁顺序本身不形成lane循环死锁。当前材料没有实际冷页数量、lane数量分布、lane等待/持有时长、owner-admission等待，GOR也缺失，**不能宣布冷页锁竞争根因**。Directory每authority的1024条FIFO proof cache存在，并不能仅凭配置证明本轮cache churn。CPU中恢复栈低权重既不证明它阻塞，也不能排除它阻塞。没有canary具体时间或冷页到ordinary expired的精确关联。

## 资源与过程连续性

六cut实际gateway histogram sum=count=le1，终态4307+4282+4213=12802批均为1。初次指标中三recipient queue均1024/1024、inflight320/320；person_directory node1/2=512/512，node3=287/512。最终报告service_queue100%只保留aggregate winner数值；原始异步cut提供独立pool证据，不能把它们视为同时同一采样。

两条measured ps观测UTC20:09:33.260938、38.335354包含相同3产品PID，无其他WuKongIM进程；这证明两个点的身份连续，不能替代完整process-start-time连续性。ps lifetime-PCPU并非interval CPU。标准process_*在六份Mac产品scrape均missing；自有host/process观测与四个复制的同主机host样本不能代替四台独立机器headroom。GOMAXPROCS指标为2；整台Mac10CPU/32GiB并共享磁盘与load/observer，不能据低host平均或单点内存62.67%排除瞬时争用，也不能据CPUprofile指认争用因果。

UTC2026-10-22 20:11:13.474352 cleanup receipt证实PGID95883零剩余进程、25000–25063全64端口零，未做任何live补查。下一步应先完成候选二进制来源闭环；在此之前本目录只描述c253实际样本并保留条件性预测，不作79a复现、cloud根因、版本验收或修复完成结论。
