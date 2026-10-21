# local-shakeout 默认启动义务预算修复证据

原始 100 SEND/s 实机失败位于相邻 `local-shakeout-ed705/`，保持不变。
本目录保存首次失败前后证据提取、预声明失败清单、真实 factory/ApplyGrant
固定时钟单变量探针、先红后绿的默认/渲染回归，以及完整 named checks。

- 入口：`findings.md`、`final-check-summary.json`、`final-policy-selection.json`。
- 最终完整源码差异：`final-source.patch`；当前基线仍 ed705，尚未提交。
- 219 包 unit、vet、format、tidy、FLOW通过；同一 scripts-integration
  首次和唯一隔离复验均触及原 9m 限额，不能报告该门禁通过。
- 默认 500 SEND/s 的固定时钟证明只覆盖启动义务供给，不代表产品真实 RECV、
  热延迟或本机持续承载；新的 30m 实机验收尚未开始。
- `runner-repair/` 是 /tmp 外层清理包装的轻量假进程验证，不是产品验收。

归档只列小文本、收据、代码和日志，排除专用 `go-cache` 与任何二进制。
历史首红和两次 scripts 超时完整保留。

## 提交与后续入口

最终提交 `0672c115950f65e6d92381aa5b3ea9f0a20fc790` 包含14文件。
`delivery-summary.json` / `commit-receipt.json` 是提交时状态；
`shipping-final-source.patch` 是最终完整差异。`final-check-summary.json`
和 `final-source.patch` 保留增加 Linux job 前的11文件阶段，不冒充最终diff。

增加的独立 Linux scripts job 的先红/绿、Actionlint与 named checks 在
`linux-scripts-gate/`。它读取未改变的 frozen base policy，全量检查不加筛选、
重试或时限覆盖；现有 default/E2E job 块逐字保持不变。
新的30分钟实机运行在相邻 `local-shakeout-0672c1159-500/`，本目录只记录
启动信息，运行终局单独归档。原始100SEND/s失败和两次本机scripts超时均保留。
