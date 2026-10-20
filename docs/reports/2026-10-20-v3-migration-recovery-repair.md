# v3 迁移重启历史读取修复（2026-10-20）

**迁移阻塞已在新候选中修复，相关回归通过。** 这不替代 Linux 性能、长稳或发布渠道验收。原候选 b469 的失败记录保持不变，见 [原 RC 记录](2026-10-20-v3-rc-qualification.md)。本次先完成迁移修复与回归，再继续性能调查。

## 固定身份

- 产品修复：`98c25091bb9587a303af982bb14bf4cedd9aa110`。
- 最终 E2E 夹具：`dee2b5c7f3f00ca6301c600c5548d3bf9be07ca8`；与产品提交相比仅调整 ordinary-history 首次追加的序号断言及场景说明。
- 所有本轮 E2E 使用同一个 Go 1.25.11、darwin/arm64、`-tags=e2e` 二进制，SHA-256：`a6b6d20a8d60cc38f164a6479f34557f698fd9400892d8ea05f9f64051db9b05`。
- 构建时显式绑定 worktree 的 `GIT_DIR` 与 `GIT_WORK_TREE`，二进制 build info 确认为产品提交且 `vcs.modified=false`。E2E 收据分别冻结产品、夹具、配置规则摘要和运行前后 clean 状态。

## 产品修复与先红后绿证据

原 1→3 测试在完整重启后的首次历史读取收到 HTTP 400，私有 cause 为 `recovery probe incomplete`。复制层未把暂时不能完成恢复证明表达为公共 `ErrNotReady`，导致读取层误归类为普通追加失败。

在真实 `quorumLog.Install` 边界先构造前沿 quorum 缺失、identity-page 探测不完整等失败，再修改产品：只为两个明确的恢复未完成 cause 加入公共未就绪身份，同时保留原 cause。现有跨节点错误协议与 HTTP 可用性映射因此可以沿原有路径返回 503 `unavailable`。恢复证明、quorum、authority barrier、写入 fencing、成功后才发布 ready/HW 的约束未放宽。

负例还暴露本地存储错误被丢弃或转成无效交换的问题。本次保留批量读取与 item 错误，并在收集本地 leader 探测失败时原样返回；普通存储错误、永久冲突、非法配置和取消不会获得新的可重试身份。远端缺失证据仍按 quorum 规则处理。

新增两个顶层测试、八个子用例，覆盖临时/永久错误身份、未完成恢复不可提交、存储不被改写、清除故障后无需业务预热即可恢复完整原记录，以及后续正确追加。已有真实 RPC 回归补充 joined readiness cause；相关 API 和 infra 测试一起通过。

[单测收据](v3-migration-repair-20261020/unit-validation-summary.json)保留四个真实红例及全部绿例。初版测试曾把未设置的期望 `Record.Index=0` 与正常持久记录的 `Index=1` 比较；在产品修改前修正为实际索引，再取得 refined red。其最初的“lost original content”断言文案是测试误判，日志中记录仍存在，不能写成产品丢失历史。

## 黑盒回归与夹具修正

历史读取只重试 HTTP 503 且 JSON 顶层 `code="unavailable"`、`status=503`；其他状态、错误包、传输错误或成功但错误的历史立即失败。每次读取仍为原来的 20 秒预算与 100 ms 间隔。保留全入口完整历史、原凭据登录、幂等性、完整重启后先读再写及适用的 Leader 故障切换断言。

首次定向 1→3 通过。随后首次全拓扑运行中，1→1、1→3、3→1、3→3 通过，3→5 在重启前的首次 WKProto 追加处因“预期序号4，实际6”失败；此前五个入口的三条原始历史全部匹配。

已有[迁移参考](../../docs-site/content/docs/server/operations/v2-to-v3-migration-reference.en.mdx)明确恢复屏障占用位置，不可假设启动后第一条业务消息为 N+1。同场景后续 HTTP 追加及既有 failover 用例也采用严格递增语义。修正首次 WKProto 断言为大于已验证末条序号，并把实际 ACK 序号绑定到每个完整期望行。原导入位置 1..N 和所有完整历史断言均保留。**失败日志不能证明位置4、5具体是两条屏障**，也不能据此认定数据丢失或本次产品修改造成序号分配回退。

保留首次失败后，仅因该明确夹具修正重新执行完整拓扑矩阵一次；产品二进制保持不变。[失败与修正说明](v3-migration-repair-20261020/sequence-fixture-correction.json)和两轮原始日志均保留。

| 验证 | 结果 | 耗时 |
| --- | --- | --- |
| 定向 1→3 原失败路径 | 通过 | 27.917 秒 |
| 修正夹具后的五种迁移拓扑：1→1、1→3、3→1、3→3、3→5 | 全部通过；55 次完整历史核对 | 106.504 秒 |
| 原始 v2 拒绝/空群控制 | 通过 | 37.237 秒 |
| 冷/热通道 SEND | 通过 | 27.836 秒 |
| Channel 故障切换、进程暂停及副本修复 | 通过 | 113.805 秒 |
| 相关默认 Go 测试 | 13 包通过，1,469 个顶层测试 | 见原始 JSONL |
| `flow-doc-contracts` 与 diff 检查 | 通过 | 见收据 |

五拓扑自然运行中的 55 次读取均未遇到需要重试的 503；它们证明历史与恢复行为回归通过。暂时故障的确定性红绿测试负责验证原错误分类缺口，不能把自然 E2E 通过写成每次都命中了原探测竞态。FLOW 检查首次提示生成索引陈旧，更新索引后通过；20 个长度建议警告、0 个无效 FLOW。独立静态审查未发现恢复证明放宽或永久错误误分类。

## 复验

完整命令、source/tree/binary 摘要、规则摘要、逐项结果与阶段日志见 [最终回归收据](v3-migration-repair-20261020/regressions-after-sequence-fixture.json)。旧的失败收据 `regressions.json` 保持原结论。

```bash
GOWORK=off go test ./pkg/channel/... ./pkg/cluster/channels ./internal/infra/cluster ./internal/runtime/readavailability ./internal/access/api -count=1 -timeout=5m -p=2 -json
WK_E2E_BINARY=/absolute/frozen/wukongim-e2e GOWORK=off go test -tags=e2e ./test/e2e/migration/ordinary_history -count=1 -timeout=12m -p=1 -parallel=1 -json
```

[证据清单](v3-migration-repair-20261020/manifest.json)记录每个文件及解压后的原始 SHA-256；较大 JSONL 使用 gzip 保存。二进制不提交到仓库，可从上述产品提交重建；不同平台重建的字节摘要可能不同，必须另记实际身份。本次没有创建 release tag、发布制品、启动付费云资源或修改主工作区。
