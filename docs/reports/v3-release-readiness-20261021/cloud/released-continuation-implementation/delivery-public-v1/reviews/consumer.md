# Released continuation consumer 独立复核

结论：五个冻结文件未发现阻塞项。范围仅为三个 consumer workflow 和两个测试；不包含仍在实现中的 authority helper，也不构成云续跑或采购授权。

基线 `ce3c4d9110f17bb71dc99a89dc3c09758fabf980`。输入清单 SHA256 `9f3a67edf7b0fb0f83fdbf11e6bea5b4bbaaced595b0a78150a914b5c18cf739`，全部清单项独立核验通过；本目录 `source/` 为审阅原始字节。精确规则与文件哈希见 `source-freeze.json`。

## 主要结论

- **既有 Lease 清理独立**：rehearsal 的 release/cleanup 在第 347/355 行，continuation 校验在 375 行；formal 对应 351/359/379 行。新增校验均 `continue-on-error: true`，显式失败位于清理证明上传及 handoff 删除之后（rehearsal 533，formal 467）。独立 YAML 比较确认两 workflow 的 release、cleanup、零库存上传、cleanup-pending 上传、handoff 删除共 10 个步骤与基线完全一致，权限和并发配置未变。这里证明的是新增 proof 失败不阻断既有清理路径，不承诺既有云 Release 或网络故障永不发生。
- **正式采购前强制认证并保留 prior**：formal starter 第 122 行的 helper 调用位于非容错认证步骤内，早于第 173 行采购编排；原 `prior + rehearsal_cost == transition.committed_micros` 检查（135 行）保留。rehearsal transition 保留相同费用加法和停止上限（436–443 行）。formal finalizer 390–391 行又要求当前 handoff、所选 current/previous terminal 的 prior 均等于 rehearsal transition committed 值。
- **原失败证据与 90 天留存**：rehearsal 305–313 行将完整 continuation 树复制到 terminal 后，才进行原 90 天上传；覆盖正常收集、diagnosis、credential-unavailable 三种新 terminal 路径。转交 formal 时复制原 sidecar（457–459），complete 的原整目录复制保留内容；formal complete 复制整份原 rehearsal。未将未知前次失败重写为健康、preflight 或通过。原 terminal/complete 90 天保留值经独立读取确认未变。
- **默认路径**：不存在 continuation 时，复制步骤不创建 sidecar，不改变旧成本方程及主流程参数。consumer 对首次普通运行仍无条件调用验证入口；真正 attempt 1 / prior 0 / 无证明的兼容语义必须由最终 helper 测试证明。本复核不会用本地 helper 替身宣称其真实认证已完成。

## 独立验证及边界

`probe.py` 实际执行冻结 formal-finalizer 的完整 continuation shell，仅 authority 调用替换为无外部操作的退出码替身，Bash 与 jq 为真实工具。current/previous 两条路径各验证：匹配 prior 成功、handoff prior 清零拒绝、所选 terminal prior 清零拒绝、authority 19 原样失败；共 8 例通过。结果见 `independent-probe-results.json`。未运行云命令、Go 编译或重负载。

已独立解析既有 JSONL：首红 7 fail，随后 17 pass、相关合同 16 pass，均与其冻结摘要吻合；actionlint 和 diff-check 的成功取自实现者收据，未重复运行。其进程测试对 sidecar 的原始 unknown marker 字节复制、无 sidecar 路径、两个 finalizer 的 current/previous 参数及失败传播有实际 shell 覆盖。

尚未验证：最终 authority helper 的 GitHub / artifact / Quote / scope 真实性、GitHub Actions 实际执行、最终所有组合文件的整体验收。两项来源认证与默认兼容应由 helper 的独立评审和最终组合测试承接；本次未评 moving helper、未修改仓库源码。
