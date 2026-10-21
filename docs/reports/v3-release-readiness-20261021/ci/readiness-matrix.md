# V3 GitHub readiness 只读审计（2026-10-21）

远端 main 仍为 b469c6e5e2b4ff65611a8fe3389fb5034de3f152；最新公开 release/tag 仍为 v3.0.0-beta.24，peel 到 4d44b39dda6fdaa7dafb637ca6cfb718e9530f77，无 v3.0.0 或 rc tag。

RC 分支 codex/v3-rc-qualification 的远端 HEAD 为 32d402f7625c36f5667a290ec95c045f5f35937c。没有该分支 PR（state=all 查询为空），仓库 open PR 列表也为空。ef798352c40a94e57f768f76b2f4713712f827fe 在 main 之后7提交，HEAD再多3提交。ef798 与32d402f 的 cmd/internal/pkg、go.mod/go.sum完全相同；main到候选有 channel recovery 产品变化，不能把main的绿灯冒认为候选绿灯。

| 项目 | 精确证据 | 结论/下一步 |
|---|---|---|
| 新候选Linux性能 | Run39453556254 attempt1，产品/工具ef798，fixed-host job118734407052 success；完整20项，43800全部完成，0错误/丢弃，最大P99 75.80928ms | 性能底线已通过，应移除“候选尚无完整Linux通过”的旧阻塞；实际publisher仍需最终tag绑定的门禁 |
| CPU竞争实验 | Run39467558163 attempt1，产品ef798冻结binary，工具07ed85a；job118782818661 success | 数值窗口通过，但双端点整体因果筛选失败、release_qualification=false；不能宣称历史退化根因已关闭 |
| RC提交/评审 | branch32d402f没有PR；head check-runs为空 | 创建可评审PR并完成候选相关CI是剩余交付缺口；并非当前PR有红check |
| 三节点nightly | b469近5次attempt1成功；最新39475335838（北京时间10/21），job118809712100及三seams成功 | 覆盖旧main。新版含recovery修复，需候选自己的unit/race/correctness/500QPS/nightly证据 |
| SDK | 39367425765（10/20）b469，Android1.0.5/iOS1.1.1/Flutter1.1.0三个job实际success | SDK不再停在10/3旧证据，但仍未测候选ef798/32d402f |
| Manager/native | 38848460385/38848460466，PR1013的891fb7d1，与main同树 | 可覆盖main，非修复后RC完整同树资格 |
| 历史红灯 | 39368295521（b469）旧混合窗口未过，导致release matrix前置jq=false，完整matrix未启动；39406775641（产品beta24、控制b469）在binary digest断言终止 | 前者为性能阈值未达历史证据，后者为身份/构建断言失败，不是产品业务失败；新候选pass不能删除这两条历史 |
| 长期可靠性 | formal最新33502076809和finalizer33502821176仍停在9/1；此前核实Acquire/Finalize skipped | 未发现新候选完整formal-chain成功证据；discovery/disarm绿灯不算长跑通过 |
| 当前open issue | #1015明确v2.2.5，#976明确v2.2.4；#949询问v3 k8s教程 | 未据此确立新增v3产品阻塞；旧v2问题不能直接算v3已复现或已修复 |

所有run链接模板：https://github.com/WuKongIM/WuKongIM/actions/runs/<run_id>。

本审计未dispatch、重跑、merge、发布、评论或修改仓库。原始GitHub JSON、相关job日志、分支差异文件和summary.json均保存在本目录。candidate-independent-supplement.json来自已提交RC分支的独立验收记录，GitHubjob日志独立确认了C产品SHA、binary SHA、gate_start和completed；本次未重复下载165MB产物。
