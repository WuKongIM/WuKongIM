# 同 host 对照结果

运行 [39369947381](https://github.com/WuKongIM/WuKongIM/actions/runs/39369947381) 完成；原二进制摘要锁、双方 source/harness/profile 身份均通过核对。**beta.24 和 RC 候选各三个 mixed 窗口全部通过**，实际 QPS 约 200/60，HTTP errors、驱动请求 drops、runtime loads、membership writes 和前后 runtime residency 均为 0。本次未要求20 endpoint matrix，也不具备发布授权。

| 版本/窗口 | list QPS | list P99 ms | sync QPS | sync P99 ms |
| --- | ---: | ---: | ---: | ---: |
| beta.24 / 1 | 200.00 | 15.45 | 60.00 | 32.14 |
| beta.24 / 2 | 200.00 | 16.30 | 60.00 | 32.75 |
| beta.24 / 3 | 199.98 | 16.43 | 59.98 | 35.59 |
| RC / 1 | 200.00 | 24.96 | 60.00 | 55.95 |
| RC / 2 | 200.00 | 23.13 | 60.00 | 48.54 |
| RC / 3 | 200.00 | 24.00 | 60.00 | 53.45 |

门槛保持 list ≥190 QPS、sync ≥57 QPS、P99 ≤500ms，errors/drops=0。原始报告保留所有窗口；独立profiling窗口不混入表中。

两个版本在同一个 **4 CPU AMD EPYC 9V45** runner 上顺序执行，baseline先、candidate后，各自新集群。它与首轮失败的 **4 CPU AMD EPYC 7763** runner 不同；同为四个可见CPU并不等于相同单核能力。

三窗口服务累计CPU：baseline 396.44 CPU秒，candidate 407.65 CPU秒，候选高约2.83%；服务累计分配110.0969GB和110.1089GB，约+0.011%，每完成请求均约2.353MB，低于5MB共享窗口门槛。host non-idle：baseline64.51–66.05%，candidate66.51–68.40%；CPU PSI some：baseline32.23–34.54%，candidate36.39–37.87%。两版均无观测到的steal或cgroup throttling增量；cpu.max缺失保持unknown。这些host cuts包含有限前后观测开销，CPU/分配属于共享窗口，不能重复分摊给两个endpoint。

假设更新：

1. **产品读取/hydration的新回退：未证实。** 本次候选list/sync尾延迟比baseline高，CPU高约2.83%，值得保留；分配几乎相同且两版都远低500ms。单次顺序实验仍有时序、集群放置和host压力差异，不能把这些差值直接归为代码回退。
2. **driver与服务同机竞争：仍获支持。** 首轮7763约95%忙、PSI约60%并未达标；本次9V45约65–68%忙且全部达标。资源余量与结果变化方向一致；未单独改变驱动放置，因果尚未隔离。
3. **runner能力/时段差异：获得更强支持。** 完全相同candidate二进制在不同CPU型号上从失败变为通过；但只有两个runner样本，不能证明所有差异仅由型号造成。

**原 run 39368295521 的失败继续有效**：其三次list窗口没有满足固定门槛，20项矩阵未执行。本次通过不能擦除它、不能替代完整矩阵，也不能替代精确正式标签的发布门禁。当前保留“固定性能环境敏感性/完整性能验收尚未闭环”，而不是宣布新产品回退或宣布QPS全面通过。没有追加dispatch、阈值修改或付费资源请求。

身份：baseline `4d44b39dda6fdaa7dafb637ca6cfb718e9530f77` / binary `2aba832435ad303bd70e47a79b618114d5522aca70109b6a0d7f45affc652853`；candidate/harness `b469c6e5e2b4ff65611a8fe3389fb5034de3f152` / binary `963bdb3d10489dea79550de3a18b4e6d7ffb903d18eedf5ebaa6c2ba516f7ecf`。仅选择性下载小JSON、身份和日志，未下载binary或profile，归档整体digest没有本地重新计算；单文件ZIP CRC由zipfile读取校验，本地SHA256已记录。
