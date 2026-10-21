# 冻结 v3 差量独审

**R1、R2 均关闭，无剩余阻塞发现。** 24 文件原始字节与 v3 清单全部一致；相对 v2 仅 helper 和 integration test 两文件变化，另外 22 文件完全一致。未修改仓库、重跑 Go/原生负载、调用真实 GitHub/云或访问凭证。

- **R1 已关闭**：`pre-clock-terminal.json` 在 ZIP allowlist 过滤前作为拒绝 marker。对现有实际 closed code `runtime` 和 `grant`，独立真实 CLI 均 exit 1 / `measured_or_runtime_predecessor`，且不产生 sidecar。没有新增不存在的 correctness 枚举，也不把已知终态改写为 unknown。
- **R2 已关闭**：同 request 的可信 retained cleanup/complete 身份仍核对 exact numeric/full-name repo、head repo、main、允许 path/event 和合法 head SHA；不再以 producer 是否 completed 决定能否归零。completed / in_progress / queued 三状态均精确拒绝 `request_already_spent`，没有 ZIP 下载或副作用。
- **兼容与其他边界保持**：六类不可信 producer（不同 path、fork head repo、不同 repo、非 main、无效 event、无效 head SHA）仍 first_start / prior 0，未输出 sidecar。无历史默认路径与合法 continuation 保持；合法 continuation 的 producer 转为 completed/success 后，在另一个 consumer run 中仍验证成功。重复 zero scope 仍精确拒绝。

`probe.py` 重放的是本目录冻结 helper 的实际 Python CLI，只有 `gh` 由本地 synthetic API/ZIP 替身响应，全部请求均为固定 GET；15 个场景加 1 次后续 consumer validate 全符合预期。`probe-results.json` 保存完整实际退出码和原因。它证明代码边界，不能证明 live GitHub authority、真实云续跑或账单。

已独立解析作者 red/green JSONL：原 4 个目标失败子例及其父失败保留，新聚焦 85 pass events / 0 fail / 0 skip；go-format、flow-doc-contracts、workflow-contracts 三项 v3 收据 passed，未重复运行。v2 full go-unit 的通过不能改称 v3 完整 suite；其余 named checks 由 root/作者在独立 cache 完成，不在此提前记绿。

v3 `files.json` SHA256：`33b0fbaa8423b1ff6062bde59888af6ccb8de7cabdab932aefb96fe508f6eb90`；输入清单：`6b760029b1c1e9ba836f8df8d7fd645d5ad13aa8829f181c97841cc35c7af52c`；两文件 delta：`616c12acfed0452729b5fe54116821b9839b8c002af7ee54a6285d76f3e981e8`。原 v2 review 与探针留在父目录未修改；本代只关闭已证实的两处缺陷。
