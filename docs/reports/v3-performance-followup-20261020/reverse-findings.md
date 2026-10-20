# 反序对照独立复核：没有复现稳定的候选版本尾延迟回退

独立从 [39407349500](https://github.com/WuKongIM/WuKongIM/actions/runs/39407349500) 的原始 report/receipt 重算，未使用已有派生结论作为输入。**两版各三个固定窗口全部通过；原正序观察到的 b469 尾延迟与平均服务端阶段增幅没有稳定复现。但本次也不能宣称候选提速约 19%，更不能确认“第二组必慢”或确定根因。**

此次比较角色是 **comparison/=b469 先执行、root=beta.24 后执行**。完整逐窗判定、统计量、资源、stage/call 差分及输入 SHA-256 在 `reverse-independent-check.json`；重算脚本为 `reverse-independent-check.py`。

## 身份与有效性

- b469：`b469c6e5e2b4ff65611a8fe3389fb5034de3f152`，binary receipt `0d25039bfc518b166eded2871a365231a041d710c52636a2154eda27179a3825`。
- beta.24：`4d44b39dda6fdaa7dafb637ca6cfb718e9530f77`，binary receipt `e327d0252e814b47efdc06ff9c6b80d847af29e30c20a1b22ba1dee98cd45a9a`。
- 两份 report 的 harness 均为 b469、`harness_dirty=false`；profile SHA 均为 `1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1`，config 与保留的固定 profile 一致，driver/node GOMAXPROCS=4/2。
- report 摘要与各自 `binary.sha256` 一致；`go version -m` 对应精确 source、clean、Go 1.25.11、linux/amd64、GOAMD64=v1、tags=e2e。**没有下载或独立重哈希产品 binary**。
- 新 host 为 Intel Xeon Platinum 8573C；旧正序为 AMD EPYC 9V45。两者均报告 4 logical CPUs、2 cores×2 threads。不可混合绝对耗时当同硬件交叉试验。
- checkout 角色与路径交换，build 未启用 `-trimpath`，两版本的新旧 binary 摘要都不同；除 `build.txt` 首行路径外，同版本保存的 build 元数据完全相同。这保持 source/toolchain 合同一致，**不构成逐字相同二进制对照**。此前摘要不匹配的 `39406775641` 为未进入测量的前置失败，应继续保留。

## 六个窗口确实通过固定门槛

每窗 60 秒、list/sync 各 8 workers、offered 200/60 QPS、队列 100/30。独立核验 scheduled、completed、in-window QPS、P99≤500 ms、QPS≥95%、report verdict、零不期望错误/运行时加载/成员写入/驻留，以及共享分配上限 5 MB/完成请求；不是仅凭 Actions success 判断。

| 版本/窗 | list QPS | list P99 ms | sync QPS | sync P99 ms | errors / drops |
|---|---:|---:|---:|---:|---:|
| b469 先 W1 | 199.983 | 49.986 | 59.983 | 99.114 | 0 / 0 |
| b469 先 W2 | 199.983 | 26.150 | 60.000 | 57.505 | 0 / 0 |
| b469 先 W3 | 199.983 | 25.538 | 59.983 | 56.093 | 0 / 0 |
| beta 后 W1 | 199.983 | 24.941 | 60.000 | 52.260 | 0 / 0 |
| beta 后 W2 | 199.983 | 42.022 | 60.000 | 95.306 | 0 / 0 |
| beta 后 W3 | 199.983 | 32.184 | 60.000 | 71.147 | 0 / 0 |

每版均完成 46,800 次请求；少数截止后完成被正确排除在 actual QPS 中。到达时间线最大延迟均小于 200 ms，没有原 7763 那种持续队列积压。

## 同时看中位数和均值，避免选择有利统计量

下面是**三个窗口各自 P99 的中位数/算术均值**，两者均不是 pooled P99，也没有统计置信区间。

| 对照 | endpoint | beta P99 中位/均值 ms | b469 P99 中位/均值 ms | b469 变化：中位/均值 |
|---|---|---:|---:|---:|
| 旧正序：beta→b469 / AMD | list | 16.298 / 16.060 | 24.004 / 24.031 | +47.28% / +49.63% |
| 旧正序：beta→b469 / AMD | sync | 32.752 / 33.494 | 53.447 / 52.644 | +63.19% / +57.17% |
| 新反序：b469→beta / Intel | list | 32.184 / 33.049 | 26.150 / 33.892 | −18.75% / **+2.55%** |
| 新反序：b469→beta / Intel | sync | 71.147 / 72.905 | 57.505 / 70.904 | −19.17% / **−2.74%** |

新反序中，b469 首窗较慢、后两窗较快；beta 第二窗较慢。按均值看双方接近。因此证据削弱“b469 稳定比 beta 慢约 50–60%”解释；中位数随第二位置较高，只能使执行位置/时段解释仍然合理，不能证明位置决定性能。

新反序 list 的 driver-wait P99：b469 8.36/2.39/2.30 ms，beta 2.17/3.66/2.74 ms；sync 均约 2–3 ms。request-call P99：b469 list 41.20/25.49/24.86、sync 98.53/57.20/55.63；beta list 24.02/38.99/31.29、sync 51.50/94.80/70.98。主要差异仍在 request 调用内，包含 HTTP、响应读取、JSON 解码、校验和调度；不是纯服务端耗时。独立 P99 不可相加。

## 资源与服务端阶段接近，没有复现旧约 20% 平均 handler 增幅

| 三窗汇总 | beta 后 | b469 先 | b469 变化 |
|---|---:|---:|---:|
| 产品 CPU 秒，3 进程合计 | 476.11 | 479.50 | +0.712% |
| CPU ms/完成请求 | 10.1733 | 10.2457 | +0.712% |
| 分配 B/完成请求 | 2,352,270.96 | 2,352,715.85 | +0.0189% |
| driver ticks/完成请求 | 0.18483 | 0.18716 | +1.260% |
| list handler 平均 ms | 7.955 | 7.903 | −0.654% |
| sync handler 平均 ms | 20.189 | 20.017 | −0.853% |

metadata / heads / edit-overlay 均值：beta 1.611 / 2.618 / 2.690 ms，b469 1.584 / 2.617 / 2.658 ms，差异约 −1.63% / −0.046% / −1.19%；各读取阶段 count 相同。业务相关 RPC exact-call 总数相同；唯一已记录的 call total 差异是 controller write：beta 74、b469 72，不能因此写“所有 RPC 数绝对相同”。旧正序这些业务调用数也相同。

本次 host non-idle 约 78.1–79.7%、cgroup 四 CPU 利用率约 76.1–77.8%、CPU PSI some 占比约 42.6–46.3%。两版都没有已记录的 steal 或 throttled-usec 增量，`cpu.max` 仍不可读。没有重现原 7763 的约 95% host non-idle 和持续 list drops。

CPU/分配为混合请求、全产品进程计数；host 窗口含预热与 scrape；driver CLK_TCK 未采集，故不将 ticks 当秒。stage 是重叠、不同人口的 wall time，不是 CPU 或可加总延迟；缺少 stage histogram buckets，无法重建服务端 P99。上述合同详见先前 `cost-findings.md` 的冻结源码引用。

## 准备历史仍随版本不同

反序 step→首窗 b469 **107.929 秒**、beta **105.313 秒**，表面接近；但第一组有 test 编译等开销，不能称为相同准备时间。结合 PASS test 耗时和 report 时间，test 内首窗前约 b469 **33.10–33.95 秒**、beta **103.71–104.19 秒**（step 秒级精度另留约 1 秒）。

这重复了旧正序 beta 比 b469 长约 70 秒的 test 前缀，说明先后交换没有消除版本相关的启动/造数/预热历史差异。`prepare()` 没有分段计时，cold metadata 等待 500→20 ms 和 commit batching 差异可以影响 fixture 写入，但不能将时间差全部归因于它们，更不能凭此猜测 GC/compaction。fresh cluster 仍不等于同进程年龄或共同后台稳定状态。

## 已有新 CPU profiles 仅作独立短窗支持

本次两版独立 profile 窗位于各自三窗之后；3 个 server 同时采约 8 秒，driver 约 10.85–10.87 秒，包含该独立负载的 warmup。未采原 60 秒窗口。

- driver 样本：b469 4.96 CPU 秒、beta 4.83；JSON Unmarshal cumulative 2.91 秒/58.67% 对 2.62 秒/54.24%。两版都存在有分量的驱动解码成本，不等于原读窗的差异根因。
- 三 server 合并 CPU 样本：b469 20.19 秒、beta 19.90。主要 task 分布相近：RPC service 34.08% 对 32.06%、HTTP 17.48% 对18.49%、connection-write 10.65% 对10.80%。采样百分比与累计栈不可据此声称某函数回退。
- 独立 profile 窗 P99 为 b469 list/sync **28.72/62.50 ms**、beta **27.38/55.73 ms**，又与主窗中位数排序不同。短窗采样不能替代完整测量。

独立 pprof 输出为 `reverse-b469_first-{driver,server}-cpu-cum.txt`、`reverse-beta24_second-{driver,server}-cpu-cum.txt` 及两份 server-tags。

## 对发布判断的影响

可收敛的结论是：**现有正序/反序证据不能确认稳定的 b469 相对 beta.24 读取性能回退，也不能证明性能完全相同。** 两个不同硬件、不同时间的 sequential pair 仍有主机、准备历史、构建路径与观测波动混杂。当前没有充分依据按某个热点直接改产品代码。

原 `39368295521` 的 7763 环境失败仍保留，其根因和容量适用范围没有关闭；本次未执行 20-endpoint release matrix、正式长稳或迁移修复后版本的 gate。Actions completed success 与上述有限固定窗口 pass 均不能提升为正式 v3 发布通过。本分析不申请或启动额外重试。
