# 外部 immutable audit seal → stable/composite transport：最小实施提案

基线 `c8aae37d13787a0aceceac751d6d35bbc1d89c35`（#72 冻结、工作树 clean；root 已报告真实 CI 通过，合并许可另行进行）。本提案不改 #72、不创建工作树、不执行任何网络 API、私钥/凭据读取或生产动作。待 root 审核此 scope 后再从已合并的精确 main 开实施分支。

## 推荐一个完整、可独立审阅的切片

**复用现有 bind/seal/resolve 的“两资产 immutable audit”状态机，加显式 stable 分支；把该分支的外部封存身份接到现有 transport planner 的双频道输入。** 一次交付离线 API 替身上的 `empty draft → bind → upload exact candidate pair → immutable seal → read-by-ID → verify candidate → composite plan`。不新增签名交换系统、不新增第三种远程资产、签名密钥或签名回执链。实际创建 draft、ruleset、生产 workflow 接线、OSS/CDN 上传/激活仍不在本次执行范围。

候选 archive 和原 v3 receipt 保持**原文件名与原 bytes**；Release 的真实数值 ID、reserved tag、两资产 ID/API SHA256 和下载 bytes 在 archive 外绑定它们。GitHub 的封存对象就是这个外层，不需要把 audit ID 填回 `snapshot.json`/plan，也不需要改已签 APT/RPM。保留 `native-package-stable-r<ID>` 与固定 audit 名称的独立命名空间；audit Release 仍可维持 `prerelease=true / make_latest=false`，这是审计容器的既有策略，不是把正式源版本改成 prerelease。产品来源必须仍是 stable 分类。

本地 `seal-resolution.json` 只是 reader 产生的**观察收据**；把字符串 `immutable:true` 写进文件不会产生 GitHub 信任。离线 planner 继续 `plan_only=true`、`github_provenance_verified=false`、`control_commit_verified=false`、`cloud_verified=false`、`activation_authorized=false`。后续生产 activator 在写前必须 fresh 调用数值 ID resolver/来源预检并比对同一封存身份，不能只接受离线 plan 或自报 seal。真实 writer 的生产接线也必须保留现有签验、来源/权限/控制 fencing 前置；本切片的 API 替身成功不是外部写入授权。

## 现有 engine 与最小变更文件

| 文件 / seam | 保留并复用 | 最小增加 |
| --- | --- | --- |
| `scripts/resolve-audit-release.py` | `GitHubReader`、按数值 asset ID 下载、API size/SHA 上限、redirect 去授权头、双读 Release/tag、canonical two-asset 状态分类 | 默认 preview 名称/输出不变；显式 `--channel stable` 接受由外部候选事实推导的固定 archive/receipt 名称及 digest，验证 stable reserved tag 与完整 immutable 两资产；返回 stable resolution v2 的候选身份绑定。禁止任意 URL/文件名白名单输入。 |
| `scripts/bind-audit-release.py` | exact empty draft、轻量 tag create-only、已有正确 tag 恢复、detached draft 分支、不能重绑不同 commit | 显式 stable 命名空间参数化；默认 preview 字节/调用不变。它仍只绑定空 draft，不创建 draft、不接收任意 tag。 |
| `scripts/seal-audit-release.py` | `_regular_artifact`、不覆盖已存在资产、archive-only 恢复、receipt-only 拒绝、发布前后 policy/main/tag fencing、下载后核对、一次 PATCH 和有限轮询 | stable 模式以原 candidate pair 为唯一允许资产；先调用 #72 已有 candidate audit/独立验证路径，确认 `test_only=false`、原外部 plan/control/source 和真实签名；输出 stable seal v2。保持所有远程行为可由现有 loopback API fixture 完整记录。不得提供“仅信 seal JSON”跳过验证的生产选项。 |
| `scripts/plan-object-storage-publication.py` | bounded descriptor/file reads、canonical extractor、每代完整验证、immutable URI 冲突拒绝、确定性排序、保留全集/no GC | v1 preview 原输入/输出保持；显式 v2 输入为 `current={preview:descriptor,stable:descriptor}` + `retain`，全局仍最多 8 snapshots / 2 GiB。stable descriptor 额外绑定外部 seal-resolution、原 candidate plan/receipt/control/source 和各原始 SHA；复用 #72 audit verify 与公共闭包 engine。输出 v2 composite inventory，不执行远端 I/O。 |
| 相关 tests + docs | 原 tests/public interfaces、既有 loopback HTTP server 和 tiny canonical fixtures | 新负例先红后绿；新组合 CLI E2E；README/contract/object-storage-planning/CHANGELOG 明确当前能力与未授权部分。 |

预计生产逻辑只改上述 **4 个 helper**；不为这个切片修改 #72 的 composer、package-audit-receipt、独立 verifier、signer、archiver、bootstrap builder。若复用时确需触及这些核心，应先给 root 具体 diff 原因，不能为了方便自动扩大。候选所绑定的 producer 清单恰好不包含这四个新改 helper，因此旧候选的核心 producer bytes 可维持；仍须逐项核验，不能伪填新 producer hash。需要运行历史 producer 时用受审精确 checkout/固定工具链，不能从 archive/control 数据目录动态导入代码。

### 身份与 CLI 的最窄形状

- legacy 命令、schema 和 preview defaults 不变；新模式只由显式 `--channel stable` 进入，不按 schema 猜频道。复用 #72 已有 `--plan`、`--control-root`、`--candidate-source-root` 及 audit 输入参数来验证候选；不另建一层 signing request JSON。`--test-only` 不能让真实 remote writer 接受测试产物；API 替身测试的合成 IDs/密钥只存在 loopback fixture，输出标明 TEST ONLY，绝不伪装真实来源。
- stable names 由 strict `version + snapshot_sha256` 经过现有 `candidate_archive_names` 推导；不接受 caller 任意下载/上传名。允许数据字段是固定 reviewed facts，不是脚本、命令或 URL。
- **两个 control 角色分开：**现有 `expected_control_sha` 仍是 reserved audit tag/当前封存控制 SHA；candidate `control_sha` 是生成该 archive 的历史声明。必须保留各自原 bytes/hash、由受审历史 checkout/后续 fresh control 验证分别核实；不能强行把 candidate 改成当前封存 SHA。第一实际发布若二者恰好相同不需要特殊分支。来源 Release/tag/commit 与这两个控制身份也不可混用。
- resolution v2 / seal v2 只增加必要的 `channel` 与 candidate tuple（原 archive/receipt/snapshot/plan digest、candidate control/source 身份）。真实 release/asset IDs 仅来自对应 API；外层 seal 不可依赖“稍后填 ID”。离线 fixture 使用显式 TEST ONLY 的假 API，不产生任何可用于真实激活的凭据或成功声明。
- composite input v2 需要 current preview 和 current stable 各一份；首版不允许漏掉 preview 后“顺便”覆盖现有域名。legacy v1 preview-only remains unchanged。所有 retained 代先独立验证，再合并依赖，不因已见相同 digest 而跳过验证。
- TEST ONLY 端到端若需新 opt-in，只允许 writer/resolver 的 API 与 download 两个 origin 都是 loopback，输出必须携带 `test_only=true`；真实 host 必须在任何请求前拒绝。离线 planner 的 TEST ONLY v2 opt-in 同样强制输出测试标记，正常 v2 拒绝它。不能把 #72 的 null source IDs 换成伪“生产来源”来凑成功案例；后续 activator 一律拒绝测试标记。

## Composite 路由必须显式，不做 site tree union

| 公共/对象范围 | 首版策略 |
| --- | --- |
| `snapshots/<channel>/r<ID>-<snapshot_sha>/site|audit/...` | 每代逐对象保留原 archive 成员 bytes；内部 UNPUBLISHED/stage/null ID 不改。 |
| 现有根 `/repo`、`/index.html`、`/status.json`、`/signing-manifest.txt`、`/bootstrap/manifest.json` | 继续由 current preview 唯一拥有，exact bytes 不变。stable 同名成员仅保留在 snapshot，不覆盖这些公共根。 |
| stable APT 入口、keys、版本化 product/bootstrap 下载、by-hash | 按原 canonical 路径映射；跨两频道/所有代的同 URI 不同 bytes 一律失败，不以排序决定 winner。保留旧 by-hash 与 payload 全集。 |
| `/rpm/stable/el/9/x86_64/mirrorlist` | transport 自己生成唯一 `HTTPS origin/snapshots/stable/r<ID>-<sha>/site/rpm/stable/el/9/x86_64/` 一行正文；绑定生成 bytes/size/SHA 与 no-store，origin 固定 reviewed `packages.githubim.com`。不是 archive 成员，也不修改 signed repomd。 |
| stable RPM repomd / `.asc` / plain metadata | 正常客户端沿 mirrorlist 的完整 snapshot base URL 访问；不再暴露会换代的 stable floating metadata 作为“无缝”路径。物理快照可沿现有保守 cache 分类；不能凭 `.gz` 后缀给永久 alias。 |
| stable 专属安装脚本/人类状态页 | 本切片不自动发布 `/repo-stable`，也不把 candidate UNPUBLISHED `/repo` 文本改为 ready。后续四客户端/公网入口切片如需要显式 installer，再产生**另一个有独立 transport digest 的派生入口**并审其路径/提示，不能静默改 archive。stable bootstrap 固定下载与原配置足以验证当前运输闭包。 |

`mirrorlist` 仅有 source tuple 和依赖，不把整个 plan 的 digest 写回其自身，避免内容自引用。所有 generated objects 也必须进入同一 upload inventory/保留 proof；planner 不下载镜像、访问网络或激活。当前 v2 范围仍是各 snapshot 的 add_release；不引入 stable rotation/update_bootstrap/retirement/删除。

## 测试 seam、红绿次序与验收

只测现有公开 CLI 与 loopback HTTP API seam。先列失败库存，再逐个案例 red → minimal green；不用内部 helper mock 代替封存调用链。推荐顺序：stable numeric resolver拒绝/下载 → binder/未完成draft恢复 → writer完整状态机 → stable descriptor拒绝 → composite 冲突/retention → 一条端到端 API 替身→候选验签→plan。具体库存见 `failure-matrix.md`。

交付必须同时给出：固定 source/rules SHA、preview 原回归不变、每次先红后绿日志、完整 loopback 请求 method/path transcript（token脱敏）、精确候选 archive/receipt/hash 在封存前后相等、确定性 plan digest、无 Delete/rename/overwrite 请求证明、失败不留新输出/不移除既有文件、未改 protected 资源的逐项证明。需要原生签名的最后一条 TEST ONLY E2E 复用 #72 Linux toolchain，不把 mock RPM 当真实密码学通过。

## 本切片保持的受保护范围

- `manifests/*.json`、四份 `keys/*.asc`、现有 preview/live `site/`、所有生产 `.github/workflows/`、rulesets、GitHub Apps/Environments/权限、OSS/CDN/DNS均不改。stable 继续 disabled/object_storage_required。
- README/release-contract 的 reviewed control、immutable policy、create-only reserved tag、防覆写、source/control fencing 与四客户端/installed CLI 要求继续有效。没有任何本地 seal JSON 能替代这些规则。
- root AGENTS 的 test-first、适用 FLOW/AGENTS 摘要冻结、用户可见 CHANGELOG、必要相关测试、工作流调用先读 README 等适用。packages checkout 没有自己的 AGENTS/FLOW；未调用工作流。根产品 Issue/Review Agent 控制文件保护也不会被这个 packages 切片触及。
- 根任务已明确要求先审此 scope 再开实施 worktree。本阶段没有代码、测试、loopback服务、云/外部API操作；不是新的发布批准或生产资源审批清单。

## 固定源码依据

- [scripts/resolve-audit-release.py:48](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/resolve-audit-release.py#L48) — 现有 preview 两资产名；:218 固定 reserved tag，:445 数值下载及双读
- [scripts/bind-audit-release.py:37](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/bind-audit-release.py#L37) — 固定 preview 命名；:256 绑定状态机
- [scripts/seal-audit-release.py:335](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/seal-audit-release.py#L335) — 两资产 fill/seal/恢复及 policy/main/tag fencing
- [scripts/plan-object-storage-publication.py:187](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/plan-object-storage-publication.py#L187) — 外部 anchors/旧代完整验证；:273 legacy preview-only planner
- [scripts/package-audit-receipt.py:1079](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/package-audit-receipt.py#L1079) — candidate 两文件内容命名；:1089 外部锚与实际验签
- [scripts/compose-package-site.py:1890](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/compose-package-site.py#L1890) — candidate producer 文件清单；不包含 bind/seal/resolve/transport
- [docs/release-contract.md:481](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/docs/release-contract.md#L481) — 现有 immutable 两控制状态机；:597 候选未封存契约
- [docs/object-storage-planning.md:20](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/docs/object-storage-planning.md#L20) — 当前离线身份声称、大小上限与 plan-only 信任边界
