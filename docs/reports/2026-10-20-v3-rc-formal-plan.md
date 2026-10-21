# v3 RC 正式长稳验收执行草案

更新于 2026-10-21：迁移修复后的候选 `ef798352c40a94e57f768f76b2f4713712f827fe` 已取得五拓扑迁移回归及完整 Linux 20 项性能验收证据，见[发布复核](2026-10-21-v3-release-readiness.md)。原始 b469 的失败保留在历史报告，不再称为当前未修复阻塞。候选尚未整合进 protected main，仍需自己的相关 CI；整合后重新冻结 source、bundle 和授权文本。下列云方案依据仍固定到 `b469c6e5e2b4ff65611a8fe3389fb5034de3f152`，相关控制没有因迁移修复而改变。

这条 2h → 72h → 容量 → 恢复链是本次既定 RC 验收计划，不能表述为 RELEASING 对每次发行的通用机械门禁。历史 CPU 差异未唯一归因也不新增付费长稳的前置门禁。

本草案只准备执行边界；尚未报价、采购、部署、创建跟踪 Issue 或启动云测试，云身份及现有资源状态也尚未核实。本轮“同意”授权继续 RC 准备，不代替仓库要求的明确付费授权。

## 执行路径和授权

采用正式 GitHub OIDC 路径：`chat-lifecycle-rehearsal.yml` → 预演 finalizer → `chat-lifecycle-formal.yml` → 正式 finalizer。公开付费入口只有第一个；后续自动延续同一请求与累计预算。它在 protected `main` 上执行，产品 `source_sha` 固定为修复并验收后的新候选；正式 dispatch 前重新验证该提交仍可从 trusted main 到达。本草案的配置依据仍固定到本次审计基线，新候选若改变这些依据，执行计划也须同步核准。

公开输入只有 `source_sha`、`operator=tangtaoit`、请求专属 Ed25519 诊断公钥和唯一 `request_id`。由执行者生成 request/key；私钥留在仓库外的本地受限目录，绝不上传。先核准 GitHub OIDC Provisioner/Observer/Releaser、`cloud-deployment` wrapping identity 和清理 backstop。缺少身份时列出具体缺项；不得悄悄变更权限或改用 direct-lab 凭据。任一旧请求没有认证的 exact zero-inventory proof，禁止新采购。[^1][^2]

后续最小明确授权文本模板（请求授权前须替换为已验收候选的完整 SHA，当前不是启动指令）：

> 授权对 `<已验收候选完整 SHA>` 开始聊天生命周期全流程压测：按仓库正式 GitHub OIDC 方案在阿里云购买临时资源，执行预演、72 小时长稳、容量及恢复，创建跟踪 Issue 并完成清理。整个请求累计预算上限人民币 1,500 元，估算累计达到 1,350 元停止。

正式设计记录的启动短语是“开始聊天生命周期全流程压测”。当前 `wukongim-chat-lifecycle` Skill 已改为 **direct-lab 诊断修复路径**，其“开始聊天生命周期稳定性长跑”及默认 ¥300/¥250 不属于本方案；即使 direct-lab 连跑 72 小时，也不能转为正式证据。[^3]

## 固定资源与成本

| 项目 | 固定配置 |
| --- | --- |
| 放置 | 阿里云杭州、同可用区、同 VPC/vSwitch；4 台同型号非突发 PostPaid x86_64 Ubuntu 24.04 |
| 服务节点 | 3 台，每台 4 vCPU/8 GiB、40 GiB 系统盘、500 GiB 独立数据盘；保持私网 |
| 负载节点 | 1 台 4 vCPU/8 GiB、40 GiB 系统盘、200 GiB 数据盘；承载 3 个 worker、协调器和监控 |
| 公网 | 仅负载机 1 个 EIP、20 Mbps；按 ADR0042 的临时 22/80 入口范围 |
| 集群 | 3 节点、12 个逻辑 Slot Group、256 hash slots、Slot/Channel 均三副本 |
| 预算 | 两阶段及容量、修复持有时间、磁盘、流量、诊断和清理合计 ¥1,500；¥1,350 停止，余 ¥150 保留给安全收尾 |
| Lease | 预演不可延长的 12 小时上限；正式不可延长的 96 小时上限；成功或终止后立即清理，不刻意持有至到期 |

先构建并密封不可变 bundle，再按当前可用资源和价格取得保守 Quote；无法容纳剩余预算时在 Acquire 前拒绝。¥1,500 是授权上限，不是已经查询得到的实际报价。实际 provider 账单尚未返回时保持 pending/unresolved。[^4]

## 阶段与连续性

1. 完成候选的相关本地验证及 runbook 的 shakeout，冻结 source、二进制、bundle、配置和负载 YAML digest；shakeout 不充当正式证据。
2. 新预演 Lease：满规模运行 **2 个测量小时**。全部 10,000 用户完成 CONNECT 和新的 version-zero 全量同步，并接受第一份完整 2,000 SEND/s grant 后才计时。成功必须是 `rehearsal_pass`。
3. 上传预演证据并将预演 Lease 释放到 exact zero inventory。正式 transition 绑定同一 source、bundle、request、诊断身份及累计成本账本，之后才允许新正式 Lease。
4. 正式空数据开始，连续运行 **72 小时**；24 小时仅输出 qualification，不停止、不重启、不拼接结果。远端 `wkbench-formal.service` 的同一个 `wkcli bench formal-chain` 进程接续后续阶段。
5. 仅 Soak `outcome=pass` 可进入同进程、同 worker fence、同 observer、同老化数据集的容量阶梯：从 2,000 SEND/s 起，每档 +25%，10 分钟稳定 +20 分钟测量，边界细化到 10%，最多 8 小时。
6. 恢复至 2,000 SEND/s 连续 30 分钟，必须恢复正式健康、延迟、错误、队列和生命周期边界；`capacity.recovery_passed=true`。若期限内未找到失败边界，只报告实测下界，不能声称已找到极限。

正式启动要求至少 81.5 小时剩余 Lease 时间，后续每 5 秒检查累计成本及一小时清理余量。产品 source/bundle 在预演与正式之间也不改变。部署或计时前 readiness 故障可以在同 Lease 上使用新 protected-main 控制修复，但不得替换产品/bundle；产品必须修改则本请求终止并清零，新请求需要新的明确授权。[^5]

## 本候选的有效阈值

以当前 YAML 和严格代码校验为准，不回用旧设计中的数字。[^6]

| 信号 | 门槛 |
| --- | --- |
| 基准负载 | 10,000 在线，bootstrap 100 登录/秒，250,000 新用户/日，2,000 主 SEND/s；90% 单聊/10% 群聊；包含十万成员群 |
| 正确性 | 最终发送失败、activation rejection、确认丢失、重复持久化、损坏和序列回退均为 0；持久化创建与成功首次发送证据对账 |
| 首发错误率 | 全程 <0.01%；任一分钟 ≤0.1% |
| 热 SENDACK | p99 ≤400 ms，p99.9 ≤1 s |
| 冷创建/激活 | p99 ≤2 s，p99.9 ≤5 s |
| 完整会话同步 | p99 ≤1 s，p99.9 ≤3 s |
| 延迟窗口 | >10 s 捕获异常；连续 5 分钟超阈值按资源饱和/产品/证据不足归因，不用全程均值掩盖 |
| 内存与队列 | 2 小时 warmup 后，forced-GC live heap 每滚动 6 小时增长 ≤5%，goroutine 24 小时增长 ≤5%；队列回稳，runtime 不随历史 Channel 数无界增长 |
| 资源饱和 | CPU >90%、memory >85% 或 bounded queue >80% 持续 15 分钟触发容量归因；警告不自动等于 RC 全面通过 |
| 健康与磁盘 | 每 5 秒采样，30 秒持续健康异常门禁；每台服务数据文件系统 ≥500,000,000,000 bytes，<5% 空闲停止；Prometheus 达 140 GB 停止 |

旧 8 月文档仍有热 p99 200 ms、bootstrap 25/50 登录每秒及 1 TB 等描述；本候选实际是 **400 ms、100 登录每秒、500 GiB 数据盘**，由 YAML、`defaults.go` 和 ADR0041/Run Plan 共同核准。本方案不替代其他性能档位或 SDK/迁移验收，也不凭该负载声称满足 4,500 SEND/s。

## 失败、交付证据与清理

- 不自动重跑预演运行、正式运行、容量、恢复或正确性失败。故障先停新流量；不重启服务或清数据掩盖故障。可检查故障最多保留 2 小时只读诊断，磁盘、预算、到期或不可达会缩短窗口。
- 仅有绿色 Workflow、进程退出或 `passed_with_capacity_warning` 不算完整 RC 验收。当前实现仅在 Soak `pass` 时继续容量；必须核对 24 小时 qualification、72 小时 formal 和 capacity/recovery 的 JSON/Markdown 成对报告及实际结论。
- 执行 `wkchatlifecycle validate-formal-chain` 校验同 dataset digest、worker fence、thresholds 和严格有序时间窗口；完整终端 evidence 缺失会被 finalizer 判为 `harness_invalid`。容量必须 attempted/completed、recovery_passed；容量上限与 warning 单独报告，不能自动折算为 GA 结论。
- 最终 `chat-lifecycle-formal-complete-*` 保留 90 天：身份/digest manifest、预演、24/72 小时、容量/恢复、两阶段成本、固定边界指标/日志/profile、两次 selector-bound zero-inventory proof；不能只给一段结论。
- 终端报告上传救援最多额外 2 小时，之后即使 Artifact 不可用也必须释放。清理未完成保留 `cleanup_pending` 和 exact selector，继续 stage finalizer 与独立 15 分钟 sweeper；不把 API“接受删除”当清零。只有最终 Artifact 成功且 exact zero inventory 后才关闭跟踪 Issue。
- 人工停止使用 `chat-lifecycle-stop.yml`，保留 request stop marker 并由清理 owner 收尾；不能硬取消 cleanup owner。清零前保留请求状态及必要身份；清零后按既有机制移除短期 credential/handoff，保留非秘密证明。[^7]

## 核准依据（固定到本候选）

[^1]: [.github/workflows/README.md:8](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L8)、[L945](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L945)；[.github/workflows/chat-lifecycle-rehearsal.yml:7](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/chat-lifecycle-rehearsal.yml#L7)、[L61](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/chat-lifecycle-rehearsal.yml#L61)。
[^2]: [.github/workflows/README.md:863](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L863)、[L906](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L906)、[L1020](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L1020)；[docs/adr/0043-bootstrap-chat-lifecycle-oidc-from-existing-accesskeys.md](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/docs/adr/0043-bootstrap-chat-lifecycle-oidc-from-existing-accesskeys.md)。
[^3]: [docs/superpowers/specs/2026-08-07-automated-chat-lifecycle-cloud-simulation-design.md:93](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/docs/superpowers/specs/2026-08-07-automated-chat-lifecycle-cloud-simulation-design.md#L93)；[.agents/skills/wukongim-chat-lifecycle/SKILL.md:15](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.agents/skills/wukongim-chat-lifecycle/SKILL.md#L15)、[L117](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.agents/skills/wukongim-chat-lifecycle/SKILL.md#L117)。读取 Skill 仅用于核实边界，本方案不调用其 direct-lab 操作。
[^4]: [configs/cloud/chat-lifecycle/rehearsal-v1.json:4](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/configs/cloud/chat-lifecycle/rehearsal-v1.json#L4)、[formal-v1.json:4](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/configs/cloud/chat-lifecycle/formal-v1.json#L4)；[docs/adr/0041-use-four-postpaid-hosts-for-automated-chat-lifecycle.md](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/docs/adr/0041-use-four-postpaid-hosts-for-automated-chat-lifecycle.md)；[docs/adr/0042-expose-the-load-node-for-the-bounded-lease.md](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/docs/adr/0042-expose-the-load-node-for-the-bounded-lease.md)。
[^5]: [.github/workflows/README.md:960](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L960)、[L1041](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L1041)、[L1063](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L1063)；[docs/adr/0047-retain-one-lease-across-deployment-repair.md:25](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/docs/adr/0047-retain-one-lease-across-deployment-repair.md#L25)；[docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md:115](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md#L115)。
[^6]: [configs/wkbench/chat-lifecycle/formal.yaml:7](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/configs/wkbench/chat-lifecycle/formal.yaml#L7)、[L69](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/configs/wkbench/chat-lifecycle/formal.yaml#L69)、[L105](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/configs/wkbench/chat-lifecycle/formal.yaml#L105)；[internal/bench/chatlifecycle/defaults.go:14](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/internal/bench/chatlifecycle/defaults.go#L14)、[L134](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/internal/bench/chatlifecycle/defaults.go#L134)；[internal/bench/chatlifecycle/validation_formal.go:267](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/internal/bench/chatlifecycle/validation_formal.go#L267)。
[^7]: [.github/workflows/README.md:997](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L997)、[L1005](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L1005)、[L1029](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L1029)、[L1071](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/README.md#L1071)；[cmd/wkcli/internal/benchmark/formal_chain_command.go:338](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/cmd/wkcli/internal/benchmark/formal_chain_command.go#L338)；[cmd/wkchatlifecycle/main.go:91](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/cmd/wkchatlifecycle/main.go#L91)；[scripts/chat-lifecycle/formal-finalize.sh:125](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/scripts/chat-lifecycle/formal-finalize.sh#L125)、[L175](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/scripts/chat-lifecycle/formal-finalize.sh#L175)。
