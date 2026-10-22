# 精确79a本地 profile：已确认等待位置，尚未确认上游因果

本轮native产品SHA `5b30743481398ad8175476d18ffbc676c6da5288657919ae8e704324e67351ab` 已与run preflight核对；clean source为79a7177032a6ae1ec7015cefe0e6f94eb0f19f06。适用AGENTS/FLOW与解释源码、依赖源码均固定SHA。离线工具通过显式安装的Go1.25.11、GOTOOLCHAIN=local/GOPROXY=off只读定位已缓存pprof可执行文件，再直接执行四个top40/-cum，`-symbolize=none`；无编译、下载、产品启动、网络或新增采集。17个原始输入与产品二进制在分析后重新验SHA。

原报告窗口UTC20:42:06.546400–20:42:17.288828，elapsed10.742274875秒；ACK12001/attempt12882/firstfail160/rejected172/RECV14433，sample102/delivered100/expired2。原判 `overall_first_attempt_rate`、exit7原样保留。**它先触发本地首尝试门禁，不能把drain后expired2直接认定为原云message_loss同bug。** 这是三节点共用Mac、GOMAXPROCS2、带profile扰动的诊断，不是Linux性能配对或正式验收。

## 单次GOR的互斥分解

GOR准确时间UTC20:42:11.818699，比首grant晚5.272299秒，共4928个goroutine、156个聚合栈。下列数字是该单点栈计数，不是等待时长；不同表之间可能重叠，不能相加。

| 观察 | 精确路径与数量 | 能证明与不能证明 |
| --- | --- | --- |
| 冷lane直接等待 | 204个 `AuthorityRecovery.recoverPage:132 → runtime.selectgo/gopark`；其中127 RPC服务、57 delivery worker、20 helper | 已确认lane获取等待存在；不知道每页持有多少lane或等待多久 |
| 已持lane的owner读 | 2个 `recoverPage:167 → RecoverRoutesByTargets:106 → readOwnerPage:182/196 → Conn.Call:207` | 两条均走源码的remote分支，是向非本node owner等RPC；没有精确目标node、request或UID，不能连接到node2 apply栈；无owner-admission:67堆积 |
| fresh Slot barrier | 212个 `Runtime.readBarrier:96` 等request响应，均经 `readSendPermissionSlot:594` | 确认fresh barrier调用者在等完成；无法分解control队列、quorum、durable apply或调度 |
| Slot apply完成等待 | 7个 `runApplyTask:361 → FSM.ApplyBatch → meta.WriteBatch.Commit → Coordinator.submitResult:329` | 请求已通过queueSlots准入且进入requests，等pending.done；不是准入槽等待，也不直接证明磁盘等待；缺Slot/request对应关系，不能把212全部连到这7个 |
| 物理commit执行侧 | 2个 `Coordinator.run:413 → commit:598 → engine.Batch.Commit(true) → Pebble.commitPipeline.publish:492`；另1个同publish来自raftlog.flushWriteRequests:184 | Pebble该行同时允许等序号发布或WAL sync，不能再细分；未见Coordinator.collect或Slot.compactLog栈，仅代表本次快照 |

320个delivery runWorker互斥闭合为：209远端presence authority RPC、57本地冷lane、46 presence多leader WaitGroup join、1 owner-recovery RPC、7 owner-push阶段join/inline。另414个post-commit handoff均在 `orderedPlanQueue.enqueue:81` 等全局recipient-plan容量token；不是store append队列。此处可证明存在delivery背压，不能由计数计算其历时或expired归属。

664个 `Conn.Call:207` 等响应互斥闭合为：399 EndpointsByTargets、102 ForwardSendBatch、83 send permission、37 Slot proposal forward、23 quorum exchange、9 authoritative metadata、8 PushBatch、2 owner recovery、1其他callService。调用者分类不说明远端服务内部究竟等待什么。

## 既有指标对等待段的约束

两个顺序保留cut之间，permission barrier均值node1/2/3为272.920/230.390/288.087ms，snapshot约0.013/0.012/0.011ms。node2 Slot apply完成均值按其leader Slot为249.5–273.4ms，ReadBarrier为230.389ms。源码计时跨度重叠，不能相加；无quorum/control/apply分段或请求级join，仍不足确定barrier上游原因。first cut有限apply gap在terminal均归零，只证明两个切面的进度收敛。

node2 meta WAL fsync Δ103次/Δ1.399439秒（约13.587ms/次），channel_log Δ73/Δ1.387398秒（约19.005ms/次）；slow与write-stall所保留值/差均0。它们不能排除短IO等待，更不能单独解释230ms barrier。**meta commit batch/request阶段耗时与queue系列缺失**；现有commit observer只有store=message，不能把message的均值套到7个meta请求。message批commit约39.470ms、collect约5.071ms，均为异步cut差；普通0.5ms配置窗口或这一个collect均值本身不构成瓶颈证明。

Pebble compaction与physical Slot snapshot/log compaction是两种机制。现有数据没有为失败窗口建立Slot compaction的实际发生/时序，因此本报告不支持据此默认启动一次compaction参数对照；单点compactLog=0也不证明全窗未发生。

## CPU和范围限制

CPU实际UTC20:42:06.791800–20:42:16.921169，duration10.129369秒、样本4.21秒。writev累计1.08秒/25.65%，readLoop0.32秒/7.60%，madvise0.39秒/9.26%；包裹栈cumulative不能相加。样本约相当于单核41.56%，不是主机利用率、off-CPU占比或饱和证明。它支持描述transport/runtime样本权重，不能将syscall热区直接定性为网络/磁盘根因。

profile capture完整并joined；natural SEND admission终点仍未知，CPU/GOR可覆盖drain。仅node2一个GOR点，没有等待历时、持有者lane分布、跨节点栈、UID/request/Slot因果join。root原始CPU/GOR二进制、原日志、配置、身份和payload不在公开allowlist。cleanup receipt UTC20:43:49.817745确认PGID8675及64端口清零，本轴没有live核查或操作。

目前优先的三个可证伪机制仍分开保留：①首尝试拒绝附近的fresh barrier/metadata完成等待；②已接纳投递的冷lane/远端presence等待与recipient背压；③transport/共享主机对上述路径的放大。①②已有等待位置证据，根因和相互因果仍未知；③尚无区间资源/等待分解支持。不得靠增cap、timeout、改expiry/retry或gate强行通过，也不据本轮expired下结论为cloud同bug。后续最小验证由root根据这些缺口选择，本轴不再扩诊或发起实验。
