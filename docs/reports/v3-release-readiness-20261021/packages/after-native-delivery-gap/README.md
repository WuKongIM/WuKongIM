# #74 原生客户端演练之后的生产交付缺口（只读续审）

核查基线：packages 候选 `983e567f155a982c45ca066e4dc67d33851f0d24`，main `335cd2569d2b62de577916ed86c097fd3a22547e`。准确观察时间、文件摘要和 GitHub 回执见 `source-freeze.json`。
观察时 #74 仍为 draft/open；Pages 与既有 native 成功，stable 四客户端/A-B job `119303802807`（run `39623787283`）仍运行。以下按“49 项最终通过且合入受审阅控制之后”讨论；本次没有宣称 49 项通过。

**结论：公网 stable 客户端验收 CLI 已有；OSS 上传及 CDN 激活执行器仍没有。无需新总方案，继续旧 `storage-design/design.md` 的 D 切片即可。49 项通过不补齐云执行层，也不代表正式发布。**

主项目证据 worktree HEAD 为 `8e5696fbd912e838d3dde54025c3e7f5b6e5f4f2`；其 [RELEASING.md:34](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-qualification/docs/development/RELEASING.md:34)、[66 行](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-qualification/docs/development/RELEASING.md:66)与[94 行](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-qualification/docs/development/RELEASING.md:94)已有对应渠道、四发行版与 installed CLI 要求，本审计未提高门槛。packages main 的 [stable disabled](https://github.com/WuKongIM/packages/blob/335cd2569d2b62de577916ed86c097fd3a22547e/manifests/channels.json#L68)、[Pages 禁用检查](https://github.com/WuKongIM/packages/blob/335cd2569d2b62de577916ed86c097fd3a22547e/scripts/validate-control.py#L358)及[未接云执行层合同](https://github.com/WuKongIM/packages/blob/335cd2569d2b62de577916ed86c097fd3a22547e/docs/stable-audit-transport.md#L43)另行核对；精确摘录保存在 `source-and-main-lines.txt`。

## 已经具备，不再重复实现

| 能力 | 当前证据与实际边界 |
|---|---|
| Mac 保管与独立生产公钥 | [合同 417–435](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/docs/release-contract.md#L417)明确接受 Mac 在线工作站保管；[公钥清单](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/manifests/stable-public-keys.json#L1)已登记。无需再选择硬件或离线保管。本次未读取私钥或 Keychain。 |
| stable 候选、签名输入、封存与读回 | [封存合同 16–49](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/docs/stable-audit-transport.md#L16)已有独立历史控制/生产者字节、fresh source preflight 和 immutable audit 围栏。这里的 upload 是 GitHub audit 两个原始资产，不能称 OSS 上传。 |
| 双通道运输计划 | [planner 520–534](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/scripts/plan-object-storage-publication.py#L520)已有原始对象、不可变别名、入口、派生 mirrorlist 和保留集合，但明确 `plan_only`，GitHub/cloud/activation 标志为 false。 |
| 四发行版公网精确版本与 installed CLI | [调用合同 735–749](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/docs/release-contract.md#L735)与[实现 1468–1595](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/scripts/validate-production-package-clients.py#L1468)：生产模式固定 canonical HTTPS origin；重新验证候选签名，核对公钥/bootstrap/InRelease/mirrorlist，再运行四个 pinned native clients 和 installed CLI，前后重查入口。无需另造 stable 下载验收器。 |

公网调用仍需真实 sealed stable 输入及公开可达对象，不能用 TEST ONLY 输入替代。现成 CLI 的 receipt 明确 `github_provenance_verified=false`、`publication_verified=false`（[1594 行](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/scripts/validate-production-package-clients.py#L1594)）：它证明该次公网客户端结果，不独自证明整个发布事务。交付器须另保留 fresh provenance、上传/配置回读与该 receipt 的绑定，不能修改这些标志冒充已完成。

## 真正剩余的代码切片

现有仓库没有 OSS Put/Get 与 CDN 配置/刷新适配器，搜索回执见 `source-search.txt`；[对象存储合同 110–115](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/docs/object-storage-planning.md#L110)和[stable 合同 43–49](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/docs/stable-audit-transport.md#L43)也明确此边界。现有生产 workflow 仍调用 [Pages deploy](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/.github/workflows/native-package-publish.yml#L1576)。

最小可离线先准备的是 **既有 transport v2 → OSS/CDN 交付适配器**，先用无凭证 dry-run 与替身 API 验证请求生成和失败状态机；不另写签名、封存或四客户端实现：

1. 重新生成/核对已审阅计划，按 `source.audit_release_id/member` 或 `content_utf8` materialize 精确对象；生成 create-only 上传请求，GET 读回 byte SHA-256/size/cache metadata，冲突仅接受精确同字节恢复。全部对象确认前不产生可执行激活步骤。
2. 从同一清单生成有序有限路由、缓存配置和 before/after diff，绑定实际资源身份、当前配置摘要、控制 SHA、audit IDs、plan digest；activation 前复查来源/审计/当前控制，变化即停止。配置写后回读、公共入口验证，再调用现成四客户端 CLI，保存一份绑定各阶段证据的交付 receipt。API 并非跨 POP 事务，不承诺原子切换。
3. 明确对象存储的受审阅生产目标/操作入口，默认仍禁用；不能直接松开 Pages 的禁 stable 规则。[validate-control 358–362](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/scripts/validate-control.py#L358)硬拒启用，且[历史候选合同 602–606](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/docs/release-contract.md#L602)绑定 disabled 控制。新发布控制必须与这些已封存历史 bytes 分开，不能回写候选或靠 `enabled=true` 当完成。是否提供独立 workflow 属实现选择，不是新增验收门槛。

这就是旧设计 D，不需要新增一轮总体计划。当前任务只把它收窄为输入、输出、失败边界；没有写适配器或测试。

## #74 带来的一个精确运输接口增量

旧资源提案基于 #73，写有“`/snapshots/...` 相同对象路径直达”。#74 为 DNF 跨两条命令的缓存闭包新增[跨代 payload aliases 492–499](https://github.com/WuKongIM/packages/blob/983e567f155a982c45ca066e4dc67d33851f0d24/scripts/plan-object-storage-publication.py#L492)：公网 URI 是 `/snapshots/stable/B/.../Packages/A.rpm`，实际对象 key 是 `public/snapshots/stable/B/.../Packages/A.rpm`。

因此适配器须**先按清单匹配精确入口/不可变别名，再让原始 snapshot 对象直达**。不能对整个 `/snapshots/` 无条件直达，也不能给所有 `Packages` 请求盲加 `/public/`：当前代自己的原始包未必在那个派生 key。保留各条 source member/digest，按真实清单合并等价 CDN 规则；不能合并且超过既定 provider 限额时明确拒绝。此为消费新 planner 输出的必要实现细节，不增加发布指标，不需改旧已签快照。

## 下一切片最小 failure inventory（尚未执行）

| 故障 | 应有结果 |
|---|---|
| TEST ONLY、伪造/旧 resolution、audit/source/control 身份改变，或输入在读取中变化 | 不进行生产写入；真实 GitHub 回读仍必须在线完成。 |
| 成员缺失/错源/错 digest，上传中断、权限失败、对象已存在但字节或 cache metadata 不同 | 不激活；同字节冲突允许可验证恢复，不覆盖、不删除。 |
| bucket versioning 不符合已审阅 create-only 约定，或 endpoint/bucket/domain 越界 | 拒绝执行，不能仅依赖上传成功。 |
| `/snapshots/...` alias 被直达规则抢先，root `/` 漏映射，mirrorlist/浮动元数据错误长缓存，规则超限 | 在离线路由模拟及请求表检查中失败；保持 `/repo` 默认 preview。 |
| 对象未完整读回就激活、CDN 部分更新/配置回读不同、刷新超时、配置或 control 并发漂移 | 不报告完成；保留精确失败阶段及可回滚前代 composite，不宣称 CDN 原子。 |
| 回源错误被缓存命中掩盖、公网证书/byte 不符、合法旧版但非精确目标、四客户端/CLI 任一失败 | 发布仍 incomplete；不能用 HTTP 200、缓存刷新受理或 TEST ONLY 49 项替代。 |

## 必须等待的真实数据与权限边界

- 用户仅接受 Mac 保管；专用 OSS/CDN 首 7 日 ¥100 仍是待批准提案，原 ECS ¥1500 不涵盖。本子任务不含资源采购/权限/DNS/TLS/生产发布操作授权，本次不做这些动作。
- 沿用 `storage-resource-proposal/proposal.md` 的未决实值：bucket 可用名/真实地域 endpoint、版本控制状态及 Mac 上传 API 适用性；CDN 真实 CNAME/config IDs、HTTPS OSS HOST/SNI/私有回源角色当前策略；备案/域名归属、权威 DNS record ID/线路/CAA、TLS证书及部署/续期身份、实际计费。未知值保留 null，不读取 AccessKey、私钥或 Keychain 代填。
- 生产执行还需要真实 stable source Release、经其预检和 Mac 签名形成的 candidate、immutable numeric audit，及迁移当刻 fresh preview/retained 身份。现成代码能处理这些，但当前 TEST ONLY 成功不是这些生产数据。
- 公网 verifier 需要 Docker 与合同列出的 native 工具环境；可以复用符合条件的环境，并非强制再采购主机。公网执行必须保留普通 TLS/签名校验、四发行版精确版本和 CLI 身份。
- 首次接入继续遵循旧方案：preview 同 bytes 迁移与校验，然后单独 stable；stable 已公开后回滚到完整前代 composite，不能回退 preview-only Pages 让 stable 消失。生产前再核实当前状态，不冻结 beta.24 为未来迁移目标。
- 只读续审未修改代码、测试、规则、channels、信任根、权限、云资源、DNS 或发布状态；未运行 native 测试/重跑 workflow。protected agent control、生产 signer 与既有 Pages publisher 均未改变。
