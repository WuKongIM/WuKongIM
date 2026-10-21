# Released continuation Go seam 独立复核

审查基线 `ce3c4d9110f17bb71dc99a89dc3c09758fabf980`，五文件字节在 2026-10-21 11:39:44 UTC 独立冻结，与 root-go/validation.json 逐项相同。适用 AGENTS 与 package FLOW 已读。本轮没有改源码、编译、重跑 Go 测试、访问 GitHub/云或触及凭证。

**未发现这五文件范围内的阻塞问题。** 结论只覆盖 Go 对已受信材料的严格解析/绑定；不代表尚在实现的 helper、消费者、API provenance、默认入口防重放或付费操作已通过。

- 默认 nil 分支仍是 attempt1；rehearsal/repair 原 prior=0，formal 按旧 transition prior。新 proof 只允许 rehearsal，不能与 formal Transition 同时存在。旧模板与 provider 字节一致。
- proof reader 从同一最多 256 KiB 缓冲扫描并解码；对象形状、重复字段、大小写别名、unknown、额外 JSON、空/null、类型错误均受限。与 proof-schema 的字段集合独立比对一致。字段 missing/null 不会借零值变成合法身份：每项均有非零/枚举/格式/时间或公钥检查。
- 成本只接受 proof 的正数 reserve 且小于固定 1350 stop；Materialize 的 committed 必须精确相等，模板 hard=1500/stop=1350 保持。不能用 context 私填零或降价覆盖 proof。Go 不独立计算 Quote，也不能证明 proof 自身的金额真实；这一点由 helper 的认证/重算和下游原始证据绑定承担。
- input request/source/operator 与 proof 相同；诊断 Ed25519 key 规范化后相等，允许无关 comment 差异，仍不能复用 deployment key。repo 字符串绑定 trusted context；numeric repo/workflow/run/control/evidence 的真实性刻意不由 Go 自报证明。
- 限原 `<request>-rehearsal-1`、next_attempt2、双方 run_attempt1、相同正 workflow ID、相邻正 run_number；先验证大小关系再做差，不接受 int64 环绕。四个 run ID 正数且彼此不同。created/zero 非零 UTC，zero 不早于 created，Materialize 再拒绝 future zero。
- failure class 只接受 preflight 或 unclassified-before-accepted-run-start 两枚举，runtime 等值拒绝；未知没有被转换为产品根因已修复。Go 不自行确认远端是否实际运行，外部证据仍必须承担此判断。

已核先红文件：缺 CLI flag、缺 proof 类型、encoding/json 大写别名；最终 06-related-regression.jsonl 实际含 121 个 pass test events / 34 顶层 / 0 skip / 0 fail，两包通过，且绑定当前五文件哈希。未为了重复计数重新运行测试。完整脚本集成与 flow-doc-contracts 仍由整合切片完成。

公开 API/费用认证、相邻 run 与当前 workflow 环境绑定、旧 zero 的 12 scopes、同 request 省略 proof 默认启动拒绝、sidecar 到 formal 的保存/复核均在本次范围外；不将它们记录为已实现或遗漏 finding。此 Go proof 是内部可信输入，不是可直接采购的凭证。无需为这层增加新协议或一般化 retry 结构。
