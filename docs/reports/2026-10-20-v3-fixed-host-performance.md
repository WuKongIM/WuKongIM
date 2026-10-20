# V3 迁移修复后的 Linux 性能验收与差异排查

**迁移修复后的候选通过本次 Linux 性能门禁；版本性能差异的根因尚未完全关闭。**

[Run 39453556254，attempt 1](https://github.com/WuKongIM/WuKongIM/actions/runs/39453556254)
于 2026-10-20 16:00–16:53 UTC 完成。实际验收的是
`ef798352c40a94e57f768f76b2f4713712f827fe`（下称 C），不是后续归档提交。
C 的 `cmd/`、`internal/`、`pkg/` 与迁移修复提交
`98c25091bb9587a303af982bb14bf4cedd9aa110` 完全相同。

- C 的三个混合负载诊断窗口和原有完整性能矩阵 **20/20 项通过**。
  完整矩阵 43,800 次请求最终全部完成，窗口内完成 43,795 次；错误、丢弃、
  运行时激活和成员写入均为 0。最大 P99 **75.80928 ms**，最小窗口吞吐完成比
  **99.6667%**，最大分配量占对应预算 **67.8367%**。
- 同一旧候选 B 的第 4、5 轮，list/sync 的三窗 P99 中位数差为
  **31.1834% / 36.0929%**，超过预声明的 25% 重复性界限。
  本轮 **A/A 重复性未通过**，不能据此宣称 beta 到候选没有回退、候选更快，
  或历史宿主差异已经得到因果解释。
- 历史失败的直接机制已确认：处理能力不足使压测端有界队列持续积压并丢弃到达请求。
  原始服务端 handler 耗时已经接近工作线程的占用预算；不能把积压单独归咎于
  客户端 JSON，也没有证据证明版本增加了 RPC fanout。

本次没有修改产品性能代码、放宽门禁、重跑测量窗口或发布正式版本。
完整矩阵的通过是这一候选、这一环境的性能底线验收；正式发布还需要其余发布条件，
包括适用的签名原生包通道及公共客户端验收。

## 固定环境和预声明判据

[dispatch-plan.json](v3-fixed-host-20261020/dispatch-plan.json) 在运行前绑定了精确 C。
同一个 Ubuntu 24.04 hosted Linux job 内，A/B/C 在同一绝对源码目录各构建一次，
复用同一已编译 driver，顺序固定为 **B、B、A、B、B、A、C**：

| 角色 | 精确源码 |
| --- | --- |
| A：beta.24 | `4d44b39dda6fdaa7dafb637ca6cfb718e9530f77` |
| B：先前候选 | `b469c6e5e2b4ff65611a8fe3389fb5034de3f152` |
| C：迁移修复候选及测试工具 | `ef798352c40a94e57f768f76b2f4713712f827fe` |

实际 CPU 是 Intel Xeon Platinum 8573C，4 个逻辑 CPU（2 核 × 2 线程），
affinity 固定 0–3，Go 1.25.11，driver/node GOMAXPROCS 为 4/2。
所有进程共享这些 CPU；这不是独占物理主机的证明。
每轮启动全新的三节点集群和相同 fixture，准备必须在最早节点年龄 180 秒前完成，
到点才释放原有 warmup。实际释放年龄为 180.0144–180.0172 秒，
首窗开始年龄为 181.0083–181.0645 秒。然后保留三个原有 60 秒混合窗口和单独的短 profile。

有效 cgroup membership 为 `/system.slice/hosted-compute-agent.service`。
可见 leaf 和 `/system.slice` 的 `cpu.max` 都为 `max 100000`，有效 cpuset 0–3；
`memory.max`、`memory.high` 为 `max`。根的 `cpu.max` 不存在，按未知记录。
采集了全部可见祖先；所有测量前后切片的 `nr_throttled`、`throttled_usec`
增量为 0，可用 `memory.events` 的增量为 0。它们只排除已观测层级的相应事件，
不证明没有不可见宿主约束、调度竞争或频率变化。

预声明的两组 B/B（1/2 和 4/5）均要求：每窗通过、错误/丢弃为 0，
服务端 CPU/完成请求差 ≤10%，每个接口的三窗 P99 中位数差 ≤25%。
相对差统一使用 `abs(x-y)/min(x,y)`。这是诊断重复性条件，不是发布门槛或显著性检验。
CPU 分子是三个节点计数之和，只计一次；分母含 list/sync 的排空完成请求。
所有失败数据都保留，禁止挑选一轮代表版本。

C 独立执行三个混合窗口及原有完整门禁：12 个基础结果，加上 7 个压力窗口的
8 个接口结果。使用 C 提交中的原有 v2 jq validator，额外校验门禁实际二进制与 C 一致。
短 profile 不参与候选通过判定。整个实验只 dispatch 一次，无测量重试。

## 全部混合窗口与重复性

全部 21 个混合窗口、42 个接口结果均通过，327,600/327,600 请求最终完成，
错误、丢弃、运行时激活、成员写入均为 0。表内 P99 保留每个窗口，单位 ms：

| 轮次 | 角色 | CPU ms/完成请求 | list 三窗 P99 | sync 三窗 P99 |
| --- | --- | ---: | --- | --- |
| 1 | B | 10.9412 | 32.71 / 31.00 / 196.20 | 71.01 / 68.45 / 132.85 |
| 2 | B | 10.8585 | 29.29 / 29.29 / 30.32 | 64.24 / 63.60 / 64.31 |
| 3 | A | 10.8376 | 30.33 / 27.97 / 59.89 | 64.12 / 62.79 / 116.76 |
| 4 | B | 10.1017 | 23.64 / 21.75 / 22.42 | 50.85 / 46.38 / 48.11 |
| 5 | B | 10.5669 | 28.07 / 29.40 / 29.64 | 58.79 / 66.44 / 65.47 |
| 6 | A | 10.6288 | 27.58 / 28.79 / 30.87 | 59.95 / 61.33 / 68.28 |
| 7 | C | 10.8073 | 28.39 / 28.76 / 31.36 | 61.92 / 61.31 / 70.01 |

| A/A 对 | CPU/请求相对差 | list P99 中位数相对差 | sync P99 中位数相对差 | 重复性 |
| --- | ---: | ---: | ---: | --- |
| B1/B2 | 0.7615% | 11.6532% | 10.5255% | PASS |
| B4/B5 | 4.6049% | 31.1834% | 36.0929% | **FAIL** |

四个 B 轮次的 P99 中位数最大/最小比为 list 1.4592、sync 1.4760。
即使把源码、二进制、逻辑 CPU、driver、进程年龄固定，仍然有明显的同版本波动。
因此 A/B/B/A 的描述性排序不能用于稳定版本归因。
“窗口 P99 的中位数”也不是合并全部请求后的 P99。

## 候选 C 的完整 Linux 门禁

下表中节点数 1 均指 **single-node cluster**；节点数 3 指三节点集群。
所有项目错误、丢弃及非预期运行时写入均为 0，分配量均未超原预算。
原有 P99 上限 500 ms、负载、分页和 allocation budget 均未修改。

| 场景 | 节点 | 接口 | page size | offered QPS | actual QPS | P99 ms | 结果 |
| --- | ---: | --- | ---: | ---: | ---: | ---: | --- |
| base | 1 | list | 25 | 400 | 400.00 | 3.77 | PASS |
| base | 1 | list | 100 | 200 | 200.00 | 10.52 | PASS |
| base | 1 | list | 200 | 100 | 100.00 | 13.62 | PASS |
| base | 1 | sync | 25 | 80 | 80.00 | 7.00 | PASS |
| base | 1 | sync | 100 | 60 | 60.00 | 16.05 | PASS |
| base | 1 | sync | 200 | 40 | 40.00 | 26.79 | PASS |
| base | 3 | list | 25 | 400 | 399.93 | 8.43 | PASS |
| base | 3 | list | 100 | 200 | 199.93 | 13.00 | PASS |
| base | 3 | list | 200 | 100 | 100.00 | 17.67 | PASS |
| base | 3 | sync | 25 | 80 | 80.00 | 12.81 | PASS |
| base | 3 | sync | 100 | 60 | 60.00 | 23.94 | PASS |
| base | 3 | sync | 200 | 40 | 40.00 | 34.02 | PASS |
| mixed | 3 | list | 100 | 200 | 199.98 | 28.22 | PASS |
| mixed | 3 | sync | 100 | 60 | 60.00 | 60.82 | PASS |
| hidden_50_percent | 3 | sync | 100 | 20 | 20.00 | 34.51 | PASS |
| hidden_50_percent | 3 | sync | 50 | 20 | 20.00 | 29.90 | PASS |
| hidden_90_percent | 3 | sync | 100 | 20 | 20.00 | 23.26 | PASS |
| hidden_90_percent | 3 | sync | 50 | 20 | 20.00 | 17.58 | PASS |
| hidden_after_99_visible | 3 | sync | 100 | 20 | 19.93 | 75.81 | PASS |
| hidden_after_99_visible | 3 | sync | 50 | 20 | 19.93 | 69.44 | PASS |

精确结果和每项 allocation budget 见
[independent-supplement.json](v3-fixed-host-20261020/independent-supplement.json)。
此完整矩阵与前面的七轮诊断分别计数，不能将诊断通过替代完整矩阵验收。

## 根因证据与尚未关闭的边界

[历史因果审计](v3-fixed-host-20261020/historical-causal-boundary.md)
重新核对了原始 EPYC 7763 失败和此前正反序比较：

1. 原始 list P99 为 813/671/657 ms，driver wait P99 为 750/620/608 ms，
   三窗丢弃 1,441/1,142/1,032 个到达请求，最后十秒仍持续丢弃。
   这是队列处理能力不足的直接证据；不是服务端返回错误，也不是消息持久化丢失的证据。
2. 原始 list handler 平均耗时 39.12–40.48 ms，已接近
   `8 workers / 200 requests per second = 40 ms` 的平均占用预算，
   还未计入客户端收包、解码和校验。三个服务节点占约 3.12–3.18 CPU 核，
   主机非 idle 约 95%，CPU PSI 约 60–62%。这支持共享 CPU 资源压力的解释，
   但不足以区分 CPU 型号、限额、调度竞争和准备状态的因果贡献。
3. 所有九个历史窗口中，五类 RPC 和 edit-slot barrier 总数完全符合相同的
   list/sync 完成数整数模型（54 个残差均为 0）。原始 list 丢弃提高了 sync
   在完成请求中的占比，所以 RPC/完成请求增大不能单独证明新增 fanout。
4. 原始采集取的是 cgroup mount 根，而进程属于实际 service cgroup。
   历史 service/祖先限额未知，旧根计数的零节流不能排除更窄限额。
   本轮已经补全有效层级观测，但不能补回已经消失的历史主机状态。

本轮资源和请求时序详见 [resource-analysis.md](v3-fixed-host-20261020/resource-analysis.md)；
短 profile 分析见 [profile-analysis.md](v3-fixed-host-20261020/profile-analysis.md)。
B4→B5 的 list/sync handler 加权平均耗时分别增加 26.95%/26.54%，
CPU/请求只增加 4.60%，分配量近乎不变；多个服务端阶段同时变慢，未定位到
具体新增代码热点。B1 第三窗则在约第 55 秒后请求变慢，随后 list 等待逐秒累积，
没有证据把它定为单次 GC 暂停。C 单独短 profile 窗也出现 list P99 243.14 ms、
driver wait P99 202.05 ms，已完整保留；它没有覆盖前面的三个 60 秒窗口。

准备阶段和年龄收据可检测状态差异，不能证明 GC/compaction 状态完全一致。
测量资源切片围住测试 helper，包含 warmup 和观测开销，不能当作精确 60 秒窗口。
单独采集的短 profile 只描述其自身时段；不能据其证明历史长窗内发生了某个暂停。

剩余归因需要能先通过 A/A 的 Linux 环境，再用相同负载做受控对照；
如果要精确区分共享 CPU 竞争，应进一步控制压测进程与节点的资源隔离并捕获
与异常同窗的调度/CPU 时间线。当前 hosted VM 的隐藏调度状态不可读，
原失败 VM 也不能重建。继续随机更换 hosted runner 直到比较“变绿”不能补足这个证据缺口。
本轮没有把尚未证实的假设变成产品性能补丁。

## 验证与证据保存

- 本地相关纯契约、E2E 编译、scripts Workflow/ConversationFixedHost/ReviewAgent
  契约、7 个 response-budget proxy 测试和 Actionlint 1.7.9 均通过。
- 本地 macOS 跳过的超时进程清理 integration preflight 已在这次 Linux job
  中真实执行并通过；其 JSONL 收据保留在 artifact。
- 独立检查器确认实际 A/B/C 和 driver 二进制字节、构建目录、clean VCS、工具链、
  控制收据、完整窗口和 C 门禁一致，`issues=[]`。
  结论为 `evidence_valid=true`、`candidate_regression_floor_pass=true`、
  **`all_aa_criteria_pass=false`**、**`historical_root_cause_confirmed=false`**。
- 完整 artifact（ID `12282956979`，165,242,098 bytes）已下载并验证 GitHub digest
  及全部 141 项 CRC。SHA-256 为
  `1adc8cfd1fa800cd1372f68f872b3ee395712b1da97eece073d8c63a99cf1718`。
  仓库保存其中全部 137 个非可执行文件，包括每轮原始 report、资源切片和 pprof，
  不只保存候选或通过结果；四个可执行文件在原 artifact 中保留并已核验实际字节。

[证据目录和复算说明](v3-fixed-host-20261020/README.md) 包含预声明计划、
源码规则摘要、下载收据、逐项哈希、离线分析器、独立验收和最终 manifest。
较早的 [排查报告](2026-10-20-v3-performance-followup.md) 保持历史原貌；
本报告与历史审计修正了其中 RPC mix 和 cgroup 观测范围的归因限制。
