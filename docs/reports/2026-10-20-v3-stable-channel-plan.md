# v3 RC：stable 原生包渠道实施草案

状态：供审阅；2026-10-20 只读核对完成，尚未实施、预置资源或发布。
结论：stable 需要补齐发布链路，不能仅修改 `enabled` 或存储配置。

## 固定基线与核对范围

- 产品候选：`WuKongIM/WuKongIM@b469c6e5e2b4ff65611a8fe3389fb5034de3f152`。
- 包仓库当前 main：`WuKongIM/packages@feb7f21503d35133cf23df7d7ab250c5d4129e71`；已用 GitHub API 再核对。
- 所有下列源码链接均固定到该包仓库控制 SHA，后续实施须重新读取当前控制与审计身份。
- 当前 stable 为 `enabled=false`、`status=object_storage_required`、空 releases。[频道控制](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/manifests/channels.json#L68-L72)
- 该控制的历史 publisher [37102109407](https://github.com/WuKongIM/packages/actions/runs/37102109407) 成功；不是本次公网复验。
- 本次直接读取公网 `status.json` 遇本机 TLS `SSL_ERROR_SYSCALL`，因此没有重新确认 live snapshot。
- GitHub API 确认 Pages 自定义域名为 `packages.githubim.com`，HTTPS enforcement 开启，build type 为 workflow。

## 已证实的实施缺口

| 环节 | 当前限制 | 必需的改变 |
| --- | --- | --- |
| 控制模型 | validator 与 composer 强制 stable 禁用 | 版本化频道、发布计划、快照及审计 receipt 契约，保持未知字段与重复键拒绝 |
| 来源预检 | resolver 和 attestation verifier 只接受 prerelease | 按频道校验严格 SemVer 与 GitHub prerelease 标记，保留 immutable/祖先关系/七资产/证明闭包 |
| 签名 | 只有 preview APT/RPM，现 signer 要求加密本地子钥匙 | stable 独立证书、离线或硬件签名流程及结果验证，stable 私钥不得进入 hosted CI |
| 审计身份 | draft/bind/seal 和审计标签/资产名固定 preview | stable 身份、规则覆盖及可恢复状态机，不得改绑或覆盖既有标签与 Release |
| 发布承载 | 整站 Pages artifact/deploy | 对象存储/CDN 的完整快照部署、切换、缓存闭包和可验证回滚 |
| 安装引导 | 源定义、key path、bootstrap、`/repo` 固定 preview | stable 专用信任根及显式频道行为，保留既有 preview 客户端兼容 |
| 客户端 | 公网与 CLI 验证器固定 preview | stable 精确版本验证，并回归仍受支持的 preview 客户端 |

直接证据：

- [控制 validator 的 stable 禁令](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/validate-control.py#L357-L361)；[composer 的独立禁令](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/compose-package-site.py#L356-L362)。
- [source resolver](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/resolve-source-release.py#L69-L83)；[来源证明 verifier](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/verify-source-attestations.py#L134-L146)。
- [stable 密钥托管要求](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/docs/release-contract.md#L322-L325)；[当前 signer 拒绝 token reference](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/validate-signing-material.py#L573-L581)。
- [bootstrap 固定源定义](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/build-repository-bootstrap-packages.py#L33-L66)；[现有 Pages 部署入口](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/.github/workflows/native-package-publish.yml#L1540-L1584)。
- [审计标签身份](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/bind-audit-release.py#L36-L41)；[审计标签保护合同](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/docs/release-contract.md#L361-L386)。

## PR 1：频道与来源控制，生产 stable 继续禁用

范围：channels/schema、来源 resolver/attestation、planner、prepare、composer、audit receipt、audit resolver/binder/sealer。
把频道作为受审阅控制的一部分，不能由 dispatch 或仓库变量绕过；更新相关合同与失败案例。
保留现有 preview 审计重放兼容，stable 发布计划绑定版本、source SHA、数字 source/audit ID 与原始 DEB/RPM 摘要。
保留原生 DEB/RPM 版本比较，拒绝降级、重复身份、错误频道和正式版标记冲突。
先记录失败场景并补测试，再实现代码；静态契约测试可用隔离 fixture，完整链路以黑盒演练为主。
完成条件：`tests/contract.sh` 与受影响测试通过，历史 preview fixture/审计可重放，稳定版来源可正确分类但不能生产发布。

## PR 2：stable 签名协议及安装引导

范围：stable 公开证书/签名 manifest、待签包与签后回执协议、独立结果校验、bootstrap builder、`/repo`、客户端验证器。
选择离线或硬件签名后，提交可复验的输入摘要、来源身份和 APT/RPM 分离结果；hosted CI 不接触 stable 私钥。
APT 验证同一 Release 的 InRelease/Release.gpg；RPM 验证包签名、签后重新生成的 metadata 与 repomd.xml 签名。
RPM 保留 RSA3072/4096 与 SHA-256 约束；验签必须确认实际签名者，不能仅信 issuer 字段。
stable/preview 和 APT/RPM 使用独立信任根；新证书、轮转、失效/吊销和恢复流程均进入审阅。
引导包一旦更改证书或源定义必须升版；明确已有 preview 用户升级后仍处于原频道。
测试：错误、过期、吊销、跨频道/家族钥匙，错误摘要/回执，截断引导脚本及两次安装幂等。
完成条件：TEST ONLY 钥匙的双频道端到端演练和四客户端通过，签名边界无私钥日志/产物，生产仍禁用。

## PR 3：对象存储/CDN 部署与 preview 平滑迁移

范围：受审阅存储 manifest、独立发布 Environment/OIDC、完整快照上传器、切换器、公网 verifier、回滚 runbook。
先将当前已审计 preview 完整字节迁至候选存储，保留原证书、bootstrap、payload、签名和历史审计身份。
使用不可变快照路径；部署前校验完整闭包；设计并实测旧缓存命中时仍有对应 metadata/payload。
具体 provider 能否支持条件切换须验证，不能把多个覆盖式对象上传称为原子发布。
按读/写/刷新/证书管理职责限定角色，固定 bucket/prefix/domain，不让源代码或 dispatch 选择任意资源。
DNS/TLS 切换前保留旧路由、证书与回滚快照；成功读源、CDN 与公网客户端后才宣布迁移完成。
测试：部分上传、摘要冲突、陈旧控制、签名校验失败、切换中断、缓存混合、域名传播和恢复旧快照。
完成条件：当前 preview 四客户端持续可用，故障注入未暴露缺失闭包，回滚有可重放回执，stable 仍禁用。

## PR 4：stable 生产预置、启用和精确版本验收

范围：已验证的 stable 公共证书/指纹、外部签名流程、storage/CDN 身份、Environment、审计标签规则和启用变更。
外部资源/权限/签名保管由明确负责人预置；只有公开配置、指纹和非秘密验收回执进入代码审阅。
扩展现有 create-only 与 immutability 两条独立标签保护，不给发布 App 更新/删除审计标签的绕过权限。
正式版流程：source immutable Release → preflight → 双家族签名 → 完整快照验证 → immutable 审计 → 存储/CDN 发布 → 公网四客户端。
版本必须是精确正式 tag，GitHub `prerelease=false`，对应 source SHA 和 package Release ID；不可通过 RC 包重命名代替。
每个客户端必须选择并下载目标版本，再由隔离无凭据容器安装校验 server/CLI 身份及功能。
完成条件：全部客户端 `installed_cli_verified=true`；记录版本/SHA、源与审计 Release、发布 run、快照摘要及公网回执。
任何一项失败保持“正式发布未完成”，不得以 Docker、GitHub Release 或 CDN 已部署替代。

## 公共验收矩阵与保留证据

- 发行版：Ubuntu 24.04、Debian 13、Rocky Linux 9、AlmaLinux 9；包仓库当前固定镜像见[客户端矩阵](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/scripts/validate-production-package-clients.py#L117-L138)。
- 源 preview CI 的 Debian 12 生命周期检查保留，但不能替代上述包仓库公网 Debian 13 检查。
- APT 必须证明 Release → Packages → DEB；RPM 必须证明 repomd → primary metadata → signed RPM。
- 安装后的 `wukongim`、`wkcli` version/commit/build_source 都等于快照目标，不能两者一致但都错误。
- CLI 验证包括 bench validate、固定离线用户查询、migrate diagnose、无效输入失败及 fixture 字节不变。[既有合同](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/docs/release-contract.md#L442-L464)
- 保留精确控制 SHA、工具链 digest、公开证书拓扑、来源证明、签名回执、快照摘要、四客户端功能回执和回滚记录。

## RC 发布前可独立处理的 preview 容量要求

当前四个 active 版本为 beta.21、beta.22、beta.23、beta.24，`max_online_versions=4`。[当前列表](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/manifests/channels.json#L6-L55)
因此不能直接追加 rc.1 为第五版；先验证 live status 与其 immutable audit，再选择最老 beta.21 退休。
退休必须两阶段：先 `remove_indexes`，保留 payload 并设置至少 30 分钟后的绝对 `not_before`；另次 `remove_payloads`。
两阶段均须经当前 draft/control/bind/publish 合同，不改写旧 Release，不用延长 workflow sleep 代替第二次运行。
容量释放后 RC 仍发布到 preview，独立于 stable 实施。[保留与退休合同](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/docs/release-contract.md#L124-L165)

## 需要明确的决定与受保护边界

1. 存储/CDN：云账号、region、bucket、域名、预算和资源负责人；建议评估现有阿里云，但不能把 Cloud Lease 角色视为包存储权限。
2. 签名：离线或硬件方式，以及 stable 密钥保管/签名负责人；只提供公开证书、指纹和流程证据，不传私钥给 agent。
3. 安装入口：是否将新用户 `/repo` 默认设为 stable；建议先明确频道选择，现有 preview 安装不得隐式切换。

只读 API 当前显示 packages 仓库变量为空，Environment 仅有 preview/source/toolchain/Pages，未见 stable 存储或 signer 预置证据。
受保护变更包括 manifests、keys、发布/签名/审计代码、workflows、toolchain pins、Environment/App 权限、标签 rulesets、DNS/TLS 与存储资源。
main 开启 admin enforcement，要求 `Validate complete Pages snapshot`；当前 API 的 required approving review count 为 0，不能误称平台强制双人审批。
上述平台事实不撤销 reviewed-control、精确 SHA、不可变身份和签名保管合同；本草案不修改或放宽这些边界。
实施前读取新的控制 SHA 与运行状态；draft/bind/publish 在途时不得推进包仓库 main，重试只能恢复精确匹配身份。
本次包渠道调研仅下载只读代码到临时目录并编写此草案；未在包仓库创建 issue/PR、dispatch workflow、购买资源、签名或发布。
