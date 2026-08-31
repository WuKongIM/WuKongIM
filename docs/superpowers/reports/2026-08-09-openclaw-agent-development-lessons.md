# OpenClaw agent 开发实践对 WuKongIM 的借鉴报告

## 结论摘要

OpenClaw 最值得借鉴的不是更多提示词或更多自动化，而是三类可执行约束：把根规则、目录边界和操作流程分层；把 skill 当作带脚本、夹具和测试的工程资产；让 CI、安全扫描、依赖审查和模板形成机器可验证的入口合同。

WuKongIM 不应照搬 OpenClaw 的规模与权限模型。WuKongIM 现有 Issue Agent / Review Agent 已在模型无写凭据、候选代码与发布者隔离、签名状态、精确 head fencing、受保护检查目录等方面更严格。尤其不应复制 OpenClaw 的 Docs Agent 和 Test Performance Agent 直接向 `main` 推送模型生成提交的做法。

建议优先级：

1. 为仓库内 skills 建立统一静态合同和测试入口。
2. 将 secret、依赖和 Workflow 安全校验纳入现有受保护检查目录。
3. 扩展 CODEOWNERS 到真正的安全、协议、存储和依赖边界。
4. 补齐 PR 模板、feature/docs issue 表单和 blank-issue 路由。
5. 仅在确认需要常开 PR CI 后，再设计稳定聚合 gate；不要复制 OpenClaw 的超大 CI 文件。

本文只提出可借鉴项和反例，不实施任何建议。

## 核验范围与快照

- 核验日期：2026-08-09（Asia/Shanghai）。
- OpenClaw：官方仓库 `openclaw/openclaw` 的 `main`，冻结提交 [`632808a674ee5beeb4b7f1b7fb89500fb5bc10e3`](https://github.com/openclaw/openclaw/commit/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3)，提交时间 2026-08-08 20:50:29 -07:00。
- WuKongIM：本地提交 `1218a90807e72ad3c01c4ae298e2ece5a056adf7`。核验时工作树已有与本报告无关的用户改动；本报告没有读取这些改动作为结论依据，也没有修改它们。
- 一手资料范围：OpenClaw 官方仓库上述提交中的源文件、官方仓库文档和 Workflow；WuKongIM 当前仓库文件。未使用博客、媒体文章或第三方总结。
- 数量只用于说明规模，不作为质量判断：OpenClaw 快照有 25 个 `AGENTS.md`（其中一个位于测试 fixture）、`.agents/skills/` 下 44 个开发/维护 skill、产品根 `skills/` 下 51 个 bundled skill、82 个 Workflow；WuKongIM 主工作树有根规则、以 E2E 场景为主的 scoped `AGENTS.md`、4 个仓库 skill 和 22 个已编目的 Agent Tool / Safety Automation Workflow。

## 十条高价值结论

### 1. 借鉴“根规则负责硬政策与路由，目录规则负责边界，skill 负责流程”，但不要继续膨胀根文件

OpenClaw 在根文件开头明确规定“Root rules only”“Skills own workflows”，并要求进入子树前读取 scoped `AGENTS.md`；根文件随后提供目录地图。([`AGENTS.md#L1-L16`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/AGENTS.md#L1-L16))。其 `extensions/AGENTS.md` 把插件边界落实成禁止深层导入、依赖归属、公共 facade 和同步 contract test 等可审查规则，而不是泛泛编码风格。([`extensions/AGENTS.md#L1-L46`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/extensions/AGENTS.md#L1-L46), [`extensions/AGENTS.md#L71-L83`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/extensions/AGENTS.md#L71-L83))

WuKongIM 根规则已经具备更清晰的运行时语义、依赖方向和测试分层，并要求先读包内 `FLOW.md`（`AGENTS.md:14-33,37-71,96-134`）。差距不是“缺少更多 AGENTS.md”，而是 scoped 规则主要集中在 `test/e2e/**`；生产代码的边界知识主要依赖根规则和 `FLOW.md`。

**建议：借鉴，但克制。** 只在跨目录导入边界、公共 API、测试隔离或高风险操作已反复出错的 ownership seam 增加短 scoped `AGENTS.md`；包内行为流继续由 `FLOW.md` 承担。不要照搬 OpenClaw 当前 388 行、混入大量产品和操作细节的根文件。新增规则应能被测试或 review 明确执行，否则留在文档而非 agent 硬政策中。

### 2. 明确区分“开发 agent skill”和“产品运行时 skill”

OpenClaw 的 `<workspace>/.agents/skills` 是 project-agent skill 来源之一；产品 bundled skills 则是另一个有优先级、allowlist、环境/二进制 gating 和插件可见性规则的加载层。([`docs/tools/skills.md#L32-L56`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/docs/tools/skills.md#L32-L56), [`docs/tools/skills.md#L79-L101`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/docs/tools/skills.md#L79-L101))。产品包明确包含根 `skills/`。([`package.json#L353-L377`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/package.json#L353-L377))

WuKongIM 的 `.agents/skills/*` 目前都是仓库开发/运维能力，根规则也只把 `.agents/` 定义为“Repository-local agent skills and support files”（`AGENTS.md:171-176`），WuKongIM 产品本身没有 OpenClaw 式用户技能运行时。

**建议：借鉴概念，不借鉴产品机制。** 在命名和文档中始终把 repository agent skills 与产品插件/协议能力分开；不要因为 OpenClaw 有 ClawHub、skill watcher、环境注入和运行时 allowlist，就在 WuKongIM 内引入一套无当前产品需求的 skill 平台。

### 3. 把 skill 当作可测试、可发布审查的工程资产，而不只是 `SKILL.md`

OpenClaw 的 skill 约定允许 `scripts/`、`references/`、`assets/` 等支持文件，并要求最小 frontmatter；创建指南要求本地加载和实际 agent 调用验证。([`docs/tools/creating-skills.md#L11-L89`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/docs/tools/creating-skills.md#L11-L89), [`docs/tools/creating-skills.md#L200-L218`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/docs/tools/creating-skills.md#L200-L218))。CI 对产品 skill 中的 Python 脚本运行 Ruff 和 pytest，release check 还验证 shell 脚本 executable bit。([`ci.yml#L2734-L2786`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/ci.yml#L2734-L2786), [`scripts/release-check.ts#L267-L289`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/scripts/release-check.ts#L267-L289))

WuKongIM 已有正确雏形：`wukongim-chat-lifecycle` 带 `references/operator-workflow.md`，`wukongim-cloud-analysis` 带 tool contract 和多种 verdict fixture，多个 skill 带 `agents/openai.yaml`。但仓库没有一个统一入口验证所有 skill 的 frontmatter、引用存在性、fixture 可解析性、脚本权限和示例可运行性；当前 `scripts/github_workflows_test.go:44-87` 只为 Workflow 做了类似的目录完整性与 pin 合同。

**建议：直接借鉴。** 将 skill catalog/contract test 作为普通快速测试：扫描 `.agents/skills/*/SKILL.md`，验证 name/description、相对引用、`agents/openai.yaml`、JSON fixture、脚本权限，并允许每个复杂 skill 声明自己的 focused test。不要要求每个纯文档 skill 都建专用框架。

### 4. 借鉴“按变更路由 + 单一稳定聚合 gate”，不要复制 3,709 行 CI

OpenClaw CI 先在一个 preflight 中计算变更范围和矩阵，再按 Node、UI、插件 contract、channel contract、skills Python、Windows、macOS、iOS、Android 等 lane 执行，最后由稳定的 `openclaw/ci-gate` 汇总：必选任务必须 success，未选 lane 可以 skipped。([`ci.yml#L63-L118`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/ci.yml#L63-L118), [`ci.yml#L674-L731`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/ci.yml#L674-L731), [`ci.yml#L3565-L3654`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/ci.yml#L3565-L3654))

WuKongIM 是有意采用不同模型：普通 PR 默认不自动运行模型，Review Agent 从保护策略按完整路径选择 named checks，`Review Agent Verdict` 是唯一自动 review gate（`docs/development/CI.md:1-42,69-88,133-139`）；合同测试甚至明确要求传统 `ci.yml`、`nightly.yml` 不存在（`scripts/github_workflows_test.go:90-98`）。

**建议：条件借鉴。** 现在先把“稳定聚合结果”和“未知路径不能漏检”继续落实在 Review Agent trusted-check selector 中。只有当外部贡献量、管理员触发延迟或非模型的快速反馈需求证明常开 CI 有价值时，才另行设计 always-on secretless fast gate。不要把 OpenClaw 的生态矩阵、缓存和跨平台复杂度搬来。

### 5. 增加独立的快速安全基线：secret、Workflow 和生产依赖

OpenClaw 的 `security-fast` 在普通 CI 之前/之外执行完整 PR 历史的 TruffleHog、从 base SHA 读取可信 pre-commit 配置、对变更 Workflow 运行 zizmor，并审计 production dependencies；外部 Action 使用 full SHA 且 checkout 禁止持久凭据。([`ci.yml#L819-L938`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/ci.yml#L819-L938), [`ci.yml#L940-L1015`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/ci.yml#L940-L1015))。另一个 Workflow sanity gate 从可信 base 取审计配置，固定下载 checksum，并运行 actionlint、zizmor 和 composite-action 插值检查。([`workflow-sanity.yml#L60-L139`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/workflow-sanity.yml#L60-L139), [`workflow-sanity.yml#L141-L195`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/workflow-sanity.yml#L141-L195))

WuKongIM 已用合同测试强制所有 Workflow 外部 Action 使用 40 字符 pin（`scripts/github_workflows_test.go:39-87`），Review Agent 也隔离候选代码、模型和凭据（`docs/development/CI.md:90-131`）。但本地受保护检查目录未显示一条覆盖提交历史 secret scanning、Action 专项静态审计和 Go/JS 生产依赖漏洞审计的统一快速基线。

**建议：直接借鉴能力，沿用 WuKongIM 权限模型。** 将这些检查作为固定命令加入现有 trusted-check catalog 或一个零写权限、只运行可信控制代码的安全 gate；不要为此让模型或候选代码接触 write token。

### 6. 将 CODEOWNERS 从“Agent 控制面所有权”扩展到真正的安全边界

OpenClaw 的 CODEOWNERS 先保护自身，明确提醒 GitHub last-match-wins，然后为依赖锁、CodeQL、security workflows、secrets、auth、sandbox、gateway security 和相关文档指定 secops owner。([`.github/CODEOWNERS#L1-L29`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/CODEOWNERS#L1-L29), [`.github/CODEOWNERS#L30-L69`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/CODEOWNERS#L30-L69))

WuKongIM CODEOWNERS 目前保护 Workflow、Agent 控制代码、根规则和相关文档，主要 owner 都是同一维护者（`.github/CODEOWNERS:1-30`）；Issue Agent policy 已把配置、cluster、storage、protocol 和依赖列为 high-risk（`.github/issue-agent/policy.json:49-71`），但这种风险分类没有对应到 CODEOWNERS 路径规则。

**建议：直接借鉴。** 至少让 CODEOWNERS 与已存在的 high-risk path/topic 一致，并保护 `go.mod`/`go.sum`、安全/认证、协议、持久化格式、发布与云身份边界。若当前只有一名 owner，也值得先形成路径合同；不要伪造不存在的团队或审批层级。

### 7. 借鉴依赖更新节流与“依赖变化需额外授权”，但不要复制 npm 专属 autoscrub

OpenClaw 用 Dependabot 对 npm、GitHub Actions、Swift、Gradle、Docker 分生态设置 daily/weekly cadence、2 天 cooldown、minor/patch grouping 和 PR 数量上限。([`.github/dependabot.yml#L13-L50`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/dependabot.yml#L13-L50), [`.github/dependabot.yml#L52-L143`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/dependabot.yml#L52-L143))。Dependency Guard 在 `pull_request_target` 上只 checkout 可信 base 脚本，并把检测、可选清理、最终 enforcement 分开。([`dependency-guard.yml#L1-L39`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/dependency-guard.yml#L1-L39), [`dependency-guard.yml#L89-L109`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/dependency-guard.yml#L89-L109))

WuKongIM 当前没有 `.github/dependabot.yml`，但 Issue Agent 明确把 dependency changes 拒出自动发布范围，并把 dependencies 列为 high risk（`.github/issue-agent/policy.json:30-71,114-128`）。

**建议：借鉴更新节流和额外审查，不照搬实现。** 先覆盖 Go modules、GitHub Actions、npm workspaces 和 Docker；按生态分组并限制并发 PR。依赖变更继续由人或受保护策略批准，不要复制 OpenClaw 面向 fork/npm lockfile 的 autoscrub 写操作。

### 8. 补齐结构化 PR / issue 入口，让 Agent 获得“问题、影响、证据”而非自由文本

OpenClaw PR 模板只保留四个稳定部分：What Problem This Solves、Why This Change Was Made、User Impact、Evidence，并要求标题面向用户症状。([`.github/pull_request_template.md#L1-L24`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/pull_request_template.md#L1-L24), [`.github/pull_request_template.md#L26-L65`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/pull_request_template.md#L26-L65))。它禁用 blank issues 并把 support/onboarding 导向 Discord；bug、docs bug、feature 使用不同表单。([`config.yml#L1-L8`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/ISSUE_TEMPLATE/config.yml#L1-L8), [`docs_bug_report.yml#L1-L26`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/ISSUE_TEMPLATE/docs_bug_report.yml#L1-L26), [`feature_request.yml#L11-L39`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/ISSUE_TEMPLATE/feature_request.yml#L11-L39))

WuKongIM 的 bug 表单已经要求版本、部署拓扑、最短复现、期望/实际结果和脱敏日志（`.github/ISSUE_TEMPLATE/bug.yml:1-70`），这是好的基础；当前没有 PR 模板、feature/docs issue form 或 issue chooser config。

**建议：直接借鉴模板结构。** PR 模板应对齐 WuKongIM Review Agent 已需要的 intent、风险和证据；feature 表单强调规模、cluster semantics、兼容性和替代方案；docs 表单要求精确路径。不要复制 OpenClaw 特有的 provider/model/channel 字段或 Discord 路由，除非 WuKongIM 已有对应官方支持入口。

### 9. 保持 WuKongIM 的模型/验证/发布三权分离；只借鉴 OpenClaw 的 artifact handoff 模式

OpenClaw 的 Dated TODO sweep 是正面范例：Codex 在只读 contents 权限的分析 job 中生成 report，只有 report artifact 进入新 runner；新 job 用可信代码验证后才 mint App token 并更新 issue。([`dated-todo-sweep.yml#L14-L74`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/dated-todo-sweep.yml#L14-L74), [`dated-todo-sweep.yml#L76-L156`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/dated-todo-sweep.yml#L76-L156))。其 generated-PR composite 也把 branch token 与 PR token 分开，并要求明确 generated/invalidation paths。([`create-generated-pr-tokens/action.yml#L1-L51`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/actions/create-generated-pr-tokens/action.yml#L1-L51), [`publish-generated-pr/action.yml#L17-L49`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/actions/publish-generated-pr/action.yml#L17-L49))

WuKongIM 已更系统地实现这条原则：Issue Agent 模型无 GitHub/App/cloud/deploy 凭据，clean Verifier 在独立 checkout 复验，Publisher 才能创建精确 App-signed commit 和 Draft PR，且不能写 `main` 或 merge（`docs/agents/issue-agent.md:88-143`）。Review Agent 同样由零权限 Signal、可信 Controller、模型、验证者、State Writer、Publisher 分层（`docs/agents/review-agent.md:23-72,100-150`）。

**建议：保持现状。** 可借鉴的是更小型自动化也采用“模型产物 artifact → 可信 schema/path validator → 最小权限 publisher”的复用形态，而不是改变 WuKongIM 现有 authority model。

### 10. 明确不照搬：模型生成内容直接推 `main`、超大根提示、与 WuKongIM 无关的生态自动化

OpenClaw Docs Agent 虽然核对当前 `main`、限制现有 docs 路径并运行 docs check，但 Codex 和后续 `contents: write` 发布处在同一 job，最终把提交直接 push 到 `main`。([`docs-agent.yml#L11-L43`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/docs-agent.yml#L11-L43), [`docs-agent.yml#L150-L211`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/docs-agent.yml#L150-L211), [`docs-agent.yml#L213-L250`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/docs-agent.yml#L213-L250))。Test Performance Agent 也让模型修改代码/测试，做路径和性能检查后直接 push/rebase `main`。([`test-performance-agent.yml#L126-L180`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/test-performance-agent.yml#L126-L180), [`test-performance-agent.yml#L193-L269`](https://github.com/openclaw/openclaw/blob/632808a674ee5beeb4b7f1b7fb89500fb5bc10e3/.github/workflows/test-performance-agent.yml#L193-L269))

**不照搬理由：** 这与 WuKongIM 已声明的模型不编辑/提交/推送/合并、Publisher 不执行候选代码、Agent PR 需独立 Review Agent 或人工合并的边界冲突（`docs/development/CI.md:25-42,90-131`）。同样不应复制 OpenClaw 为 TypeScript、多客户端、插件市场、发布列车和 ClawHub 服务的 82 个 Workflow、51 个产品 skill、跨平台缓存和专用机器人；只在 WuKongIM 出现相同需求时提取最小机制。

## 建议的落地顺序（仅路线，不实施）

1. **低风险、立刻有价值：** skill contract test；PR 模板与 feature/docs issue forms；CODEOWNERS 与 high-risk path 对齐。
2. **中风险、需安全设计：** secret scanning、dependency audit、actionlint/zizmor 进入受保护 named checks；Dependabot 分生态和限流。
3. **架构决策后再做：** 是否增加常开 secretless fast CI，以及如何把 selected lanes 汇总成稳定 check。现有 `Review Agent Verdict` 仍应保持独立，不应被一个未经同等威胁建模的新 gate 取代。
4. **明确排除：** 模型直推 `main`；把产品 runtime skill 系统移植进 WuKongIM；按 OpenClaw 数量复制 scoped rules、skills、bots 或 workflows。

## 核验限制

- 本报告核验的是一个精确提交，不宣称 OpenClaw 后续 `main` 保持不变。
- GitHub Ruleset、Environment 审批和 App 安装权限中不可由仓库文件证明的线上配置未纳入事实判断。
- 数量统计包含 OpenClaw 一个测试 fixture `AGENTS.md`，不把它视作真实治理范围。
- WuKongIM 当前故意移除了传统 `ci.yml`/`nightly.yml`；本文将 OpenClaw CI 只作为条件性设计参考，不把“没有常开 CI”直接判为缺陷。
