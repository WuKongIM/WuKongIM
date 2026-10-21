# 原 D 最小离线切片独立范围复核

结论：**已足够开始测试先行实现；建议由同一个窄 object/route 核心消费现有 v1 preview 与 v2 composite。** 不必改任何既有输入/输出 schema、签名、candidate、audit、Pages authority，也不必先决定真实 bucket/CNAME。当前只交付无凭证 dry-run、精确本地对象展开、替身 API 的上传/激活失败状态机及明确 TEST ONLY 的组合 receipt。真实 SDK、生产启用、云验证仍是后续原 D 的接线工作。本审查没有运行测试、CI、云 API 或修改仓库。

复核固定 packages HEAD `983e567f155a982c45ca066e4dc67d33851f0d24`，工作树干净。这里不更新 #74/49 场景状态；输入报告记录的观察时状态不能当作当前 CI 结论。

## 必须写清的四个实现边界

1. **首次迁移要能只用 preview。** 续审 README:25 写 v2，而 :56 仍要求先 preview 同 bytes 迁移；原设计:78–82、资源提案:47 也是此顺序。现有 `composite_inputs`:273–282 强制 preview+stable，不能填 null/假 stable。最小处理是内部窄归一：严格调用原 v1/v2 planner 重算并核对原始 plan digest，统一读取 `snapshot_objects`、`immutable_aliases`、`entrypoints`；v1 无 `generated_objects`，内部视空，source 的 preview channel 由严格 v1 envelope 导出；v2 读取既有 channel/source/generated 字段。仍保留两种原 schema/摘要，不序列化一个“伪 v2”，不改变既有 planner/publication authority。首次 preview 验收复用原 preview 路径；stable 复用现成 stable 四客户端路径。

2. **计划、验证事实和执行权限不能折成一个 `verified=true`。** 续审 README:19、27–29 要求 fresh provenance 和阶段绑定；`docs/stable-audit-transport.md`:63–67 明言本地 resolution 不是 GitHub truth；v1 planner 不证明 preview 密码学签名。离线核心可生成 upload intents、route preview/diff、模拟阶段 receipt，但它们不授权生产，不能由可编辑 JSON 跳过未来真实 resolver/验签调用。默认 disabled；假的 success 只能生成 TEST ONLY/simulated receipt。保留候选历史控制、seal 控制及当前 activation 控制的独立字段/摘要，不把 producer 历史字节改成当前配置。既有客户端 receipt 的两个 false 标志保持原样，由外层真实交付 receipt 组合证据。

3. **具体云 wire/API 与 IAM 仍需真实接线；不要借离线切片扩权。** 续审 README:45、53 要求 versioning 与 endpoint 事实，而原上传角色只有 Put/Get；谁实际读取版本状态及其最小只读授权尚须接线明确，不能把 bucket 管理权加给 uploader 来“解决”。先定义固定资源身份绑定的 preflight 结果边界与 fake 响应，缺失/旧状态不得执行生产。路由编译应先产出完整有序匹配表和待适配配置差异；SDK 才负责实际 API 参数、ConfigId/错误响应/规范化。必须处理前代残留受管规则，不能只追加新规则后把部分回读当目标状态；遇到未受管配置、必要但未授权的删除/修改或无法表达的规则，拒绝该激活计划，不更改其他域名/RAM/DNS/TLS。以上是原 before/after fencing 与最小权限约束的落实，不是新发布门槛。

4. **“未完成”可能已有云副作用。** 续审 README:47 和原 D:112 覆盖部分更新、刷新超时与回滚。最小状态机须区分未上传、部分/完整对象读回、未激活、激活部分失败、配置已读回但公网/客户端失败、完成；失败 receipt 保留已观察副作用及前代 composite，不假称恢复旧状态，也不自动删除对象。上传全部确认后才允许可执行激活；路由 preview 可提前生成但不可执行。模拟成功不等于真实云完成，不承诺 CDN 原子。

## 可以立即实现的窄范围

- **一个共用消费者，不造新发布平台。** 输入为已受审阅的原 v1/v2 descriptor 文件、其重算 plan 与摘要、本地受限对象来源，以及显式 disabled 的资源意图。现有 archive extractor、candidate/signature/provenance helper 和客户端 verifier继续作为既有验证入口。严格保持其能力边界；不要另写签名/四客户端。最小函数级 seam 足够，不需插件注册表、通用工作流引擎、凭证管理器或新的总 schema 体系。
- **精确 materializer。** 每个 `snapshot_objects`/`immutable_aliases` 由其 `source.audit_release_id/member` 找到原已验 archive 成员；v2 mirrorlist 仅用 `content_utf8` 精确 UTF-8。entrypoint 引用现有对象，不是另一份需凭空生成的 payload。尤其 B 路径的 A.rpm 从 A 的真实来源取，不能按目标快照 ID 找文件。对象 key 去重必须同时同 SHA/size/cache-control，源身份保留；复用原安全提取和资源上限，避免为每个 alias 重复展开整份 archive。
- **create-only 的轻量 fake API 状态机。** Put intent 固定禁止覆盖；已有/超时后恢复均 GET 实际 bytes+size+约定 cache metadata，不凭 ETag/HEAD/saved receipt 放行；异字节或 metadata 冲突即停止，无 overwrite/delete。用按 exact object key 列出的对象集合，不引入任意 bucket/path 或运行时 List 的需求。失败后重新验证输入和 remote 事实，不能靠 journal 将旧成功视为当前真相。
- **同一请求表驱动路由和测试。** 显式 `/` → 当前 preview 的 `/index.html` 同一对象；清单精确 entrypoint/alias 优先，最后只允许原 snapshot 对象直达。#74 的 `public/snapshots/...` alias 必须先于 snapshot passthrough；不能把所有 Packages 盲加 public。保留默认 `/repo` preview、按对象 cache-control、非递归/锚定匹配；合并规则前后逐个 URI 等价，并测试邻近错误 URI不误命中。原提案 provider 限额可作为明确的编译能力输入；不能把本地模拟宣称 CDN 实际语义已验证。
- **阶段 receipt 与调用 seam。** 绑定输入/plan、source/audit/control、逐对象确认、目标资源、before/after 配置摘要、公开入口检查、原客户端 receipt 摘要和 exact target。通过 fake verifier 检查调用顺序/参数与失败传播即可；本切片不重跑 49、不运行公网四客户端。生产 false，无法写出 production-complete。

这仍是原 D 的无凭证部分。启用实际 uploader/activator 时，再针对原有同一个 seam 加 OSS/CDN SDK；不需要返写 planner 或签名 archive。

## 真实事实挡住哪里

| 尚缺事实/授权 | 不阻塞的离线工作 | 阻塞的真实动作 |
| --- | --- | --- |
| 实际 UID/bucket/region/endpoint、版本状态、Mac OSS API 适用性 | 原对象展开、固定 key 请求意图、冲突/中断测试 | 真实 Put/Get、create-only 保证与资源越界检查 |
| CDN CNAME/config IDs、原配置、HOST/SNI、回源角色策略、API 参数/规则限额与回读形态 | 精确路由请求表、顺序/合并等价证明、部分失败状态机 | 配置写入、未缓存回源、真实缓存/规则行为 |
| DNS/TLS/备案/域名归属、费用及资源审批 | disabled 目标字段、缺值拒绝测试 | 首次接入、公开切换、采购/权限改变 |
| 真实 source Release、Mac签名 candidate、immutable audit、当时 fresh preview/retained | 复用现有 TEST ONLY fixture 与拒绝测试 | 真实发布 provenance、公开 exact version + installed CLI 验收 |

¥100/7日仍只是资源提案，ECS 预算不能代用；这是既有权限边界，不新增总审批阶段。未知云值保持 null，不代填、不读取私钥/Keychain/NAS。真实 Linux/native 与云端验证绝不能由离线 receipt 替代。

## 最小证据

本目录 `inputs/` 仅冻结三个公开设计文本；`source-excerpts.txt` 保存本次引用的固定 HEAD 代码与合同节选；`source-freeze.json` 记录完整相关公开文件 SHA 与原文摘要；`failure-inventory.md` 是建议先行红病例，未执行；`ARCHIVE_ALLOWLIST.txt` 和 `archive-sha256.json` 是小型归档白名单。没有保存凭证、密钥、账户盘点、候选二进制或客户缓存。
