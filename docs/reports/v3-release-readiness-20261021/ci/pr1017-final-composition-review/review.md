# #1017 最终组合差异只读复核

**未发现可行动阻塞问题。** 范围为 `ed705c0fbffdea485bbb65608d6e340bb049d97e..ab75c226f4b3c246797aee4f876f23af602d07ce` 的 19 文件，工作树精确 head 且干净。此结论是最终差异、已存独审与定向收据的组合复核；没有重复执行测试。head 的首次完整 Linux scripts 门禁仍在进行时冻结本记录，不把旧 head 或本机聚焦绿色代替它。

## Standards

0 项发现。根 AGENTS、`internal/bench/FLOW.md`（subtree）及 `internal/bench/chatlifecycle/FLOW.md`（package）已读并冻结摘要。其运行时边界不因本次配置/测试改动失准。修改的真实进程/信号/睡眠测试均留在 integration tier；default dry-run 不启动后台进程。Go 的 cmd/internal/pkg 非测试生产源码无差异，运行时发送义务、会话有效期、延迟分类、重试和 Controller 语义未改。配置和 shell 运维脚本确有操作员可见变化，已在 Unreleased 正确记录，未宣称“所有产品文件不变”。

## Spec

0 项发现。

- local YAML/helper 默认同步为 500 SEND/s、两秒 burst credit、最大 burst 1000；显式 override 的两个 sed 锚同改。2,500 online users、12 logical groups / 256 hash slots、replicas 3/3、五分钟 eligibility 和 400ms hot SENDACK P99 不变。正式 profile 未改。三个 worker 的固定时钟回归及历史100失败臂，证明的是供给预算/准入；注释、文档和 PR 没有把 fake ACK 当 RECV/产品性能证明。
- Bash 修复只为四个成功/no-op Analysis 清理分支和首个 baseline operator trap 明确 return 0。cleanup 仍按原顺序执行并退出原始状态；close失败警告保留。与此前 `scripts-shell-cleanup-review` 的三文件冻结逐字一致，17/143×四分支先红后绿收据保留。
- 最终 signal fixture 两文件与独审 a9f82b05 补丁结果一致。Go operator 代替 fixture-only Bash signaler；production wait/join 函数没有修改，5s/50ms和原状态/PID断言保留。新负向 controls 拒绝 early0 和 malformed/timeout，17/255不被吞掉，owned PGID 被回收。三次有界尝试失败即停，不是重试取绿。FD3 trace 不混入断言 stdout。
- VCS fixture 用继承的编译缓存配合随机 build ID 强制新链接，并核 build ID 和原 exact revision/GOWORK 断言；`go version -json=false -m` 仅固定该文本解析接口。两处 profile 断言改为相同 literal 的 `grep -Fq`，没有放宽内容检查。buffer test 只避免复制 sync.Pool，未改生产 buffer 语义。
- 新 scripts CI job 独立执行原 frozen-base policy 的 scripts-integration：原 arguments/working_dir/timeout/output budget 全部原样传入，policy 文件本身不变（9m package / 600s outer / parallel2）。仅该 job 的 GOFLAGS 增加 JSON 观测；无 filter、自动 retry、预热或门禁放宽。workflow contents:read、exact checkout、无持久 cache、always evidence/cleanup 保持；default 和 E2E 原依赖关系不变。20min job 预算不是放宽600s命令限制。
- README/runbook/PROJECT_KNOWLEDGE 准确说明供给不足根因与局限；CHANGELOG 两条分别描述 budget 与清理修复。已保存 PR API 在 exact ab75 head 的 body 与 `pr-body.md` 逐字相同，表述保留全部旧失败、Mac测量1753.104s、876529 SEND/ACK、2 retries、102 hot样本>400ms及route/stop warnings；不声称正式Linux/72h资格或完整30min measurement。当前写“新 exact-head Linux gate仍需完成”与已知状态一致。

所复用证据覆盖：startup预算/渲染的先红后绿、原vet copy-lock定向修复、Bash3/5清理、VCS fresh-link/JSON、portable profile、最终Bash3/5 operator controls与两fixture。具体引用和文件 SHA 见 review.json。新增的 workflow JSON 观测与实际此前两个完整 Linux runs 已说明其他修复例通过；这些仍不是本head完整通过证据。

后续只需按原合同判断本head首次完整 CI 结果，保留失败并据证据修复。此复核不授权合并/发布，不替代 v3 剩余稳定性或 stable 公网交付要求。
