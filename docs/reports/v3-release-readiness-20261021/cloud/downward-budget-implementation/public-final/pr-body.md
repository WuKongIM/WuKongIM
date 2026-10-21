## Summary / 变更摘要

允许新建正式云验收请求显式降低预算上限，并将同一预算贯穿 Run Plan、Lease/部署收据、正式阶段转换、finalizer 和远端 wkcli 停止守卫。可选 `request_budget_limit_micros` 只接受 `150000000 < H <= 1500000000` 的规范十进制整数，停止线固定为 `H - 150000000`，保留 ¥150 清理余量；省略时保持原 ¥1500/¥1350 默认行为。

例如保留旧两次 Quote 的 ¥148.9552 保守 reserve 后，新请求可使用 `1351044800/1201044800` micros 的上限/停止线。该 reserve 不是实际账单。输入、收据或阶段预算不一致会被拒绝；降低预算不能与旧 released-preclock 续跑证明混用，也不会清除 Stop/已花费请求限制。预算资格失败仍保留 Release/零库存证明上传。

旧产品 `fc182` 的 wkcli 不支持降低预算。未来产品候选必须包含本次运行时守卫改动并单独完成验收；本 PR 保留 bundle 的 product/control 构建来源，不选择新候选、不执行云资源操作，也不授权下一次付费启动。

## Release notes / 发布说明

- [x] I added every user-visible change to `CHANGELOG.md` under `[Unreleased]`.
- [ ] This pull request has no user-visible change. I explained why below and a maintainer added the `skip-changelog` label.

## Verification / 验证

- 先记录 42 项失败情形并保留真实首红；四个完整 Go 包相关验证通过，最新脚本/消费者回归 129 个 pass events、0 fail、0 skip。实际本地 shell/jq 验证预算拒绝与 cleanup；外部 GitHub authority 使用替身，未执行云 Release 或 Actions 上传。
- 双轴审查最终无未解决 finding。Spec 首轮 P2（预算拒绝后上传被隐含 `success()` 跳过）先红后修：两处零库存上传增加 `always()`，仍要求 `cleanup.zero == true`；原预算拒绝保留。
- 冻结 v2 的 named checks：`go-format`、`flow-doc-contracts`、`workflow-contracts`、`go-unit`、`go-vet`、`go-mod-tidy` 通过。完整 `go-unit` 为 219 个包通过、15 个包无测试。
- `scripts-integration` **未通过**：原 9 分钟包总时限触发 timeout（包 540.676 秒，外层 544.260 秒），没有打印单项断言失败；当时两个既有用例运行、37 个测试栈在等待 parallel admission。所属 session 清理完成、无剩余进程。不归因 CPU 或某个代码缺陷，未重跑、过滤或改时限；需要本提交首次 Linux 完整门禁验证。
- v1 named run 的 session 丢失：只保留前三项成功，`go-unit` 无终态及原 SID，不能算通过、超时或已证明历史清理。v1 原件保留，v2 是修复后冻结源的首次完整本地检查结果。
- 测试时的冻结 27 文件与提交 `9a4900dc67ac674791edba11ea9a719cd91ec570` 全部逐字匹配；测试先于该提交运行。基线 `ff0aa096fe1bd125be70fc1844b2f45cc7f0115e`，v2 patch SHA256 `9c6fa1bb330b10f79359af1905e74b47b04adff34de8e1b7b865e563ef1ceef3`。
