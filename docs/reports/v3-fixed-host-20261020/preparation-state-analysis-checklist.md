# Fixed-host 准备状态：离线分析口径

适用于 `fixed_environment.schema=wukongim/conversation-fixed-host/v1`。
本清单只定义归档后的读取/计算方法，不启动新负载，也不预判本轮结果。
**180 秒对齐的是最早产品进程的估计年龄；它没有对齐 GC 周期、
heap 状态、flush/compaction 状态或停止业务写入后的等待长度。**

1. **先核对身份和时间。** 每 block 固定 harness/product/binary/profile
   身份、`valid`、三节点 PID/starttime、clock tick 与误差，保留全部窗口，
   不选最好一次。读 `stages[]` 的
   `name/started_at/finished_at/duration_ns/failed`；时长以单调时钟
   `duration_ns` 为主，不把跨 block UTC 差当进程年龄。
   并列 `preparation_age_seconds`、`release_age_seconds`、
   `window_start_age_seconds[]` 与各节点 `estimated_start`。
   后启动节点的年龄小于最早节点，不把三者都标为恰好 180 秒。

2. **区分准备工作与等待历史。** 保留 startup（含 suite readiness）、
   显式 readiness、prepare、eviction、preparation_age_wait 各阶段。
   另计算 `released_at - prepare.finished_at`、
   `released_at - eviction.finished_at`、
   `release_age_seconds - preparation_age_seconds`。三者口径不同：
   前两者含后续边界观察/其他阶段；最后一项由单调时钟年龄之差得到。
   “等待段”仍有后台工作和观察，不能称为整个进程无活动。
   比較 B/C 同版本重复 block 的这些时长，再比较版本；
   相同年龄但 prepare 更早结束的 block 可能经历不同后台周期。

3. **按同一节点、同一完整标签集连接四个边界。** 顺序为
   `after_readiness_before_prepare`（R）、
   `after_prepare_before_eviction`（P）、
   `after_eviction_before_age_wait`（E）、
   `after_age_wait_before_original_warmup`（A）。
   数据路径：
   `boundaries[].metrics[].{node_id,error,scrape,selected_samples}`。
   注意 `selected_samples` 使用 **`Name/Labels/Value` 大写字段**；
   键为 `(node_id, Name, sorted Labels)`，不能只按 Name 汇总 store。
   验证 HTTP 成功、无 error、same-body SHA/time/bytes 完整；
   缺失、重复、非有限值或 counter 负差明确标为 unavailable/reset，
   不能补零，也不能跨不同进程求差。

4. **边界区间不是精确阶段计时。** 分别报 R→P、P→E、E→A 差分，
   对每个节点使用该节点两次 `scrape.started_at/finished_at`；
   可用 midpoint 差作为观测间隔估计，同时保留起止区间。
   不把三个串行节点 scrape 当同一瞬间。E→A 包括等待与边界观察，
   A 在 release 之后；每窗原有 warmup 仍在 A 之后。
   首窗 `metrics_before` 可继续观察 A→warmup 后的 GC/heap 变化，
   但原 `metrics_before` 没保留 storage family，不能补出对应
   storage 差分或精确单节点 scrape timestamp。

5. **GC 与 heap。** 每节点每区间计算：

   | 字段 | 计算/解释 |
   | --- | --- |
   | `go_gc_duration_seconds_count` | ΔGC 次数；不是强制 GC 次数 |
   | `go_gc_duration_seconds_sum` | Δ累计 pause 秒；除以 Δcount 得该区间观测均值（count=0 留空） |
   | `go_memstats_alloc_bytes_total`、`mallocs_total`、`frees_total` | Δ累计分配字节/对象、释放对象；可附 Δ/观测秒 |
   | `go_memstats_last_gc_time_seconds` | 报绝对值及 `scrape time - last_gc` 的年龄区间；时间戳差不是 GC 次数或 pause |
   | `heap_alloc_bytes`、`heap_objects`、`heap_inuse_bytes`、`heap_idle_bytes`、`heap_released_bytes`、`next_gc_bytes` | 报各切点状态及带符号变化；不是累计工作量，不把负差当 counter reset |
   | `go_gc_duration_seconds{quantile=...}` | 切点 summary 值，不做差、不跨节点相加，不当本区间 P99 |

   `last_gc_time_seconds=0` 记为尚无 GC 时间，不计算从 Unix epoch 的年龄。
   首先列出各 node 在 A 的 GC 次数、last-GC age、heap/next-GC 状态，
   再看 E→A 是否发生 GC/分配变化。三节点累计 pause 之和不是全机
   wall-clock 停顿，也不覆盖并发 GC/assist CPU。仅凭 count、last-GC
   或等待长度不能识别某次 GC 的触发原因；不推断 Go 强制 GC 周期因果。

6. **存储按完整 `store` 标签比较，遵循语义而非 Prometheus 类型。**
   `wukongim_storage_pebble_*` 有不少以 Gauge 暴露的累计量：
   `flush_count`、`flush_bytes_written`、`compaction_count`、
   `compaction_bytes_read/written`、`wal_bytes_in/written`、
   `write_stalls{reason}`、`write_stall_seconds`、
   `wal_fsync_count/seconds` 可在同进程/store 内作累计差。
   不跨 store/节点把 bytes read+written 当逻辑业务写入量。
   `compaction_estimated_debt_bytes`、`compactions_in_progress`、
   `compaction_in_progress_bytes`、`flushes_in_progress`、memtable 大小/数、
   WAL 活文件/大小、SSTable 大小、read amplification、stall active
   是切点状态，保留原值和带符号变化；max duration 不是区间累计时长。
   A 时 in-progress=0 仅证明切点没观察到正在执行；E→A 的 count 增量
   能证明区间完成过工作，但不能给出精确开始/结束或全部峰值。
   不同版本缺失某族记 unavailable，不能当成该版本零 compaction。

7. **关联与归因分开。** 输出每 block、每 node 的阶段时间、R/P/E/A
   状态和三段差分，再关联首窗 P99/drops/服务端阶段均值及实际 cgroup
   CPU/pressure Δ。资源 cut 同样包含 warmup/观察开销，不是精确
   60 秒积分。若首窗差异随 GC age、heap 或 compaction debt 变化，
   只能报告关联；没有窗口内时间对齐或机制对照，不声明其导致延迟。
   固定进程年龄提升可比性，但不能声明“GC/compaction 完全对齐”
   或“已排除准备历史”。全部结论保持 diagnostic 口径。

来源：冻结候选的 `fixed_environment_e2e_test.go` 定义字段和四次边界；
`test/e2e/suite/metrics.go` 定义大小写样本字段与 scrape receipt；
`pkg/metrics/storage.go:115–173,200–354` 定义累计/瞬时存储量。
样本不是完整 raw metrics body 归档，SHA 不能恢复未保存的指标。
