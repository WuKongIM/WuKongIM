# #1017 合并后的正式云 RC 执行草案

**当前不能启动付费链。** 本目录只读取精确 Git 对象和既有证据；没有网络、Quote、Acquire、Actions 调度/启用、权限修改、密钥生成或仓库写入。`plan.json` 与两个 dispatch 模板故意保留 `null`，不是可直接提交的完成请求。预算是仓库固定的 ¥1,500 硬上限、¥1,350 操作停止线，尚无当前报价。

更新：用户批准后 #1017 已于 09:39:17Z 合并为 `fc18221282f289088ec9b4282fda8e0bfc67a3b8`。本地精确 Git 对象验证其完整 tree 与 ab75 相同；父任务另确认远端 main。当前 `plan.json` 和模板 source 已填 fc182，request/key/control/bundle 保持未定。预合并计划完整保存在 `premerge/`，未覆盖。

## 身份与已完成前置

审计固定于 PR #1017 head `ab75c226f4b3c246797aee4f876f23af602d07ce`，tree `3254faa77819a1dc7c4b7230b567ec1115be5a7a`。父任务已冻结四个首次 CI 全绿及 synthetic checkout `7204d1ba4a5fe101397c85545e84df15e28e0082` 的 tree 等价证据；本次只复核本地 head tree 与该收据，synthetic Git 对象本地不存在，没有联网补取或宣称独立重算。scripts gate 为 602 根用例通过、6 个明确跳过、0 失败/未完成，545.528 秒；其余实际结果继续以父任务原收据为准。

本次最终 `S=fc18221282f289088ec9b4282fda8e0bfc67a3b8`，其 parents 为 ed705 与 ab75。对应 CI 的 synthetic checkout/tree 映射保留；无需因 merge SHA 不同伪造新一轮 CI。用户本地 main 的其他提交不是此次 source，也不构成新的候选阻塞。需要重新映射的是最终合并身份，不是无条件重跑所有已通过项目。

本地预检为 `0672c115950f65e6d92381aa5b3ea9f0a20fc790` 的唯一 500 SEND/s 运行：helper 1800 秒，实际测量 1753.104 秒，结论 `operator_stop`；原告警、清理与凭据布尔检查已封存。`0672 → ab75` 在 cmd/internal/pkg、chat-lifecycle 配置和实际 shakeout helper 上无差异，JSON 保留该精确检查。它不能称为 Linux 长稳、完整 1800 秒测量或正式 pass；合并后仍需核对该映射没有新变化。

`source_files` 固定 36 个 AGENTS/FLOW、Run Plan、工作流、脚本和规范的 SHA256。最终 `S`/控制版本若改变其中约束，先审核差异。原正式计划为历史依据；实际数值由当前 YAML/代码优先，不采用旧 bootstrap 25/50、热 p99 200ms 或 1TB 描述。

## 入口及可提交输入

**唯一顶层付费入口**为 `chat-lifecycle-rehearsal.yml`，`ref=main`，只有：

| 字段 | 填入方式 |
|---|---|
| `source_sha` | 最终已验收且可从远端 main 到达的 40 位 `S` |
| `operator` | 固定 `tangtaoit` |
| `request_id` | 执行者生成新请求，匹配 `[A-Za-z0-9][A-Za-z0-9._-]{0,95}`；先排除旧 handoff/stop marker/Issue 冲突 |
| `codex_diagnostic_pubkey` | 此请求专属 Ed25519 公钥；私钥在仓库外 0700 目录、0600 文件保管，不进入此证据目录 |

预算、机器、时长和门槛没有可覆写输入。公开表面和 main/source 检查见 [rehearsal workflow L7](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/.github/workflows/chat-lifecycle-rehearsal.yml#L7)、[L61](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/.github/workflows/chat-lifecycle-rehearsal.yml#L61)。

可先准备一次**独立、无云凭据**的 `cloud-deployment-bundle.yml`，输入只有 `source_sha=S`、新的关联 `request_id`，`ref=main`。`bundle-dispatch.template.json` 给出精确 API JSON 形状；本子任务明确不执行。该工具 `contents:read`、ubuntu-24.04、45 分钟上限，没有 Environment、OIDC 或 Lease；属于非付费构建准备，不等于付费授权。[workflow L4–28](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/.github/workflows/cloud-deployment-bundle.yml#L4)

但顶层 rehearsal **仍自行调度并绑定自己的 bundle**，没有输入可注入预先构建的 bundle run。独立构建只提前验证工具链/密封，不能省略正式契约。[stage-orchestrate L258](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/scripts/chat-lifecycle/stage-orchestrate.sh#L258)

旧 ed705 bundle（run 39555917411，artifact 12325112901，digest `sha256:f274ffe1a3a74278d893fd16dd3a8df2e563fda375ac0295dfd8f6f5e982900a`）只留作历史证据，不能冒充 `S`。新构建需保存实际 producer run/attempt/head、source、bundle control、ZIP/API SHA、tar SHA、完整 manifest、ELF/二进制/YAML 摘要。`control` 是实际运行该工作流的 main SHA；`source` 是 `S`。子工作流使用 `ref=main`，各实际 control SHA 可能不同，不能假定预演后 main 不再前进；记录并核准实际控制来源。正式阶段必须复用原 `S` 与同一个 bundle digest，不重新构建产品。[README L965](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/.github/workflows/README.md#L965)、[L1008](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/.github/workflows/README.md#L1008)

## 固定执行与通过判据

- 杭州同区同型非突发 PostPaid Ubuntu24.04 x86_64 四主机，每台 4vCPU/8GiB；三 service 为 40GiB 系统盘+500GiB 数据盘，私网；一 load 为 40+200GiB，唯一 20Mbps EIP，承载三 worker/协调器/监控。ADR0042 的临时 load 22/80 公网范围也在授权方案内。型号/可用区/镜像/配额必须来自当天 Quote，不能预造。
- 预演 12h 不可延长 Lease，readiness 后全部 10,000 CONNECT+fresh version-zero sync、第一份完整 2,000 SEND/s grant 才开始测量 2h；只接受 `rehearsal_pass`。上传终端证据、释放并认证 exact zero 后，才可产生正式 transition。
- 新正式 96h Lease；连续 72h，同一进程 24h 出 qualification 而不重启。仅 Soak `pass` 进入同数据、同 worker fence/generation/observer 的容量阶梯：2000 起，每档 +25%，10m 稳定+20m 测量，细化至 10%，最多 8h；随后 2000 SEND/s 恢复 30m，要求 recovery_passed。未碰到边界只报容量下界。
- 正式启动须剩余 ≥81.5h；预演须 ≥3h。每5秒检查累计成本和1h清理余量；整个请求 ¥1350 停止、¥1500硬限，涵盖两阶段、诊断、延迟收尾、磁盘/公网流量及保留风险。不是每阶段分别1500，也不是报价承诺。
- 原负载/门槛保持：10,000在线、bootstrap100/s、250,000新用户/日、SEND2000/s、90/10单聊群聊、十万成员群；热ACK p99≤400ms/p99.9≤1s，冷≤2s/5s，sync≤1s/3s，正确性零错误及原首发失败率边界。原 YAML 所有资源、磁盘、队列、健康、生命周期和持续窗口判据保留。

依据：[Run Plan模板](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/configs/cloud/chat-lifecycle/formal-v1.json#L4)、[formal YAML L7](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/configs/wkbench/chat-lifecycle/formal.yaml#L7)、[L69](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/configs/wkbench/chat-lifecycle/formal.yaml#L69)、[admission L201](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/cmd/wkcli/internal/benchmark/formal_chain_command.go#L201)。

## 仍缺什么；哪些步骤现在不能执行

1. PR 合并及 source/tree 解析已完成；将父任务的远端 merge 收据纳入最终执行冻结。不得改动用户本地 main 来代替。
2. **明确付费授权**必须覆盖最终 `S`、两次顺序 Lease、跟踪 Issue、资源公网范围、整个预算和清理。最小文本在 `plan.json.authorizations.paid_phrase_template`；目前这份委派只允许离线计划，不能据“同意准备”或合并许可启动。
3. 启动前的 live OIDC 三角色 exchange、精确 sole-policy/trust/一小时 session，以及 wrapping 公私钥匹配。04:56–05:02 UTC 的非秘密变量/Environment 快照与8月成功记录不能证明现在可用。不要读取 Secret 值、自动 force reconcile 或改权限；`cloud-lease-oidc-setup.yml operation=apply` 只有既有七变量齐全且不 force 时才走既有绑定验证，不能笼统称为无条件只读。
4. 重新核对 GitHub 活跃 producers、完整 handoff/cleanup-pending/stop marker 及历史 exact selector 零库存证明；过去27份结构相符 cleanup 和空闲快照不证明当前账户清零。当前云库存仍未查询。
5. 获准后由原入口证明 finalizer 与独立 generic release 已 arm，然后 Quote 必须预算/容量/配额均合格；Acquire 仅消费该 exact admitted Quote。selector 和密封部署身份须采购前落盘；每阶段仅一次采购，不改规格或追加新 placement 求绿。[stage-orchestrate L472](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/scripts/chat-lifecycle/stage-orchestrate.sh#L472)

本目录没有创建请求或诊断密钥，因此缺失字段不是伪值。绑定变量存在不构成云授权/可用性证据；任何权限修复单独处理，不混入这份付费方案。

## 失败与交付

运行/正确性失败不自动重跑。计时前部署控制修复可在同 Lease 上尝试不同 protected-main 控制修订各一次，原 source/bundle/身份不变；需改产品则本请求终止、清零后重新明确授权。可诊断故障至多保留2h；预算/磁盘/expiry/不可达/operator stop缩短窗口。报告上传救援至多另2h，随后即使上传失败也释放。[README L1032](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/.github/workflows/README.md#L1032)

最终必须验证24h/72h/capacity JSON+Markdown成对、`validate-formal-chain`、完整 bounded terminal evidence、两阶段成本及两次 selector-bound zero proof。缺终端证据将原通过降为 `harness_invalid`；`passed_with_capacity_warning`单独解释。保留90天 combined artifact，实际 provider 账单未到仍为 pending。Release未清零留cleanup-pending交下次finalizer和15m sweeper，不把删除请求成功当清零；只有最终归档成功且exact zero才关闭Issue。[formal-finalize L125](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/scripts/chat-lifecycle/formal-finalize.sh#L125)

人工停止只用 `chat-lifecycle-stop.yml` 的 request_id 与 `operator-stop-chat-lifecycle`，先写stop marker，不能硬取消cleanup owner。当前 [Direct Lab Skill](https://github.com/WuKongIM/WuKongIM/blob/ab75c226f4b3c246797aee4f876f23af602d07ce/.agents/skills/wukongim-chat-lifecycle/SKILL.md#L117) 明确仅诊断，默认¥300/¥250不适用于这条正式OIDC链。本轮只读它核实边界，没有调用direct-lab。正式云验收本身仍不等于v3发行完成；stable签名APT/RPM发布与公开精确版本客户端验证另行完成。

## 离线复核

```sh
python3 -B verify-plan.py --repo /path/to/WuKongIM --plan plan.json
```

仅 `git show` 读取本地精确SHA，核对36文件摘要、预算、拓扑、阶段和输入闭包；成功表示**草案与源码一致**，不表示可以dispatch。生成器 `build-plan.py` 接受显式路径，输出文件独占创建；复算请给新空输出目录。原合成merge对象本地缺失的生成失败记在 `build-first-attempt.json`；首次复核遇到合法工作树清理记在 `verification-worktree-cleanup.json`。改用现存主仓库只读精确对象后成功，没有联网、checkout 或伪造修复。
