# OpenAI Codex agent-native 开发实践对 WuKongIM 的借鉴报告

## 结论摘要

Codex 最值得 WuKongIM 借鉴的不是 Rust、Bazel 或更多机器人，而是四种可执行工程合同：把重复维护流程做成带脚本和测试的 repository skill；把 PR 快反馈、合并阻断和 post-merge 重验证拆成明确层级；让 agent 逻辑通过公共协议、快照和跨执行环境的集成测试验证；把模型限制在只读分析 job，再由窄权限 job 消费结构化输出。

Codex 也暴露了三个不宜照搬的点：322 行根 `AGENTS.md` 与官方“短、准、按目录拆分”的建议有张力；review 规则同时存在于根规则、review skills 和 GitHub prompt，当前快照已经出现内容漂移；外部贡献邀请制及其极简 PR 模板并不适合直接移植到 WuKongIM。

与 OpenClaw 相比，Codex 的仓库自动化规模小得多、PR CI 更清晰、模型写入动作更保守；但 OpenClaw 在 skill 脚本进入 CI、secret/Workflow 专项扫描和 PR 模板上更完整。WuKongIM 的 Issue Agent / Review Agent 在模型、验证、状态、发布和精确 head 权限隔离上仍然最严格，应保留这一优势。

本文只提出借鉴判断，不实施任何建议。

## 核验范围与快照

- 核验日期：2026-08-09（Asia/Shanghai）。
- Codex：官方仓库 `openai/codex` 的 `main`，冻结提交 [`646f7c0a91b8e327d263335da68ae8ef212895ce`](https://github.com/openai/codex/commit/646f7c0a91b8e327d263335da68ae8ef212895ce)，提交时间 2026-08-09 03:05:10 +00:00。
- OpenClaw 对照：本仓库既有报告 `docs/superpowers/reports/2026-08-09-openclaw-agent-development-lessons.md`，冻结 OpenClaw 提交 `632808a674ee5beeb4b7f1b7fb89500fb5bc10e3`。
- WuKongIM：本地 HEAD `992e3520c2fb0541a28d3e0d12191fc133ff3791`。核验时工作树已有与本报告无关的用户改动；没有修改或把这些改动当作事实依据。
- 官方产品语义：使用本线程已刷新的 `/var/folders/q8/sn__8sjn6z318y0s4scxjq240000gn/T/openai-docs-cache/codex-manual.md` 及其 outline。它明确建议 `AGENTS.md` 保持短而准确、重复流程做成 skill、默认收紧 sandbox/approval，并强调 Code Review 不能取代测试、分支保护和必需审批（`codex-manual.md:1676-1700,1776-1800,26226-26378`）。
- 数量只用于说明形态：该 Codex 快照有 2 个 `AGENTS.md`、14 个 `.codex/skills/*/SKILL.md`、27 个 Workflow，共约 7,067 行 Workflow；不以数量判断质量。
- 线上 GitHub Ruleset、Environment 审批、Codex GitHub App 安装权限和 `go/workflow-approvals` 的具体实现不在仓库中，因此不对这些不可见配置作事实推断。

## 十二条高价值结论

### 1. scoped `AGENTS.md` 应只出现在真正的局部行为边界，根规则应继续减重

Codex 根 `AGENTS.md` 把格式、API 形状、测试命令、review 风险和平台矩阵写得非常具体，并明确 agent 逻辑变化必须有集成测试、复杂改动应控制规模。([`AGENTS.md#L64-L131`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/AGENTS.md#L64-L131))。但整个仓库只有一个真实 scoped 文件：它只约束 TUI bottom pane 的状态机文档同步，规则短且紧贴反复变化的局部 seam。([`codex-rs/tui/src/bottom_pane/AGENTS.md#L1-L12`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/codex-rs/tui/src/bottom_pane/AGENTS.md#L1-L12))

这比 OpenClaw 的 25 个 `AGENTS.md` 更克制，但 Codex 根文件本身已有 322 行，与官方 manual 所说“短、准；变大后引用 task-specific 文件”存在张力（`codex-manual.md:1676-1700`）。WuKongIM 根规则已经集中表达 cluster semantics、依赖方向和测试层级，并用包内 `FLOW.md` 保存行为流（`AGENTS.md:14-33,37-71,96-134`）。

**分类：直接借鉴，P1。** 继续保留“根规则 = 全局不变量，`FLOW.md` = 包行为流”；只在一个目录拥有独特状态机、协议兼容性或高风险操作边界时增加十几行 scoped `AGENTS.md`。不照搬 Codex 322 行根文件，也不按 OpenClaw 数量铺规则。

### 2. review 规则必须只有一个权威来源，其他载体应生成或做同步合同

Codex 把 model-context、breaking-change、testing、change-size 四个 review 维度同时写入根 `AGENTS.md` 和 `.codex/skills/code-review-*`。([`AGENTS.md#L85-L131`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/AGENTS.md#L85-L131), [`.codex/skills/code-review-context/SKILL.md#L1-L13`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.codex/skills/code-review-context/SKILL.md#L1-L13), [`.codex/skills/code-review-testing/SKILL.md#L1-L14`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.codex/skills/code-review-testing/SKILL.md#L1-L14))。GitHub 的 Rust review prompt 又维护第三套检查清单。([`.github/codex/labels/codex-rust-review.md#L7-L27`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/codex/labels/codex-rust-review.md#L7-L27), [`.github/codex/labels/codex-rust-review.md#L125-L139`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/codex/labels/codex-rust-review.md#L125-L139))

漂移已经可见：根规则把 `rawResponseItem/*` 列为 breaking surface，而对应 skill 没有这一项。([`AGENTS.md#L102-L110`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/AGENTS.md#L102-L110), [`.codex/skills/code-review-breaking-changes/SKILL.md#L1-L12`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.codex/skills/code-review-breaking-changes/SKILL.md#L1-L12))

**分类：直接借鉴教训，P1。** WuKongIM 不应把同一 review policy 手工复制到 `AGENTS.md`、skill、prompt 和 Workflow。维持一个受保护的结构化权威源，其他视图由生成器产生或用合同测试证明等价；`FLOW.md` 继续只承载行为知识，不成为第二份 review policy。

### 3. repository skill 的成熟形态是“小入口 + 确定性脚本 + fixture/test”，但 skill 测试必须进入门禁

Codex 的 14 个仓库 skill 不只是提示词。`babysit-pr` 和 `codex-issue-digest` 都带 Python 实现与测试，前者还带 GitHub API 和判定启发式引用。([`babysit-pr/SKILL.md#L42-L86`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.codex/skills/babysit-pr/SKILL.md#L42-L86), [`test_gh_pr_watch.py#L1-L42`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.codex/skills/babysit-pr/scripts/test_gh_pr_watch.py#L1-L42), [`test_collect_issue_digest.py#L1-L30`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.codex/skills/codex-issue-digest/scripts/test_collect_issue_digest.py#L1-L30))。这与 OpenClaw “skill 是工程资产”的方向一致；差别是 OpenClaw 已把 skill Python lint/test 接入 CI，而 Codex 当前可见 Workflow 和 `justfile` 没有引用这两组测试。

Codex 官方 manual 推荐共享 team skill 放在 `.agents/skills`（`codex-manual.md:1776-1800`），但仓库自身仍放在 `.codex/skills`。WuKongIM 已使用当前推荐的 `.agents/skills`，没有必要为模仿 Codex 仓库而改名。

**分类：直接借鉴，P0。** 优先落地 OpenClaw 报告已经提出的统一 skill catalog/contract test，并把每个含脚本 skill 的 focused test 纳入普通快速门禁；保留 WuKongIM 的 `.agents/skills` 路径。Codex 提供脚本化形态，OpenClaw 提供 CI 完整性，两者组合才是目标。

### 4. “按审查维度并行”可提高召回率，但不应直接成为发布权威

Codex 的 `code-review` orchestrator 要求为每个 `code-review-*` skill 启一个独立 subagent，汇总全部发现，并默认不在 GitHub 留言。([`.codex/skills/code-review/SKILL.md#L1-L14`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.codex/skills/code-review/SKILL.md#L1-L14))。这种按“context、breaking changes、testing、change size”拆 lens 的方式，比让多个 agent 重复做同一泛化 review 更有价值。

它也有成本：结论要求“一个不漏地返回”，但没有定义跨 agent 去重、置信度合并、证据冲突和最终风险预算；多 agent 输出本身不能成为可靠权限边界。

**分类：条件借鉴，P3。** 可把多 lens review 用作 WuKongIM Review Agent 的离线实验或同一 signed generation 内的 advisory 第二遍，并量化新增有效发现、重复率、token/时延。最终 verdict、证据验证和 exact-head authority 仍必须由现有可信代码统一裁决；不要因为 subagent 数量增加就扩大权限。

### 5. PR 快反馈、唯一聚合 gate、post-merge 全矩阵是清晰的 CI 分层

Codex 明确把 PR 阶段设计为“Bazel 主验证 + 很小的 Cargo-native 检查”，把完整 Clippy、nextest、release build、remote-env 等重矩阵留到 `main`。([`.github/workflows/README.md#L1-L34`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/README.md#L1-L34))。`blocking-ci.yml` 是单一合并阻断入口，`CI required` 用 `always()` 聚合所有 reusable workflow；`rust-ci.yml` 自己也用一个稳定结果 job 正确处理被跳过的按路径任务。([`blocking-ci.yml#L1-L79`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/blocking-ci.yml#L1-L79), [`rust-ci.yml#L223-L268`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/rust-ci.yml#L223-L268))

OpenClaw 也采用 path routing + stable gate，但覆盖 82 个 Workflow 的生态规模；Codex 的分层更容易理解。WuKongIM 则有意不设传统常开 CI，而由管理员显式启动 Review Agent，再由受保护路径选择 named checks（`docs/development/CI.md:1-42,69-88`）。

Codex 的聚合入口对每个 reusable workflow 使用 `secrets: inherit`。([`blocking-ci.yml#L13-L46`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/blocking-ci.yml#L13-L46)) 这简化了 wiring，却不是 WuKongIM 应复制的权限接口。

**分类：条件借鉴，P3。** 如果未来普通 PR 的反馈时延和贡献规模证明值得，优先参考 Codex 的“少量 PR gate + 重型 post-merge”，而不是复制 OpenClaw 的大矩阵。现阶段可以只借鉴聚合语义到 trusted-check selector；不要在没有需求数据时恢复传统全量 CI，也不要以 `secrets: inherit` 取代逐 job 的最小 secret 接口。

### 6. 为人和 agent 提供同一个任务入口，并用合同保持多构建系统一致

Codex 根规则禁止直接 `cargo test`，要求通过 `just test`，并让 focused、full、format、fix、schema generation、Bazel lock 都有稳定入口。([`AGENTS.md#L64-L70`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/AGENTS.md#L64-L70), [`justfile#L43-L100`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/justfile#L43-L100), [`justfile#L137-L194`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/justfile#L137-L194))。它还用 repo checks 验证 Cargo workspace 继承、TUI/core 依赖边界和 Bazel/Cargo lint 配置一致。([`repo-checks.yml#L21-L37`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/repo-checks.yml#L21-L37))

**分类：直接借鉴语义，P2。** WuKongIM 可以让开发者、skill、Issue Agent 和 Review Agent 引用同一组稳定任务名/manifest，并为生成配置、协议 schema、前后端产物或重复命令增加 drift test。不要照搬 `just` 或 Bazel；Go 原生命令和现有 protected named-check catalog 已足够，重点是唯一入口和参数不可漂移。

### 7. agent 产品应优先测试公共协议、可见输出和本地/远端执行等价性

Codex 要求 agent logic 变化添加 integration test；app-server 测试必须走公共 JSON-RPC API；TUI 可见变化必须更新 `insta` snapshot；connected app-server/exec-server 还要覆盖不同主机/目标 OS。([`AGENTS.md#L112-L123`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/AGENTS.md#L112-L123), [`AGENTS.md#L180-L220`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/AGENTS.md#L180-L220), [`AGENTS.md#L252-L258`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/AGENTS.md#L252-L258), [`.codex/skills/remote-tests/SKILL.md#L6-L47`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.codex/skills/remote-tests/SKILL.md#L6-L47))

**分类：直接借鉴原则，P2。** WuKongIM 已有 process-level E2E、integration build tag 和 single-node/multi-node cluster 语义（`AGENTS.md:14-23,96-130`）。进一步的价值不是增加内部 mock，而是让 agent control plane、协议/schema、manager 可见输出和 remote cloud worker 的关键行为都有稳定的边界 fixture/golden；耗时和真实资源测试继续留在 integration/E2E 层。

### 8. 供应链与 CI 凭据采用“固定引用、无持久 checkout、fork fail-closed、敏感路径 owner”

Codex 的 CI 示例普遍以完整 SHA 引用外部 Action并设置 `persist-credentials: false`；`cargo-deny` 是 merge-blocking workflow。([`cargo-deny.yml#L1-L32`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/cargo-deny.yml#L1-L32), [`bazel.yml#L54-L82`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/bazel.yml#L54-L82))。BuildBuddy 只对可证明来自 upstream 的运行开放，fork 或事实不完整时退回无 OpenAI 凭据路径。([`run_bazel_with_buildbuddy.py#L66-L97`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/scripts/run_bazel_with_buildbuddy.py#L66-L97))。CODEOWNERS 覆盖核心 crate、macOS signing、release workflow 和自身；Dependabot 对 Bun、Cargo、devcontainer、Docker、Actions、Rust toolchain 使用 weekly + 7-day cooldown。([`.github/CODEOWNERS#L1-L17`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/CODEOWNERS#L1-L17), [`.github/dependabot.yaml#L1-L42`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/dependabot.yaml#L1-L42))

OpenClaw 在这一项更完整：它另外运行 secret history scan、actionlint、zizmor 和 production dependency audit；现有对照报告已把这些列为 WuKongIM P1 建议（OpenClaw 报告 `:61-75,107-110`）。WuKongIM 已有外部 Action full-SHA 合同测试，但 CODEOWNERS 仍主要覆盖 Agent 控制面，没有对齐 policy 中已有的 cluster/storage/protocol high-risk paths（`scripts/github_workflows_test.go:39-87`，`.github/CODEOWNERS:1-30`，`.github/issue-agent/policy.json:49-71`）。

**分类：直接借鉴并合并两方优点，P1。** 保留 WuKongIM 的机器 pin 合同，补 OpenClaw 的专项扫描，再按 Codex 的 targeted ownership 思路把 CODEOWNERS 对齐真实高风险边界；依赖更新采用分生态、限流、cooldown。不要把“仓库当前都写了 SHA”当成无需合同的保证。

### 9. issue-first 与机器可读诊断值得借鉴；邀请制贡献和 Codex PR 模板不适合照搬

Codex 明确不接受未经邀请的外部代码贡献，鼓励社区先在 issue 中提供复现、分析、根因假设和方案；被邀请后才进入 focused branch、测试、atomic commits、maintainer squash-merge 流程。([`docs/contributing.md#L1-L27`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/docs/contributing.md#L1-L27), [`docs/contributing.md#L29-L67`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/docs/contributing.md#L29-L67))。其 issue form 按 App、IDE、CLI 等 surface 分流，CLI 表单优先收集经过脱敏设计的 `codex doctor --json`。([`.github/ISSUE_TEMPLATE/3-cli.yml#L1-L71`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/ISSUE_TEMPLATE/3-cli.yml#L1-L71))

但 Codex PR 模板只有“外部贡献需邀请、写高质量说明、链接 issue”三项，信息密度明显低于 OpenClaw 的 Problem / Why / User Impact / Evidence 模板。([`.github/pull_request_template.md#L1-L8`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/pull_request_template.md#L1-L8))

**分类：条件借鉴 + 明确不照搬，P1/P2。** P1 仍采用 OpenClaw 的 PR 模板结构，并补 feature/docs forms；P2 再评估让 `wkcli`/manager 输出可公开提交、默认脱敏、schema 稳定的诊断包。保留“先对齐问题与方案再写代码”，不复制 Codex 邀请制，也不复制按其产品 surface 划分的字段。

### 10. 自动 issue agent 的正确最小形态是“只读模型 + 结构化输出 + 独立窄写 job”

Codex 的 issue labeler 和 translator 在模型 job 中只给 `contents: read`，使用 `drop-sudo`、`read-only` sandbox 和 JSON output schema；随后单独的 `issues: write` job 解析结果并执行标签、标题或评论更新。Translator 还把 issue 文本写入数据文件并明确声明它是 untrusted content，而不是指令；这是比 Labeler 将标题/正文直接插进 prompt 更稳妥的输入边界。([`issue-labeler.yml#L9-L27`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/issue-labeler.yml#L9-L27), [`issue-labeler.yml#L83-L107`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/issue-labeler.yml#L83-L107), [`issue-labeler.yml#L109-L147`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/issue-labeler.yml#L109-L147), [`issue-translator.yml#L9-L35`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/issue-translator.yml#L9-L35), [`issue-translator.yml#L50-L76`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.github/workflows/issue-translator.yml#L50-L76))。官方 manual 也明确提醒 `read-only` 本身不能保护 secrets，需同时 drop sudo/降权、收紧 trigger，并把 Codex 放在 job 最后（`codex-manual.md:30090-30210`）。

WuKongIM 已把这条原则推进得更远：Issue Agent 模型无 GitHub/App/cloud/deploy 凭据，clean Verifier 在独立 checkout 复验，Publisher 只写精确 agent branch/Draft PR，永不写 `main` 或 merge（`docs/agents/issue-agent.md:88-143`）；Review Agent 还使用 signed generation、trusted checks 和 exact-head fencing（`docs/agents/review-agent.md:23-72,100-150`）。

**分类：保持现状，P0。** 对未来低风险 issue 分类/翻译可复用 Codex 的小型两-job模式，但任何代码、状态或 merge 自动化继续使用 WuKongIM 更强的 verifier/publisher 隔离。不要倒退成“schema 合法即可发布”，也不要给模型 job 写 token。

### 11. skill 中的权限文字是交互政策，不是安全边界；高风险写操作必须有平台硬门禁

Codex 的 `babysit-pr` 对自动 push、重跑 CI、解决 review thread 和与人类互动写了很细的政策：不得自动回复其他人、只处理 PR head branch、不得用破坏性 Git、歧义时请求用户确认。([`babysit-pr/SKILL.md#L116-L150`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.codex/skills/babysit-pr/SKILL.md#L116-L150))。这些规则提升操作质量，但使用的是操作者现有 `gh` 凭据，技术上仍属于“软约束”。相比之下，`pushing-ci-changes` skill 明确记录 `.github/**/*.yml` 的上传需要临时角色，agent 自己不能申请豁免，说明真正的权限边界位于服务端。([`.codex/skills/pushing-ci-changes/SKILL.md#L1-L17`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/.codex/skills/pushing-ci-changes/SKILL.md#L1-L17))

OpenClaw 的部分 agent 直接 push `main`，其风险已经在对照报告中明确；Codex 的仓库 issue 模型只做低后果 projections，而其 `.github/codex/labels/codex-attempt.md` 虽要求创建 branch/PR，相关 App 权限并不在仓库可见范围内，因此不能拿来证明安全设计。

**分类：直接借鉴交互规范，权限实现不照搬，P0/P2。** 可以采用“agent 发出的公开回复必须可辨识、替他人发言需确认、只修改精确目标”的软政策；Workflow、规则文件、发布和 merge 必须继续由 GitHub App/Environment/Ruleset/签名状态实施硬授权。若多人频繁修改 Workflow，再条件性引入 Codex 式临时上传角色。

### 12. 把模型可见 context 当成有预算、可审查的公共接口

Codex 规定 context 只能增量构建、每个注入项必须有硬上限、单项不得超过 10K tokens，新出现的超过 1K tokens 单项按 P0 人工审查，并要求所有注入片段通过统一接口定义。([`AGENTS.md#L91-L100`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/AGENTS.md#L91-L100))。其 `AGENTS.md` 加载实现还对跨层级合并设总字节预算，并有截断测试。([`agents_md.rs#L50-L90`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/codex-rs/core/src/agents_md.rs#L50-L90), [`agents_md_tests.rs#L615-L664`](https://github.com/openai/codex/blob/646f7c0a91b8e327d263335da68ae8ef212895ce/codex-rs/core/src/agents_md_tests.rs#L615-L664))

WuKongIM Review Agent 已有 changed files、encoded context、完整文件行数与输出 token 上限（`docs/agents/review-agent.md:152-157`），方向正确；但其他 Issue/Analysis/ops skill 的日志、指标、诊断片段还应共享同一套“最大条数/字节/token、截断顺序、provenance、超限 verdict”语言。

**分类：直接借鉴并推广，P1。** 把每类 agent 输入的预算和截断规则做成结构化合同与测试；超限应产生 `inconclusive`/`insufficient_evidence`，不能静默截断后仍给确定结论。

## 推荐优先级

| 优先级 | 建议 | 来源判断 |
| --- | --- | --- |
| P0 保持 | 保留 WuKongIM 模型—Verifier—State Writer—Publisher—exact-head merge 的权限分离；skill 文字永不作为授权依据 | WuKongIM 强于 Codex 和 OpenClaw |
| P0 新增 | 建立统一 skill catalog/contract test，并把含脚本 skill 的测试接入快速门禁 | Codex 的脚本化形态 + OpenClaw 的 CI 完整性 |
| P1 新增 | 统一 review policy 权威源，消除 `AGENTS.md` / skill / prompt 手工复制 | Codex 当前漂移的直接教训 |
| P1 新增 | 采用 Problem / Why / User Impact / Evidence PR 模板，补 feature/docs forms | OpenClaw 优于 Codex；WuKongIM 当前缺口 |
| P1 新增 | CODEOWNERS 对齐 high-risk paths；加入 secret、actionlint/zizmor、依赖安全检查 | Codex targeted ownership + OpenClaw security-fast |
| P1 新增 | 统一模型 context 的大小、截断、provenance 和超限 verdict 合同 | Codex context API；推广 WuKongIM Review Agent 既有预算 |
| P2 改善 | 让本地开发、skills 和 trusted checks 使用同一任务 catalog；为生成产物与重复配置加 drift test | Codex `just`/repo-checks 的语义，不复制工具 |
| P2 探索 | 设计默认脱敏、schema 稳定的 WuKongIM 诊断包，供 bug form 与 Agent 消费 | Codex `doctor --json` 的入口质量 |
| P3 评估 | 只有数据证明有必要时，再增加常开 fast PR gate、post-merge full matrix 或多-lens review | Codex 形态优于复制 OpenClaw 规模 |

## 明确不照搬

- 不复制 Codex 的外部代码贡献邀请制；它是团队吞吐策略，不是 agent-native 的普遍最佳实践。
- 不复制 Codex 的 Bazel/Cargo 双构建系统、跨 OS 矩阵或具体 Rust lint；只借鉴稳定入口与 parity contract。
- 不把 322 行根 `AGENTS.md`、三份 review 检查清单或 `.codex/skills` 路径当模板。
- 不把 skill 里的“不得做某事”视为技术授权；高风险写入必须由 token scope、Environment、App API、Ruleset、signed state 和精确 SHA fence 实施。
- 不复制 OpenClaw 的模型直推 `main`；也不因为 Codex 的 GitHub prompt 能要求“创建 branch/PR”就降低 WuKongIM 的独立验证和发布隔离。

## 核验说明

- 通过 `git ls-remote https://github.com/openai/codex.git refs/heads/main` 与 shallow clone 两次确认 Codex SHA，所有 Codex GitHub 文件链接均固定到该 SHA。
- 对快照执行了文件清单、`AGENTS.md`/skill/Workflow 数量统计、外部 Action ref 形态检查和 skill-test CI 引用搜索；这些是范围核验，不代表运行 Codex 的完整测试套件。
- 本报告是 Markdown 研究产物，没有改动产品代码、Workflow、现有 OpenClaw 报告或用户文件。
- 不可从仓库证明的线上 Ruleset、Environment、App token scope 与内部临时角色实现均列为核验限制，而不是假设。
