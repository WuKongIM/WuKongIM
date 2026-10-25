# 性能门禁适用性复评

本次复评以 `79200dcef557b8ca02f3debe790a742cabe31aea` 的现行测试合同及
R30 封存结果为依据。结论是：**需要修正本地诊断与正式验收的使用口径；现有证据
尚不足以把正式 hot SENDACK P99 从 400ms 调高，也不足以把 Mac 短测当作正式
Linux 性能门禁失败。** 本文未修改阈值、工作流或已有运行结果。

## 数值的来源与局限

`540e42ae441ab067a64c91c16a66df681e880320` 在 2026-08-24 将 hot SENDACK
P99 从 200ms 调为 400ms，同时更新 formal/rehearsal YAML、默认值和测试。
该提交的 PROJECT_KNOWLEDGE 记录过一个 2,500 在线、约 2,000 SEND/s 的本地样本：
180,002 条发送及 ACK、99.37% 在 400ms 内、无重试或丢失。这说明数值经过历史
经验校准；本次没有重新核验那次原始产物，不能把该记载当作今天 10,000 在线或
当前候选的性能保证。原始八月设计文档仍写 200ms，是历史设计值；现行可执行值
以 `internal/bench/chatlifecycle/defaults.go` 和对应 YAML 的 400ms 为准。

本次未找到独立于基准结果的客户体验/SLA 推导，不能称 400ms 是所有部署通用的
行业标准。SLO 应服务于用户体验，并有可迭代的确定过程；Google 的
[Implementing SLOs](https://sre.google/workbook/implementing-slos/) 也采用这一原则。
这不意味着可以把当前测出来的慢值直接写成新的合格线。

## 四种证据不能混用

| 场景 | 实际约束 | 延迟结论的适用范围 |
| --- | --- | --- |
| 正式长稳 | 四台 Linux 云主机，三服务节点各 4vCPU/8GiB、各自数据盘；10,000 在线、2,000 SEND/s | 现行 400ms/1s hot P99/P99.9；按正式窗口取证 |
| Linux PR 回归 | 现行固定四个可见 AMD64 CPU；500 SEND/s；60 秒预热及三个连续 60 秒窗口 | 检测这一固定规模下的回归；不证明 2,000 SEND/s 长稳 |
| 本地默认 shakeout | 同一文件系统上三服务及三驱动；2,500 在线、500 SEND/s | 本地配置自己的有效窗口，不能替代四主机验收 |
| R30 原规模诊断 | 一台 Mac/APFS；10,000 在线、2,000 SEND/s；120 秒主动窗口 | 正确性、实际速率及排队/存储归因；延迟原始分布是诊断证据 |

依据为 `docs/adr/0041-use-four-postpaid-hosts-for-automated-chat-lifecycle.md`、
`configs/wkbench/chat-lifecycle/README.md`、对应 YAML 和
`.github/workflows/README.md` 的现行回归合同。ADR 定义四主机形状；Ubuntu 和每台
服务机的数据盘另见
`docs/superpowers/specs/2026-08-07-automated-chat-lifecycle-cloud-simulation-design.md`。
共享本地磁盘的诊断范围另见
`docs/superpowers/specs/2026-08-13-chat-lifecycle-local-performance-diagnostics-design.md`。
三节点共用一个物理盘不等同于三个
独立数据盘；也不能仅凭这一差异就认定所有慢请求都是环境问题。

## 时间窗口存在明确的资格缺口

现行 formal/rehearsal 共用两小时 warmup；`verdict.go` 在第一个跨过 warmup 的
累计直方图切点仅建立基线，之后的有效增量才进入延迟判定。连续超标五分钟才
触发对应终止条件。

因此 R30 的 120 秒样本没有进入正式延迟资格窗口。其原始 hot P99 在 (1,2] 秒、
99.4662% 超过 400ms，是需要解释的慢现象；正式结果仍是 `operator_stop`，不能
改写为正式延迟 `FAIL`，也不能用 warnings=0 写成 `PASS`。

更值得注意的是：预演主动时长本身恰好两小时，无法提供计划内的 post-warmup
延迟窗口，更没有连续五分钟的正式延迟判定窗口。terminal/drain 跨过两小时的
数据不能冒充全速测量。`production_controller.go` 的 `rehearsal_pass` 是独立于
formal `Finalize` 的结果；它证明的范围必须按预演合同描述，不能解释成正式延迟
已经合格。此处是资格范围限制，不能仅凭预演结果名判定实现错误。

## 建议的调整顺序

1. 立即统一报告口径：本地原规模短测标为压力诊断；分别列出原始延迟、正确性和
   正式资格状态。正式性能证据缺失与正式性能失败分开记录。
2. 保留现行 Linux 500 SEND/s 回归，防止已有能力退化。继续保留已 ACK 消息的
   loss/duplicate/corruption/sequence regression 零容忍；它与 100% 服务可用性
   是不同指标，配置也已经另有首试失败率预算。
3. 本地容量以固定配置下的负载—延迟曲线、队列收敛和资源证据描述；2,000 SEND/s
   是压力点，不能把它自动变成所有开发电脑都必须达到的统一发版容量承诺。
   既有 local diagnostics 设计已明确独立测量 shared-storage capacity knee。
4. 在目标 Linux 拓扑按现行合同复验，再判断 400ms 是否与 v3 的目标用户体验和
   推荐硬件相符。若调整数值，先写清业务依据、适用负载、测量边界及对应成本，
   再版本化测试合同并重建基线；历史失败记录仍保留。
5. 另行评估把“延迟预热”和“长期资源趋势预热”分成两个明确定义的窗口，让有界
   预演也能测延迟。新窗口需要明确样本量和稳定区间，不能简单删除预热、移动
   当前运行边界或补算一个通过结果。现阶段仅提出合同设计问题，未更改实现。

这次复评不应得出“只要 Mac 达不到 400ms 就持续增加并发”的工程方向。R30 的
慢 append 仍有实际优化价值，但它与正式发布资格需要分别论证。此前真实 APFS
A-B-B-A 对照仅得到有限吞吐变化，单批平均等待反而升高，已据此停止产品双组
并发方案；详情及收据见 `2026-10-24-sampled-raft-read-blocking.md`。
