# v3 会话性能差异排查续报（2026-10-20）

**结论：原失败已确认存在持续的压测驱动队列过载；尚不能认定 beta.24 → b469 存在稳定的版本性能回退，也没有关闭原环境未达标的问题。** 新反序对照中，三窗 P99 中位数的版本差异反转，但逐窗波动较大，P99 的算术均值接近。当前证据不支持据此修改产品读取路径或降低门槛。

本轮先完成了[迁移恢复修复和相关回归](2026-10-20-v3-migration-recovery-repair.md)，再继续分析性能。以下性能实验仍比较原候选 b469 与 beta.24，**不是迁移修复候选 `98c25091b` 的 Linux 性能验收**。

## 实验身份与完整结果

| 项目 | 固定值 |
| --- | --- |
| 基线 | beta.24：`4d44b39dda6fdaa7dafb637ca6cfb718e9530f77` |
| 原候选及诊断 harness | `b469c6e5e2b4ff65611a8fe3389fb5034de3f152`，clean |
| 工具链/平台 | Go 1.25.11，Linux amd64 |
| 负载 | 每版三个 60 秒窗口；list 200/s、sync 60/s；各 8 workers；每页 100 |
| 进程 | 同机驱动 GOMAXPROCS=4；三个产品节点各 GOMAXPROCS=2 |
| profile 配置摘要 | `1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1` |
| 新实验 | [39407349500](https://github.com/WuKongIM/WuKongIM/actions/runs/39407349500)，attempt 1，先 b469、后 beta.24；每版新建集群和 fixture |
| 新主机 | Intel Xeon Platinum 8573C，4 logical CPUs，2 cores × 2 threads |

下表保留所有窗口。正序主机是 AMD EPYC 9V45；不能将不同主机的绝对毫秒值直接用作版本比较。两次对照均 errors=0、drops=0、runtime loads=0、membership writes=0，list/sync 实测约 200/60 QPS，全部混合窗口 verdict=pass。

| 对照与执行位置 | 产品 | list 三窗 P99，ms | sync 三窗 P99，ms |
| --- | --- | --- | --- |
| [旧正序 39369947381](https://github.com/WuKongIM/WuKongIM/actions/runs/39369947381)，先 | beta.24 | 15.45 / 16.30 / 16.43 | 32.14 / 32.75 / 35.59 |
| 旧正序，后 | b469 | 24.96 / 23.13 / 24.00 | 55.95 / 48.54 / 53.45 |
| 新反序，先 | b469 | 49.99 / 26.15 / 25.54 | 99.11 / 57.51 / 56.09 |
| 新反序，后 | beta.24 | 24.94 / 42.02 / 32.18 | 52.26 / 95.31 / 71.15 |

旧正序的三窗 P99 中位数，b469 比 beta.24 高 47.3% / 63.2%；新反序则低 18.75% / 19.17%。但新反序 b469 第一窗慢于 beta.24 第一窗，后二窗方向相反。新反序三窗 P99 **算术均值**为 list 33.89 对 33.05 ms（b469 +2.55%），sync 70.90 对 72.90 ms（b469 −2.74%）。中位数和这些均值都不是合并请求的 P99，不能据此宣称稳定提速 19%。

这次反序削弱了“b469 必然更慢”的假设，却没有证明“第二个位置必然更慢”。每版只有三个窗口，且换了 CPU 型号、执行时间和构建目录；未建立统计置信区间或隔离所有混杂。

## 原失败的范围已缩小

[原失败 39368295521](https://github.com/WuKongIM/WuKongIM/actions/runs/39368295521) 使用 EPYC 7763。list 三窗实际 QPS 174.25 / 179.20 / 181.05，低于 190 门槛；P99 813.44 / 671.01 / 656.81 ms，高于 500 ms 门槛。

- list 的驱动等待 P99 为 750.03 / 619.83 / 607.65 ms，请求调用 P99 为 82.84 / 77.37 / 78.98 ms。后者包含 HTTP、读取响应、JSON 解码、完整身份校验和 CPU 调度，不等于服务端耗时；两个独立 P99 不能相加或相减。
- 请求丢弃持续到每窗末尾，最后十秒仍有 245 / 276 / 182 个 arrival 被丢弃。不是仅首窗冷启动；drops 指压测队列满时丢弃请求，不是消息数据丢失。
- host CPU non-idle 约 95%，CPU PSI some 累计时间占比约 60–62%。cgroup throttling/steal 可观察增量为零，但 `cpu.max` 不可读，不能由此断言没有配额限制。
- 服务端 persisted admission 拒绝数为零，记录的占用样本均 ≤8，而上限为 16。没有证据表明该 admission 上限拒绝了请求。

证据支持驱动与三个产品进程共享 CPU 时出现持续排队。产品消耗了多数可观察 CPU，驱动也有显著 JSON 解码成本，不能将失败单独归咎于压测器。两次后续主机均通过，不能覆盖原失败，也不能证明 CPU 型号是唯一原因：原失败的已完成请求组合和 RPC 扇出亦有所不同。

## 服务端成本与代码候选

按三个窗口实际完成的 46,800 个请求归一化，比较同一次运行内的版本：

| b469 相对 beta.24 | 旧正序 / 9V45 | 新反序 / 8573C |
| --- | ---: | ---: |
| 三节点 CPU / 完成请求 | +2.83% | +0.71% |
| 三节点分配字节 / 完成请求 | +0.011% | +0.019% |
| 驱动 CPU ticks / 完成请求 | +4.66% | +1.26% |
| 服务端 list handler 平均耗时 | +21.2% | −0.65% |
| 服务端 sync handler 平均耗时 | +19.8% | −0.85% |
| edit-slot barrier 平均耗时 | +19.5% | −0.36% |

新反序各版分配约 2.352 MB/request，低于 5 MB 上限。对应 stage count 和读取路径 exact RPC 总数相同；新运行背景 controller write 计数为 72 对 74，不能声称所有工作量完全一致。阶段数据是 `_sum/_count` 的平均 wall time，存在并行、嵌套和不同统计人口，不能相加或充当服务端 P99。驱动未记录 CLK_TCK，故只比较 ticks，不擅自转换成秒。

静态差异审计确认一个直接可达候选：`81c93d726` 的 Slot ReadIndex 合并。Persisted conversation heads 即使没有编辑记录，仍会读取权威 message-update 页面，经 `ReadSlotBarrier` 进入该路径；runtime loads=0 不能排除它。但它没有新增定时等待，现有阶段数据也没有显示反序后持续变慢，**目前不能认定该提交是根因，未进行推测性回退**。

Commit batching 可能影响 fixture 准备和后台存储状态；metrics 快照变更可能影响边界 CPU/分配统计，均没有被证实为窗口尾延迟根因。旧对照排除编译后，beta.24 的测量前准备仍比 b469 多约 70 秒；反转运行位置不会消除这种与版本有关的准备历史。缺少逐准备阶段数据，不能推断已发生 GC、compaction 或缓存异常。

详见[成本推导](v3-performance-followup-20261020/cost-findings.md)、[延迟口径](v3-performance-followup-20261020/latency-findings.md)、[静态调用链审计](v3-performance-followup-20261020/source-delta-audit.md)和[反序独立复核](v3-performance-followup-20261020/reverse-findings.md)。前三份为反序执行前形成的证据，保留当时的假设与下一实验建议。

## Profile 的解释边界

新实验保留两版各三个 server CPU profile 和一个 driver CPU profile。独立短窗中，驱动 JSON Unmarshal cumulative 占 CPU 样本 54.24%（beta）/ 58.67%（b469）；服务端 transport 标签占 51.26% / 54.93%，slot 占 14.67% / 14.02%。两版均有 RPC、持久读取、分配和 GC 成本，没有据此定位版本新增热点。

这些 profile 在三个正式窗口之后单独采集。服务器八秒 profile 从独立负载 warmup 前开始，不能代表完整十秒负载，更没有覆盖此前的 60 秒窗口。三个服务端 profile 合并显示约 24 秒 duration 是文件时长之和，不是连续 24 秒采样。累计函数百分比相互包含，不可叠加；CPU profile 也不能直接解释所有等待延迟。原失败 profile 及新 profile 全部保留，不用短窗替代门禁结果。

## 构建身份修正与证据完整性

反序实验首次调度 [39406775641](https://github.com/WuKongIM/WuKongIM/actions/runs/39406775641) 在构建摘要校验处停止，**没有执行任何测量窗口**。原因是输入沿用了 beta 在旧 comparison 子目录构建的摘要，新角色在根目录构建得到另一摘要；工作流没有使用 `-trimpath`。除第一行二进制路径外，两份 `go version -m` 元数据逐字相同，但这不能独立证明全部二进制差异仅由路径引起。

随后在测量开始前保存修正计划，用预检观察到的根目录 beta 摘要重新锁定一次实验。调用参数只改变该期望摘要；计划也撤销了将旧 b469 根目录摘要作为新 comparison 目录预期摘要的假设，并保留为 prior-role identity。工作流本来只有根目录产品的预期摘要参数，comparison 通过精确 source、clean build 和实际摘要记录身份。因此不能把本次称作两版沿用旧冻结二进制字节，或声称两版都有事先已知的二进制锁。

| 产品 / 构建角色 | 新实验实际 SHA-256 |
| --- | --- |
| b469 / comparison，先测 | `0d25039bfc518b166eded2871a365231a041d710c52636a2154eda27179a3825` |
| beta.24 / root，后测 | `e327d0252e814b47efdc06ff9c6b80d847af29e30c20a1b22ba1dee98cd45a9a` |

精确 source/toolchain/build 参数核验通过，report 与 binary receipt 摘要相符。未下载二进制，摘要来自工作流证据；ZIP 采用选择性 range 下载、逐文件 CRC 校验，再记录本地文件 SHA-256，未声称重新计算了整个 artifact archive 的摘要。所有失败预检、计划变更和最终窗口均保留。负载、阈值、harness、配置没有改变，没有对不合格测量反复重跑。

## 未关闭事项与下一判别点

原失败环境的完整 20 endpoint 发布矩阵未启动；两次版本对照都未请求该矩阵。新迁移候选没有新 Linux 性能门禁，正式长稳与 stable 原生包渠道也没有因本次工作获得验收。**不能据此发布正式 v3。**

下一有效实验应先固定处理器/逻辑核拓扑、CPU 配额、驱动位置和准备阶段边界，做同一二进制的重复性控制，再比较版本；保存逐阶段准备时间、实际配置/路由和测量窗口内有界诊断。若相同二进制都不能重复，应先解决测量资源基线，不能把波动归因于 ReadIndex。驱动隔离或资源变化用于诊断时必须单列条件，不能放宽现行发布门槛。本次没有继续追加不受控 runner 重跑。

[证据目录](v3-performance-followup-20261020/README.md)提供原始报告、哈希清单、离线重算命令和独立复核。迁移修复的交付状态见独立报告；本续报不修改历史 RC 失败结论。
