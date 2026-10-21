# v3 readiness — Spec / 验收边界复核

审查日期：2026-10-21（Asia/Shanghai）。仓库：`.worktrees/v3-rc-qualification`，固定审查 HEAD `32d402f7625c36f5667a290ec95c045f5f35937c`。只读，不修改仓库、不运行负载/测试、不调用云资源或发布工具。读取报告、规范和现成 JSON 收据；未进行新增产品源码缺陷审计。所有源文件 SHA-256 见 `source-rules.json`。

## 结论

迁移和 Linux 完整性能矩阵两项已有候选验收证据；不能继续把旧 b469 的两个失败当作当前未解决产品阻塞。当前未发现新的已证实功能缺陷。新 CPU 实验未通过双端点机制筛选，只限制“历史失败唯一根因已确认”和“beta→候选不存在回退”的表述；不构成新发布门禁。

现在可以进入候选整合、既定长稳准备和 stable 渠道实施。还不能宣布 v3.0.0 正式发布完成。硬性发布交付、现有 RC 长稳计划、建议性后续验证须分别叙述，不能把三者混成一张无限扩张的强制清单。

## 已关闭的候选阻塞

1. **迁移重启读取。**产品修复 `98c25091bb9587a303af982bb14bf4cedd9aa110` 只给两种明确的临时恢复 cause 增加公共 ErrNotReady 身份，保留 quorum/fence 和永久错误。相关失败先行测试通过；1→1、1→3、3→1、3→3、3→5 全通过，55 次完整历史检查，冷/热和 Channel failover 回归通过。来源：`2026-10-20-v3-migration-recovery-repair.md:14–44`；`v3-migration-repair-20261020/regressions-after-sequence-fixture.json` 的 status=pass、四组 cases 全 pass。
2. **完整 Linux 性能矩阵缺失。**候选 C=`ef798352c40a94e57f768f76b2f4713712f827fe`，run 39453556254 attempt 1，20/20、43800/43800 最终完成，error/drop=0，max P99 75.80928ms，min completion 99.6667%，max allocation/budget=67.8367%。独立收据 evidence_valid=true / candidate_regression_floor_pass=true / issues=[]。来源：`2026-10-20-v3-fixed-host-performance.md:3–30,93–124,164–178`；independent-audit.json 与 independent-supplement.json。
3. **身份继承边界。**`git diff --name-status ef798352c HEAD -- cmd internal pkg` 为空；C 的这三棵目录又与迁移修复相同。可据此复用相关产品证据，但不把不同 harness/最终 tag 的身份自动视为同一运行。最终发布 tag 仍须自身门禁。

## 尚待完成及其依据级别

| 条件 | 级别和依据 | 当前判断 / 下一步 |
| --- | --- | --- |
| 最终精确 tag 的 QPS/P99/allocation gate、20 端点收据 | **仓库强制发布门禁**。RELEASING.md:17–30；Workflow README:544–591 | C 的验收支持准备发布，但不能代替最终 tag 在两 publisher 中的门禁；不能重跑直到绿或改阈值。 |
| release notes 精确版本章节、source identity、immutable Release、受保护 Environment | **仓库强制发布条件**。AGENTS.md Release Notes；RELEASING.md:17–30；Workflow README:769–790 | 当前 CHANGELOG 仍 Unreleased，未有 v3.0.0 章节；在发布版本确定后整理并校验。临近打 tag 重新验证环境限制与 immutable 设置。 |
| Docker 三镜像源、GitHub 完整二进制、文档、stable 签名 APT/RPM + 四发行版公网/CLI 验收 | **仓库强制交付条件**。RELEASING.md:3–8,34–74,76–84,122–127 | stable 实施草案仍列完整链路缺项，不能只改 enabled，更不能用 preview 替代 stable。实时状态由包渠道审计另行确认。 |
| 2h rehearsal→新 Lease 72h Soak→同老化数据 capacity→30m recovery、证据连续性和 exact cleanup | **现有 RC 计划的验收条件**。RC qualification:110；formal plan:33–42,63–70 | 尚无本次已完成证据；按该计划继续则需完整执行。RELEASING.md 未将其定义为每次发行的一般机械门禁，不能声称普遍 MUST。付费启动有明确预算/授权与云资源边界，独立准备可继续。 |
| 完全解释旧性能环境的唯一因果 / A/A 或 CPU O/D 筛选变绿 | **诊断结论边界，非新增强制门禁**。fixed-host:58–60；Workflow README:515–530 | 保留历史失败和未证实标记即可，不应阻止已有完整矩阵合格候选继续下一阶段。只有新证据确认可复现产品违反既定 SLO 才重新立产品阻塞。 |
| self-chat 专项、100GiB/4h迁移、生产RPO/RTO、任意SDK/插件、4500SEND/s性能资格 | **缺失或有限范围证据；不得扩张承诺**。RC qualification:73–74；formal plan:61 | 不自动变成核心 IM GA 新门禁。若将相应能力/容量纳入本次明确发行承诺，再制定或补充对应验收。 |
| MQTT 完整 GA | **本次明确排除范围**。RC qualification:22 | 继续默认关闭和开发预览，不以核心 IM 放行宣称 MQTT GA。 |

## 可直接修正的文档问题

- `2026-10-20-v3-rc-qualification.md:3,5,7,101,109` 仍以迁移失败/完整矩阵未完成/尚未解释的任何失败保持阻塞作当前态，尽管新报告已证明候选修复和20/20通过。建议顶部加明确最新状态、产品身份和链接；第107节把旧时点条件标为“当时条件”，保留原失败数据，不改写历史。把“历史唯一因果未确认”移至诊断限制，勿列作未满足的普适发布门禁。
- `2026-10-20-v3-rc-formal-plan.md:3` 前置叙述仍称完整发布矩阵尚未运行、迁移仍失败。更新待冻结候选、已经完成的产品资格及仍未授权的付费边界；后续对实际 source SHA 重新核对，不能沿用 b469 的产品身份。
- 本结论不要求再发起随机 hosted runner 试验，也不建议因未通过机制筛选而修改产品性能逻辑。

## 新CPU证据限定

`2026-10-21-v3-cpu-contention.md:7–12,66–77`：run 39467558163、六轮18窗口36端点，280800请求完成、error/drop=0、max P99 118.777ms。list 两对等待降幅满足局部筛选，sync 不满足，整体 false。`delivery-validation.json` 明确 historical_root_cause_confirmed=false、beta_to_candidate_non_regression_proven=false、new_release_qualification=false；这与完整矩阵的 true 不冲突。
