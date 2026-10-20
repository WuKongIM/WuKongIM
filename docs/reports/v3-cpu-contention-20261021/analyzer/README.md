# 独立 CPU 竞争结果复核

此目录只含离线分析脚本；不启动产品、测试进程或远程任务，也不导入仓库的验收实现。
目前没有假定实验结果，尚未针对新实验 artifact 执行。

```sh
python3 /tmp/wukongim-cpu-causal-20261021/analysis/analyze_cpu_contention.py \
  /absolute/path/to/extracted/conversation-cpu-contention \
  --expected-harness-sha FULL_40_CHARACTER_COMMIT \
  --output-dir /tmp/wukongim-cpu-causal-20261021/analysis/run-ACTUAL_RUN_ID
```

输出目录必须不存在，以保留历次原始证据。非零退出表示身份、控制或收据校验失败；
纯数值门禁拒绝保留在结果中，不等价于采集失败。

输出：

- `audit.json`：完整独立验算、每秒任务匹配差分、可见 cgroup 各层独立差分、配对筛选、
  同模式重复差异与明确的证据限制。
- `endpoints.csv`：全部 6 × 3 × 2 个端点结果，含 drop/error、producer、queue、
  total/request/driver-wait P99 和原始门禁 verdict。
- `windows.csv`：18 窗产品 CPU/分配、完成请求混合比例、采样 CPU/读取成本。
- `roles.csv`：72 个角色窗口的 process CPU、匹配 TID runtime/runqueue wait、
  匹配区间计数、出生/退出缺口。

冻结的分析约束：

1. 固定产品 `ef798352c40a94e57f768f76b2f4713712f827fe`，实际产品字节、profile 摘要、
   独立 harness SHA、S/O/D/D/O/S 顺序都必须匹配。
2. 使用 O2/D3 和 O5/D4。对每个端点分别取三个窗口的 P99 中位数；绝不视为 pooled P99。
   driver-wait 中位数下降至少 25% 且至少 1 ms，同时 total P99 下降，只构成算术筛选。
   任何无效控制或 error/drop 都禁止把筛选当作可解释的改善结果。
3. S1/S6、O2/O5、D3/D4 全部列出。重复差异分母为两个正数中的较小值；零分母保留 null，
   不新增或追认通过标准。
4. 使用原始 stat/schedstat/status 复算 TID 身份、掩码和 matched delta；自然退出的首尾区间
   未知，不计作零。matched/union task-interval ratio 仅是计数覆盖率，不是时间覆盖率。
5. process CPU tick span 和 matched task span 均约为首末观察点之间，不声称覆盖整个 60 秒。
   cgroup 各祖先有重叠，禁止相加。单点缺失、回退和读取错误为显式 issue/null。
6. producer 覆盖全部计划到达；total/request/driver-wait/queued-worker 覆盖实际尝试请求，
   包括错误但不含 drop。不同人口的分位数不能相加；timeline.completed 同样包含错误请求。
7. 1 Hz 采样开销本身会干扰本实验；driver CPU 已包含 sampler，禁止重复计入或擅自扣除。
8. 不产生自动根因确认、历史失败唯一归因、无性能回退或正式发布结论。

Linux integration preflight 日志和 GitHub artifact 下载 SHA/CRC 由主验收单独核验；
它们不因本脚本返回零而自动得到证明。实际运行后必须人工检查可观测性和归因边界。
