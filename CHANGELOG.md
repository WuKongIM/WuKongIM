# Changelog

## [Unreleased]

### 🔧 Improvements / 改进

- Chat Lifecycle 演练收尾定时器现在仅在付费演练或待清理 handoff 存在期间启用，取得全局空闲与零库存证明后自动停用，避免仓库空闲时持续产生 GitHub Actions 运行。
