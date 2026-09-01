# Changelog

WuKongIM release notes are maintained here. User-visible pull requests add
their entries under `Unreleased`; before a tag is created, release maintainers
move those entries into a version section named for that exact tag.

## [Unreleased]

### 🐛 Bug Fixes / 问题修复

- 文档 CDN 证书检查现在正确接受“已安装证书且暂无需续期”以及强制初始化时“尚未安装证书”的布尔结果，同时仍会拒绝缺失或类型错误的状态字段。
- 文档 CDN 证书轮换现在兼容阿里云已启用的手动上传证书省略 `Status` 字段的响应，同时仍会拒绝免费或未知类型的空状态以及尚未生效的证书状态。
- 文档 CDN 的 ACME 账户初始化现在使用固定生产端点和已审阅条款的账户专用流程，不再因合法邮箱或 Let's Encrypt 省略可选联系人字段而失败，也不会在初始化账户时误发起证书申请。

### 🔧 Improvements / 改进

- 文档发布新增默认关闭的阿里云 CDN 定点刷新与 Let's Encrypt DNS-01 边缘证书轮换支持；两条路径使用独立的 GitHub OIDC 角色，不在仓库保存长期阿里云凭据，且在完成外部配置和切流前不会改变现有 GitHub Pages 服务。
- Chat Lifecycle 正式演练启动器、正式收尾器和通用 Cloud Lease 回收扫描现在仅在存在 transition、handoff、付费资源生产者或云端库存期间启用；取得完整空闲与零库存证明后会自动停用，并在下一次 transition、停止请求、精确清理或付费 Acquire 前安全恢复；完整 Artifact 盘点会重试短暂的 GitHub API 分页错误。

### 🔒 Security / 安全

- 二进制发布的手动恢复现在必须从目标版本的精确 tag ref 启动，并同时绑定事件提交与 Workflow 提交；从 `main` 为其他 tag 生成无法被软件源信任的 provenance 会在构建前失败。

### 📚 Documentation / 文档

- Linux 服务端部署文档现提供 `v3.0.0-beta.6` 签名 Preview 软件源的 APT 与 DNF/YUM 安装流程，并在写入专用 keyring 前固定核对仓库主密钥指纹。
- 中英文 v3 文档站现由仓库内 GitHub Pages 工作流执行完整静态验收后发布到 `docs.githubim.com`，发布产物与通过验收的 `docs-site/out` 保持一致。
- WuKongEasySDK 中英文文档现固定 Web `2.0.4`、Android `1.0.5`、iOS `1.1.1` 与 Flutter `1.1.0` 正式包，并补充四端 example 与正式包的独立验收回执及可复现流程。
- Docker 服务端部署现在提供一键安装脚本，只需一条命令即可生成随机凭据、创建持久卷、启动固定摘要镜像并等待单节点集群就绪；未指定版本时自动选择最新 GitHub tag，也可通过 `WK_VERSION` 指定版本，中英文主流程均保持两步。
- 服务端配置文档新增独立的中英文常用配置页，以表格解释最常用的 10 个配置项；配置参考改写为可搜索的逐字段手册，为全部公开 TOML 与 `WK_*` 配置补充用途，并标明关键默认值、`0` 值、互斥、敏感和迁移说明。
- 文档站资源菜单移除官网链接，并使用聊天演示与 Manager 演示当前可用的 HTTP 地址。
- 中英文公共文档新增可访问的 Mermaid 架构与流程图，精简产品、指南和部署导航，并将已撤下的 Kubernetes 页面重定向到受支持的部署入口。

<!--
Use only the non-empty categories that apply: `⚠️ Breaking Changes /
破坏性变更`, `🚀 New Features / 新功能`, `🐛 Bug Fixes / 问题修复`,
`🔧 Improvements / 改进`, `⬆️ Upgrade Notes / 升级说明`,
`🔒 Security / 安全`, `📚 Documentation / 文档`, and
`⚠️ Known Issues / 已知问题`. Prefix the selected category with `### `.

Every category must contain at least one "- " list entry. Release headings use
the exact form: ## [v3.0.0-beta.5] - 2026-09-01
-->

## [v3.0.0-beta.6] - 2026-09-01

### 🐛 Bug Fixes / 问题修复

- 服务端二进制现在内置 IANA 时区数据库，官方最小 Docker 运行时可在 Manager 中保存 `Asia/Shanghai` 等非 UTC 备份计划，不再因运行镜像缺少 `zoneinfo` 返回无效请求。
- 二进制发布恢复流程不再使用 Actions `GITHUB_TOKEN` 无权访问的仓库管理接口，并可从精确工作流提交取得旧标签缺失的 Release Notes 解析器；不可变 Release 设置改由管理员在发布前外部核验，流程仍在发布后强制验证 Release 已封存为不可变。

### 🔧 Improvements / 改进

- GitHub Release 正文现由人工维护的 Changelog 生成，并在二进制文件与三个 Docker 镜像仓库的版本身份、摘要和平台验证完成后再公开发布。

### 📚 Documentation / 文档

- Docker 服务端部署文档已切换到三仓库同摘要的 `v3.0.0-beta.5` 非 root 镜像，并同步更新预发布风险说明。

## [v3.0.0-beta.5] - 2026-09-01

### 🚀 New Features / 新功能

- GitHub Release 新增未签名的 Linux amd64 DEB/RPM 安装包，并与四个平台的压缩包共用校验和与构建来源证明；软件源发布仍保持关闭。

### 🔧 Improvements / 改进

- Chat Lifecycle 演练收尾定时器现在仅在付费演练或待清理 handoff 存在期间启用，取得全局空闲与零库存证明后自动停用，避免仓库空闲时持续产生 GitHub Actions 运行。
- Chat Lifecycle handoff 发现现在可安全穷尽最多 20,000 个保留 Artifact，仓库中超过 5,000 个无关 Artifact 时不再阻塞空闲定时器停用。
- 官方 Docker 镜像新增 `/readyz` 健康检查和 `SIGTERM` 优雅停止契约，并显著缩小 Docker 构建上下文。

### ⬆️ Upgrade Notes / 升级说明

- 官方 Docker 镜像默认改为 UID/GID `10001:10001` 非 root 用户；命名卷可直接使用，自定义宿主机绑定目录需在升级前授予该 UID/GID 写权限。

### 🔒 Security / 安全

- Docker 运行时升级并固定到受支持的 Alpine 3.24.1 摘要，构建基础镜像同步固定摘要，镜像内 Go 安全相关依赖完成升级。
- Docker 发布流程现在会分别扫描 amd64 和 arm64 候选镜像；发现 Critical 或 High 漏洞时阻止发布，恢复发布也会重新扫描现有规范摘要。

### 📚 Documentation / 文档

- 中英文 Docker 服务端部署文档改为 Compose 优先的可验证单节点集群流程，补充固定镜像、随机凭据、端口保护、持久化、健康检查、日常运维和 `docker run` 备用路径。
