# Stable 离线候选后的最短上线路径

截至 2026-10-21T09:44:23.725812+00:00。沿用已审阅 [storage-design](../storage-design/design.md)，本补件只收敛依赖，不增加产品 GA 指标、不执行云/签名/发布动作。代码参考当前候选分支 `d79bff5219f5a69872bb1720cbbaa224b1c7b3eb`，不据此宣称 #72 已合并或真实 native CI 已通过。

**最短顺序：候选闭包验收 → 外部不可变 audit 封存与 stable transport 消费能力 → 完整快照上传和未激活验收 → preview 同字节迁移/稳定入口激活 → 四个公网客户端精确版本及 installed CLI 验收。** 云资源准备、封存/transport 代码与本地 A/B 测试可并行推进；产品长稳等既有资格项仍由主任务独立完成，不能被签包进展替代。

原设计第 6/8 节中的“stable 密钥未创建、需要离线/硬件保管”已被后续事实取代：APT/RPM 独立生产公钥已登记，Mac 本地加密备份、Keychain 与恢复后角色签名验证已有公开收据；当前 sign 子钥至 2027-01-19 到期，正式签署时仍按既有有效期规则检查。用户选择的是日常 Mac 本地保管，NAS 可备份；不是离线仪式，不再要求第二台设备、外置盘或另找负责人。此收据不等于任何 v3 正式包已签署。原设计留作历史，不覆盖它。

## 依赖矩阵

| 步骤 | 下一项实际能力 / 完成证据 | 当前依赖与可继续范围 |
| --- | --- | --- |
| 0. 完整候选 | 完成 #72 的真实 Linux TEST ONLY 签名、archive 恢复和负例；代码合并后冻结 producer/control 身份。生产目标确定后，沿已有 stable source preflight 读取七资产与原证明，Mac 显式签名，独立验签。 | 不再缺签名设备/独立公钥。TEST ONLY / mock 不能冒充生产签名、来源证明或 Release ID。正式目标版本/source SHA 尚须主任务完成资格判断并按既有发布授权执行。 |
| 1. 外部 audit seal | 扩展现有 audit 的**外层**封存/读取契约：绑定原 candidate archive/receipt/snapshot digest、source/control/producer 身份及由 GitHub 实际返回的 immutable Release/asset IDs；读回下载并独立验签。复用既有 draft→bind→seal fencing / 不能覆盖已存在资产规则。 | 可先离线实现与 API 替身失败测试，无生产密钥/云信息依赖。现有 preview reserved tag/two-asset 发布器不能直接接 v4/v3 candidate；必须明确新增 stable 受审分支。内部 null IDs 不回填，已签 APT/RPM 字节与原 archive 均不改。实际 draft/tag/asset/immutable publish、稳定命名空间保护/权限变更，留到精确方案获相应授权。 |
| 2. stable + preview transport | 在现有 planner 上增加显式 sealed-stable 输入与 composite 公共路由；每代独立锚定 seal/archive/控制文件。生成确定性对象/入口/保留集合，保留旧代 by-hash、payload 和完整 RPM snapshot。实际生产消费必须拒绝 unsealed/test-only 候选。 | 可离线实现，与第 1 步接口一并审阅，避免只新增演练。现有 planner 仅 preview add_release，不能改个 channel 就称支持 stable。上传/激活不进入纯 planner；首版不做删除/GC。 |
| 3. 四客户端 + A/B | 将现有 public client validator 显式参数化 stable，复用固定 Ubuntu 24.04、Debian 13、Rocky Linux 9、AlmaLinux 9 镜像与 installed CLI 检查；用真实签名 A/B 双代 fixture 验证 APT by-hash 与 DNF mirrorlist。 | 可用 TEST ONLY 本地网络 fixture / 既有无凭证 CI 完成。必须保留实际 mirrorlist，不能沿 preview helper 把它改成固定 baseurl 后宣称验证了正常 stable 客户端。记录请求路径/选中代/字节与拒绝原因；默认 preview 行为保留。 |
| 4. OSS/CDN 配置 | 一个专用私有分发 bucket + `packages.githubim.com` CDN 回源、TLS、CNAME、缓存/路由；受审 prefix 与最小角色。先给出精确资源 diff 和恢复配置。 | 尚缺 bucket 名/region、CDN 加速范围、Origin Host、证书绑定与当前 CNAME 的实测值。已有 Chrome 登录是可继续只读核对的账号上下文，不代表专用资源已存在或收费/权限/DNS变更已批准。不得把备份 bucket 当分发 bucket。 |
| 5. 上传与激活 | uploader 只创建目标对象、遇同 key 核对完整 bytes/metadata、上传后 GET 验 SHA/size；所有对象可读且未激活客户端通过，再冻结旧入口、切受审路由并回读。 | 可以先写 adapter/mock tests；实际需已确认资源/精确角色和该次上传/配置/DNS授权。CDN 刷新、ETag、一次 HTTP 200 都不替代字节与客户端证明；不存在已证实的跨对象或全球 POP 原子事务。 |
| 6. 公网验收 / 回滚 | 对 exact target 运行四 clean clients：签名更新、认证下载、bootstrap 内容与二次安装兼容；再在独立受限容器装这些已核对字节，验证 server/CLI version + source SHA + release build source、固定 bench/query/migrate cases。 | 代码/TEST ONLY 可先就绪；最终证明必须来自真实公开域名及 sealed snapshot，不能由文件协议演练替代。旧合法版本通过签名仍不算新 target 发布成功。失败保留完整证据，回到已验签前代 transport；不自动降级已安装版本。 |

第 1–3 步是下一段可以立即继续的实现工作：优先交付**可消费真实外部 seal 的 stable/composite planner 和同一条四客户端路径**，TEST ONLY A/B 是验收手段。无需再造跨机签名请求/回执交换系统；本地签名核心已存在。第 4 步只读清点可独立进行。不要先启用 stable 再补第 1–3 步能力。

## 生产动作前需具体化的事项

这些是已有发布/受保护资源要求的执行点，不是新增多层审批。已有同范围授权继续复用。

1. **资源/权限方案：**给出 exact bucket/region、域名/HTTPS证书、origin 与 CNAME、有限缓存/改写规则；说明收费资源。上传身份仅目标 prefix Put/Get，回源仅读专用 bucket，激活身份仅该 CDN domain 配置/刷新；常规角色无 Delete、RAM/证书/DNS管理和 stable 私钥。一次性 RAM/服务关联角色与 DNS/TLS变更单独列清，不借日常发布扩权。
2. **GitHub stable audit/控制：**明确真实 source Release/tag/commit、受审 control/producer SHA、原 candidate archive digest、由 API 实际分配的 audit ID，以及新 stable tag 保护规则。先准备可审阅 diff，再在授权范围内创建/绑定/上传/封存；不得沿用 preview tag 或假造当前不存在的 ID。stable enabled/公开 transport 指针变更属于后续精确控制变更，不包含在候选成功中。
3. **preview 迁移 + 激活：**先冻结当时的最新 preview immutable audit（不硬用历史 beta.24 ID），将相同 bytes 放到新源站；切 DNS 前验同 Host/TLS 和真实回源。保留 Pages 旧快照，DNS 混合期两边一致。再单独激活 stable。保留 `/repo` 当前 preview 默认；如果选择 `/repo-stable` 等显式入口，先纳入受审 transport 公开路径。不要在存储切换时暗改默认频道。
4. **回滚：**冻结旧 CNAME/CDN ConfigId、原公开入口/transport digest、合法前代完整对象。stable 已上线后不能无条件把整个域名退回 preview-only Pages；优先激活 OSS/CDN 前代 composite。第一代 stable 失败时保留 preview、停止宣称 stable 发布完成，而不是暴露未验收候选。

## 必须先写的运输失败场景

外部 seal 与 archive/snapshot/control 不匹配、可变/意外资产 audit、unsealed/test-only 混入生产；同 URI 不同 bytes、缺旧代 by-hash/RPM引用、Packages.gz 错分 immutable；APT 旧 InRelease + 新浮动索引、DNF 读 mirrorlist A 后入口切 B、repomd A + `.asc` B、普通 metadata 同名跨代；上传中断/409字节冲突、权限拒绝、错误回源被缓存掩盖、部分路由更新/并发控制变化、刷新超时/304/404旧缓存；四客户端安装到旧合法版本、错误 source SHA、回滚不完整。结果应是选中 A/B 某一完整可验代，或预期安全拒绝，绝不关闭签名/SSL/摘要校验。具体 OSS forbid-overwrite / versioning 限制和缓存规则沿原设计，实施时按目标资源再次核验。

## 源码依据与原设计来源

- [当前冻结候选合同](https://github.com/WuKongIM/packages/blob/d79bff5219f5a69872bb1720cbbaa224b1c7b3eb/docs/release-contract.md#L597) 第 597–720 行：明确未封存、外部 plan、独立验证和外层 seal 边界；[Mac custody](https://github.com/WuKongIM/packages/blob/d79bff5219f5a69872bb1720cbbaa224b1c7b3eb/docs/release-contract.md#L417) 第 417–426 行（精确源摘要见 `source-freeze.json`）。
- `docs/object-storage-planning.md`：现有 planner 为 preview/plan_only，稳定/多渠道/激活均未支持；`scripts/validate-production-package-clients.py:118–137,261–323,1267–1274`：四镜像、现有 preview baseurl 路径和真实能力缺口。
- `docs/release-contract.md:162–182,484–527,559–594`：stable 不上 Pages、四客户端必须 exact target、immutable audit/fencing、installed CLI 门禁。
- 原设计已验证的官方资料继续适用为设计依据，不在本轮重复联网确认：[OSS 单对象 PutObject](https://www.alibabacloud.com/help/en/oss/developer-reference/putobject)、[CDN 回源改写](https://www.alibabacloud.com/help/en/cdn/user-guide/rewrite-urls-in-back-to-origin-requests)、[CDN 缓存/刷新](https://www.alibabacloud.com/help/en/cdn/user-guide/refresh-and-prefetch-resources)、[DNF mirrorlist 配置](https://dnf.readthedocs.io/en/latest/conf_ref.html#repo-options)。
