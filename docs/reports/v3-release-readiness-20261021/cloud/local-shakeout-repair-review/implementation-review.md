# local shakeout 500 SEND/s 最小修复独立 review

结论：已审阅冻结的 10 个变更文件，未发现剩余可行动实现问题。修复限定为本地固定配置/脚本默认速率及对应回归和文档，未修改运行时活动义务、deadline、通用 Config.Validate、正式 profile、400 ms 延迟/正确性阈值或受保护控制。CHANGELOG Unreleased 与 PROJECT_KNOWLEDGE 已同步。适用 FLOW 的运行时语义保持正确，无需更新。

## 行为与测试可信度

- YAML 与 helper 默认从 100/200 改为 500/1000；突发预算继续等于两秒速率。helper 精确 sed 锚一并更新，显式 `--send-rate` 仍生效。
- 新 `TestLocalShakeoutBootstrapGrantBudget` 从已提交 YAML 读取配置，通过真实 worker factory、bootstrap 和 coordinator grant allocator，覆盖三个 worker 到虚拟 360 秒。每个默认 worker 在 240 秒时活动队列为 0，最终 ActivityUnderDelivered=0 / HarnessInvalid=0。历史 100-rate 负例仍发生 admitted UnderDelivery，并检查 activity 减少及错误计数为 1。
- 测试替换了 session 传输、full sync 和时钟，使用立即 fake ACK。它验证配置预算和 grant admission 机制，**不验证真实 RECV、产品延迟、磁盘持久化、实机性能或完整稳定性**。尚需另一个新的、保留首失败的真实运行来证明该主机能完成 shakeout。
- 原测试先红：三个默认 worker 均在 240 秒仍积压约 9,521–9,546 个活动；配置例和 helper dry-run 亦拒绝旧默认。随后 focused-green 成功，预算回归 6.30 秒（三个默认臂及一个历史负例），原负例在虚拟 315 秒 / sequence 316 返回预期错误。历史错误时刻受准确定时及异步 settle 影响，不固定精确秒数。
- 实际脚本渲染的 integration fixture 在修改前先红，修改后 default 500/1000 与 override 400/800 两个子例通过；这验证了 sed 实际输出。静态 dry-run 另验证操作员可见默认值。
- 三次先行 500 control 均到虚拟 340 秒，120 秒时活动队列已清空。新正式回归增加至 360 秒，超过已知约 342 秒最迟初始 deadline。

## 独立发现与处置

审查发现历史负例最初要求调用前 heap-head deadline<=0 不可靠：原 v1 重复曾在 head 尚有约 0.97 秒时，由同批 grant 的后续活动触发失败。实现者已在最终回归中改成实际错误/计数和活动减少条件，没有修改运行时来迎合测试。未采用 `最大义务上界 > 服务下界` 的通用拒绝器，因为已通过的 rate200 对照证明该条件会误拒绝可行配置；此次按授权保持最小 profile 修复。

## 验证状态与交付边界

本 reviewer 独立执行 `git diff --check`、`bash -n` 与四个修改 Go 文件的只读 `gofmt -l`，均 exit 0 且无输出。没有运行 Go 编译/高负载，也没有更改仓库文件。上述红/绿测试由 rc_cloud_plan 执行，本 reviewer 阅读源码与冻结日志/收据并核对 SHA256；原失败不覆盖。

repository policy 选择的 `go-unit`、`go-vet`、`go-format`、`go-mod-tidy`、`scripts-integration` 正由实现者/主代理独立运行。此 review 不将待完成 named checks 写成通过，也不将局部回归等同完整测试、发布资格或正式稳定性结果。

文件内容身份见 implementation-freeze.json；原始诊断引用、先红后绿日志引用及本 reviewer 静态检查分别见 evidence-references.json、implementation-evidence-references.json、independent-static-checks.json。该快照尚未提交，合并/运行之前应把最终 commit 与此内容哈希核对。
