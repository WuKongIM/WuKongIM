# v3 RC 候选验收记录（2026-10-20）

状态：**RC 验收未通过：会话 QPS 前置窗口和 1→3 迁移后重启读取存在阻塞，不能晋级正式 v3。** 本记录不创建版本标签、不代表 RC 包已发布。产品、测试夹具与发布控制的身份分别记录，避免以重试或其他提交的绿色检查替代本候选。

后续更新：新候选 `98c25091b` 已完成[迁移恢复修复与相关回归](2026-10-20-v3-migration-recovery-repair.md)。下文保留 b469 原验收时点的失败与待办，不将新候选通过覆盖旧证据；性能及正式发布条件仍待满足。

性能续报：[反序对照与持续过载分析](2026-10-20-v3-performance-followup.md)已完成。反序后三窗 P99 中位数差异反转、逐窗波动明显，尚不能认定稳定的版本回退；原失败环境及新迁移候选的性能验收仍未关闭。

## 候选与范围

| 项目 | 固定身份 |
| --- | --- |
| 当前公开版本 | `v3.0.0-beta.24`，tag 指向 `4d44b39dda6fdaa7dafb637ca6cfb718e9530f77` |
| 本次产品候选 | `b469c6e5e2b4ff65611a8fe3389fb5034de3f152` |
| 候选 Git tree | `6b6c60cbac17a5a9570a332bc840b397e6bd7f77` |
| 本地工具链 | Go 1.25.11，macOS 15.1，darwin/arm64 |
| E2E 产品二进制 | `-tags=e2e`；SHA-256 `5bffb20d465d46f269a3eeadd4ddae3ef4775dc3679e56e33702ae1f69addc36` |
| 验收工作分支 | `codex/v3-rc-qualification`；原主工作区未改变 |

beta.24 之后共有 23 个提交（含 merge），重点涉及权限 fresh-read 合并、冷 Channel 初始化、存储 commit batching、压测到达模型及 Manager 冷运行时展示。相关用户可见记录保留在 `CHANGELOG.md` 的 Unreleased，尚未搬入新版本。

目标是核心 IM v3 正式版：单节点集群/多节点集群、认证和收发、权限、生命周期、故障恢复、迁移、SDK 与交付链。MQTT 仍为默认关闭的开发预览，不能因为核心 IM 验收通过就宣称 MQTT 同时达到 GA。所有性能结论必须限定到实际拓扑、资源、负载和测量窗口。

## 已有有效证据

- 本候选完整 Go 默认测试层通过：219 个包通过，15 个包无测试。命令为 `GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1`。
- 本候选 commit coordinator / permission cohorts 的相关 integration 通过；完整命令、日志摘要见随附验证收据。
- 最近四次 nightly 均首次通过，实际执行三节点 500 SEND/s、600 秒测量及性能 seam 检查：[38947713133](https://github.com/WuKongIM/WuKongIM/actions/runs/38947713133)、[39058074517](https://github.com/WuKongIM/WuKongIM/actions/runs/39058074517)、[39154207979](https://github.com/WuKongIM/WuKongIM/actions/runs/39154207979)、[39303302254](https://github.com/WuKongIM/WuKongIM/actions/runs/39303302254)。最后一次明确绑定本候选；这些短跑不替代正式 72 小时长稳。
- PR #1013 测试 head 与候选 tree 相同；[核心/race/性能](https://github.com/WuKongIM/WuKongIM/actions/runs/38848460382)、[Manager 浏览器](https://github.com/WuKongIM/WuKongIM/actions/runs/38848460385)、[Linux 原生包安装生命周期](https://github.com/WuKongIM/WuKongIM/actions/runs/38848460466)通过。相同 tree 说明源码相同；它们不是本候选正式签名包的公网验证。
- 历史 nightly 存在 hot-latency/preflight 失败；随后合并了到达模型、storage batching 和 preflight 的相关修正，最近四跑通过。通过结论限定到新提交和修正后的负载模型；历史失败仍保留，preflight 修正不单独证明延迟问题已修复。

## 本次补充验收

本地黑盒矩阵串行运行；所有应用进程使用同一个冻结二进制，所有初次运行保留，不自动重跑。默认关闭的诊断/高负载用例保持关闭，跳过项明确记录。结果由最终收据生成。

| 验收组 | 首次结果 | 后续状态 |
| --- | --- | --- |
| 单节点消息与发送权限 | 通过 | — |
| 跨节点投递及 2/3 副本拓扑 | 失败 | 修正测试凭据后，四项默认测试通过；一个 opt-in 负载比较跳过 |
| 冷热通道隔离 | 通过 | — |
| Channel 故障切换 | 通过 | — |
| 禁言与权限故障 | 通过 | — |
| 自然回收/激活及跨入口顺序 | 通过 | — |
| 原始 v2 迁移前置/凭据 | 通过 | — |
| 普通历史五种迁移拓扑 | 失败 | 四种拓扑通过；1→3 全集群重启后的读取失败，未重跑 |
| 单节点备份恢复 | 通过 | — |
| 三节点故障备份恢复 | 通过 | — |

首次八组通过、两组失败；只修正已明确的凭据准备遗漏后，九组通过、迁移组仍失败。普通 v2 迁移与单/三节点备份恢复结果分别保留；备份恢复通过不能覆盖普通历史迁移的失败。

完整命令、原始 JSONL、场景 JSON 及摘要见 [证据索引与复验说明](v3-rc-20261020/README.md)。

SDK 使用注册表已发布的 Android 1.0.5、iOS 1.1.1 和 Flutter 1.1.0，在真实模拟器上与已发布 Web peer 双向通信；[本次 run 39367425765](https://github.com/WuKongIM/WuKongIM/actions/runs/39367425765) 的 source SHA 固定为本候选。

SDK 首次运行（attempt 1）全部成功，2026-10-20 04:15:21 UTC 完成。Android instrumentation 实际执行一项消息测试并由 Web peer 独立确认；iOS、Flutter 各自保留双向收发及断开回执。精确源码、job 身份与验证依据见 `v3-rc-20261020/sdk-summary.json`，未进行重跑。

## 发现、修正与证据限制

### 跨节点认证测试准备遗漏

初次两项双向投递测试在 CONNECT 阶段返回 `ReasonAuthFail`，尚未运行到 SEND。测试 helper 传空 Token，而默认认证已开启；Gateway 在进入 verifier 前拒绝。修正范围仅为场景 helper 经公开 `/user/token` 注册 APP 凭据、携带 Token 连接及相应场景说明；保留默认认证、五秒 deadline 和全部双向投递/ACK/副本断言，产品二进制不变。

修正后的 harness commit 为 `0935cff8933454aab0074b60f958cac9d3f9cbbf`，已单独运行完整 cross-node package：四项默认测试通过，opt-in 2,000 SEND/s 副本比较保持跳过。记录见 [独立重测收据](v3-rc-20261020/cross-node-retest.json)。原始失败日志和固定 b469 产品二进制 hash 保留，未重复运行未改变的其余九组。

### 1→3 迁移后重启的历史读取失败

五种 ordinary-history 拓扑中，仅 `1_source_nodes_to_3_target_nodes` 失败。此案例此前已通过 CLI 迁移校验、每入口原始历史、原凭据登录、追加到 sequence 4 及四条历史校验；完整重启后首次 `/channel/messagesync` 返回 `HTTP 400: internal/message: append failed: channel replication: recovery probe incomplete`，后续故障切换断言尚未运行。其余 1→1、3→1、3→3、3→5 首轮通过。

静态调用链确认：复制层公开 Install 边界直接返回私有 `errRecoveryProbeIncomplete`，infra 将未知 cause 映射成通用 `ErrAppendFailed`，读取可用性分类器未识别它，最终输出400；同时迁移 fixture 仅重试旧式400 route-not-ready 文本，未按现行503 `code=unavailable` 契约处理。只修改测试重试不能修正本次产品响应。

具体哪个 probe 分支没有完成、是否随后收敛、是否存在持久恢复故障均未证实，没有数据丢失证据。原日志已保留，无成功重跑覆盖。下一修复先在真实 `quorumLog.Install` 边界构造缺失稳定 quorum 的失败用例，验证精确临时错误身份且不可写；保留永久冲突非重试，再修正公有错误身份和严格503读取重试。修复会产生新的产品候选，须重新验证1→3、完整五拓扑及相关恢复/冷读场景。精确行号、失败库存和验证范围见 [迁移诊断](v3-rc-20261020/migration-diagnosis.json)。

- 本地 macOS 的功能测试不认证 Linux 资源容量、长时间内存增长或线上 SLA。迁移的小规模五种拓扑测试不证明 100 GiB/4 小时生产迁移目标或生产 RPO/RTO。
- 本次矩阵没有专门的 self-chat 黑盒场景；不得将双用户单聊测试写成已验证 self-chat。

### 会话 QPS 前置窗口未达标

会话 QPS 发布门禁由 Docker/binary publisher 对精确 release tag 在发布写入前调用。本轮设置独立诊断入口的 `run_release_matrix=true`，对当前候选请求刷新完整矩阵（run 39368295521），实际被前置窗口阻止；最终目标 tag 仍须执行发布门禁，诊断完成或 beta.24 的通过不能继承。

本次 [run 39368295521](https://github.com/WuKongIM/WuKongIM/actions/runs/39368295521) 首轮失败：诊断采集完成，但三个 60 秒混合窗口没有全部通过，因此前置 `jq all(pass)` 阻止完整 20 endpoint 发布矩阵启动；没有发布矩阵收据，也没有重跑。

| 窗口 | list 实测 QPS（须 ≥190） | list P99（须 ≤500 ms） | list 请求丢弃数 | sync 结果 |
| --- | --- | --- | --- | --- |
| 1 | 174.25 | 813.44 ms | 1,441 | P99 654.88 ms、6 次请求丢弃，未通过 |
| 2 | 179.20 | 671.01 ms | 1,142 | 通过 |
| 3 | 181.05 | 656.81 ms | 1,032 | 通过 |

三窗均为 HTTP errors=0、runtime loads=0、membership writes=0；共享窗口分配量约 2.40–2.42 MB/request，低于 5 MB 上限。此处 drops 是压测到达/调度队列中的 HTTP 请求丢弃，不是消息数据丢失。host CPU 约 95%，PSI CPU some 累计 stall 约 60–62%，可观察 cgroup throttling 增量为零，但缺少 cpu.max。证据支持高 CPU 竞争伴随门槛失败；不能仅凭这些总量唯一归因于产品、驱动或 runner。精确报告、Linux 二进制/主机身份、失败步骤和原始窗口保留在 `v3-rc-20261020/qps/`；摘要见 `qps-summary.json`。

已有 profile 的热点分析及三个可证伪假设见 [QPS 归因记录](v3-rc-20261020/qps/diagnosis.md)。基于这三个预声明假设，另执行一次固定 b469 harness 的同机版本对照：在同一四 CPU runner 上先测 beta.24，再测当前候选，各三个窗口，并保留全部拒绝窗口与二进制身份。

同机对照 [run 39369947381](https://github.com/WuKongIM/WuKongIM/actions/runs/39369947381) 已完成：两版各三个窗口全部通过，HTTP errors/drops 均为 0，list/sync 约 200/60 QPS。候选二进制摘要与首次失败运行完全一致；未修改产品、负载或门槛，也未执行完整发布矩阵。

| 同机产品版本 | 三窗 list P99（ms） | 三窗 sync P99（ms） |
| --- | --- | --- |
| beta.24 | 15.45 / 16.30 / 16.43 | 32.14 / 32.75 / 35.59 |
| 当前候选 b469 | 24.96 / 23.13 / 24.00 | 55.95 / 48.54 / 53.45 |

关键限制：此次四 CPU runner 型号为 **AMD EPYC 9V45**，首次失败运行是 **AMD EPYC 7763**。可见 CPU 数相同不代表处理能力相同；对照没有严格复现原失败主机环境。证据使 runner/硬件环境差异成为后续优先排查项，但尚不能唯一归因于硬件，也不能据一次先旧后新的顺序实验认定候选出现性能回退。候选本次尾延迟高于旧版，仍远低于 500 ms 门槛。两份独立原始报告和 CPU/分配比较见 [同机对照收据](v3-rc-20261020/qps-comparison-summary.json)。

原失败及其完整发布矩阵未启动的事实继续保留。下一步需明确可复现的性能资源基线，解释跨 runner 差异并取得完整矩阵证据；一次换 runner 后的通过不关闭本项。

### 云上验证边界

本地 direct-lab 只做了不联系 provider 的 preflight，因缺临时凭据/精确启动标记而停止。正式验收使用 GitHub OIDC，与该诊断路径的本机 STS 缺项无关；尚未核实正式 OIDC、实时价格或创建任何云资源。

## 后续放行条件

1. 关闭 1→3 迁移后重启读取的恢复/错误分类缺口，并完成 Linux 会话 QPS 未达标窗口的归因或修复，再取得完整功能与发布矩阵合格证据。任何尚未解释的功能或性能失败保持阻塞，不能以其他用例通过覆盖。
2. 核心候选冻结后完成[正式长稳计划](2026-10-20-v3-rc-formal-plan.md)：2 小时预演、全新 Lease 的 72 小时连续 Soak、同老化数据的容量与 30 分钟恢复；验证完整报告、连续性和两次 exact zero inventory。付费执行需要明确预算授权。
3. 按[stable 渠道实施计划](2026-10-20-v3-stable-channel-plan.md)完成签名、存储、控制和公网四发行版验收能力；stable 当前禁用且实现缺失，不是配置开关。
4. 若先发布 RC，使用 preview；现有四个 active beta 已达容量上限，先完成合规退休，再进行不可变 RC 发布及精确版本公网验证。
5. 真正发布 v3.0.0 前，把 Unreleased 变更移到唯一且非空的精确版本标题；最终 tag 跑发布门禁，完成 Docker 三镜像源、GitHub immutable 二进制、文档以及 stable APT/RPM 精确版本和 CLI 验证，全部收据绑定同 source SHA。任一失败按 [RELEASING](../development/RELEASING.md)保持发布未完成。

本次交付仅为内部验收及测试准备，无用户可见产品行为变化，不修改 CHANGELOG。若以 PR 交付，须在描述解释该原因并由维护者应用 `skip-changelog`。
