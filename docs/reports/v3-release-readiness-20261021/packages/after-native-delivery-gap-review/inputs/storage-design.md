# Stable 原生包的 OSS/CDN 接入设计（只读审阅稿）

记录日期：2026-10-21。源码边界：`WuKongIM/packages@0ce7184bbf69472f607159f74a096e9160852843`；其生产控制基线为 `feb7f21503d35133cf23df7d7ab250c5d4129e71`。本稿未修改代码、云资源、DNS、权限或发布状态；不是部署批准或生产验收。官方资料检索、源码摘要见同目录 JSON。

**可实施的最小方案是：一个专用私有 OSS bucket、现有公开域名 `packages.githubim.com` 的 CDN 接入、不可覆盖的快照命名空间、有限入口路由，以及保持签名校验的原生客户端验收。不能把 OSS 或 CDN 描述为整站原子发布系统。** 建议先独立完成无凭证的运输计划器及客户端切换演练；它们不需要等待云资源或 stable 私钥。此处是现有“签名原生包公开交付”要求的实现设计，不增加 WuKongIM 产品的 GA 指标。

## 1. 现状与可复用边界

生产 preview 已存在：本次任务已读取的公开状态为 `3.0.0-beta.24`，audit Release `402342684`，control `feb7f215…`，snapshot SHA-256 `242af0efec792b089661a5411e0b57b954998432cf13df9c657fb2d823851be8`。其 canonical tar 为 308,536,832 bytes，API/receipt 声明 digest `71eb2b1b338742b9669ebfc7135ede8a12f73f395b3171e6375f18240d5c3a07`。这是父任务已有 API/receipt 观察，**本子任务没有重新下载整包或重做全量密码学验签**。不能把 tar digest 与 snapshot JSON digest 混用。

当前 canonical archive 的逻辑布局是：

```text
site/
  repo, index.html, status.json
  keys/apt-preview.asc, keys/rpm-preview.asc
  bootstrap/manifest.json, <versioned-bootstrap.deb|rpm>
  apt/dists/preview/{InRelease,Release,Release.gpg}
  apt/dists/preview/main/binary-amd64/{Packages,Packages.gz}
  apt/dists/preview/main/binary-amd64/by-hash/SHA256/<digest>
  apt/pool/main/w/wukongim/<versioned-product-or-bootstrap.deb>
  rpm/preview/el/9/x86_64/repodata/{repomd.xml,repomd.xml.asc,<referenced-metadata>}
  rpm/preview/el/9/x86_64/Packages/<versioned-product-or-bootstrap.rpm>
audit/
  snapshot.json, plan.json, apt-signing.json, rpm-signing.json
  signing-toolchain.json, source-attestations/<exact eight-file set>
```

布局依据是 composer 的 [APT 文件闭包](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/scripts/compose-package-site.py#L1415)、[RPM 元数据引用闭包](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/scripts/compose-package-site.py#L1629)、[site/audit 组装](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/scripts/compose-package-site.py#L1920)，以及 publisher 的签名 receipt/plan 传递。该列表不是本次 tar 成员清单的替代品；实现前仍由现有 archive validator 枚举实际成员。

已有可保留能力：独立来源核对、离线完整快照验签、精确版本与依赖闭包、不可变 GitHub audit Release、四个公开客户端及 installed CLI 验收。现有代码仍是 preview/Pages 专用：[composer 明确禁止 stable](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/scripts/compose-package-site.py#L343)，[bootstrap 固定 preview 路径](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/scripts/build-repository-bootstrap-packages.py#L34)，[public verifier 固定 preview 公钥](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/scripts/validate-production-package-clients.py#L1153)。不能只把部署动作改成 `ossutil sync` 就宣称 stable 完成。

## 2. 原子性边界

阿里云明确保证的是**单个 Object**操作原子、PUT 成功后强一致；没有由这些文档证实的跨对象事务、目录 rename 或“整站 pointer 文件自动解引用”。OSS 是扁平 key 空间。`current.json` 即使一次完整写入，也不会令 APT/DNF 自动把后续 URL 绑定到它。[OSS 一致性](https://www.alibabacloud.com/help/en/oss/user-guide/what-is-oss)、[PutObject](https://www.alibabacloud.com/help/en/oss/developer-reference/putobject)。

CDN Origin Path Rewrite 可以把请求映射到快照前缀，但不改变缓存 key；已有缓存可能继续命中旧内容。CDN 的配置 API 和全网刷新文档均不足以证明所有 POP 同时切换，或证明某个客户端多次请求看到同一代。刷新是失效任务，不是多对象 commit；软刷新还可能保留 304 对应旧内容。[回源路径改写](https://www.alibabacloud.com/help/en/cdn/user-guide/rewrite-urls-in-back-to-origin-requests)、[刷新机制](https://www.alibabacloud.com/help/en/cdn/user-guide/refresh-and-prefetch-resources)。

因此只承诺“**完整快照先验收并上传，随后激活入口；每个通过签名的客户端取得自洽闭包；发布完成后验明精确版本**”。不承诺全球瞬时切换，也不承诺旧固定 URL 客户端在切换瞬间绝不重试。当前本地 `os.replace(stage, output)` 只是本地目录导出，[不是 OSS 发布原语](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/scripts/compose-package-site.py#L2088)。

## 3. 建议对象布局和激活顺序

下面的 `<...>` 均为待填入的受审阅值，不是已存在资源。bucket 名称、地域和账号 ARN 不在本稿虚构。

```text
snapshots/<channel>/r<audit_id>-<snapshot_sha256>/site/<canonical-site-path>
snapshots/<channel>/r<audit_id>-<snapshot_sha256>/audit/<canonical-audit-path>
transport/<transport_manifest_sha256>/manifest.json
transport/<transport_manifest_sha256>/entrypoints/<public-path>
public/<append-only-canonical-public-path>
```

* 每个 channel snapshot 精确保留其被审计的 bytes；`site/` 和 `audit/` 不能通过上传时改路径内容来伪造另一份审计。
* 独立 transport manifest 绑定 preview/stable 各自的数字 audit ID、archive digest、snapshot digest、控制 SHA、所有公开路径、OSS key、bytes digest、size、缓存分类。两套 channel 可以共享公开域名，但不重写旧 preview audit，也不把某一 channel 的 `status.json` 冒充整个站点的新 schema。
* `transport/.../entrypoints/` 存需要换代的有限入口（APT InRelease/Release/Release.gpg、非 hash 索引、keys、bootstrap manifest、repo 入口、channel status、RPM mirrorlist）。入口激活通过域名上**一个受审阅配置集合**把这些有限 URL 改写到确定 transport digest；不可使用任意 bucket/path 输入。实际规则数量、顺序、ConfigId 与回读结果进入发布 receipt。
* `/snapshots/` 和 `public/` 的不可变对象路由不会随入口换代。拒绝未经 manifest 列出的公开对象、路径穿越、编码斜线、同 URL 不同 bytes，以及公钥/脚本被自动压缩或转换后错误地当作相同 bytes。
* 发布步骤：读取并验明不可变审计 → 生成 transport plan → 上传全部不可变对象 → 对上传对象重新 GET 校验 SHA-256/size（ETag 不是 SHA-256 证据）→ 未激活路径上的原生客户端演练 → 重新检查控制/既有入口 → 激活 → 回读云配置与公开入口 → 原有四客户端精确版本验收。半途失败只留下未激活对象；不能先切入口再补文件。
* 生产激活继续串行化。CDN API 没有在已读资料中给出 `expected ConfigId/config digest` 的 compare-and-swap 事务；发布前后 fencing 可发现干扰，不能声称消除最后一个竞态。保持已有“发布中不合入控制变更”约定。[现有约定](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/docs/release-contract.md#L441)。

所有上传使用 `x-oss-forbid-overwrite: true`；已存在时只允许精确 byte/digest/size/metadata 匹配的恢复。**该 header 在 versioning 为 Enabled 或 Suspended 时无效**，不能同时选择这些状态又依赖它保护同名对象。建议这个专用分发 bucket 首版不启用 versioning，恢复依据保留在 GitHub immutable Releases；这不是 WORM 保证。拥有 `PutObject` 的身份若绕过受审阅 uploader，仍可能覆盖，不能把 IAM 的 Put 权限称为不可变权限。若未来要求服务端不可变保留，可另行评估 WORM，但本稿不创建或锁定它。[PutObject 限制](https://www.alibabacloud.com/help/en/oss/developer-reference/putobject)。

## 4. APT 与 RPM 的缓存闭包

**APT：** `/apt/dists/<channel>/InRelease` 是单个带签名的发布入口；其引用的 `by-hash/SHA256/<digest>` 与 pool payload 在 `public/` 里追加并保留，入口只在所有依赖可读后激活。旧 InRelease 与新 InRelease 都能找到各自的 hash 文件。不能把只保存“最新 Packages 与 Packages.gz 的两个 hash”的现有闭包直接当作跨代缓存保护。当前 composer 正是这种单代闭包，见 [1490–1506 行](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/scripts/compose-package-site.py#L1490)。Debian 格式建议当前及至少两代旧 by-hash 索引可用；本设计把跨代保留放在单独的 transport inventory，不放宽每个 canonical snapshot 的精确闭包。[Debian 仓库格式](https://wiki.debian.org/DebianRepository/Format#indices_acquisition_via_hashsums_.28by-hash.29)。

APT 的 `Release` + `Release.gpg` 兼容路径同样是两个对象，不宣称其跨请求原子。现有 Ubuntu/Debian 验收应记录实际选中 InRelease/by-hash，以及故意交错入口时的成功或安全拒绝；不得关闭签名、固定哈希或改用 Trusted。

**RPM：推荐 stable 从第一版采用单对象 mirrorlist 入口。** 新 stable `.repo` 使用一个 HTTPS mirrorlist URL，例如既定公开树下 `/rpm/stable/el/9/x86_64/mirrorlist`；正文仅一行，指向该代完整、不可变 `.../snapshots/stable/r<ID>-<sha>/site/rpm/stable/el/9/x86_64/`。客户端解析一次入口后，`repomd.xml`、`.asc`、全部 repodata 和 Packages 均来自同一不变 base URL；旧 mirrorlist 命中仍指向完整旧代。保留 `gpgcheck=1`、`repo_gpgcheck=1`、`sslverify=1`，不加入固定浮动 baseurl 作为 fallback。[DNF 原生 mirrorlist 与签名选项](https://dnf.readthedocs.io/en/latest/conf_ref.html#repo-options)。

这是有明确原生配置支撑的设计推论，**本次没有运行 DNF A/B 切换实测，不能提前宣称实现已证明不混代**。必须在现有 Rocky/Alma 镜像演练中拦截/记录每次 HTTP 请求：mirrorlist 切代发生在读取 repomd 与签名之间时，后续仍只访问已选 snapshot。也要覆盖 DNF 缓存持久化、retry、断网和 mirrorlist 重读。

旧 preview 目前固定 `baseurl=.../rpm/preview/el/9/x86_64`，源码 [55–65 行](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/scripts/build-repository-bootstrap-packages.py#L55)。迁移期保留这个路径，以及旧、新 repomd 引用的所有带摘要 repodata 和 payload。**只保留 hash 闭包并不能让固定路径 repomd.xml 与 .asc 跨代匹配**；最坏结果应是签名失败并拒绝安装，不能把这种短暂不可用误报为无缝原子升级。独立 `update_bootstrap` 可以把未修改的 preview repo 配置迁到 mirrorlist；修改过的 `%config(noreplace)` 文件须继续兼容并明确可能留下 `.rpmnew`。它不自动迁移到 stable。

**缓存分类：** 不可变 snapshot、hash 索引、版本化包可长缓存；浮动入口先采用 `Cache-Control: no-store` / CDN TTL 0、禁止“忽略源站 no-cache”和 serve-stale，错误响应不长缓存。路径分类必须以实际 manifest 为准，不能按 `.gz` 后缀把浮动 Packages.gz 误归为永久对象。修改缓存规则不会立即替换已缓存对象的旧策略，切换必须检查实际 Age/Cache-Control/bytes，刷新仅是辅助收敛工具。[CDN 缓存规则](https://www.alibabacloud.com/help/en/cdn/user-guide/configure-the-cdn-cache-expiration-time)。

retirement 沿用先移除索引、再独立移除 bytes 的两次运行状态机；其保留集合增加“仍被允许服务的旧入口/旧 mirrorlist 所引用的完整闭包”。当前 30 分钟是 Pages 合同的下限，不能引用它证明新 CDN 所有缓存和任意离线客户端都已过期。初次迁移不执行对象删除；GC 以后只处理受审阅可达性计划，保留合法 rollback 代与审计。长时间未更新索引的任意客户端永不出现 404 不是本设计承诺。[现有 retention](https://github.com/WuKongIM/packages/blob/0ce7184bbf69472f607159f74a096e9160852843/docs/release-contract.md#L199)。

## 5. preview 平滑迁移与回滚

1. 冻结当前 preview audit 为唯一迁移源；下载并验签 canonical tar，逐对象生成 inventory。先将**完全相同的公开 bytes**部署到新 OSS/CDN，stable 仍 disabled，`/repo` 默认仍 preview。这一步不重新签旧 RPM。
2. 在 DNS 切换前，通过 CDN 提供的可验证接入方式/本机受控解析验证同域名 Host、TLS、未缓存对象、范围请求与所有关键 paths；不能用“缓存命中 200”代替私有 OSS 回源成功。官方特别提醒缓存可能掩盖错误 Origin Host。[OSS/CDN 接入注意事项](https://www.alibabacloud.com/help/en/oss/user-guide/cdn-acceleration)。临时验证地址如需申请需另审阅；本稿不虚构现存 staging 域名。
3. DNS 切到 CDN 后仍保留原 Pages 同一完整 preview。DNS 混合期两个入口必须返回同一签名 bytes，先验收 beta.24 的四客户端和安装后 CLI，再进入新渠道变更。记录旧 CNAME、TTL、Pages 配置、CDN ConfigId、证书和 rollback audit 身份；DNS 回退不是瞬时事务。
4. 再单独完成 stable 公钥/离线签名输入与 generic channel composer/verifier；给 stable 明确可选择的 bootstrap 入口。保持现有 `/repo` 默认 preview，建议新增 `/repo-stable` 或经评审显式 `--channel stable`，不能因“stable URL”一词偷偷改变既有脚本默认。不同 channel bootstrap 的文件归属、包名/冲突关系要在该切片中审定，不在存储迁移里暗改。
5. 发布后回滚只激活一个已验证、签名当前可用的前代 transport；不可覆盖旧包或篡改 GitHub audit。若 stable 客户端已上线，整个域名回到 preview-only Pages 会令 stable 消失，因此**那时首选 OSS/CDN 上的前代 composite**；只有旧端也持有完整 stable 闭包且证书有效时才允许整域 DNS 回退。回滚仓库不会自动降级已安装产品。

## 6. 精确资源与职责分离

| 范围 | 最小资源/权限意图 | 明确不包含 |
|---|---|---|
| 一次性基础设施管理 | 一个专用私有、Standard OSS bucket；一个 `packages.githubim.com` CDN domain；相匹配的 Origin Host、HTTPS 证书、CNAME；受审阅地域/分发范围；显式缓存和回源规则 | 不复用现有 backup/integration bucket，不修改 docs CDN，不假定已有签名证书等于 HTTPS 证书 |
| 上传角色 | 仅目标 bucket `snapshots/`、`transport/`、`public/` 所需 `oss:PutObject`、`oss:GetObject`；若确需枚举则最小 `oss:ListObjects`+prefix；短期 STS，精确仓库/受保护环境 identity | 无 DeleteObject、bucket ACL/policy/lifecycle/versioning 管理、CDN、DNS、GitHub audit 写入、签名私钥 |
| CDN 回源角色 | 同账号 STS 私有 OSS 回源；目标 bucket 对象 Get，必要的最小读权限；精确 bucket 范围需要接入演练验证 | 不默认授予账号所有 bucket 读取，不对 bucket 开 public-read；不放私钥/日志/secret 到分发 bucket |
| 激活角色 | 仅该域名 `cdn:BatchSetCdnDomainConfig`、配置只读、必要的 URL `cdn:RefreshObjectCaches` 与 task 查询 | 无 OSS 内容写入/删除、DNS、证书/RAM 管理、签名权限；不把域名级权限宣传成只可改一个 rewrite 参数 |
| 公共验收角色 | 公共 HTTPS GET、固定容器工具、不可变 audit/source 只读 | 不持云写凭证或稳定签名私钥 |
| stable 签名保管人 | 离线/硬件签署 exact manifest 绑定的 APT/RPM 输入并返回签名/已签包及 receipt | stable 私钥不进入 GitHub-hosted CI；上传角色不兼任签名 |
| 受审阅回收任务（首版不启用） | 只对独立 GC inventory 允许的过期 key 执行 Delete；每次保留 proof | 不与常规 uploader 共用删除权限，不删除 rollback/audit 可达对象 |

`BatchSetCdnDomainConfig` 官方资源粒度是 domain，无 function 参数条件；API 表还列出 `ram:CreateServiceLinkedRole` 依赖。该依赖应在一次性 provisioning 中核对并预建所需角色，**不直接给日常激活角色 RAM 创建权**；若服务仍要求它，应记录实测约束后调整职责，而非悄悄扩大。[配置 API 授权](https://www.alibabacloud.com/help/en/cdn/developer-reference/api-cdn-2018-05-10-batchsetcdndomainconfig)。

私有 OSS 回源文档的默认策略示例是 `oss:List*`/`oss:Get*` + `Resource:*`，且开启后该域名可读源 bucket 的资源；因此专用分发 bucket 是边界，公开审计也不能混入秘密。最小 Get-only 自定义策略可作为待验证目标，不能称已在账号中验证。首次授权属于权限变更，需根任务按已授权范围办理，不是本稿已执行。[私有回源文档](https://www.alibabacloud.com/help/en/cdn/user-guide/grant-alibaba-cloud-cdn-access-permissions-on-private-oss-buckets)。

## 7. 可独立交付的代码切片与红病例

**A：最先做，不需要云或私钥。** 新增只读 `plan-object-storage-publication`，只接受现有 archive/receipt/control 数字身份；产出 canonical transport manifest、上传/入口/保留集合及 hash。默认 dry-run，不读云凭证。静态契约验证 stable 仍 disabled、现有 preview bytes/发布流程不变。

先写失败输入：tar/digest 不符；路径穿越/软链；重复 public URI；旧 URI 相同而 bytes 不同；APT 旧 by-hash 缺失；RPM repodata 引用缺失；bootstrap/key 指纹漂移；source stable/preview 混用；不同 audit 指向同 snapshot 名；未收齐闭包就生成激活计划；把 `Packages.gz` 误判 immutable；rollback 代不在允许集合。

**B：凭证隔离的真实客户端切换演练。** 在现有 pinned TEST ONLY Linux 工具链起一个本地 HTTP/HTTPS fixture，生成 A/B 双代签名仓库并随机/确定性插入切代；输出完整请求路径、选中镜像代、metadata/包 digest、清理证明。先覆盖失败：旧 InRelease+新索引、镜像列表 A 读取后切 B、repomd A+signature B、旧 repodata 缓存、缺失新对象、返回 304、遗留 404 cache、损坏署名、modified preview repo 配置。验收通过是四客户端拿到 A 或 B 的完整合法闭包，预设不一致故障被签名拒绝；不得允许无签名 fallback。它是运输实现的测试，不是再次性能资格测试。

**C：stable 签名/通道与 bootstrap。** 让 builder/composer/public verifier 的 channel 从审阅控制派生，默认 preview；新增 stable 公钥拓扑与离线签名请求/返回 receipt 验证。证据绑定 source archive、unsigned/signed RPM、NEVRA、签署者、control SHA。严格保留现有 preview 固定行为测试；新增 stable mirrorlist 与显式 bootstrap 选择的原生测试。不能把 PR #67 的只读来源预检当作这一部分已完成。

**D：最后接云。** 独立 uploader/activator/verifier 工作流，分别注入所需 STS。伪 transport 或 API sandbox 先验证：部分上传中断、409 相同/不同 digest、IAM 拒绝、启用 versioning 使 forbid-overwrite 失效、CDN 返回 ConfigId=0、只部分规则更新、并发 control 变化、刷新超时、错误 Origin Host 被缓存掩盖、客户端得到旧合法版本但不等于 exact target、切换后回滚。获得具体资源/权限授权后再执行真实演练；无权限时仍能完成 A/B/C 的独立代码和测试。

## 8. 仍需决定/验证的事项

- 真实 bucket 名称/地域、CDN scope、证书和 DNS 管理者；当前账号可用并不等于这些资源已配置。选择中国大陆加速前核对该域名适用的接入条件，不凭空推定。
- 最新用户澄清：目前没有 stable 公钥，提供目录中只有现有 preview 私钥备份；这一澄清取代先前“stable 密钥/保管方案已存在”的陈述。根任务只读 README 中的六个公开指纹，确认均与 preview 一致，未读取私钥或吊销文件。stable 独立证书及相应离线/硬件签名方案仍待创建和核对；不得把 preview 备份当作 stable 密钥。
- DNF mirrorlist 的实际 A/B 请求行为、既有 preview `.rpmnew` 行为与 public verifier 的 channel/transport schema 兼容性需要 B/C 演练证据。
- 日常发布按 source/control + immutable audit 验收；CDN 刷新成功、DNS 成功、OSS 上传成功都不能单独成为“正式版已发布”的证明。最终仍是已有的签名链、精确目标版本、公开四客户端与 installed CLI 要求。
