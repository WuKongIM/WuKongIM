# Final Spec review — d08031d5

**0 findings**：未发现本切片遗漏、越界或错误实现。仅静态 Spec 轴，未读取 Standards 结论，未运行测试、构建、产品、HTTP或采样。

需求：“继续定位 permission read admission busy 和 person_directory 队列积压根因，修复后相同规模本地2k，原gate不变”。批准范围是目录完成并发与 finite Ready chain 的控制调度公平性，不预先认定现场根因。

- `pkg/cluster/node_channel_migration_worker.go:226–254` 复用原十路 wave，使独立物理 Slots 重叠；保留编码、路由及逐任务错误对齐。原 `node_meta.go:920–963` 完整回收已提交调用；取消仍可能具有未知 durable outcome，没有增加回滚保证。
- `pkg/slot/multiraft/runtime.go:144–177` 仅移除前两次 Ready 后的提早返回，保留第三次 requeue 和每阶段 shouldProcess 检查。未改 `slot.go:879–1056` 的先持久化、配置/快照同步应用、Advance、异步应用回退及 fresh-read durable fence。控制队列因此可在仍有 Ready 时获得一次调度，而非等待链耗尽。
- 新 finite-chain fixture `ready_control_fairness_integration_test.go:21–134` 经真实 Runtime.Propose/Step 与 fake storage 注入有限消息链；确认排队 proposal 在八次注入耗尽前到达 Save，再等待 futures 和 public ReadBarrier。它验证可复现机制，不能证明现场消息序列同因。目录 integration 保留十路峰值、取消和 join 检查。
- 文档对应上述机制；1ms/cohort64、permission admission、schema、工作负载与原门禁文件未改。未新增采样或放宽成功条件。

**整体工作仍待验证，非代码 finding：** 1618 同规模失败保留；本审查没有 d080 同规模未采样2,000 SEND/s 原门禁通过证据，不能宣称 permission busy、积压或消息过期已消除，也不能声称正式发布就绪。

固定范围：base `966ec67df1435ad07bbb82724cd940f550755179` → head `d08031d576c3497c56b0dfba60e4db8370224c04`，三提交 a30a/1618/d080，tree `e084b653ba2aea7e549c6421eaf203a16820d294`。前后 HEAD、九文件 blob 与 clean 状态一致；patch SHA256 `a2b1cc3eb2797f4d46e0e77b1283f704eb9d0c60dd8ee678e78024bd4ae526ca`。

已读规则 SHA256：AGENTS `2220f49835b8dba20bafbc277854421222765be52e874fc6ee994bcd3ee0d746`；cluster FLOW `0a7ba4b4094ba483f4b7bdc17a654496e7e1a3560349c3ddb4ae401f19633d36`；slot FLOW `6a86881b38b156b05f6098226a1d8290150ce50921794a55f7585a9b493e77ba`。两 FLOW 均为 subtree；无更具体规则。
