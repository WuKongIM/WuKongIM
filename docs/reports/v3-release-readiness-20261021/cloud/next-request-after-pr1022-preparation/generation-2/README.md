# 第二版增补：固定合并源选择

root 已作出实施选择：**最终合并后的 PR #1022 提交同时作为 product 与 control**。这不是待用户选择 SHA 的问题。该提交必须包含降低预算 guard 和迁移恢复 barrier 修复，且合并 tree 必须等于已验收候选 `b3fdb777b9270128fe56086bc47562b321c2c947` 的 tree `ae5a4175337f10f521a13a6c704c1b3040cf2bd2`。

当前仅记录选择，没有查询或填入合并 SHA，也没有核验合并完成。新版 `dispatch-draft.json` 的 `product_sha`、`control_sha`、`draft_inputs.source_sha` 统一保留 `pending_merged_sha`；未合并的 b3f 只保留为验收基准，不能用作执行 source。root 将在合并后填写同一个精确 SHA、验证 tree 一致并刷新最终计划。若 tree 不同，不能沿用等价结论，须先重新审查和完成相关验证。

本增补取代父目录初稿中“产品源待选择”及将 b3f 写入 dispatch source 的部分。父目录十份冻结文件均保持原字节；旧 GitHub 状态仍只是原快照，本次没有轮询。其他预算、清理、报价/身份/库存准入和授权边界不变：H 字符串 `"1351044800"`，S=`1201044800`，新请求 prior=0/attempt=1，predecessor 为空；旧 ¥148.9552 是 reserve，不是实际账单。

“自动合并”不等于付费启动批准。最终源固定后，由 root 一次性提出具体付费启动问题；此次不再请求用户选择 source，也未派发 Workflow、创建 Issue 或写外部资源。

`source-choice.json` 记录选择、验收前提及父目录原文件摘要。`manifest.json` 列出本代公开内容的 SHA-256；`SHA256SUMS` 同时覆盖内容和 manifest（不含自身）。在本代目录复验：

```sh
LC_ALL=C shasum -a 256 -c SHA256SUMS
```
