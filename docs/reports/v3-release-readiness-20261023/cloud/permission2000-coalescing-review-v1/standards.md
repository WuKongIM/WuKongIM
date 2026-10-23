Standards：0 项硬性违规，0 项需要整改的启发式发现。

范围：先审 d08031d 的 8 个 tracked 修改及 1 个 untracked 集成测试；作者随后提交，最终以 `git diff d08031d 8100d7f` 复核同一 9 文件、干净工作树。83c0→8100 仅 CHANGELOG 对空闲快路径措辞的限定。未读取 Spec 轴报告。

- `send_permission_cohort.go:13–23` 只把收集窗从 1 ms 改成 5 ms，并用英文注释说明空闲快路径及独立上限；64 cohorts、1024 calls、16 MiB 和原 fresh/cancel/join 路径未改，符合 AGENTS.md:86–91。
- 新 paced 测试 `:1,15–84` 先列失败机制，使用 integration 标签、3 秒期限、400 容量结果通道及既有关闭/归零检查。收据保留旧窗 143/400 失败→新窗 0/400，以及相关 normal/race 通过；诊断四个公开调用边界也保留先 RED 后 GREEN，符合 AGENTS.md:129–139。这里只读已有结果，未复跑。
- `client.go:1406–1458` 与 `metrics_parse_failure.go:84–85` 使用私有队列族和四个固定原因，保留拒绝条件、旧错误文本及旧枚举兼容；不暴露 labels、UID 或响应正文，符合 internal/bench/FLOW.md 的闭合诊断要求。
- CHANGELOG、PROJECT_KNOWLEDGE、Slot FLOW 及原规格同步，符合 AGENTS.md:101–102、174–175、203–205；FLOW 收据为 88 合规/0 invalid/20 既有警告。受保护控制、policy、工作负载和验收门禁没有修改。

十二类 Fowler smell 均按启发式检查；复用现有 cohort/parser 边界，无需增加抽象。新候选尚无同规模本地 2,000 SEND/s 结果，不能称 permission busy 或历史故障已解决。
