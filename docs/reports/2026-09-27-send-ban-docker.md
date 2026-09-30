# 本机 Docker Linux 验证

本机 Docker 路径已实际运行：Linux/arm64 构建和三节点小规模正向验证通过。
5000 频道、4500 SEND/s、30 分钟的原定持续场景在发送 277.717 秒后 EOF
失败。R2 仍未通过。三组九轮 Linux 对照已执行完毕：旧基线 2/3、逐 Slot
fresh 0/3、节点聚合 3/3 通过；没有完整 fresh 配对，R6 尚未验证。全部
结果见[三组对照报告](2026-09-27-send-ban-docker-comparison.md)。

## 输入与环境

- 产品及共用 harness 来自 `a2c8e9f79d83efccc36cca77c970d6380ff468ab`，
  `git archive` 的 SHA-256 与冻结规则摘要见
  [source.json](assets/send-ban-docker-20260927/source.json)。生产代码与
  `a1e06e54c` 相同，未带入已撤回的 L0 候选。
- 使用已有 `golang:1.25.11-bookworm` 镜像原生构建 Linux/arm64；实际
  build info、二进制 SHA-256、内核及文件系统类型见
  [构建身份](assets/send-ban-docker-20260927/linux-buildinfo.json)。源码快照
  没有 `.git`，故不声称二进制内嵌 VCS 身份；用外部源码摘要绑定输入。
- Docker VM 为 10 CPU、8217968640 字节内存。任务容器上限 8 CPU、6 GiB，
  memory-swap 等于 memory，不使用容器 swap。VM 内还有两个已有容器；
  测试前其中一个约占 1.6 GiB，另一个约 99 MiB，未停止或修改它们。
  CPU 配额不是独占核，现有共享负载限制性能归因。
- 源码、Go 缓存、程序及测试临时数据库都在独立命名 volume
  `codex-sendban-linux-20260927` 内，文件系统报告 `ext2/ext3`；没有把
  macOS bind mount 或 tmpfs 用作数据库。三节点为同一容器内三个独立
  产品进程，不能外推为三台物理机器的容量。
- Docker 虚拟磁盘报告的 341 GB 可用空间不是主机真实剩余空间。构建后
  macOS 约剩 26 GiB，runner 每 15 秒采集主机空间和容器 cgroup 数据，
  主机低于 8 GiB 或外层超过 2460 秒时停止本任务容器并记录未完成。
  不清理用户容器、volume 或镜像。

## 已执行

首次离线构建因模块压缩缓存不完整而失败。随后独立联网构建等待依赖，
主动终止后改用主机已有的 156 个解压模块目录。第三次在断网测试容器中
完成 `go build -p=4 ./cmd/wukongim` 和指定 E2E harness 编译。
[所有构建日志](assets/send-ban-docker-20260927/build-logs/build-attempt03.log)
所在目录同时保留前两次记录，没有把中止下载算作构建通过。

小规模正向验证使用 25 频道、500 SEND/s、10 秒；三节点、256 Hash Slots、
10 个物理 Slots、三副本配置及原有门槛不变，append workers128、gateway
batch32 与待验场景一致。结果：5000 条完成，SENDACK P50/P95/P99 为
8/14/26 ms，RECV P99 为 27 ms，permission busy、transport RPC rejected、
membership mutation rows 均为 0。测试总耗时 22.29 秒，退出码 0；结束后
容器只剩保活进程，没有遗留产品或 harness 进程。

证据：[原始 E2E 日志](assets/send-ban-docker-20260927/positive-01/e2e.log)、
[命令与容器约束](assets/send-ban-docker-20260927/positive-01/command.json)、
[资源记录](assets/send-ban-docker-20260927/positive-01/resources.jsonl)、
[退出与进程核对](assets/send-ban-docker-20260927/positive-01/result.json)。
这只是环境和消息闭环验证，不替代持续资格。

## 原定持续场景结果

持续尝试 `sustained-01` 使用原定 5000 频道、4500 SEND/s、30 分钟，
无额外 pprof/driver/stall/storage-history 诊断。沿用已有 harness 和全部
通过条件，不放宽复制、持久化、延迟或零拒绝门槛。运行时没有编译或其他
本任务压力进程。冷预热 5000 频道用时 167.448 秒，整个测试 468.14 秒。
发送窗口在 277.716900 秒、1249726 次 SEND 调用后因 sender_read EOF 失败，
当时 pending5485，SENDACK P50/P95/P99 为 23/263/1047 ms，RECV P99 为
1021 ms。permission busy、transport RPC rejected、Channel admission full、
membership mutation rows 均为 0，834 个节点采样没有抓取错误。

失败后的公开连接指标在节点 3 记录 `async_dispatch_queue_full=1`。
三节点 goroutine 输入均完整（88580/104714/90539 字节），过滤输出未饱和。
这次栈未出现 `memTableWriteStall`；可见 Linux SyncData 等存储栈，但单次
失败后采样不能证明同步耗时或磁盘饱和，不能套用之前 Darwin 的停写结论。

对原始栈进一步提取后，三个节点分别有 103/128/107 个 Channel worker
位于 `runDurableRound`，48/37/44 个调用者等待 `Coordinator.submitResult`；
各有一个提交协调器等待 Pebble `commitPipeline.publish`，以及一个 WAL
线程处于 Linux `fdatasync`。节点 2 另有两个 L5/L6 compaction 同步栈。
这些调用链使“存储同步延迟向提交和 gateway 传播”成为下一步待验证的
假设，但不能证明某个 WAL 对应哪个数据库，也不能证明顺序抓取的三节点
同一时刻停顿。计数、完整函数链及源摘要见
[栈提取](assets/send-ban-docker-20260927/sustained-01/stack-attribution.json)。
现有 profile 选项在 30 分钟场景第 7 分钟才启动，此次第 4 分 38 秒即失败，
因此后续独立诊断需要在故障前采样，不能指望现有定时 profile 捕获本次窗口。
当前九轮对照保持原采集方式，不在中途加入 profiler。

容器 cgroup 内存最大 3836305408 字节，memory.events 的 max/oom/oom_kill
均为 0；主机最少可用 22855491584 字节，资源保护没有触发。最终累计
CPU throttle 963726 微秒包含前序构建/正向测试，并非本窗口的净值；不能
从它或共享 VM 的资源上限推断具体瓶颈。结束后产品及 harness 均退出，
容器仅保留 sleep。失败不是容器被 OOM kill 或主机磁盘保护终止。

证据：[完整原始日志（无损 JSON）](assets/send-ban-docker-20260927/sustained-01/e2e.raw.json)、
[结构化摘要](assets/send-ban-docker-20260927/sustained-01/summary.json)、
[资源记录](assets/send-ban-docker-20260927/sustained-01/resources.jsonl)、
[退出与进程核对](assets/send-ban-docker-20260927/sustained-01/result.json)。

## 三组对照进展

复现入口为 [run.py](assets/send-ban-docker-20260927/run.py)：
在上述任务容器及 `/lab/bin` 构建输入准备好后运行
`python3 docs/reports/assets/send-ban-docker-20260927/run.py <新名称>`。
runner 拒绝覆盖已有目录。每次失败及资源保护退出均保留，不以重试替换。
后续仍须完成三组各三轮的完整 fresh 对照及 5% 门槛审核。

已准备 [对照构建脚本](assets/send-ban-docker-20260927/prepare-comparison.py)，
在容器空闲后导出冻结旧基线和独立逐 Slot fresh 源码、校验并应用既定补丁、
运行参考契约，再构建 Linux 二进制。两组的 go.mod/go.sum 与当前输入相同。
长测完成且确认容器空闲后，已完成两组构建；逐 Slot 参考的既有 fresh
barrier 契约通过（0.106 秒）。两组各自的 25 频道、500 SEND/s、10 秒
正向验证也通过，结束后没有遗留进程。源码及补丁摘要见
[对照构建身份](assets/send-ban-docker-20260927/comparison-build/source.json)，
二进制身份见 [buildinfo](assets/send-ban-docker-20260927/comparison-build/buildinfo.json)。
[检查记录](assets/send-ban-docker-20260927/comparison-preparation-validation.json)
保留构建前的最初 Docker ps 参数问题及修正后的运行中拒绝结果。

[compare.py](assets/send-ban-docker-20260927/compare.py) 已启动固定顺序
baseline/slot/node/node/baseline/slot/slot/node/baseline，均为 5000 频道、
1200 SEND/s、60 秒，共用同一 harness 和容器配置。每轮保留资源和原始
日志；另以相同频率记录所有容器 Docker accounting，不额外抓取产品
指标。比较阶段未运行构建或其他本任务负载。完整九轮现已结束，所有
失败都保留；不能从部分窗口或 RPC 数量推断 P99/CPU 的 5% 门槛通过。

后续分析入口为
[analyze-comparison.py](assets/send-ban-docker-20260927/analyze-comparison.py)。
它保留预定九轮（包括失败、缺失和进行中状态），只将退出码 0、完整
72000 条/5000 频道/1200 SEND/s/60 秒窗口用于 fresh 配对，逐对检查
P99 和 CPU 的 5% 门槛；缺失数值保持 null，不用中位数覆盖超限配对。
该脚本已在实际未完成数据上运行，正确保留“尚无完整 fresh 配对”。
共享资源记录还显示第一轮期间其他 WuKongIM 容器曾切换与退出；分析会
保留名称变化时间线，不能仅凭数值通过就声称获得独占环境结论。

## 故障前 profile 尝试：预热失败

九轮结束后，用同一 Linux 二进制和 volume 启动独立的任务容器，仍为
8 CPU/6 GiB。目标为 5000 频道、4500 SEND/s、5 分钟诊断，启用既有
driver/stall/storage-history/replication 采集和第 4 分钟 CPU/heap profile。
5 分钟只用于诊断，不能替代 30 分钟资格。

这次 `diagnostic-01` 在冷预热 172.643 秒时，第 4870 个频道发送返回
HTTP 408 `request timeout`；整个测试 199.22 秒，退出码 1，未进入
测量窗口。上述采集都在预热之后启动，因此没有 CPU/heap、driver、stall
或 storage-history 产物，不能声称捕获了故障前 profile。

已有预热失败快照覆盖三个节点，输入均未截断，但抓取时主要剩空闲后台
调用链，不能用它证明此前阻塞所在。当前预热失败 metrics 选择器也没有
保留权限 admission/barrier 阶段的 count/sum；需要补充有界的既有指标
证据或在故障前采样，而不是继续重复同条件运行。

容器内存峰值 726474752 字节，cgroup max/oom/oom_kill 和 CPU throttle
均为 0，未触发 8 GiB 磁盘保护。节点和 harness 均已退出。结束后仍观察
到另一个 WuKongIM checkpoint 测试容器使用 CPU/磁盘；仅是结束后的
共享负载观测，不能倒推出这次 HTTP 408 的根因。已询问用户能否安排无
其他压测/构建的窗口，尚未得到安排；没有停止其他任务。

证据：[原始日志](assets/send-ban-docker-20260927/diagnostic-01/e2e.raw.json)、
[摘要](assets/send-ban-docker-20260927/diagnostic-01/summary.json)、
[退出与清理](assets/send-ban-docker-20260927/diagnostic-01/result.json)、
[产物清单](assets/send-ban-docker-20260927/diagnostic-01/manifest.json)。
[diagnose.py](assets/send-ban-docker-20260927/diagnose.py) 复用原资源保护 runner；
[容器环境](assets/send-ban-docker-20260927/diagnostic-container-env.json) 固定所有
诊断开关。该失败保留，不以重试覆盖，产品源码和验收门槛未改。
