# 2026-10-21 原生包发布阻塞复核

结论：新产品验收不会自动解除原生包交付阻塞。stable 当前仍未具备正式发布条件；这是一项需要实现、外部预置和精确版本验收的工作，不能只切开关。

## 当天重新确认的事实

- `WuKongIM/packages@main` 仍为 `feb7f21503d35133cf23df7d7ab250c5d4129e71`。本次重新下载的 release-contract 和九个关键控制文件与 10 月 20 日字节一致。
- 北京时间 2026-10-21 12:35，公网 `https://packages.githubim.com/status.json` 返回 HTTP 200，目标为 `3.0.0-beta.24`，audit `402342684`，control SHA 为上述 main。
- 数字 audit Release 已发布且 immutable；审计标签仍指向该精确 control SHA；下载的 receipt SHA-256 与 GitHub asset digest 匹配，其版本、control、audit ID 和 snapshot 摘要均与公网 status 一致。
- 这次已解除昨天“本机 TLS 导致无法重读公网 status”的证据限制。但没有重跑四个客户端，也没有重新下载并密码学校验 308 MB 全量快照。
- preview 仍保留 beta.21—beta.24 四个 active 版本，达到 `max_online_versions=4`。已审计站点约 294 MiB，未达到 600 MiB warning 或 750 MiB 硬阈值；RC 的直接限制是版本数量。
- preview APT/RPM 当前公开签名子钥匙均于 2026-12-30 UTC 到期，约剩 70 天；尚不触及 45 天轮转或 30 天发布最低有效期。只核查公开证书，没有读取私钥，也没有证明秘密保管/恢复可用性。
- 仓库变量为空；Environment 仍只有 preview/source/toolchain/Pages；现有审计标签 rulesets 仅覆盖 `native-package-preview-r*`。未发现 stable 存储或签名预置证据；不能据此断言外部账户绝不存在相关资源。

## 当前真实阻塞

| 阻塞 | 必须补齐 | 完成证据 |
| --- | --- | --- |
| 正式版本被当前控制拒绝 | 频道模型、SemVer 分类、source preflight、来源证明、planner/composer/audit 与 publisher 全链路支持 stable | 正式版/prerelease 分类正反例，历史 preview 审计重放，失败关闭测试 |
| stable 不能发布在 Pages | 对象存储/CDN 的完整快照、不可变路径、切换、缓存闭包和回滚 | 候选存储演练、当前 preview 迁移验证、公网精确快照回执 |
| stable 签名尚无可用协议 | 独立 APT/RPM 信任根和离线或硬件签名；hosted CI 只消费可复核的公开结果 | 公开指纹、待签摘要、实际签名者与包/metadata 闭包验证、恢复演练 |
| 安装链路固定 preview | stable bootstrap、显式频道行为、来源/证书路径、公开安装验证和 CLI 验收 | 四发行版精确正式版本下载与 `installed_cli_verified=true` |
| 新 RC 的 preview 容量 | 按既有合同退休最老 beta.21 后再增加 RC | 两次受审阅发布；去索引后至少 30 分钟才删除 payload |

直接源码：

- [stable 当前状态](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/manifests/channels.json#L68-L72)；[validator 禁止 stable](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/validate-control.py#L357-L361)。
- [source resolver 拒绝正式 SemVer](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/resolve-source-release.py#L69-L83)；[证明验证器仅接受 prerelease](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/verify-source-attestations.py#L134-L146)。
- [composer 再次拒绝 stable](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/compose-package-site.py#L356-L362)；[publisher 部署 Pages](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/.github/workflows/native-package-publish.yml#L1540-L1584)。
- [stable 私钥不得进入 hosted CI](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/docs/release-contract.md#L322-L325)；[当前 signer 不能直接接受 token reference](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/validate-signing-material.py#L573-L581)。
- [preview 引导定义](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/build-repository-bootstrap-packages.py#L33-L66)；[版本上限和两阶段退休](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/docs/release-contract.md#L124-L165)。

## 可在本轮总任务授权内继续准备的具体修复

本子任务只做只读复核；以下为主任务可继续推进的准备工作，不表示已经实现或修改生产。

1. 在隔离包仓库副本冻结精确 main，准备“stable 契约与来源分类”变更，生产仍禁用；先列失败场景，再实现和验证，保留历史 preview 兼容。
2. 准备签名输入/输出接口及 TEST ONLY 黑盒演练。实际托管方式未定时，只实现公开输入与结果校验边界，不假装硬件签名可直接复用 preview 私钥导入器。
3. 准备频道引导兼容方案与测试：新用户显式 stable、已有 preview 不隐式迁移；为需要改变的 bootstrap 证书/源定义升版。
4. 为存储发布器准备 provider 中立的快照完整性、切换失败、旧缓存与回滚验收；具体对象存储条件更新能力需选定 provider 后验证。
5. 准备 RC 退休变更及发布操作清单，引用当前公网 audit 身份；执行时重新验证 live 状态并沿用已有 review/draft/bind/publish 顺序。

详细四阶段实施范围和完成标准可复用[昨天草案](/tmp/wukongim-v3-rc-20261020/stable-channel-draft.md)，本次事实更新以此报告为准。

## 明确需要维护者决定或外部配置

- 存储/CDN：账号、region、bucket、域名、预算和资源负责人。现有 Cloud Lease 角色不能视为包仓库发布授权；预置/采购/修改权限不属于本只读任务。
- 签名保管：离线或硬件方式、负责人、独立 APT/RPM 公共证书、备份及恢复流程。无需也不得向 agent 提交秘密材料。
- 新安装默认频道：`/repo` 是否默认 stable；应先完成明确的兼容设计，不能借 bootstrap 更新悄悄切换原 preview 用户。
- stable Environment/OIDC、审计标签 ruleset 覆盖、存储/CDN/TLS/DNS 必须由相应维护者按受审阅方案配置；不建议放松现有边界。

## 保护与交付约束

main admin enforcement 开启，必需检查为 `Validate complete Pages snapshot`，禁止 force-push 和删除；immutable Releases 开启。
API 显示 required approving review count 为 0，没有平台强制双人审批，不能虚构该要求；但 reviewed-control、精确 control SHA 与签名托管合同仍有效。
审计标签创建与不可变性是两条独立 active ruleset，创建 App 可绕过创建限制，更新/删除/非快进规则无 bypass。
不得在 draft/bind/publish 在途时推进控制 main；不得改绑既有审计 tag 或覆盖 immutable assets；失败只能按精确身份恢复。
正式 release 必须包含精确版本的签名 APT/RPM 公网客户端验收；产品测试、Docker 和 GitHub Release 不能替代该交付条件。

机器可核对结论见 [summary.json](/tmp/wukongim-v3-readiness-20261021/packages/summary.json)，原始 API、状态头、receipt、控制文件摘要与公开证书拓扑在同目录。
本次没有 dispatch、渠道启用、发布、权限变更、采购或秘密读取；仅向指定 `/tmp` 证据目录写入文件。
