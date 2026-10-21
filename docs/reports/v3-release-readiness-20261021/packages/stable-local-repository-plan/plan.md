# stable 首代离线候选：实现计划（proposal，不是已实现能力）

基线：packages `fb62e03bb8ceaf300610becdc1f23ff41558c675`；此树已由 #71 合并为 `0cd625cbee214927a81b0a1015d825a6f53fbf28`（合并/CI 状态由 root 提供，本计划源码读取固定在 fb62e03）。本次仅读公开源码；未运行测试、签名或云操作。这里的“离线”指无需网络的仓库组合/复验，并不把用户批准的 Mac 在线保管称作离线密钥仪式。

**建议一个完整 PR 实现“stable 首代 add_release 的未发布候选闭包”**：现有 planner → prepare → 本地 family signer → compose → canonical archive → audit receipt → independent verifier。不是再增加一个仅测试的准备器。保持 checked-in stable disabled、preview 默认行为/字节及生产工作流不变。首代只接受一个严格正式版本；拒绝 base、历史 payload、rotation、update_bootstrap 和 retirement。TEST ONLY 原生演练是这个生产能力的验证方式。

## 已有能力与确切缺口

| 层 | 已有能力 / stable 拒绝点（固定源码行） |
|---|---|
| 源输入 | [release-contract:96](https://github.com/WuKongIM/packages/blob/fb62e03bb8ceaf300610becdc1f23ff41558c675/docs/release-contract.md#L96) 已有 stable source preflight，真实 immutable source Release、七资产、main 祖先、attestation、native metadata 规则不得削弱。`verify-source-package-metadata.py:99` 的 v2 收据已有 channel 和 exact native versions。 |
| 公钥、bootstrap | [validate-control:522](https://github.com/WuKongIM/packages/blob/fb62e03bb8ceaf300610becdc1f23ff41558c675/scripts/validate-control.py#L522) 读取 stable public roles；完整 validator 校验四套证书及 full/16/8-hex 全局唯一。`build-repository-bootstrap-packages.py:655` 可生成独立 stable 两包，但输出明确为 unsigned preparation schema，不能直接冒充 signed bootstrap inventory。 |
| plan / prepare | [plan:134](https://github.com/WuKongIM/packages/blob/fb62e03bb8ceaf300610becdc1f23ff41558c675/scripts/plan-package-publication.py#L134) 的 package_versions 先要求 prerelease；`build_plan:352` 固定 preview。`prepare-package-site.py:628–650` 要求正整数 audit Release，读取 preview releases；`:476` 未接 stable bootstrap 调用；RPM 路径也固定 preview。 |
| unsigned builder / signer | 无需新增执行器。trusted builder@`ed7e67d8f781b4f92872e42116c11b304fe03b5c` 已接受 `--apt-suite stable --rpm-channel stable`，生成 APT by-hash 及 simple-named RPM metadata；文件 pin 在 `manifests/trusted-toolchain.json`。 [signer:827](https://github.com/WuKongIM/packages/blob/fb62e03bb8ceaf300610becdc1f23ff41558c675/scripts/sign-package-family.py#L827) 已有文件输入、临时 home、精确 current key 证明、APT 双签名、先签 RPM 再建 metadata/签 repomd、原子输出及 family receipt。 |
| compose | [compose:343](https://github.com/WuKongIM/packages/blob/fb62e03bb8ceaf300610becdc1f23ff41558c675/scripts/compose-package-site.py#L343) 固定 preview 控制；`:419` 固定 preview Environment/secret 名；`:605` plan 正整数 audit ID；`:657/716/744` 路径/包名/bootstrap schema；`:1415/1703` 两树闭包。`:2032–2079` 生成 preview snapshot、ready status 和硬编码 `TEST_ONLY=false`，不能只把路径换 stable。 |
| audit | [audit:339](https://github.com/WuKongIM/packages/blob/fb62e03bb8ceaf300610becdc1f23ff41558c675/scripts/package-audit-receipt.py#L339) 固定 preview，`:787/814/891` family 路径和 key path，`:1114`/`:1371` 要求 preview-r数字文件名。`:1037` audit 文件白名单闭合。canonical tar helper 本身可复用，不需复制/另造归档器。 |
| 独立验签 | [verify:493](https://github.com/WuKongIM/packages/blob/fb62e03bb8ceaf300610becdc1f23ff41558c675/scripts/verify-production-package-site.py#L493) 控制/快照固定 preview；`:939` Suite/Codename，`:995/1213` APT/RPM 路径；`:1409` 完整站点闭包及 TEST_ONLY=false。OpenPGP/RPM 单 key DB 的实际 signer 验证核心可复用，不能用 signer 自报 receipt 代替。 |

`apt_package_version`/`rpm_package_version` 的替换本身对 `X.Y.Z` 已是恒等；需要修改的是 prerelease-only 输入约束，不应另造稳定版本转换规则。现有 signer/archiver、public-role validator、source preflight 尽量保持不变。

## 一个 PR 的明确实现范围

1. **候选控制和身份，扩展已有协议而非交换系统。** 给现有 plan/prepare/compose/audit/verify CLI 增加明确 stable candidate 入口；推荐 `--channel stable --offline-candidate`（preview 默认为原接口，二者不是发布授权）。版本化扩展现有 plan/snapshot/receipt 类型，旧 preview v1/v3/v2 严格分支保持原样。新分支共同绑定 `channel=stable`、`stage=offline_candidate`、`published=false`、`sealed=false`、`test_only`、control 原始 bytes 的 SHA/size、工具源码/control commit、source 包及公钥身份。字段精确、拒绝 unknown/duplicate/bool-as-int。

   `audit_release_id`、`base_audit_release_id`、候选的 `package_release_id` 必须为 null；无外部封存事实就不接受非空值。候选文件采用稳定版本 + snapshot SHA 名字，例如 `wukongim-stable-<version>-<snapshot_sha256>-candidate-site.tar`，不能伪造 `r123`。这是必要的语义区分，不能直接放宽旧 schema 的正整数约束。已有生产 publication / replay / transport consumers 必须拒绝这类 unsealed candidate；不要让任何默认或 fallback 自动选择新分支。

2. **真实 source 和 TEST ONLY 输入的边界。** 生产候选计划从已完成 stable source preflight 的原始 source resolution、七资产、八文件 attestation 证据及 metadata v2 收据导出唯一正式版本；原字节、asset ID/SHA/size、source SHA/tag/channel、工具 pin 全部闭合。归档保留这些公开证据；原 audit 白名单明确扩展，不改成任意 audit/*。

   本地 hash/receipt 一致性不是 GitHub provenance 认证。输出明确：`source_preflight_evidence_bound=true`（适用时），`github_provenance_reverified=false`、`github_immutable_audit_verified=false`、`public_deployment_verified=false`；未来发布端必须重新执行原真实 source/provenance/immutable 检查。不能在 CLI 添加 skip-source/skip-signatures。

   原生 CI 用 scratch TEST ONLY 控制根和两套独立 TEST ONLY key，source kind 明确 `test_fixture`，source Release/asset ID 和 source commit 若无事实则 null，provenance=false，test_only=true。测试入口生成 tiny data-only 包并调用同一组合/签验核心；若需要让候选命令读这种 fixture，必须是显式 nondeployable candidate 分支，production source 输入分支仍完全拒绝它。不要制造声称真实的 GitHub attestation、commit 或 audit ID。scratch 修改的 control 用实际文件 digest 表示，不声称它等于 checked-in control tree。现有预检 mock 测试只能证明拒绝逻辑，不能充当原生 provenance 证据。

3. **prepare 接通既有 stable bootstrap。** 复用受 pin 的 builder 和 #71 builder 的 `--channel stable --control-root`，验证两包与注册 cert 的 exact bytes/拓扑一致；组装 product+bootstrap 两包各自的完整 APT/RPM 输入及三份 RPM allowlist。APT 原包不变；RPM unsigned source digest 和 signed published digest 必须分开。不能把 preparation 的 published:false 简单改 true，当作已签。

4. **compose + 独立 verify 同时支持 stable 布局。** 用明确固定的 stable layout（APT dists/stable；RPM stable/el/9/x86_64；stable cert 和独立 bootstrap 名）参数化现有路径/闭包检查，不复制两套验签引擎。Reuse `STABLE_LAYOUT` 的 bootstrap 内容检查。必须验证 APT Release/InRelease/detached 同文及 SHA256、Packages/gzip/by-hash/所有包完整闭包；RPM actual current signer、signed RPM bytes→全部 repodata compressed/open hash→repomd SHA256 签名闭包。首代不接受 historical signer。bootstrap convenience download 必须等于仓库内已签 bytes。`/repo` 本地候选生成 stable 两包对应脚本并独立重建 byte-compare；当前 preview `/repo` 不改。

   status/index/signing manifest 标明 local candidate/unpublished 和真实 test_only；不能延续现有硬写 ready/TEST_ONLY=false。这些文件也要被独立 verifier 精确校验，不能作为自报成功信任根。归档及候选生成应保持 exclusive/atomic 输出，失败不留下看似成功收据。

5. **archive / audit / replay 闭合。** 复用 `archive-package-snapshot.py` canonical tar 安全实现；候选 audit 从独立外部 control/source anchors、完整 signed site、family receipts 重建，而非只 hash 嵌入 JSON。要求源码/控制/源证据/签名收据/归档 site exact closure，一处改动失配即拒绝。audit receipt 证明的是重建一致性；独立 verifier 另外执行实际签名与 native package identity 检查。解包后再复验同一候选，是这次可交付的本地恢复路径。

**文件范围（预计）**：主要修改现有 `plan-package-publication.py`、`prepare-package-site.py`、`compose-package-site.py`、`package-audit-receipt.py`、`verify-production-package-site.py` 及对应测试；如共用 layout/identity 少量纯函数能避免上述五处漂移，可集中在现有 composer 的明确窄 helper，不引入通用 publisher 框架。新增一个 process-level `tests/native_stable_candidate_drill.py` 或扩展既有 native drill；existing TEST ONLY workflow 增加调用/严格 receipt 断言和 workflow contract 测试。同步 README / release-contract。**不改** production publish/replay workflow、channels、preview signing、source/audit permissions、live site、私钥读取方式或 transport planner；若 shipping consumers 需要防回退断言，仅加拒绝测试。

## 必须先写的失败库存 / 验收矩阵

| 类别 | 先红案例 | 必须保留的断言 |
|---|---|---|
| stage / channel | 缺显式 candidate flag；stable 已 enabled；preview 被喂 stable inventory；stable 被喂 preview signing/control；candidate 流入旧 publication/audit replay/transport consumer | fail closed，无候选输出/无发布/preview 默认字节不变；unsealed 永不 publishable |
| 首代界限 | base ID/root 非空、两版本、retained/removed/historical payload、rotation/update_bootstrap/retirement、prerelease/build metadata、audit ID 伪非空 | 明确 unsupported，不能静默只处理第一项 |
| source / control | target/channel/nativeversion/architecture/name 不符；source 或 control digest/size 错；证据缺项/多项；metadata 与七资产不一致；验证后替换控制/公钥/源文件；将 test fixture 升为生产输入 | 拒绝且无输出；自报 booleans/自报 receipt hash 不能赋予 provenance |
| 输入路径/资源 | traversal、父目录或叶 symlink、hardlink、重复 JSON/path、input/output 交叠、已有 output；真实大小超限而声明很小；gzip/XML 扩张、重复 metadata type、escaping href | 在无界读取/外部工具前拒绝适用错误；保持现有预算及 safe copy，不扩大 750MiB/64MiB 限额 |
| APT | Release headers 不是 stable；错 current/next/preview family key；InRelease cleartext/detached 不同；改 DEB / keyring / source / Packages.gz / by-hash；缺 hash object 或多未引用文件 | actual gpgv/状态证据 + exact content/closure 拒绝，不能以 receipt signer 字符串通过 |
| RPM | unsigned/错 current/next/preview-family RPM；签名使用非 SHA256；metadata 仍引用 unsigned bytes；open/compressed digest/size 错；primary/filelists/other 漏项/重项/未引用项 | 单 key RPM DB 实际验签；RSA 约束保持；plain filelists.xml.gz 等合法布局能过（不要重犯 digest-only 兼容问题） |
| bootstrap / entrypoint | stable 包变成 preview 包名/拥有路径；证书/source repo 不同；脚本或 product 混入 bootstrap；download 与 signed repo bytes 不等；/repo 下载 digest/包名错 | 真实 DEB/RPM 内部内容及 exact script 字节检查，无安装/签名 bypass |
| audit / recovery | archive+嵌入 snapshot/receipt 联改但外部 anchor 不改；source signer receipt 互换；seal/test_only 状态篡改；archive 漏/多文件/非 canonical tar；输出中途失败 | 重建拒绝；恢复后完整 verifier 重新执行；测试私钥删除后仍能独立复验 |

验收必须包含：先红记录；相关现有 Python/contract 全绿；固定 Linux image 内真实 tiny stable DEB/RPM + stable bootstrap 经既有 family signer→compose→archive→audit create/verify→extract→独立验签成功；阴性变异实际拒绝；清理 TEST ONLY 私钥后再验一次；默认 preview 路径、受保护控制/生产 workflow 的 digest 回归。只上传小型公开 JSON/命令/身份/耗时/失败摘要，永不上传 TEST ONLY 私钥或生产材料。不得把 fake RPM 单测当原生验证。若 signer 产生 timestamp，不能要求两次独立签名后的归档字节相同；要求同一冻结 signed input 的 canonical archive/replay 可重复。

## 完成后仍明确未完成的边界

本 PR 关闭的是 stable 本地生产候选构建/签验/归档复验能力，实际执行仅 TEST ONLY。无生产私钥或实际 stable 签包、无 GitHub audit seal、无上传/删除/激活、无公网安装。以后候选成为发布输入必须新增严格 sealing/binding 阶段，绑定真实 audit Release 到 exact candidate archive digest，并重新验证 source 与签名；现有消费者本 PR 仍拒绝候选。

stable bootstrap 固定 mirrorlist URL 已存在配置，但其返回的 immutable generation URL/对象保留/缓存/切代是下一 OSS/CDN 层。当前不可发明一个不存在的 mirrorlist 内容或将 fixed baseurl 回退塞入 bootstrap，亦不可声称整站原子切换。候选内 RPM 仓库闭包是完整的；公开路由依然未就绪。真实 APT/DNF 公网 exact-version 验收、迁移 A/B/cache 演练和已确定的长稳工作仍保持各自原范围。本计划不新增额外 GA 门禁。
