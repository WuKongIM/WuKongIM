# D 的下一切片：真实 provider 接口与生产事实绑定（只读方案）

本次基线 `WuKongIM/packages@eac9d252f13ea201b405825e6303de703af08c16`，tree `ca255a7d0fc27186a68f291ccf7ce70948a976d1`。观察截至 2026-10-21 16:30 UTC（北京时间 10-22）；精确本地读取时间/规则/源码摘要见 source-binding.json。未调用云 API、读取凭据、运行测试/容器、修改工作树或外部状态。#75 首次 CI 失败由另一任务诊断，本报告不重复归因。

**建议继续原 D，先实现可被离线传输替身验证的真实 SDK 适配和 fresh 输入验证，保留生产入口关闭；不要把 #75 的模拟接口换个布尔值当生产发布。** 费用批准不是写代码/协议测试的前提。真正上传、CDN/DNS/TLS/权限变更与公网验收仍需实际资源和对应批准。CDN 回源重写的具体 API function/参数/规则顺序尚未取得足够官方 wire 证据，须先补齐，不能用第三方示例猜字段或把“支持 50 条”当作编译已经完成。

## 已有能力与具体缺口

| 边界 | 已有接口（基线行号） | 下一实现必须补齐 |
|---|---|---|
| 精确对象与路径 | `prepare-object-storage-delivery.py:186` 重算 v1/v2 原 plan；每档解包一次；按源 audit/member 校 size/SHA；`:103` 入口、alias、原 snapshot 顺序 | 直接复用。本地 resource intent 仍是 null/disabled；真实资源观察另行绑定同 plan digest，不能放宽 `_resource:67` 后让旧命令隐式写云。 |
| 云 API | `simulate_delivery:329` 强制 `simulation_only=True`；preflight/config/control/refresh 都只接受 simulated 形状 | 新薄 provider 使用官方 SDK 的真实请求/响应；模拟回执保持原样，不伪装真实。没有凭据发现器、资源创建器、通用插件或新签名协议。 |
| 当前控制 | `_activation_control:298` 只比 fake SHA，CLI 的 activation SHA 是声明 | 新调用实际 GitHub main 读取；绑定执行 checkout/受审控制和 original plan。SHA 标签、历史控制或传入的 verified JSON 均不能代替当前事实，也不等于批准 stable 发布。 |
| source / seal | planner `_stable_snapshot:291` 实际验证本地签名/闭包，但 resolution 是本地锚定 JSON；客户端也不在线证明 provenance | fresh resolver 重新取 exact numeric audit/tag/两个资产，比较原 descriptor；复用 source resolver、attestation 与 metadata 验证。见下节；不改 candidate 字节。 |
| OSS 读回 | fake `get_object` 返回整个 bytes | 真实 GET 应按已知 size 流式读取、累计 SHA/长度并校 Cache-Control，读到 EOF；拒绝截断、超限及错误对象。HEAD/ETag/CRC/旧 journal 不能代替原字节。 |
| CDN 配置 | `_route_table` 是最多 10,000 项的精确请求表，不是 wire；fake config `simulated:N` | 编译成有限、非递归、有序 provider 规则并验证等价、ConfigId 管理与缓存；读取完整相关配置，保持非受管项。最大 50 条不是 10,000 条分批可绕过的限制。 |
| 公网验收 | `validate_clients` 已有 preview/stable；stable `:1468` 强制 installed CLI、canonical HTTPS、真实本机签验工具 | 直接执行其两个 APT + 两个 RPM 客户端及完整 CLI 验收，绑定 exact version/source/build_source、plan/input/candidate 与本次云操作收据；不接受 fake callback 通过。 |

`validate-control.py:358` 与 `manifests/channels.json:68` 仍要求 stable disabled / object_storage_required。这一切片不改 12 producer、渠道/公钥/历史控制、现有 Pages/native publisher、生产权限和公网路由。真实发布 authority 的接线属于后续明确审阅控制变更，不能从本地代码存在或通过测试推导。

## 复用的身份验证顺序

1. 保留 `prepare_delivery` 的原 inputs SHA、原 plan schema/SHA、所有 source.audit_release_id/member、历史 candidate/control/seal 各自独立；v1 preview-only 仍独立合法，v2 current preview+stable 与 retained 全保留。
2. fresh 读取 `packages/main` 必须等于受审 activation/executor SHA。对 stable，复用 `verify-stable-seal-inputs.py` 的 `current_control:85`、`require_ancestor:93`、`verify_control:105`、`verify_candidate:302`、`verify_source:206` 和 `revalidate_source:242`；实际 source 7 assets + attestation + metadata 不能只信已存成功收据。候选历史 Git controls/producer 与真实 blob 相等，历史 candidate control **及历史 seal control** 都需与当前受审控制关系匹配。既有 `verify_control(expected_control_sha=...)` 的返回名是 `seal_control_sha`，调用接线不得把“本次当前控制”误写成原 seal 的控制身份。
3. 对每个明确 current/retained audit，用 `resolve-audit-release.py:472` 的官方端点、exact tag 模式和真实数值 ID 重取/核验 immutable complete 资产；stable 的 expected_control_sha 取该 **历史 seal_control_sha**，不得改为当前 main。所得稳定身份、资产 ID/size/SHA 与输入绑定；moving observation 时间另记，不能回写旧 archive 或假定 fresh receipt JSON 必须逐字相同。preview 使用其原 resolver/receipt 校验，并复用显式 `--channel preview` 的 source preflight，不能把 stable 专用 helper 硬套 preview。
4. 生产请求一律 `test_only=false`，拒绝 loopback、TEST ONLY 固定源码/伪 audit ID。helper 仅加载执行 checkout 的脚本；数据根/历史 Git 内容只作数据，不执行任意历史代码。已支持的历史 producer 不相容则明确拒绝，不自动下载执行其他版本。
5. 所有本地完整性及生产身份检查结束后才允许 PUT；每个真实写入前仍绑定同一资源/凭据身份。所有对象完整读回后，在首次 CDN 写入前再次核当前 main、source Release/tag/祖先、audit immutable identity、原 inputs/plan 和 CDN 当前配置摘要。漂移就停止；已上传对象保留，不删除。
6. 当前源码没有新的 object-storage 发布 authority，故这轮 library/离线测试**没有可执行生产 CLI 或 workflow**。未来接线必须读取受审控制；另传 `production_enabled=true` 或任意 JSON 签字不是授权协议。资金批准与频道/资源变更授权也分别保留。

## 真实 API 对接表

完整官方链接和证据局限见 official-sources.md；以下为工程推论，不是已实测云行为。

| 现有概念 | 最窄真实接口 / 处理 |
|---|---|
| resource preflight | 同一注入身份调用 STS `GetCallerIdentity`；OSS `GetBucketInfo` 校 account owner、bucket、location、endpoint、ACL；`GetBucketVersioning` 仅合法 XML 无 Status 表示从未开启，Enabled/Suspended/未知/权限失败全拒绝；CDN `DescribeCdnDomainDetail` 与 `DescribeCdnDomainConfigs` 核 exact domain/CNAME/HTTPS443 OSS源/Host/SNI/缓存/配置状态。读取权限单独列，不能给 uploader 顺带加 admin。 |
| create-only | 官方 OSS SDK V2 `PutObject`，固定 bucket/key、明确长度、Cache-Control、`x-oss-forbid-overwrite:true`。只处理精确 `FileAlreadyExists` 冲突；不能把所有 409 当同对象。超时结果未知只 GET 同 key 核事实，不能默认覆盖重试。当前总档案 2 GiB 低于单 Put 5 GB，不引入 multipart/断点续传。 |
| GET readback | 固定 endpoint/bucket/key、流式 full GET、bounded chunks/总长度/超时；不接受重定向到任意 host、下载 URL 输入或隐式代理凭据泄露。请求 ID/状态/耗时/已读字节和 digest 公开；Authorization、STS token、签名 URL 与原 SDK 异常不入公开收据。 |
| configuration | `DescribeCdnDomainConfigs` 取受管 config ID/函数/参数及相关缓存/回源全量事实；排序仅适用于无序集合，不能丢 rewrite 的执行优先级。观察摘要包含足够的原始语义，不只保存 `observation_id`。 |
| activation | 官方 `BatchSetCdnDomainConfig` 2018-05-10，单域每批 Functions≤20；逐条检查返回 ConfigId 非0，再 fresh readback。按已授权 ConfigId 更新，完整区分管理范围和不相关配置；未知规则/需要删除旧项先拒绝。删除是另一个 `DeleteSpecificConfig` 权限/副作用，不隐式追加。超时/部分写入必记录可能发生及实际回读，后续刷新/成功声明停止。该 API/SDK 请求无已证 revision/CAS 字段，read→write fence 不是原子锁；部署入口需要串行执行约定并检测漂移，不能声称全 POP 原子。 |
| refresh | `RefreshObjectCaches` 仅精确已变浮动 URL，不全域目录清扫；保存真实 task IDs。`DescribeRefreshTaskById` 按本次 ID 查询，最多10个一批，逐个 exact URL/type/ID Complete 才结束；missing/重复/Failed/Canceled/Timeout/期限到都不通过。此读取官方权限为 Resource `*`，应如实列最小只读权限，不能宣称所有 CDN 读操作都是 domain-scoped。 |
| public checks | 独立 origin full GET + 规范域名 HTTPS/证书/入口/cache验证；刷新 Complete 不能替代。复用原四客户端入口，保留它输出的 `github_provenance_verified=false`、`publication_verified=false`，外层只绑定各项真正执行证据，不能篡改旧字段为真。 |

### CDN 还有一个实现前的具体待定点

官方文档已确认回源重写的 PCRE、50 条、顺序与 break、cache key 不变；但此次公开 function 参数总表及 SDK 通用 Functions 字符串没有给出该功能的完整 `functionName`/argument/顺序编码。**尚不能宣布 wire compiler 可落地完成**。下一作者应先从官方具体功能定义/可信既有同域配置导出取得 exact wire contract；本任务没有调用云配置。第三方搜到的名称未采信。

规则必须按 exact table 证明：入口优先；跨代 alias 即使长在 `/snapshots/stable/B/.../A.rpm` 仍去清单指定的 `public/...` 对象；原始 snapshot 仅对应已列对象。允许对同一 key 变换的封闭 URI 集合做等价分组，禁止无限前缀回源。额外核邻近/编码/重复斜线/穿越/查询语义及 regex/参数长度上限。无法在 provider 限额内表达就明确不生成 activation；不转 EdgeScript/边缘函数/ESA，也不放宽对象访问面。真实 PCRE/服务端排序最终仍要 live 验收，Python fake 等价检查不冒称云已验证。

## 待审最小文件切片

建议先审 **D2：provider SDK 与输入绑定 library**，预计 7 文件，不改 #75 核心或受保护控制：

1. 新 `scripts/aliyun-object-storage-provider.py`：固定 bucket/domain 的 OSS Put/stream Get/preflight、CDN read/config-response/refresh 包络与 bounded SDK transport；无资源创建/删除/权限/证书/DNS/自动发现。
2. 新 `scripts/verify-object-storage-delivery-inputs.py`：复用上述已有真实 GitHub/source/candidate helpers 的窄接线，输出独立 anchor 摘要；不签包、不封存 Release、不把本地 JSON 当授权。
3. 新 `tests/test_aliyun_object_storage_provider.py`：真实官方 SDK 请求序列化，经注入 HTTP transport 返回固定 provider 响应；完整矩阵先红再实现。
4. 新 `tests/test_object_storage_delivery_inputs.py`：精确官方 GitHub schema 的替身，覆盖各历史/当前锚与写前 fence；沿用 #75 真档案 fixture，禁真实凭据/网络。
5. 新 `requirements-object-storage-provider.lock`：只锁实际使用的官方 OSS/CDN/STS SDK 及依赖哈希，独立可选安装，不让原 stdlib 默认测试需要云 SDK；精确版本尚未选定，不能把官方 master 阅读结果称锁定。
6. `docs/object-storage-delivery.md`：真实/模拟接口、权限与结果未知处理、未授权/live 未验边界。
7. `CHANGELOG.md`：operator-facing 离线可测能力与未启用事实。

CDN writer/规则编译须先补上上一节 wire 定义；如果该定义未取得，D2 只实现已确证 OSS 与 CDN read/refresh 接口并显式拒绝 apply，不交付假的成功适配。实际 apply 函数可在同一模块后续窄补，不为阻塞新增路由平台。若团队希望此 PR 直接覆盖整个真实交付编排，则应先明确受审 production authority 和 wire 定义后另审范围，不能在上述7文件中偷偷新增可写 CLI。现有 `simulate_delivery` 保留，纯 helper 直接复用，不复制签名/下载/客户端引擎。

## 批准前后边界及真实待取值

现在可完成：上述代码/测试先红后绿、官方 SDK 的纯序列化/错误解析替身、真实本地档案与 fake GitHub/provider 顺序测试、规则静态能力判断、精确权限 diff 草稿。不得把其结果称实际 upload/route/public verification。#75 红 CI 也仍须按原严格约束单独解决，不能由新 adapter 豁免。

等待对应批准和接入后才做：创建专用桶/域名、IAM/私有回源角色增量、DNS-01/TLS/DNS修改、真实 PUT/GET 流量与 CDN配置/刷新/public4客户端。既有 ECS ¥1500 不是 storage 的批准；旧7日¥100只是提案，本报告不改预算、不新增报价。

真实输入仍需：account/principal（不含 secret）、确切 bucket/region/可用上传 endpoint 与从未开启 versioning 观察；canonical domain 的 CDN ID/CNAME、回源 Host/SNI/role及策略版本、受管 config IDs/函数原值和缓存/配置组状态；TLS SAN/有效期/部署记录、DNS record/线路/旧值/TTL、ICP备案/归属和实际费用范围；当刻 fresh preview/retained、真实 stable source Release+验真候选+immutable audit二资产、历史与当前控制 SHA。旧资源提案中的名称/域名推导和四桶清点不能当当前资源事实。未再次盘点云。

发布完成顺序保持原方案：完整对象读回 → 同字节 preview 迁移及既有客户端确认 → 经独立 authority 的 stable 激活 → canonical 公网四客户端 exact target/CLI。stable 已公开后的回退目标只能是受审前代完整 composite，不能退回 preview-only Pages。CDN批次有间隔/缓存传播，必须如实记录；不承诺多项原子切换。
