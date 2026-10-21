# packages 阿里云接入：待审批资源建议

截至 2026-10-21 11:02:37 UTC。**本次仅准备方案，未创建资源、申请证书、修改权限/DNS/CDN或付款。** packages 代码依据为 PR #73 `59549efe1074562928b8ae175faeda8778849e0e`，仍属候选能力；本方案不代表 stable 已启用或发布完成。

## 建议采用的最小配置

| 项目 | 推荐值（均为建议，尚未创建） | 已知条件与待返回字段 |
|---|---|---|
| OSS | `wukongim-packages-prod-hz`；华东1杭州 `cn-hangzhou`；Standard/LRS；private；版本控制保持从未开启；不设清理生命周期、WORM、传输加速、KMS或静态网站托管 | 名称可用性未核，创建失败不能擅自换名；取得实际 Bucket/Endpoint。版本控制开启或暂停后，禁止覆盖请求头均无效，故此处选择未开启。[PutObject](https://help.aliyun.com/zh/oss/developer-reference/putobject/) |
| CDN | 现有产品 **CDN**，新增 `packages.githubim.com`；业务类型“下载分发”；推荐“全球”范围；不启用另一个“全球资源计划”或迁至 ESA/DCDN；沿用账号现有计费方式，先不买资源包 | 全球包含大陆，必须先取得 `githubim.com` 有效备案与域名归属证明；本次未核实。新 CNAME 必须使用创建返回值，不能仿造 docs 域名后缀。[接入条件](https://help.aliyun.com/zh/cdn/add-a-domain-name) |
| Origin | 源站类型 **OSS 域名**；单一主源、权重10、HTTPS/443；预期主机 `wukongim-packages-prod-hz.oss-cn-hangzhou.aliyuncs.com`，HOST/SNI均取创建后真实 Bucket 公网域名；同账号 STS 私有回源 | 此地址是命名规则推导，非已存在 Endpoint。源站类型不能误设普通域名；HOST/SNI必须实测，避免缓存掩盖错误。[OSS 接入](https://help.aliyun.com/zh/oss/user-guide/cdn-acceleration)、[源站设置](https://help.aliyun.com/zh/cdn/user-guide/configure-an-origin-server/) |
| TLS | 推荐重新取得 **packages 专用 Let’s Encrypt RSA 2048 证书**，DNS-01验证后经 CDN“自定义上传（证书+私钥）”部署，沿用 docs 已有的第三方证书接入方式 | 新 TLS 私钥独立生成；现有 GitHub 公网证书不能当作持有其私钥。需 DNS TXT 修改及部署授权，但此次均不执行。取得实际 cert ID/region、SAN、到期时间和部署记录；记录续期方式。[CDN上传要求](https://help.aliyun.com/zh/cdn/user-guide/configure-an-ssl-certificate/)、[免费签发](https://letsencrypt.org/getting-started/)、[DNS-01](https://letsencrypt.org/docs/challenge-types/) |

这里不要求额外签名设备、另一名保管人或新的密钥流程。上述 TLS 与已准备的 APT/RPM stable 签名密钥分属不同用途。可选的省维护方案是购买 CAS 正式单域名 DV 证书；尚无实际报价，不把付费证书列为发布硬条件。阿里云“个人测试证书”有用途限制，不将其默认用于生产；这与第三方 Let’s Encrypt 证书不是同一产品。[阿里云证书选型](https://help.aliyun.com/zh/ssl-certificate/functions-and-features)

## 为什么建议新桶，而非直接否定已有桶

10:24:37Z 只读清点确认现有4桶均用于备份/测试：两个 qualification 桶（含唯一杭州桶）已开启版本控制，**与本次 create-only 对象协议不兼容**；另两个北京桶未开启版本控制，技术上可能复用隔离前缀，本次没有读取对象/ACL/生命周期，不能断言其不适用。选择新桶的具体收益是：不用改变备份策略；CDN 私有回源会让其源桶对象可经加速域名访问，专用桶能保证里面只有拟公开的仓库/audit对象；避免共享删除/清理策略影响历史包。新桶是减少接入不确定性的实现选择，不是额外 GA 门禁。[私有回源范围](https://help.aliyun.com/zh/cdn/user-guide/grant-alibaba-cloud-cdn-access-permissions-on-private-oss-buckets)

## 路由、缓存及最小权限

上传器逐项使用 planner 的 `object_key`、SHA-256、size、`cache_control`；不重签或改 candidate 字节。路径和缓存依据 [精确代码](https://github.com/WuKongIM/packages/blob/59549efe1074562928b8ae175faeda8778849e0e/scripts/plan-object-storage-publication.py#L429)：

| 对象/公网入口 | OSS 路由与缓存 |
|---|---|
| `/snapshots/{preview,stable}/r<ID>-<snapshotSHA>/…` | 相同对象路径直达；保留各代全部对象，不删除。**不能给整个 snapshots 前缀强制长缓存**：按每项清单，部分 site 元数据仍为 `no-store`。 |
| 清单 `immutable_aliases` | `/原公开路径` 回源到 `public/原公开路径`；版本化包、APT by-hash、完整摘要命名 RPM metadata：`public,max-age=31536000,immutable`。旧代别名保持。 |
| 清单 `entrypoints` | 精确 URI 回源至当前 snapshot 对象；`no-store`，CDN TTL 0；含 APT Release/InRelease、浮动 Packages(.gz)、keys；preview repomd及非摘要 metadata也不长期缓存。 |
| `/rpm/stable/el/9/x86_64/mirrorlist` | 指向 `generated/stable-mirrorlists/<内容SHA>.txt`；`no-store`。文本是一行 HTTPS 的完整 stable snapshot RPM 目录；不增加浮动 stable repomd/baseurl fallback。 |
| `/`、`/repo`、`/index.html`、`/status.json`、共享 manifest | 根路径显式映射 preview index；其余按当前 preview 清单，保持 `/repo` 默认 preview。 |

CDN按清单生成**有序、锚定、非递归**回源重写，入口优先于不可变类规则，使用 `break`；不做全路径盲目 `/public/` 前缀重写。对象响应头优先，不启用“忽略源站不缓存标头”；不增加 gzip/内容改写，错误响应不缓存。官方回源重写最多50条且不改变缓存key，因此需对实际清单合并等价规则并检查限额，不能把配置批量提交称为全网原子切换。[重写行为](https://help.aliyun.com/zh/cdn/user-guide/rewrite-urls-in-back-to-origin-requests)、[缓存优先级](https://help.aliyun.com/zh/cdn/user-guide/configure-the-cdn-cache-expiration-time)

权限目标（这是待实际策略 diff，不是已授权）：

- **uploader**：仅新桶 `snapshots/*`、`public/*`、`generated/*` 的 `oss:PutObject`/`oss:GetObject`；已知清单无需 List。Put携带 `x-oss-forbid-overwrite:true`，冲突只允许相同bytes/metadata并读回；不授 Delete、BucketPolicy、版本控制、CDN、DNS、证书权限。IAM Put 本身不能承诺不可覆盖，必须由上传协议执行。[Put权限与元数据](https://help.aliyun.com/zh/oss/developer-reference/putobject)
- **origin**：同账号 STS，目标只读新桶对象，预期 `oss:GetObject`；不需要访问现有备份内容。官方默认角色 `AliyunCDNAccessingPrivateOSSRole` 的示例为 `oss:List*/Get*`、`Resource:*`，**不是最小范围**。先读已有角色，再拟定增量/受限策略并验证 Get/HEAD；不能擅自移除已服务其他域名的授权，也不声称只读单桶已配置。CDN文档支持手动自定义策略；最终实际可用范围待接入验证。[角色说明](https://help.aliyun.com/zh/cdn/user-guide/grant-alibaba-cloud-cdn-access-permissions-on-private-oss-buckets)
- **activator**：`cdn:BatchSetCdnDomainConfig`、必要配置读取、`cdn:RefreshObjectCaches`与刷新状态读取，仅 `acs:cdn:*:<实际UID>:domain/packages.githubim.com`；不含其他域名/OSS写/DNS/证书/RAM管理。读取操作的资源粒度须按各 API 实际授权表落地。BatchSet 权限粒度为域名，不能声称 IAM 限定到单个配置字段；service-linked role若缺失由一次性资源接入处理，不把RAM创建权交给日常发布器。[配置授权](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-batchsetcdndomainconfig)、[刷新授权](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-refreshobjectcaches)

## 有限实施顺序与回滚

| 步骤（待批准后实施） | 必需结果 | 失败/回滚 |
|---|---|---|
| 1. 补只读缺口，确认资源名/备案、角色、实际计费与TLS方式；创建上述空桶和 CDN 域名，取得真实 CNAME/config IDs | 保存资源 before/after；现有 DNS 不变 | 停止新增操作，空桶/未接入域名保留待清理；不影响 Pages 或 docs。不得为了重试更改已有4桶。 |
| 2. DNS-01取得新TLS，部署证书；配置OSS HTTPS私有回源、缓存/路由 | 真正回源的新对象、TLS域名/链、Cache-Control、Range读回通过 | DNS仍指Pages；恢复本次新增域名的前一配置。只撤销本次新增TXT，保留他人已有TXT。 |
| 3. fresh解析真实 immutable audit并准备 composite；上传完整当前及保留代；在不改DNS时用正确Host/SNI访问新CDN验bytes，执行 transport A/B | 部分上传/错digest/旧代/缓存混代拒绝；不可变对象读回；既有四公网客户端验收链路准备 | 不激活，保留对象；无删除。不能将缺证据 candidate当真实已封存发布。 |
| 4. 先同字节迁移 preview；将 `packages.githubim.com` 的既有 CNAME 从 `wukongim.github.io.` 改为实际新CDN值 | 迁移当刻 fresh冻结最新preview（不是固定beta.24）；Pages与OSS同bytes，TTL记录600秒只是观测值、不是生效保证 | stable尚未公开时，可恢复原 CNAME `wukongim.github.io.`及原TTL/线路；留Pages HTTPS与站点。 |
| 5. 已验证运输闭包后单独激活 stable；Ubuntu24.04/Debian13/Rocky9/Alma9公网干净客户端安装精确版本+CLI证据 | 沿用现有发布门禁；公开签验结果和来源身份齐全 | stable公开后回退**上一完整 composite OSS/CDN配置**；不能退回仅preview的Pages使stable消失，不自动降级已装客户端。 |

上表步骤3–5所需 uploader/activator、A/B及四客户端交付仍待完成；#73 的 plan-only 输出不证明云资源已验证。没有整站原子切换承诺。WORM、跨区复制、ZRS、额外审计产品、硬件保管均非这次必要条件；不增加采购。

## 费用与本次验收预算建议

**建议首次验收限7天、新增OSS+CDN+证书部署合计预算100元，不购买资源包或变更账号计费模式。** 验收主动流量控制为CDN下行≤20GB、OSS回源≤20GB、保留≤10GB、请求≤10万；这是执行预算建议，尚未获消费授权，亦不是云平台硬性扣费上限。公网开放后的真实用户流量不受此测试计数约束，账单有延迟；接近预算即暂停新增验收流量。正式长期运营另按实际用量估算。

官方文档示例费率：CDN大陆0–10TB档0.24元/GB，其他区首档约0.46–1.31；OSS LRS示例0.12元/GB/月；OSS历史计费案例回源0.15元/GB。按20GB均落最贵所列CDN区域、20GB回源、10GB存7天，约 `26.2+3+0.28=29.48元`，**再加请求、证书部署和实际价格差**。这是参考量级，不是当前账号报价或承诺封顶。Let’s Encrypt签发本身免费；CDN自定义上传路径是否另有部署费用本次未取得实际报价，预留预算，不擅自进入CAS付费批量部署。付费CAS正式DV是可选替代，必须先取得实际报价，不能把未报价证书计为0元。[CDN费率示例](https://help.aliyun.com/zh/cdn/product-overview/billing-rules-of-basic-services/)、[OSS计量示例](https://help.aliyun.com/zh/oss/understanding-billing/)、[OSS历史回源案例](https://help.aliyun.com/zh/oss/billing-examples)、[HTTPS请求费](https://help.aliyun.com/zh/cdn/product-overview/billing-of-https-requests-for-static-content)

CDN/OSS分开计费；HTTPS官方说明每月前500万次免费、超出0.05元/万次，但账号共享额度是否耗尽未查，估算不依赖免费额度。OSS请求单价/免费额度、地域流量分布、账号折扣/资源包、未来对象代数与下载量未取得；不以旧套餐或docs用量推断。[OSS+CDN计费关系](https://help.aliyun.com/zh/cdn/product-overview/billing-of-oss-content-acceleration)

## 本次只读补查结果与仍缺证据

本次补查在11:02:37Z前完成，使用现有Chrome登录态：RAM `/roles`只显示菜单/空主体，不能据此断言默认角色不存在；备案入口到达myorder但未加载业务内容，不能断言未备案；官方OSS价目页仅外层页面/iframe，未取得可核对的实际价表。未提交表单、访问AccessKey/私钥、变更登录或联系支持。归档仅保存必要的归一化状态，不收集无关账户数据。

仍缺：有效ICP备案/归属及权威DNS记录ID/线路/CAA；默认回源角色的现有策略；该账号大陆OSS公网数据API适用性（新开通用户的默认公网域名有策略限制，已有桶不能证明Mac上传可用；若适用需另行确定上传自定义域名，不能用CDN地址代替OSS写API）；实际费用/证书部署报价。创建后才能得到的字段为 bucket确认、CDN CNAME、config IDs、证书ID/有效期/部署状态、具体角色策略版本。上述真实未知值均保留null。[OSS公网策略](https://www.alibabacloud.com/help/zh/oss/user-guide/access-buckets-via-custom-domain-names)
