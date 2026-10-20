# v3 候选 CPU 竞争对照计划

本计划在运行前冻结。用户没有可独占的 Linux 主机，继续使用已有 GitHub Actions
runner，不采购云资源。本轮检验逻辑 CPU 竞争机制；历史失败主机已不可观测，不能承诺
复现或唯一归因。上一轮候选完整 Linux 性能门禁已通过，见
[验收与归因边界](2026-10-20-v3-fixed-host-performance.md)。

## 固定对象与对照

- 产品 C：`ef798352c40a94e57f768f76b2f4713712f827fe`，迁移修复已包含。
- 产品二进制 SHA-256：
  `8c3c79f2853b76be7f83dbd606404bf6ab1fdf34ce9370e82c056b389d03c57d`。
  相同 Go 1.25.11、构建参数与绝对源码目录；摘要不一致则停止，不替换期望值。
- 测试驱动：包含本计划的新提交，单次编译；与产品提交分别记录。`profile.json`
  必须和 C 逐字节相同。
- 一个 Ubuntu 24.04 Linux AMD64 runner；四个逻辑 CPU，单 NUMA/单封装、
  两个 SMT2 核心。sysfs 核验拓扑，未知或不同拓扑直接拒绝。
- 固定顺序 `S, O, D, D, O, S`，每轮全新三节点集群与原有 600 群夹具。
  256 hash slots、12 physical slots、3 副本。
- 全部准备阶段共享四个 CPU，driver GOMAXPROCS=4、节点为 2；固定 180 秒
  进程年龄释放后才改变 CPU 绑定，随后保留原 warmup。

| 模式 | Driver | 三个节点 |
| --- | --- | --- |
| S | 全部四个逻辑 CPU，GOMAXPROCS=4 | 同样四个 CPU，各 GOMAXPROCS=2 |
| O | 一个逻辑 CPU，GOMAXPROCS=1 | 固定三个 CPU，包含 driver CPU，各 GOMAXPROCS=2 |
| D | O 的 SMT sibling，GOMAXPROCS=1 | 与 O 完全相同的三个 CPU，不含 driver CPU |

O/D 每个角色的 CPU 数量和 GOMAXPROCS 一致，仅改变 driver 与节点是否共用
逻辑 CPU；可用 CPU 并集随之从三个变为四个。D 仍共享物理核心、缓存、cgroup
和不可见宿主机，不能声称物理隔离。S 是前后环境参照，S/D 同时改变多个条件。
每次绑定覆盖所有 TID，核验 PID/TID starttime；不能只绑定进程主线程。

## 先验假设与证据

1. **逻辑 CPU 竞争放大延迟。** 若成立，O→D 后 driver 等待与总延迟应在前后
   两组配对中同向下降。预先筛选条件：每个端点三窗 driver-wait P99 的中位数在 D
   下降至少 25% 且至少 1 ms，总 P99 同向下降。比较 O2/D3 与 O5/D4，
   同时报告 S/S、O/O、D/D 重复差异。该筛选不是统计显著性或自动因果结论。
2. **调度等待参与慢窗。** 若成立，慢窗应出现相应角色的 Linux runqueue wait
   增长，并与 driver producer/worker 等待或 HTTP 请求耗时在时间上相符。
   仅关联不足以证明原因；未观测到等待会削弱此假设，但不覆盖未观测任务区间。
3. **CPU 工作量或 GC 成本参与慢窗。** 若前两项缺乏证据，而 CPU/完成请求、
   分配或 GC 变化同步增大，则继续检查对应工作量。完成的 list/sync 比例变化会
   改变 CPU/请求及 RPC/请求，不允许忽略该混合比例；单次 scrape 不能证明 STW。

每轮保留三个原有 60 秒窗口：list 200/s、sync 60/s，各八个 worker，
原队列上限、完整响应校验与阈值。保留丢弃、拒绝和失败窗口，不重试、不改顺序。
延迟统计包括所有实际尝试的请求（含错误），但不包括丢弃的到达；有丢弃时不能仅凭
这部分请求的 P99 宣称改善。

只有新诊断入口增加 1 Hz 有界观测：四进程、每进程至多 256 线程、至多 70 点、
75 秒及保守 64 MiB 收据预算。保留原始 task stat/schedstat/affinity、匹配任务
差分、线程出生/退出/丢失、可见 cgroup CPU 压力/限流及 host counters。
记录采集器自己的线程 CPU、读取时间、字节和漏采；不在负载窗口写 JSON 或磁盘，
不做持续 HTTP scrape，不调整宿主 sysctl。结束后 cancel/join 再序列化。

匹配线程差分不能代表完整进程 off-CPU 或 Go goroutine 等待。动态
`sched_schedstats=0` 本身不表示独立的 task schedstat 不可用。
已确认的自然线程退出降低覆盖率，不单独否定存活线程的控制；读取失败、身份复用、
亲和性不符仍使观测无效。不得将缺失的退出区间当作零等待。
另外分开记录 producer lateness（全部计划到达，包括丢弃）与 queued-worker wait
（实际尝试的请求，包括错误）；不同分位数不能相加。
第二项从非阻塞 enqueue 尝试前的时间戳开始，包含少量 producer 记账，不是纯 channel 驻留。
观测本身可能干扰结果，必须报告其成本。
原有后置短 pprof 阶段单独保留，不能据此断言原窗口的确切等待原因。

## 执行与停止条件

工作流 `conversation-qps-diagnose.yml` 的 `cpu_contention_validation=true`
选中单独 job；产品输入为 C，其余诊断模式关闭。先实际运行 Linux 的进程清理、
all-thread affinity 与 sampler 预检，拒绝 skipped。一轮 12 分钟、脚本 84 分钟、
job 90 分钟，保留既有 owned-session 清理与不完整收据。

准备超时、拓扑/身份/亲和性改变、观测失效、意外请求错误、runtime 激活、
membership mutation 或进程泄漏均停止。数值门禁拒绝保留后继续固定序列。
新模式不会重新执行或替代已通过的完整 20 项候选门禁，也不发布版本。
结束后核验实际二进制、驱动、输入、所有窗口与 artifact 摘要，再解释实验结果；
若证据不支持归因，明确记录未确认，不能改筛选条件追求通过。
