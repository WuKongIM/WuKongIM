# 原生 Linux RPC 重复性门槛：工具已准备，云上尚未执行

本轮将前几轮临时探针整理为仓库内可重复使用的工具，位于
[`scripts/transport-perf`](../../scripts/transport-perf/README.md)。**原生Linux实测结果仍为空**，
不能将本地功能冒烟或合成数据测试当成稳定性通过。产品transport运行时未修改。

## 已完成

工具提交`9511a2b41`。构建器要求干净、已提交源码，使用同一份探针、固定临时导入路径、
Go1.25.11、CGO关闭、trimpath及buildvcs=false，输出独立源码/二进制SHA256清单。
Linux/amd64和Darwin/arm64构建均成功；已验证脏源码、覆盖既有产物时拒绝构建，临时源目录清理完成。

客户端/服务端使用独立进程，通过真实transport echo调用测量。双端记录源码与二进制身份、
CPU型号、架构、CPU亲和性、GOMAXPROCS、运行时覆盖参数、CPU时间、分配、GC次数/暂停。
宿主身份只保存摘要。实际云上部署边界仍需用精确租约库存确认，探针不能证明物理宿主机隔离。

客户端预分配有界样本；正式计时后才复制排序。记录每个调用者、每秒窗口和整窗调用数，
以及独立预热计数；服务端echo计数差必须与客户端测量完成数完全一致。
元数据控制RPC的少量边界工作包含在服务端资源差中，不把它当精确handler成本。
纯计时不启用pprof。服务端有硬生命周期并支持SIGTERM；进程退出不等于云资源释放。

验收器拒绝错误、样本截断、计数不守恒、混合二进制/主机、服务器中途重启、
重复或重叠窗口、隐藏运行时参数和不支持的环境。它只判定同版本重复性，
不会输出候选优化或生产容量通过。

## 固定首轮计划

- 使用同CPU型号的两台原生Linux amd64主机，至少4 vCPU/8GiB，以内网连接；
  GOMAXPROCS=4，固定CPU亲和性0–3。
- 同一个基线二进制，64B无预算写请求、单连接、16调用者；服务并发64、队列4096项/64MiB、
  保留上限128MiB、排队5秒、执行30秒，Observer关闭。
- 每个独立客户端先并发预热10秒，再测量20秒；客户端GC400、服务端GC100。
  这些值是测量控制，不更改产品GC配置。
- 连续六窗，全部保留。跨完整批次的`(max-min)/median`门槛为吞吐≤3%、P99≤10%。
  这是预先声明的工程判据，不是统计显著性或置信区间。
- 不通过就终止该轮、保存证据并释放租约，不挑选有利重跑。通过也仅说明此基线可重复，
  本轮不据此重新接受已拒绝的监听候选；后续AB/BA需独立预先声明完整矩阵。

## 验证

以下检查全部通过：

```sh
GOWORK=off GOTOOLCHAIN=go1.25.11 go test ./scripts/transport-perf/probe -count=1
GOWORK=off GOTOOLCHAIN=go1.25.11 go test -race -tags=integration ./scripts/transport-perf/probe -count=1 -timeout=1m
GOWORK=off GOTOOLCHAIN=go1.25.11 go vet ./scripts/transport-perf/probe
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/transport-perf -p test_analyze.py -v
```

真实子进程集成覆盖TCP回显、逐调用者/逐秒计数守恒、正常信号退出及样本耗尽必须失败。
验收器单元测试覆盖稳定/不稳定批次及无效证据拒绝，不用硬编码本机速度作为CI门槛。
Darwin冒烟完成64,152次、Linux amd64容器功能冒烟完成34,091次成功回显，双端计数与哈希匹配。
两种冒烟均被原生验收检查正确拒绝，**不是云上性能证据**。本轮容器和临时源目录已清理。

[准备和验证清单](assets/rpc-native-preparation-2026-09-22.json)保存测试日志、功能报告、
运行/清理命令、构建清单及摘要。二进制在本机
`/Users/tt/.codex/visualizations/2026/09/22/rpc-native-gate`保留路径和SHA256，不嵌入Git。

## 实际执行的前置条件

用户确认没有现成测试机。本地四个历史请求均为released，且各有保存的零库存证明；
没有复用历史地址或凭据。只读preflight未联系云厂商，发现临时凭据/验证标记和精确生命周期授权缺失。
本轮没有新报价、采购或活动租约。

若新增临时服务器，拟沿用仓库已验证的租约流程（3台服务节点、1台负载节点，从中选两台做RPC），
预算上限300元，取得本轮新报价后才Acquire。首轮只做上述六窗校准，完成或失败后立即按
精确选择器释放并取得认证零库存证明；不运行聊天生命周期长跑，不扩展业务容量测试。
复用成熟采购/释放流程，避免本轮同时修改资源管理代码。

历史采购授权对应的租约均已释放，本轮新购仍需明确授权。
项目[云资源技能](../../.agents/skills/wukongim-chat-lifecycle/SKILL.md)明确规定：
“Never infer paid authority from `继续`, `同意`, deploy, run, status, diagnose,
stop, an explanation, `下一步建议？`, or prior paid runs.”
这只限制新增付费资源；工具实现、本地验证和交付已经完成。
