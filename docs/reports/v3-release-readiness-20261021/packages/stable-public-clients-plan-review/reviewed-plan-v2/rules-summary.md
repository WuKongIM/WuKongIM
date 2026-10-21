# 本轮适用规则

已读取项目 AGENTS.md、packages docs/release-contract.md、docs/stable-audit-transport.md、docs/object-storage-planning.md、两条只读/Pages工作流与 CODEOWNERS，并核 package 工作树没有额外 AGENTS.md / FLOW.md / workflow README。精确SHA见 source-freeze.json。

先失败库存/测试后实现；复杂客户端采用真实原生E2E且产生可复核收据。保留preview兼容；发布必须签原生渠道并公网exact版本四客户端/installedCLI完成。stable继续disabled；生产Secret、环境权限、渠道、审计tag保护、云/DNS/CDN等都不在该实现范围。工作流需要读取本项目release合同；不复制WuKongIM ReviewAgent命名检查作为新政策。#73的已有producer身份不得因本切片被偷偷更改。

本轮只读代码和本地计划文件，没有GPG、Docker、测试、编译或云操作。两个并行独立读者对A/B真实不同bytes、preview根入口隔离、12producer不改达成一致；最终计划尚待root/独立review确认。
