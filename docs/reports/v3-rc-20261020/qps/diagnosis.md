# Conversation QPS RC 阻塞证据

精确运行：[39368295521](https://github.com/WuKongIM/WuKongIM/actions/runs/39368295521)，candidate/harness 均为 `b469c6e5e2b4ff65611a8fe3389fb5034de3f152`，2026-10-20 12:18（Asia/Shanghai）结束。诊断证据采集完成，但性能未通过，Workflow failure；20 endpoint release matrix 在前置三窗口全通过检查处被阻止，未执行、未产生 v2 gate receipt。不得写成“20 项测试失败”或“诊断通过”。

## 直接观测

负载为三个连续 60 秒、page-size 100 的三节点 mixed 窗口：list 200 QPS + sync 60 QPS，各 8 workers；实际 QPS 门槛分别 190/57，P99 ≤500ms，errors/drops 为 0。

| 窗口 | list 实际 QPS / P99 ms / drops | sync 实际 QPS / P99 ms / drops |
| --- | --- | --- |
| 1 | 174.25 / 813.44 / 1,441 | 59.77 / 654.88 / 6 |
| 2 | 179.20 / 671.01 / 1,142 | 59.87 / 169.41 / 0 |
| 3 | 181.05 / 656.81 / 1,032 | 59.90 / 372.06 / 0 |

`drops` 是压测驱动丢弃的待调度 HTTP 请求，不是聊天消息丢失。所有测量端点 errors/unexpected_errors、runtime loads、membership writes、前后 active runtime 均为 0。共享窗口服务分配约 2.422/2.406/2.399 MB 每完成请求，低于固定 5 MB 门槛；不能把共享 CPU/分配重复计给 list 和 sync。

4 可见 CPU，AMD EPYC 7763，driver GOMAXPROCS=4，三个服务各 GOMAXPROCS=2。窗口周围固定 host cuts 的 CPU non-idle 为 95.24/95.44/95.37%，CPU PSI some stall 比例约 60.27/62.42/60.21%；cgroup 用量约为四核能力的 94%，throttling 和 steal 增量均为 0，cpu.max 文件不可用。服务累计 CPU 约187.22/190.65/190.04秒/窗口，分配34.28/34.78/34.95GB。host cuts 含有限前后观测开销，不能当作精确请求阶段归因。

## 三个可证伪假设（对照运行前记录）

以下保留 dispatch 前的认识；实验后的证据更新见文末。

按 diagnosing-bugs 的有界证据路线列出。没有新一轮复现、缩减或 instrumentation；现有三个固定窗口是本次实际红信号，尚未证明跨独立运行可重复，更未证明相对 beta.24 的新回退。

1. **产品 list/hydration 的读取和分配成本占据主要 CPU 预算。** 预测：同 host、同 harness、同负载下，若最近候选引入回退，beta.24 应以更少服务 CPU/分配或更好 QPS/P99 完成；若旧新类似，则“新增回退”假设变弱，但既有产品成本仍可能偏高。支持：三服务CPU高；之后独立8秒CPU采样的合并样本里，RPC service handle累计32.24%，readSelectedConversationHeads累计13.32%，hydrateMembershipDirectory累计8.68%，mallocgc累计11.17%；这些调用树有重叠，**不能相加**。分配差分中的较大来源包括JSON序列化、channelRegistry、会话heads和反向消息读取。限制：5MB分配门槛并未超出；CPU热点是成本分布，不是缺陷证明；没有旧版本对照。
2. **同机驱动 JSON 校验与三个服务争用 CPU，放大队列/P99和请求丢弃。** 预测：若是共因，旧新候选都会在相近CPU压力下欠载；后续只改变驱动资源隔离的诊断实验应改善排队，但不能替代原门禁。支持：host约95%忙且PSI约60%；独立profile中driver采样6.82CPU秒/11.45秒，`encoding/json.Unmarshal`占driver累计64.96%，请求校验累计76.98%。限制：它是profile阶段，不是前三个测量窗口；driver和server采样时间不同，不能直接相加为精确CPU占比，更不能据此免除门禁。
3. **runner或时段差异是主要共因。** 预测：同一runner先baseline后candidate应趋同，而跨runner结果更不稳定；噪声/调度限制如存在应在host counters可见。支持：本轮确实是四核共享执行环境并出现CPU压力。限制：没有matched baseline；观察到的steal和cgroup throttling均为0，不支持把失败直接归为CPU steal或quota限流。现有数据同样可由本任务自身CPU成本解释，不能称为GitHub偶发抖动。

## 最小对照实验（运行前计划，现已执行）

只执行一次现有 `conversation-qps-diagnose.yml` 的同机顺序比较：

- `product_sha=b469c6e5e2b4ff65611a8fe3389fb5034de3f152`
- `comparison_sha=4d44b39dda6fdaa7dafb637ca6cfb718e9530f77`（精确 `v3.0.0-beta.24`）
- `expected_binary_sha256=963bdb3d10489dea79550de3a18b4e6d7ffb903d18eedf5ebaa6c2ba516f7ecf`
- `run_release_matrix=false`；这个实验只分辨原因，不重复尚未获准进入的矩阵，也不成为发布证据。

预期执行顺序由现有workflow固定：构建两个 clean product binary → 同一个诊断harness → 新集群测baseline三窗口和独立profiles → 新集群测candidate三窗口和profiles。所有拒绝窗口保留，不重跑求绿，不改门槛。dispatch前必须核准harness仍为期望的b469 revision；若main已前进，先识别harness差异，不能默默把它当同一实验。

静态兼容核准已完成：beta.24为b469祖先；两提交的`test/e2e/message/conversation_qps`、`test/e2e/suite`、诊断workflow、go.mod/go.sum、cmd/wukongim均无差异；API Go源码无差异，仅Demo静态bundle变动；`internal/config`仅help文字变化。即所需bench fixture/eviction、debug pprof、metrics和conversation API合同在源码上相同，workflow对二者均使用`go build -tags=e2e`。尚未实际构建或启动baseline，兼容最终仍由下一实验实际启动证明。两个报告分别记录product/harness/binary/profile SHA，不能把不同binary的证据合并成候选通过。

如果baseline明显通过而candidate同host失败，获得候选相对回退的初步证据，再围绕两版本差异收敛；如果两者均类似失败，优先调查共用读取路径/driver CPU预算；若两者都通过，仍保留本次失败，单次顺序比较不足以证明稳定通过。顺序效应和时间漂移仍是限制。

当前行动：将此项列为 RC 阻塞，先诊断并解决固定性能门槛，不进入付费长稳。不修改源代码、测试、阈值或Workflow。


## 执行更新：同机对照已完成

[run 39369947381](https://github.com/WuKongIM/WuKongIM/actions/runs/39369947381) 按上述计划执行一次，两版各三个窗口均通过，errors/drops=0，list/sync 约200/60 QPS，候选 binary lock 匹配。beta.24 的 list P99 为15.45/16.30/16.43ms，sync为32.14/32.75/35.59ms；候选分别为24.96/23.13/24.00ms和55.95/48.54/53.45ms。实际 baseline 构建、启动和诊断已成功，不再只是静态兼容核准。

本次 runner 是 AMD EPYC 9V45，首次失败 runner 是 EPYC 7763；同为四可见 CPU 不代表处理能力相同。因此没有严格复现首次失败环境，不能用本次全绿删除原失败。该差异使 runner/硬件环境成为优先排查项，但不足以证明唯一原因。候选本次 P99 高于 baseline、两者均远低于500ms；单次固定顺序对照受顺序效应、时间漂移和 fixture差异影响，不能据此直接认定新回退。

两版 CPU、分配、主机身份、各窗结果和限制详见 [对照收据](../qps-comparison-summary.json)，原始报告分别保留在 `../qps-comparison/comparison/report.json.gz` 和 `../qps-comparison/report.json.gz`。新 run 未执行20 endpoint矩阵，不产生发布门禁通过证据。后续先明确可复现的资源基线并解释跨 runner 差异，再取得完整矩阵；目前仍不进入付费长稳。
