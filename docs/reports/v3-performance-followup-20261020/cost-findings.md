# 已有性能证据的资源与服务端成本复核

本轮仅分析保留文件与冻结源码，没有重新压测、下载新 artifact、修改仓库或购买云资源。源数据是 [39368295521](https://github.com/WuKongIM/WuKongIM/actions/runs/39368295521) 和 [39369947381](https://github.com/WuKongIM/WuKongIM/actions/runs/39369947381)；两次候选均为 `b469c6e5e2b4ff65611a8fe3389fb5034de3f152`，基线为 beta.24 `4d44b39dda6fdaa7dafb637ca6cfb718e9530f77`。结论不能用作迁移修复 `98c25091` / fixture `dee2b5c7f` 的性能验收。

**新增结论：原失败是同机 CPU 高占用背景下持续的压测驱动队列过载；匹配运行观察到服务端平均处理时间也变长，所以不能仅归因于客户端 JSON 解码。但现有证据仍不能证明产品版本造成了性能回退。**

## 先声明可证伪假设

1. **产品读取/hydration 路径成本增加。** 若成立，在相同主机、固定 harness/profile、反转执行顺序后，b469 的请求与服务端阶段成本仍应持续高于 beta.24。已有同机成本为弱支持；几乎相同的分配量及完全相同的调用数不支持“新增大量工作/分配”解释。
2. **同机驱动和三个服务端争抢 CPU。** 若成立，CPU 饱和时会出现驱动等待与吞吐下降，而服务端 admission 不必拒绝；独立驱动或更多 CPU 应缓解。原失败直接支持前半预测；当前 workflow 不支持不改代码地隔离驱动，后半预测未验证。
3. **执行位置或主机状态影响尾部。** 若成立，一次反序 pair 会使慢侧跟随第二个位置，或使版本差距显著缩小。已有前序 pair 无法排除；不同主机型号的两个运行不得拼成版本对照。

## 每完成请求的成本

分母包括截止时间之后排空完成的请求；没有把 offered QPS 当作完成数。CPU 与分配是三个产品进程合计，包含有界前后 scrape，不能分给单独 endpoint，也不能将阶段 wall time 当 CPU。源合同见 [stress_test.go:97](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/stress_test.go#L97)。

| 三个 60 秒窗口合计 | 原 b469 / 7763 | 同机 beta.24 / 9V45 | 同机 b469 / 9V45 |
|---|---:|---:|---:|
| 完成请求数 | 43,179 | 46,800 | 46,800 |
| 产品 CPU 秒 | 567.91 | 396.44 | 407.65 |
| 产品 CPU ms / 完成请求 | 13.152 | 8.471 | 8.710 |
| 产品分配 MB / 完成请求，十进制 | 2.40889 | 2.35250 | 2.35276 |
| driver CPU ticks / 完成请求 | 0.27622 | 0.14994 | 0.15692 |

同机 b469 相比 beta.24：CPU/请求 **+2.83%**，分配/请求 **+0.01097%**，driver ticks/请求 **+4.66%**。三窗分配均低于当前 mixed 合同 5,000,000 B/请求。远端没有记录 `CLK_TCK`，故保留原始 driver ticks，不假设其秒数。

跨原始与匹配运行的数字不构成纯 CPU 型号实验。除了 7763 与 9V45 不同，原失败 list 丢弃较多使 sync 占完成请求约 25%，匹配为 23.08%；同时原 b469 的 slot message-update RPC / 完成请求约 12，匹配 b469 为 11.692；runtime-metadata 为 2 对 1.846。具体路由/副本位置没有被这份 receipt 完整冻结，因此不能把所有成本差异仅归因于硬件，也不能把不同请求组合的归一化值当固定单位请求成本。

## 原失败 CPU 与队列的联系

- 各窗 host non-idle 95.24% / 95.44% / 95.37%；cgroup 用量占 4 个可见 CPU 的 93.90% / 94.11% / 94.05%；CPU PSI some 时间占比 60.27% / 62.42% / 60.21%。
- 产品 CPU 为 187.22 / 190.65 / 190.04 秒，即每窗约 3.12–3.18 个 CPU；产品 CPU/cgroup CPU 比约 81.1–82.7%，但采样边界不同，仅表明产品是主要消耗方，不能将余量全部归给 driver。
- iowait 不到 0.1%、steal 为 0、cgroup throttling 增量为 0。`cpu.max` 读取不可用，不能据此证明不存在配额。低 iowait 也不能证明没有任何 I/O 延迟。
- 服务端 persisted heads/recents admission 的拒绝数均为 **0**；所有记录的 occupancy 样本均 **≤8**，配置上限为 **16**；平均占用约 1.18–1.23。没有证据表明是该 admission 的 16 槽拒绝导致失败。
- list 的 driver wait P99 为 608–750 ms、request-call P99 为 77–83 ms；每窗最后 10 秒仍分别丢弃 245 / 276 / 182 个压测 arrival。完整拆分在 `latency-findings.md`。因此可确认有持续队列过载，不能称为“消息丢失”或仅冷启动。

occupancy 是每次 admission 的共享占用采样，不是连续 gauge 最大值；合同明确没有服务端等待队列，参见 [conversation_persisted.go:8](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/pkg/metrics/conversation_persisted.go#L8)。

## 同机慢侧也出现在服务端平均 wall time

下表由所有 node 的 `_sum` 差分除 `_count` 差分，合并三个窗口得到。两个版本的对应 stage count、所有已记录的 RPC exact-call total 相同，例如 heads RPC 93,600、slot message updates 547,200、permission metadata batch 460,800。没有观察到 b469 执行更多这类工作。

| 服务端阶段平均 ms | beta.24 | b469 | 变化 |
|---|---:|---:|---:|
| list handler | 4.443 | 5.384 | +21.2% |
| sync handler | 11.336 | 13.577 | +19.8% |
| persisted heads metadata | 0.792 | 1.034 | +30.6% |
| persisted heads read | 1.540 | 1.822 | +18.3% |
| persisted heads edit overlay | 1.434 | 1.774 | +23.7% |
| edit-slot barrier | 0.328 | 0.392 | +19.5% |
| edit-slot storage | 0.0344 | 0.0397 | +15.4% |

三个候选窗口的 list/sync handler 均比基线相应窗口慢。其增长跨多个阶段，现有统计没有定位单一阶段的新异常。

这些是平均耗时，不是 P99；阶段可能并行、嵌套，人口不同，**不能相加**。handler 不含外层 middleware、客户端收包与解码。原始 receipt 丢弃了这些 stage 的 histogram buckets，只保留 sum/count，所以无法重建服务端 P99，也无法把 request P99 中的 7–20 ms 增量精确分解到服务端。字段语义见 [conversation_read_stages.go:8](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/pkg/metrics/conversation_read_stages.go#L8) 与 [diagnosis_test.go:172](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/diagnosis_test.go#L172)。

RPC `_total` 是精确调用数，RPC duration 的 `_count` 是采样人口，不能混作调用数或用两者求平均；见 [transport.go:210](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/pkg/metrics/transport.go#L210)。本分析只使用对应 `_sum/_count` 求采样均值。

## pprof 的实际覆盖与支持程度

本地已有 profile 仅来自原 7763 失败运行，**没有两版本函数级 profile 对照**。匹配运行的 profile_window receipt 仍可比较，但不能补出未保留的函数栈。

- driver CPU profile 从 04:18:00.452 UTC 开始，约 11.45 秒，CPU 样本 6.82 秒。`request` cumulative 5.25 秒，其中 JSON Unmarshal 4.43 秒（64.96% driver CPU），说明驱动解码确实消耗可观 CPU。cumulative 栈相互包含，不能相加。
- 三个 server profile 几乎同时在 04:18:00.453 开始，各约 8.14–8.15 秒，合计 CPU 样本 23.73 秒。pprof 合并后的 duration 24.45 秒是三个文件时长之和，不能称为 24 秒真实采样或与 driver 直接构造完整 CPU 份额。
- profile 负载真正开始于 04:18:01.231，因此 server 样本包含约 0.78 秒 warmup/观测，以及前约 7.36 秒实测；没有覆盖完整 10 秒，也没有覆盖任一此前 60 秒窗口。3 个 CPU profiles 的起止见 `profile-headers.json`。
- server 标签样本：transport 58.28%、api 17.61%、slot 14.79%；具体 task 中 rpc_service 35.44%、conn_write 13.57%、conn_read 8.22%、message_update_read 6.53%、raft_worker 6.28%。这些互斥 task 标签比叠加 cumulative 函数百分比更适合描述采样分布，但标签描述执行上下文，不等于可删除的开销。
- server 栈可见 persisted-head 读取、hydration、Pebble、分配/GC 与 RPC；例如 `readSelectedConversationHeads` cumulative 13.32%、`mallocgc` 11.17%。它们表明成本分布，**不证明这些函数出现新回退**。

采样是在三个窗口完成后另起的独立负载。匹配运行独立 profile 窗的尾延迟甚至反向：b469 list/sync P99 **31.85/58.78 ms**，beta.24 **40.75/78.57 ms**；产品 CPU 22.76 对 23.14 秒。这不是对原 60 秒窗口的反证，只说明短 profile 窗、采样开销与执行时段不能代替原测量。准确编排见 [mixed_attribution_test.go:93](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/mixed_attribution_test.go#L93)。

## 一次最小下一实验及边界

现有 workflow 支持在同一 runner 先 comparison、后 product，各启新 cluster/fixture，故一次 **b469 在先、beta.24 在后** 的反序 pair 最能检验当前位置混杂。保留三窗、source/profile/harness/binary receipt、CPU 型号和所有失败；不改负载或阈值、不跑到绿、不把顺序对照当迁移新候选 gate。

判别标准：慢侧随 b469 走则增加版本特异成本的证据；慢侧随第二位置走则增加时间/顺序解释的证据；差异消失或主机改变时仅说明旧差异不稳健。一次 pair 不建立统计置信度，也不会关闭原 7763 环境的持续过载阻塞。构建目录/编译参数会影响 Go binary digest，反序角色的 expected digest 必须与实际 build provenance 一致，摘要拒绝不得靠忽略/替换期望值绕过。

该反序实验由 root 独立管理；本文不声明其结果。新迁移候选仍需独立验收。正式 20 endpoint matrix 在原运行未开始、匹配运行未请求，正式长稳亦未执行。

## 可复核产物

- `cost-analyze.py`：只读重算脚本；`python3 /tmp/wukongim-performance-followup-20261020/cost-analyze.py`。
- `cost-data.json`：三个原始 report 路径及 SHA-256、身份、逐窗 host/driver/CPU/分配/阶段/RPC/occupancy 数据。
- `latency-findings.md`、`latency-data.json`、`latency-analyze.py`：到达时间线、排队/请求拆分及完整解释。
- `profile-headers.json`、`server-profile-tags.txt`：已有 profile 的精确起点与采样分布。

原失败和已有 receipt 均保留，没有修改历史原始报告。
