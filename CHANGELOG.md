# Changelog

## [Unreleased]

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
