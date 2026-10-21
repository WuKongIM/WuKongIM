# Released continuation authority 与组合独审：冻结 v2

**结论：请求修复两项 P2 后差量复核。** 仅检查 `freeze-v2` 的 24 个冻结文件；全部 size/SHA 与 `files.json` 一致，输入清单 SHA `3b0dd1c74d1e2e4a82d665cd77454488e4356524244490816454ea5e133fb7e1`。Go 五文件及 consumer 五文件与此前已审字节完全相同，复用两份既有独审。没有编辑仓库、编译、Docker、真实 GitHub/云调用或接触私密材料。

## 阻塞发现

1. **P2：明确 pre-clock terminal 被当成未知历史忽略。** `authenticate-released-preclock.py:253–266` 在归档过滤时既不拒绝也不读取根 `pre-clock-terminal.json`。该文件是现有 `stage-orchestrate.sh:390–400` 在受限 classifier 成功后实际生成的公开终态，字段为 `schema=wukongim.chat_lifecycle.pre_clock_terminal/v1`、`request_id`、`stage`、`coordinator_code`。对完整合法 baseline 的 ZIP 加入真实 closed 值 `runtime` 或 `grant`，独立实际 CLI 均返回 exit 0 / ready / attempt 2；终态文件未进入 sidecar，proof 仍写 `unclassified_before_accepted_run_start`。这违反不接续已知 runtime/测量终态的边界。最小修复是在 allowlist 丢弃之前将该固定 terminal 文件作为拒绝 marker；无需新的 retry 分类或任意日志解析。

   `probe-results-v2.json` 保留两个有效枚举反例；初次 `probe-results.json` 的第二个值 `correctness` 不属于现有产品闭合枚举，仅说明 malformed 终态也被忽略，不把它描述为产品实际会生成的值。真正缺陷由有效 `runtime`/`grant` 足够证明。基线 classifier 原字节已保存。

2. **P2：同 request 已存在可信 cleanup，但 producer 未结束时默认入口归零。** `first_start_guard` 的 `:185–192` 只有 producer `status == completed` 才拒绝。精确 repo/headrepo/main/path/event/head_sha 匹配的 scheduled finalizer 已上传同 request cleanup、但仍 `in_progress` 时，独立 CLI 返回 first_start / attempt 1 / committed 0；仅改为 completed 即精确拒绝 `request_already_spent`。这并非仅假设并发：rehearsal finalizer 第 509 行上传 zero，第 525 行删除 handoff，541 行之后还发布 Issue 状态；其第 32 行 concurrency 与 paid starter 第 35 行不同。删除到 producer 终态之间，既有 handoff guard 可以已看不到 handoff，而新 spend guard 忽略已有 cleanup。最小修复是对同 request、身份可信的 retained cleanup/complete producer 非终态 fail closed，不能视为没有花费历史；保留未知/外部不可信 producer 的身份区分，不增加跨请求账本。

## 已核对的其余边界

- `prepare` 在 Issue preflight 之后、bundle/key/Quote/Acquire 之前；stage 只有一次绑定的 attempt1 或 attempt2，原 fixed budget/stop、paid lock 与释放 trap 不改变。formal 的原 `prior + rehearsal_cost` 保留；consumer proof 失败在清理之后才使资格失败。
- sequence 重读官方 GitHub 的固定 GET：repo 数字身份、完整名称、main/path、dispatch、attempt1、相同 numeric workflow、current 环境身份、唯一相邻 run number 和旧 failure 绑定。新 run 与旧 run 的 control ancestry 有实际 compare 响应约束。此复核未真实调用该 API，也不把 synthetic 响应当 live 授权。
- ZIP 数值 ID、API metadata、size/SHA、条目/展开/JSON 界限及路径拒绝存在；Acquire/Release producer 和公开文档互相校验。首代 prior0、两 public bootstrap 身份、12 个精确零库存 scopes、Release step 时间、固定四主机模型、Quote 完整成本和风险范围受验证。仍为保守 reserve，不能当实际账单。
- 同一份 digest-bound sidecar 随后重新认证远端证据，后续消费者不把自己的 run ID 替换原 producer。独立 CLI probe 证实 producer 从 in_progress 变 completed/success 后，后续 consumer 999 仍正确验证原 proof；duplicate zero scope 精确拒绝且不输出 sidecar；无历史 first_start 保持。
- 90 天 public sidecar 保留不延长旧子资产七天 authority；作者文档明确原资产过期/丢失会拒绝续跑或资格。该拒绝不阻断既有 Lease 清理。未把 unknown 解释成已修复原因。

## 实际验证与限度

`probe.py` / `probe-v2.py` 调用冻结 helper 的真实 Python CLI，只用本地 `gh` 替身返回 synthetic 原始 API JSON/ZIP；捕获调用均为固定 GET，无外网。有效版本 8 个场景加后续 consumer validate，结果原样保留；上述两个反例未作为“测试通过”。可用 `python3 replay.py` 在归档自身的冻结 helper/fixture 上重放，输出独立 replay-results，不覆盖原探针收据。

独立解析作者 `26-final-focused.jsonl`：75 pass test events / 21 顶层 / 0 fail / 0 skip；四 workflow actionlint 成功为作者收据，本轮未重复执行。完整 named checks 正在由 root 独立 cache 串行执行，不在本结论中提前记绿。修复后应冻结新代源码/先红后绿，再只审这两处 delta 与最终完整检查；原两项失败证据不得覆盖。
