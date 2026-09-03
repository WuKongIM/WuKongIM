# Changelog

WuKongIM release notes are maintained here. User-visible pull requests add
their entries under `Unreleased`; before a tag is created, release maintainers
move those entries into a version section named for that exact tag.

## [Unreleased]

### 🚀 New Features / 新功能

- 签名 Linux Preview 软件源新增 `wukongim-archive-keyring` 与 `wukongim-release` 引导包，首次配置后可直接通过 APT 或 DNF/YUM 安装和升级 WuKongIM，并由包管理器自动接收后续公开签名证书更新。

### 🐛 Bug Fixes / 问题修复

- 文档 CDN 现在仅对静态导出的页面 RSC `index.txt` 从缓存键删除 `_rsc`，并在构建期验证每个已发布路由都有独立静态载荷；章节跳转不再因一次性 RSC 查询值反复触发高延迟回源，同时保留图片及其他查询参数的缓存隔离。
- 文档 Pages 自定义域名迁移与回滚现在会先暂存已验证产物，在域名绑定变化后立即重新部署，并以绕过 CDN 的根路径、双语首页、深层页面及搜索真实 GET 作为内容就绪门禁；证书批准或 API `204` 不再被误当作站点可用。
- 多副本频道 Leader 重启后，最近会话重试会在持久化提交 checkpoint 落后于本地日志时按当前元数据激活冷 runtime 并恢复 quorum 高水位，不再把仍存在的会话返回为空。
- 多副本频道的消息拉取现在会在持久化 checkpoint 落后时使用活动 Leader 已确认的提交高水位，发送成功后的首条消息可立即出现在频道与最近会话同步结果中。
- 文档 CDN 证书检查现在根据显式公网路由模式和多个公共解析器的直接 CNAME 答案决定是否验证阿里云边缘证书，不再将供应商 `DomainCnameStatus` 误当作公网切流证明。
- 文档 CDN 证书检查现在正确接受“已安装证书且暂无需续期”以及强制初始化时“尚未安装证书”的布尔结果，同时仍会拒绝缺失或类型错误的状态字段。
- 文档 CDN 证书轮换现在兼容阿里云已启用的手动上传证书省略 `Status` 字段的响应，同时仍会拒绝免费或未知类型的空状态以及尚未生效的证书状态。
- 文档 CDN 的 ACME 账户初始化现在使用固定生产端点和已审阅条款的账户专用流程，不再因合法邮箱或 Let's Encrypt 省略可选联系人字段而失败，也不会在初始化账户时误发起证书申请。
- WKProto 编解码器现在会安全处理空输入并拒绝未知帧类型编码，避免畸形输入触发越界崩溃或静默产出空报文。
- 插件热重载监视器现在在启动后立即停止时保持完成信号的稳定引用，避免并发清理将其置空后引发 `close of nil channel` 崩溃。
- Issue Agent 验证器现在会在判定工作目录越界前规范化 checkout 根路径，避免 macOS 上 `/var` 与 `/private/var` 别名导致合法子目录被误拒。
- JSON-RPC 解码器现在会将未知通知方法统一归类为 `ErrUnknownMethod`，与未知请求的错误分类保持一致，便于调用方稳定识别协议错误。
- JSON-RPC subscribe/unsubscribe 请求现在会在协议适配层转换为带正确 action 的 `SUB` 帧，并将可用的 `SUBACK` 关联到原请求；当前 Product Gateway 仍未发布 `SUB` 入站能力。
- 权限元数据批量读取现在会在进入 Slot 代理前拒绝未知读取类型，并保持其余合法结果的原始对齐，避免无效类型被下游结果覆盖或污染整批授权证据。
- Controller Slot 副本迁移与 Leader 转移在运行时未启动时现在统一 fail closed 为 `ErrNotStarted`，不再根据空状态返回误导性的业务校验错误。
- Slot FSM 现在会为确定性的过期元数据提案持久化已应用水位，节点重启后不再重复回放已经判定为无操作的 Raft 日志。
- 元数据存储关闭后清理终态频道迁移任务现在返回关闭错误，不再因访问已释放的底层数据库而触发空指针崩溃。
- 消息恢复后缀替换现在会在写入前校验保留边界的 Proposal 与 Entry 身份一致性，检测到损坏时拒绝替换并保留原有后缀。
- 元数据备份与恢复现在拒绝夹带运行时或迁移状态、跨注册 span 乱序及重复键的快照，并在完整性预检失败时保留目标端原有数据。
- 完整备份发布现在会在写入任何仓库对象前校验全部 256 个 Hash Slot 的完成进度，避免不完整任务提前绑定空仓库或留下发布副作用。
- Controller Raft 启动时若物化状态文件丢失且无可用快照，现在会从保留 WAL 重建；若日志已压缩且快照数据缺失则拒绝启动，避免以空状态继续运行。
- 集群节点停止时现在会撤销路由、Slot 与频道就绪状态，并阻止在途控制快照在停止后重新发布就绪，避免停机窗口暴露错误健康状态。
- 应用启动失败回滚现在会先关闭已开放的 Prometheus、Manager 与 API 入口，再停止备份调度运行时，避免回滚窗口继续接受新的管理请求。
- 阿里云 Lease 盘点、主机创建与身份移除现在会拒绝子资源角色冲突、跨实例磁盘响应及无法由 SDK 错误码证明已删除的身份资源。
- 阿里云仿真账户 Bootstrap 现在会安全处理官方 SDK 错误响应，并仅依据结构化错误码判断资源不存在，避免错误路径崩溃或把普通服务与传输故障误判为已删除。
- 阿里云只读权限探针现在兼容官方 SDK 的两种结构化 403 错误类型，合法 RAM 拒绝不再被误判为探针失败。
- 消息备份流回放现在会拒绝 `log_start_offset` 超过提交高水位的非法 checkpoint，避免校验和合法但语义损坏的快照进入恢复流程。
- 云部署离线文件适配器现在会拒绝负数读取与清单上限，并正确处理最大整数读取边界，避免非法参数触发 panic 或把已有文件静默读成空内容。
- Slot 代理现在会沿包装错误链识别“Slot 不存在”，避免上游附加上下文后被误分类为“暂无 Leader”并返回错误的 RPC 路由状态。
- WKDB bundle 导出现在会拒绝无法表示为 `int64` 的无符号 inspect 字段，并保留非法频道目录状态的 `ErrValidation` 分类，避免损坏或类型异常的数据被静默回绕或失去可识别的验证错误。
- Cloud View 现在会按原始文件大小严格拒绝超过 256 KiB 的配置和超过 64 KiB 的运行状态，即使超量部分仅为尾随空白，也无法再绕过文件上限。
- Cloud Simulation 现在会在创建任何付费资源前校验完整的 Run Locator 参数，并按原始输入大小拒绝超过 64/128 KiB 的请求与阿里云配置，避免无效命令留下资源或通过尾随空白绕过上限。
- Cloud Host 在线与离线安装现在都会在执行任何主机副作用前拒绝无效的远程根目录前缀，避免参数错误导致部分安装状态残留。
- Cloud Bundle 现在会按完整输入大小拒绝超过 128 KiB 的部署 spec，合法 JSON 后追加尾随空白也无法再绕过上限。
- Gateway 会话的 `LoadOrStoreValue` 现在保留已存储的 `nil`，并在并发初始化保留热键时只允许一个调用方取得写入权，避免会话状态被后续竞争者覆盖。
- Cloud Analysis Bridge 现在会拒绝固定 PEM 证书前的非空白数据，避免额外内容被 PEM 解析器静默忽略。
- Review Agent GitHub 适配器现在保留取消与超时错误身份，并严格限制写操作响应体大小，避免上层误判重试语义或读取过量响应数据。
- Cloud Analysis 诊断结果现在会拒绝 NaN 和无穷大置信度，确保分类一直满足 `[0,1]` 约束。
- `wkcli bench` 现在会拒绝无法安全表示的超大 payload 尺寸，并将帮助与错误文本写入命令注入的对应输出流。
- Raft 日志现在会将 `leader lost` 归类为 `leader_change` 事件，避免 Leader 丢失信号被误计入普通日志。

### 🔧 Improvements / 改进

- 原生 Linux 包 CI 现在会在 Ubuntu 24.04、Debian 12、Rocky Linux 9 与 AlmaLinux 9 的真实 systemd 环境中验证配置初始化、健康检查、显式启停/重启、活动卸载、状态保留及重装不自动激活。
- 文档发布新增默认关闭的阿里云 CDN 定点刷新与 Let's Encrypt DNS-01 边缘证书轮换支持；两条路径使用独立的 GitHub OIDC 角色，不在仓库保存长期阿里云凭据，且在完成外部配置和切流前不会改变现有 GitHub Pages 服务。
- Chat Lifecycle 正式演练启动器、正式收尾器和通用 Cloud Lease 回收扫描现在仅在存在 transition、handoff、付费资源生产者或云端库存期间启用；取得完整空闲与零库存证明后会自动停用，并在下一次 transition、停止请求、精确清理或付费 Acquire 前安全恢复；完整 Artifact 盘点会重试短暂的 GitHub API 分页错误。

### 🔒 Security / 安全

- 二进制发布的手动恢复现在必须从目标版本的精确 tag ref 启动，并同时绑定事件提交与 Workflow 提交；从 `main` 为其他 tag 生成无法被软件源信任的 provenance 会在构建前失败。

### 📚 Documentation / 文档

- Linux 服务端部署文档将软件源安装明确收敛为“添加软件源、更新索引、按包名安装”三步；首次添加后不再手工下载特定版本的 WuKongIM deb/rpm。
- Linux 服务端部署文档改为优先使用 APT/RPM 软件源引导包，保留首次 HTTPS 信任边界并说明引导包不会运行脚本、访问网络或关闭签名检查。
- Linux 服务端部署文档现提供 `v3.0.0-beta.6` 签名 Preview 软件源的 APT 与 DNF/YUM 安装流程，并在写入专用 keyring 前固定核对仓库主密钥指纹。
- 中英文 v3 文档站现由仓库内 GitHub Pages 工作流执行完整静态验收后发布到 `docs.githubim.com`，发布产物与通过验收的 `docs-site/out` 保持一致。
- WuKongEasySDK 中英文文档现固定 Web `2.0.4`、Android `1.0.5`、iOS `1.1.1` 与 Flutter `1.1.0` 正式包，并补充四端 example 与正式包的独立验收回执及可复现流程。
- Docker 服务端部署现在提供精简的 `docker run` 与 Docker Compose 两种流程，共用最小 `wukongim.toml`、持久数据卷和完整配置参考；中英文教程删除远程一键安装脚本及其自动版本解析说明，保持两步完成启动和就绪验证。
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
