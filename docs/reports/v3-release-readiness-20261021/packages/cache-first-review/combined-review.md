## Standards

Standards 轴未发现硬性违反或需阻塞的代码异味。基线 97dd5976，仅四文件，SHA 与完整 diff 已冻结。

最终测试与实现前稿逐字一致，红灯 driver 等于基线，符合根 AGENTS 测试先行要求。新增 helper 仅抽取现有挂载组合逻辑，有英文边界说明；四目录独立且均位于任务缓存根。库存限制、读证据、隔离与清理 AST 未变；仅扩展可识别的缓存根。

40 个保护文件（含 12 producer、生产 validator/planner）不变，49 项门禁和原生命令体未改。CHANGELOG、文档明确修复范围。

已读相关 65 项、完整 Python 518 项（1 既有 skip）及 4 项合同绿灯。本人未跑测试。映射、配置及本地文件读测试不代表原生缓存恢复；新 exact-head 的 49 项验收仍待 CI。

## Spec

Spec 独审：未发现需求缺失、实现错误或范围扩大。四独立绑定符合批准路径，未合并原生目录；metadata 仅限两缓存根，state-only 仍失败。独立离线检查确认实际命令完整保留 native body/选项和父 tmpfs，inode/SHA/size/atime 读证明与清理未退化；四文件匹配冻结。原生 49 场景仍未证实：gen4 仅执行24、通过23、余25未到。下一次准确源码 CI 仍须证明 Rocky/Alma 首次缓存、实际复用和 B 目标；本地518项（1跳过）不能替代。

Findings: Standards 0; Spec 0. Real native Linux acceptance is separate.
