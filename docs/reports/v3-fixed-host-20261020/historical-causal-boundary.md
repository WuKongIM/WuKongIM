# 原失败的因果边界复核

离线复核原失败 `39368295521`（b469 / EPYC 7763）及旧同机对照
`39369947381`（beta.24、b469 / EPYC 9V45）。只读归档 raw 和冻结源码；
未运行负载、修改仓库或调度工作流。本文件不声明正在执行的固定环境
`39453556254` 或候选 `ef798352c40a94e57f768f76b2f4713712f827fe` 已通过。

## 两项需要修正的历史解释

**原来的 RPC/完成请求差异有完全解释它的请求混合模型，不能再单独作为
新增 RPC 或路由扇出变化的证据。** 原失败完成的 sync 比例较高，是 list
丢弃所致。五类精确 RPC 总数和 edit-slot barrier count 均与同一组整数
系数逐窗精确吻合；九窗共 54 项残差全部为零。

`L`、`S` 分别来自 `windows[i].phases[]` 对应 endpoint 的 `completed`，
包含截止后成功排空的请求，不能换成 `completed_in_window` 或 offered 数。
观察总数是逐节点 `metrics_after - metrics_before`，仅累加 `result=ok`。

| 观察系列 | 完成请求混合模型 |
| --- | --- |
| RPC `service=slot message updates` | `8L + 24S` |
| RPC `service=slot permission metadata batch` | `8L + 16S` |
| RPC `service=slot runtime metadata` | `8S` |
| RPC `service=channel committed reads` | `2S` |
| RPC `service=channel conversation heads` | `2(L+S)` |
| Read stage `scope=edit_slot,stage=barrier` count | `12L + 36S` |

RPC 使用 `wukongim_transport_rpc_client_total`，不是采样 duration count；
stage 使用 `wukongim_conversation_read_stage_duration_seconds_count`。

| 运行 / 产品 | 窗口 | L | S | 六项模型残差 |
| --- | ---: | ---: | ---: | --- |
| 原失败 b469 | 1 | 10,559 | 3,594 | 全部 0 |
| 原失败 b469 | 2 | 10,858 | 3,600 | 全部 0 |
| 原失败 b469 | 3 | 10,968 | 3,600 | 全部 0 |
| 同机 beta.24 | 1 | 12,000 | 3,600 | 全部 0 |
| 同机 beta.24 | 2 | 12,000 | 3,600 | 全部 0 |
| 同机 beta.24 | 3 | 12,000 | 3,600 | 全部 0 |
| 同机 b469 | 1 | 12,000 | 3,600 | 全部 0 |
| 同机 b469 | 2 | 12,000 | 3,600 | 全部 0 |
| 同机 b469 | 3 | 12,000 | 3,600 | 全部 0 |

例如原失败 message-update RPC 合计 `518136`，模型为
`8×32385 + 24×10794 = 518136`；每个匹配版本为
`8×36000 + 24×10800 = 547200`。所以原约 12 RPC/完成请求和匹配
11.692 RPC/完成请求的差异无需假定额外扇出即可解释。这是对聚合数据的
精确拟合，不是逐请求追踪，仍不能证明每个节点的 placement、局部/远端
分布或实际 CPU 工作完全相同。CPU 型号、资源与准备历史仍未控制。

**历史 CPU quota 仍未知：根目录零节流不是实际 service 未受限的证据。**
原六次 `host_snapshots[j]["/proc/self/cgroup"]` 均为
`0::/system.slice/hosted-compute-agent.service`，但采集的是
`host_snapshots[j]["/sys/fs/cgroup/cpu.stat"]` 和根目录 `cpu.max`。
后者返回 `no such file or directory`。归档没有进程 mountinfo，没有
解析实际 service 的 cpu.max/cpu.stat，也没有各祖先控制文件。
不能假设 membership 对应的目录就是挂载根，不能把缺失当作 unlimited，
也不能由根 `nr_throttled/throttled_usec` 增量 0 排除叶/祖先 quota。
旧报告的约 94% cgroup 用量来自这个根观察范围，不是隔离出的四个
实验进程独占 scope。

## 已确认的失败机制与成本候选

直接的 drops 机制是驱动有界 jobs channel 满。原每 endpoint 8 workers；
list queue=100、sync queue=30。`measureRequests` 的非阻塞入队失败递增
`dropped`，并非服务端拒绝或持久消息丢失。List drops 为
1441/1142/1032，持续到每窗末尾，最后十秒仍为 245/276/182。
对应 driver-wait P99 为 750.03/619.83/607.65 ms。它合并了调度迟到与
队列等待，归档不能将两者精确拆开。List 实际 174.25/179.20/181.05 QPS；
假设满队列以该速率服务，100 个请求约需 574/558/552 ms，
与总延迟 P50 596/587/579 ms 同量级。这是数量级校验，不是实测等待均值。

服务端成本本身也占用了请求生命周期：

| 原窗口 | list handler 平均 ms | sync handler 平均 ms | 产品 CPU 秒 |
| --- | ---: | ---: | ---: |
| 1 | 40.484 | 108.553 | 187.22 |
| 2 | 39.325 | 104.666 | 190.65 |
| 3 | 39.120 | 104.874 | 190.04 |

Handler count 与各 endpoint 成功完成数逐窗相等。保持 list 200/s、8 workers
所需的平均总 worker 占用预算为 `8/200=40 ms`；服务端 handler 均值已达
这个量级，尚未计外层 middleware、客户端收包/解码/校验。因此不能把积压
全归于客户端 JSON。此预算不构成改变并发或验收阈值的理由。

旧窗口产品使用约 3.12–3.18 CPU；host non-idle 约 95%，host CPU PSI some
约 60–62%。结合排队，支持共享 CPU 争用/可用处理能力不足这一根因候选，
但资源压力是关联证据，尚不能区分 CPU 型号、有效 quota、调度竞争、
后台工作或准备历史各自的贡献。

服务端阶段提供进一步边界：

- 三窗合计 edit-slot barrier 均值 3.876 ms，storage 均值 0.0802 ms，
  两者 count 都为 777,204；barrier 是值得保留的等待/调度候选，
  不能把它当 CPU 时间，也不能把跨 Slot 的并行 sum 相加成请求延迟。
- Persisted heads metadata/heads/edit-overlay 均值分别
  9.149/10.862/16.624 ms。多个阶段一起变慢，没有定位到一个独占异常。
  阶段人口不全相同且有嵌套，不能相加解释 list P99。
- 服务端 response 阶段只占相应 handler 平均 wall time 的约
  1.37–1.39%（list）/1.49–1.52%（sync），不支持“服务端 JSON 输出
  占据主要 handler wall time”的解释。与驱动 JSON 成本必须区分。
- GC count 三节点合计 428/434/436，累计 pause 为
  0.3618/0.3506/0.3928 秒；每窗分配约 34–35 GB。
  这些数值不支持把持续数百毫秒队列普遍归为长 STW；
  未覆盖并发 GC CPU、assist 等成本，也不能排除个别 pause 触发瞬时积压。
  缺少失败窗口的执行 trace/时间对齐，不能关闭 GC 关联假设。

独立 profile 也处于较差的运行状态：其 list 2000 offered 中丢弃 67，
P99 616.25 ms；但它在前三窗之后单独采集，不能替代原窗。
现有 pprof 可确认该短阶段的 CPU 去向：driver Unmarshal cumulative
4.43/6.82 CPU 秒（64.96%）；server task 标签 rpc_service 35.44%、
conn_write 13.57%、conn_read 8.22%、message_update_read 6.53%、
raft_worker 6.28%。RPC、持久读取、分配/GC 和驱动解码是合理成本候选，
标签不是可删除开销，也不等于版本新增成本。三个 server 文件各约 8.14 秒，
开始于独立负载 warmup 前，既不覆盖完整十秒负载，也不覆盖任何此前
60 秒窗口；累计函数比例重叠，不能相加，CPU profile 不记录全部阻塞等待。

可排除或削弱的解释限于：原窗没有业务错误、runtime loads 或 membership
writes；persisted admission 没有拒绝（已有 occupancy 样本≤8，上限16）；
失败并非只出现首窗/首段；上述 RPC 差异不能单独证明新增扇出。
不能据此排除 effective quota、全部 I/O 等待、GC/调度、ReadIndex 等待，
也不能认定 b469 版本回退或认定硬件是唯一原因。

## 本次新观测补上什么，仍没有补上什么

`ef798352c` 的固定环境 receipt 在
`fixed_environment.driver/nodes[].cgroup_v2` 保存实际 membership/mount
解析与每个可见祖先的 cpu.max、cpu.stat、cpu.pressure、cpuset、
memory/I/O controls；`window_resource_cuts` 在三窗前后各重采一次并验证
PID starttime、affinity、membership 未变。缺失保留 error，不视为无限。
这补足历史“读根目录代替实际 service”的归因缺口；仍不证明不可见
mount namespace 外祖先的限额。

它另外记录 getconf CLK_TCK、proc uptime/start ticks、单调时钟估计误差、
准备分段与 180 秒 release age，准备超时拒绝；准备边界保留原 GC/storage/
compaction 样本和同一响应 SHA/time。原 raw 的 diagnostic filter 没保存
storage metrics，无法从历史补出 compaction 曲线或实际节点启动年龄。
新 boundary 也不是完整 raw body 归档。

新 resource cut 包围原 `measureMixed`，包含 warmup 和观察开销；
它不是精确 60 秒的 CPU/throttling delta。CPU profiles 仍为另起短窗，
不覆盖真正三次 60 秒。新固定实验可判别受控主机上的重复性和版本方向，
不能追溯证明旧 7763 当时的 service quota、准备状态或某个具体暂停。
若要把原失败归结为一种独占原因，仍需相应旧资源环境的可重复对照及
负载时段内的时间对齐证据；本轮没有追加该实验。

## 重算

```bash
python3 docs/reports/v3-fixed-host-20261020/historical-causal-boundary.py \
  --repo /absolute/path/to/WuKongIM \
  --output /tmp/historical-causal-boundary.json
```

脚本只读取三个归档 `report.json.gz`，计算 raw SHA-256、逐窗完成数、
54 个模型残差、原 cgroup 路径/原值、阶段 sum/count 与 GC 差分。
不启动产品、不访问网络。已执行结果为同目录
`historical-causal-boundary.json`，九窗 `all_mix_residuals_exactly_zero=true`。

精确输入：

- `docs/reports/v3-rc-20261020/qps/report.json.gz`，
  解压 SHA-256 `eca951e9fc455f1ec803c3656d33eacd7654218a998bad1c64e42ebb91306487`。
- `docs/reports/v3-rc-20261020/qps-comparison/{comparison/,}report.json.gz`，
  每份压缩/解压摘要在 JSON 中记录。
- 原 profiles 位于 `docs/reports/v3-rc-20261020/qps/`，
  标签与准确时段核对 `v3-performance-followup-20261020/profile-headers.json`；
  已离线重新运行 `go tool pprof -symbolize=none -top -cum` / `-tags`，
  与归档 driver cumulative 和 server tags 一致。
- 冻结 b469 `conversation_qps_test.go:312–411` 是入队和时间定义；
  `stress_test.go:146–175` 是 warmup/窗口定义；
  `diagnosis_test.go:174–199` 是保留字段过滤；
  `internal/access/api/conversation_list.go:77–132` 与
  `conversation_sync_legacy.go:39–108` 是 handler/response 边界。
