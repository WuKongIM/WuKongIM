# Spec review — directory source completion

**0 findings**（missing/partial implementation 0；scope creep 0；wrong implementation 0）。独立于 Standards 轴；仅静态审查。

范围固定为 `966ec67df1435ad07bbb82724cd940f550755179 → 161814ce559366847d2b026850ab0afe0e52aed6` 的两个提交、五文件，工作区干净。

原要求：“继续定位 permission read admission busy 和 person_directory 队列积压的根因，修复后按相同规模重新跑本地2,000条/秒测试，保持原门禁不变”。本轮明确限定为移除独立源 Slots 的串行完成等待。

- `node_channel_migration_worker.go:226–254` 保留原路由、批次编码和输入索引，改用现有 bounded proposal wave；编码失败只写对应任务，返回结果仍逐项对齐。`node_meta.go:25,920–963` 原十路上限、逐项结果归并及全部已提交调用 join 未改；没有新增不受限 goroutine 或跨 Slot 的事务保证。
- 新 integration 文件 `:16–46` 确认独立 Slots 同时进入、快者释放后仍等待慢者；`:52–97` 验证十路真实峰值、取消后已进入调用 join 和其余任务各自取消结果。既有对齐/错误路由用例保持原文。
- `PROJECT_KNOWLEDGE.md:3–16` 保留每任务 participant 范围，明确取消不等于回滚或 durable join。FLOW/CHANGELOG 仅描述该调度修复；schema、permission admission、工作负载配置与原验收门禁均未改变。

**未完成的整体要求，不是本切片遗漏：** permission busy 是否消失及原门禁是否通过仍须相同规模未采样2,000 SEND/s实测。本审查不证明原现场唯一根因、性能改善、无丢消息或正式发布就绪；未执行测试、构建、产品、HTTP或采样。
