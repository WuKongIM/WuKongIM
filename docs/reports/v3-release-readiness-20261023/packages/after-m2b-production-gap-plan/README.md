# M2b 之后的 stable 生产接入：最小剩余实施方案

只读方案；没有源码修改、测试/容器执行、云 API 写入、发布或读取私钥/Keychain/NAS。
本轮 GitHub GET 确认 main=`7255102de81b45c23d52e908ccdde1b7799ee3f5`；干净 route worktree=`1fb1c6ea077fd260c373434c1a3c368a7952defd`，
两者完整 tree 同为 `cd49a22102235d60bda1a703a5b1145ab112c40e`。
父任务最新回报：M2b 的 7 文件 native→SDK 全链与 refresh 聚合修复已冻结待双轴复核；默认测试仍在运行。
本轮没有读取或触碰作者树，不将冻结状态计为完成或通过。
本方案仅补原 D 生产执行接缝，不重做编译器、签名器、四客户端或全项目 GA 审计。

## 结论

M2b 即使 fixture 全过，也只证明 TEST ONLY 原生字节经 SDK 的调用链。
生产仍缺一个窄的 **reviewed object-storage control → fresh 验证 → SDK 写入 → live 路由读回 → 原公网客户端** 组合入口，
以及真实资源与授权。不能删一句异常或改 enabled 就宣称已完成；[现有 activate 无条件拒绝](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/aliyun-object-storage-provider.py#L431)。
既有计划覆盖方向，只有下文具体接线需要增量实现，不需要另立通用审批、凭据平台或新的密钥制度。

## 已有能力：直接复用，避免重新实现

| 边界 | 已存在的真实实现 / 仍不代表什么 |
| --- | --- |
| fresh 生产输入 | [verify_delivery_inputs](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/verify-object-storage-delivery-inputs.py#L120) 拒绝 TEST ONLY，原 v1/v2 planner 重算，核执行 checkout 干净且 HEAD 精确、fresh packages/main；遍历当前及 retained 的历史 Git 控制、immutable audit/tag/asset ID、source resolver、实际 attestation 和 native metadata。不是待从零开发的模块。 |
| 控制漂移 | [previous anchors](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/verify-object-storage-delivery-inputs.py#L60) 比较 executor/input/plan/audits/sources；[current main](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/verify-stable-seal-inputs.py#L85) 和 [历史 controls/producers](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/verify-stable-seal-inputs.py#L105) 已有 Git 读取与 ancestry/blob 验证。调用者须在下一写阶段重新调用，不能读取旧 success JSON 代替。 |
| 物化与外层绑定 | 原 [prepare](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/prepare-object-storage-delivery.py#L67) 保持 disabled/null resource intent；生产 wrapper 另外绑定真实 Provider resource digest，两者不伪装成同一个身份。[client_requests](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/prepare-object-storage-delivery.py#L155) 与 [receipt binding](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/prepare-object-storage-delivery.py#L307) 已给原 verifier 的参数和精确 digest 比较。 |
| OSS/CDN 原语 | [observe_resource](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/aliyun-object-storage-provider.py#L229)、[create-only + full GET](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/aliyun-object-storage-provider.py#L269)、[完整配置观察](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/aliyun-object-storage-provider.py#L302)、[apply_rewrites](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/aliyun-object-storage-provider.py#L349) 可直接复用。只针对该次 SDK 操作；原 false 字段不改写为发布成功。M2b refresh 修复先等其独立验证完成。 |
| 公网客户端 | [stable 原入口](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/validate-production-package-clients.py#L1480) 已要求 canonical HTTPS、strict X.Y.Z、真实 native 验签工具、2 APT + 2 RPM 安装/CLI；[原回执](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/validate-production-package-clients.py#L1598) 已带 input/plan digest。不要另写客户端，也不要把 TEST ONLY product 身份传入生产。 |
| 审计封存 | 原 bind/seal/resolve 已支持 explicit stable；[合同](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/docs/stable-audit-transport.md#L16) 要求真实 immutable stable source、已审 candidate、历史 producer 字节等。生产签名与封存是上游最终执行，不是本次调用 SDK 的新实现。 |

## 现有 auth 与公共来源

本轮 `gh api repos/...` 只读 metadata 确认 `WuKongIM/WuKongIM` 与 `WuKongIM/packages` 均 public。
[GitHubReader](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/resolve-audit-release.py#L121) 接受可选 read token；交付只读取已发布 immutable Release，不需要 draft-capable Publisher 权限。
现有 source-read manifest 已 enabled，并有 [既有 Source Reader job](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/.github/workflows/source-release-preflight.yml#L76) 的 Contents/Attestations read 认证接线；不需要再建 GitHub App。
fresh helper 接受 `source_token` 与 `package_token`，生产运行只需明确注入已有短期只读 token；不要默认复制本机登录或读历史 App 私钥。
[隔离 helper 环境](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/verify-stable-seal-inputs.py#L183) 只把显式 source token 传给 attestation，GH_CONFIG_DIR 指向 scratch；“仓库 public”不等于 gh attestation 已经无凭据实跑通过。
[Policy Reader/Publisher 合同](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/docs/release-contract.md#L39) 已把 immutable-policy 的 Administration read 和 draft/seal 的写权限分开。
verify_delivery_inputs 本身不查询 repository immutable-policy，也不写 Releases；不要为下载公开 immutable audit 给它追加 Admin/Publisher 权限。
实际 runtime 的 token 可用性、工具可执行性及 current-main 必须运行时验证；本轮只核 capability，不导出任何 token。

## 最小新增接线（可离线完成）

建议用一个明确的本地/受控 Linux CLI 作为 composition root，命名 `scripts/publish-object-storage-delivery.py`。
它消费原 transport inputs，不从任意 URL/历史 checkout 动态执行代码；运行需要原 SDK lock、原 native 工具和 Docker 四客户端环境。
没有理由先新建云主机或新 hosted signing 工作流；运行载体最终据已有可授权环境选择。

1. **当前 publication control。** 新增一个 object-storage 专用的、默认 disabled 的固定 manifest；不借 Pages stable flag 开门。
   限定 backend=OSS/CDN、operation=upload/activate、channel=preview/composite、exact input/plan digest、当前目标 audit/source/version、
   实际 bucket/domain/origin 及调用 principal、managed ConfigIds、before configuration digest。未知真实值保持 null，disabled 时可保存。
   executor SHA 由运行时 expected SHA 与 clean checkout/fresh main 比较，不把自己的提交 SHA 写入同一 manifest 造成循环摘要。
   仅该当前 reviewed control 可选具体操作；外部 JSON 的 verified=true、旧收据和 test_only 不构成入口权限。
   [原 Pages validator](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/validate-control.py#L358) 仍禁止其 stable；新 manifest 只管 OSS/CDN，不能使现有 Pages publisher获得 stable 权限。
2. **上传阶段。** 先原 fresh validator，再原 prepare；把 control/input/plan/preparation/objects/routes 与两份 resource digest 一起绑定。
   用 uploader Provider 对清单内每项执行原 put_create_only/full readback；旧对象只接受 exact conflict/readback。
   全部对象完成前不进入路由阶段；任一未知/部分写入保存实际 effect，不重 PUT、删除或宣称回滚。
3. **配置阶段。** 用 `verify_delivery_inputs(previous=first)` 再核，重读当前 config/resource，绑定 expected before digest 和 managed IDs，
   再使用原 compiler/apply。实际编译结果决定是否能落入既有限额，不能把 fixture 的 178/153/17 当生产固定规模。
   compiler 不表示真实 wire 等价；原 `Provider.activate()` 的拒绝不能被单一开关移除。
   新 production composition 只有在其完整 current-control 与 live 检查接线一并复核后才可发布；SDK 原语保留操作级 false 字段。
4. **live HTTP 与 refresh。** 编排 M2b 验证后的 refresh；完整 public URI 覆盖含 `/`，记录真实任务与 URL 集合，失败不假称无副作用。
   新增窄的只读 probe：切 DNS 前用实际 CNAME 作连接目标、保持 packages Host/SNI 与证书校验；切换后用 canonical public DNS 再核。
   核 exact bytes/Cache-Control、共享 preview 入口、stable mirrorlist、alias 与原 snapshot 优先关系及已有计划的邻接 URL 边界。
   真实 PCRE/capture/\z、URI/query 规范化、未匹配回源与缓存不能用 Python 正则或 Complete 任务替代；实际不支持则停，不自动放宽。
   在授予云写权限前可写完 probe 与网络替身失败测试，但无法离线产出 live 等价通过证据。
5. **原四客户端。** 直接按原 client_requests 调用 `validate_clients(**kwargs)`，真实 runner/fetcher、test_only=false、stable 必须 installed CLI；
   composite 同时保留 preview 验证，原 `/repo` 默认 preview [不改变](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/docs/stable-audit-transport.md#L69)。
   原回执逐字保存，wrapper 比较 exact version/source/audit/snapshot/input/plan，再做 fresh current-control/input fence，组合最终结果。
   原客户端 false provenance/publication 字段不改写；真实 fresh、资源、上传、路由与公网检查分别报告，未齐全只能 incomplete。
   现 [public CLI workflow](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/.github/workflows/native-package-cli-acceptance.yml#L33) 硬编码 preview，它不是可直接 dispatch 的 stable gate；最小实现先复用函数/CLI，不复制该 workflow。

## 有限文件 scope（建议 8 文件，尚未实施）

| 文件 | 增量 |
| --- | --- |
| 新 scripts/publish-object-storage-delivery.py | 上述固定操作组合、当前 manifest 解析、公开 probe、实际副作用 receipt；延迟 SDK 导入；无资源管理/签名功能。 |
| 新 manifests/object-storage-publication.json | 仅新增 disabled/null 控制模板；真实值和启用状态留待实际授权及 reviewed control change。 |
| 新 tests/test_object_storage_publication.py | 固定 manifest/CLI/阶段拒绝、原客户端绑定、public probe 的离线失败清单先红。 |
| 新 tests/provider_sdk/test_object_storage_publication.py | 原 official SDK instance adapter 的整段编排/部分副作用；复用 M2b 已审 fixtures，禁止 mock 原验签/成功 auth 冒充生产。 |
| .github/workflows/object-storage-provider-contract.yml | 只读 CI 增加新显式 suite/receipt 和 manifest 触发；[当前 workflow](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/.github/workflows/object-storage-provider-contract.yml#L3) 只执行两个固定测试入口，不能以已有绿灯覆盖新 suite。 |
| docs/object-storage-delivery.md | 固定阶段、实际凭据输入、失败/恢复与 live 证据语义。 |
| docs/release-contract.md | 明确 Pages 控制与 OSS/CDN 当前 publication control 的职责、启用与实际完成条件。 |
| CHANGELOG.md | 窄入口与仍默认关闭的操作者说明。 |

先冻结失败库存/全部相关测试/首红，再最小实现与原相关/default/SDK/合同 suite 首跑；不复制 M2b driver，不重做 49-case lab。
初片不改原 planner/materializer/签名 producer/validate-control/四客户端，也不碰 agent control、trust、Pages。
[producer byte anchors](https://github.com/WuKongIM/packages/blob/7255102de81b45c23d52e908ccdde1b7799ee3f5/scripts/compose-package-site.py#L1890) 必须保持；如后续确需更改，旧 candidate不能静默沿用，遵循原封存合同重新核相容性。
如果真实配置暴露 provider 的 endpoint/CNAME/私有回源函数 shape 不匹配，仅待有官方合同和真实 readback 后再列窄增量，不能预先放宽 allowlist。

## 依赖真实资源后的最后动作及批准点

现有登录只读清点由父任务确认：packages 仍 GitHub Pages、无专用 OSS/CDN；本轮复用已存观察，没有再次访问账户控制台。
原 `storage-resource-proposal/proposal.md` 的 ¥100/7天仍未获批准，既有 ECS ¥1500 不覆盖。
**现在无需问用户**：离线接线、tests-first、评审/正常 CI 可按现有授权继续准备，生产默认关闭。
只有需要新增消费/云资源、RAM/origin-role 权限、TLS/DNS写入或真实发布时，才提交已具体化的资源/policy/config/diff 给用户批准。
不让“代码已合并”或一份 JSON enable flag 代替这项外部授权。所缺字段见 `external-inputs.json`；不凭 docs 域名猜 CNAME。

资源批准后按原顺序：专用桶/CDN/TLS与实际受限策略 → create-only 上传和切换前 live probe → 同字节 preview 迁移并核公共客户端 →
在实际 stable source/candidate/immutable seal 已具备且正式发布获准后激活 composite → canonical HTTPS 四客户端确切目标与最终 fresh fence。
DNS/cert/role 创建不放入日常 publisher；既有 Pages和docs不自动删除，稳定版公开后只能退回上一完整 composite，不能退回 preview-only Pages。
M2b 尚在进行、实际资源未建、真实 source/candidate/audit tuple 未在本轮提供；这些都不报告为已完成。
