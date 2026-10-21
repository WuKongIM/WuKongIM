# 包分发云资源当前只读清点

观察窗口：2026-10-21 10:19–10:24:37 UTC。只读使用既有 Chrome 阿里云登录会话及公开 DNS/TLS 请求；未创建、修改或授权任何云资源，未读取私钥或凭据，未提交证书/DNS/配置表单。控制台记录为必要元数据的人工规范化转录，不是 API 导出。

| 项目 | 本轮当前事实 | 对未来资源 diff 的影响 |
| --- | --- | --- |
| 公开入口 | `packages.githubim.com CNAME wukongim.github.io.`，观察 TTL 600；`/status.json` HEAD 200，Server `GitHub.com` | 仍为现有 GitHub Pages 入口；迁移需冻结当时实际 preview 字节及旧 CNAME。 |
| HTTPS | TLS 1.3，主机名/证书链校验成功；Let's Encrypt YR2，SAN 仅 `packages.githubim.com`，2026-09-01 至 2026-11-30 UTC | 这是当前公开端点证书，不能证明它可被部署到阿里云。公开证书 PEM 和 SHA 已留存。 |
| OSS | 全部 Bucket 列表空搜索，1/1 页，4 个 backup/test Bucket；北京 3、杭州 1；无专用分发 Bucket | 不能复用这 4 个 Bucket；新分发 Bucket 的名称和 region 仍须具体化。 |
| CDN | 全业务/全区域空搜索，共 2 域名；`docs.githubim.com` 正常，另一无关域名停止；没有 `packages.githubim.com` | packages CDN 的 CNAME、scope、origin、配置 ID 均尚未存在于当前观察。 |
| 文档 CDN 隔离 | docs 为全球加速，主 CNAME `docs.githubim.com.w.kunlunaq.com`，源站 `origin-docs.githubim.com:80` | 保持现有文档域名、源站和证书配置不变；不复制其整套配置作为包分发实现。 |
| 阿里云 HTTPS 证书 | cn-hangzhou V2 正式/测试均 0，上传 1；V1 正式 0、测试 11，全部过期（2 页）。唯一已上传的有效证书绑定 docs，ID `27000373-cn-hangzhou`，Let's Encrypt/RSA_2048，UI 到期 2026-12-01 | 在已检查 scope 内无 packages 或通配域名的可用证书；需要独立确定 packages 证书/绑定。未下载证书包或查看私钥。 |

OSS 的 4 个 Bucket 名称/地域/versioning 列表、CDN/证书精确必要元数据及控制台 URL 见 `console-observations.json`。控制台日期未标时区，保持原字符串。公有 CLI 原始结果保留在 `dns-*.json`、`public-head.json` 和 `public-tls.json`。

本机 A 查询/连接 peer 为 `198.18.3.216`，属于非公有地址；不能据此报告公网源站 IP。CNAME、HTTP 服务器字段和验证后的 TLS 是本次能够证实的公开观察。其他证书 region/账号、IAM/服务关联角色、Bucket ACL、备案接入条件及价格均未检查；不将未知当作已满足。

既有 `storage-design/design.md` 第 3/5/6 节与 dependency-matrix 第 4/5 步要求一个专用私有 Standard Bucket，按 snapshot/transport/public 前缀分离上传、回源和激活权限；不启用依赖 forbid-overwrite 时不兼容的 versioning，不修改 docs CDN，不用 DNS/刷新成功替代 exact bytes 与四客户端验收。原设计关于缺少 stable 密钥/必须硬件保管的历史文字已由 dependency-matrix 明确取代，本清点不再引入该要求。两文档 SHA 冻结在 `document-freeze.json`。

`resource-diff-inputs.json` 列出已知基线、必须具体化的资源字段和保留边界；本轮没有选择或开通新 region/scope、申请证书、创建 Bucket/CDN、变更 DNS 或 IAM。精确未来 diff 应从这些未知项继续，不把 docs 资源或四个备份桶当作已经就绪的包分发设施。

归档仅包含 allowlist 指定文件与 SHA 清单；未归档账号余额、流量统计、无关域名身份、登录数据、完整控制台页面或私钥。所有公有信息均为该时点观察，不代表后续不会变化。
