# 首窗之前的时间与额外混杂

只读使用旧运行 `39368295521`、`39369947381` 的 `run.json`、`report.json`、`test.log`。精确时间与公式在同目录 `preparation-timing.json`。未读取或操作新反序运行。

| 运行/执行位置 | 诊断 step 开始 UTC | 首窗开始 UTC | step→首窗 |
|---|---|---|---:|
| 原 b469 / 7763 | 04:12:55 | 04:14:57.423 | 122.423 s |
| 同机 beta.24 第一组 / 9V45 | 04:31:53 | 04:34:35.202 | 162.202 s |
| 同机 b469 第二组 / 9V45 | 04:37:48 | 04:38:21.581 | 33.581 s |

这列是“首窗前所有步骤的总时长”，可作为 fixture 准备的宽松上界，**不是 `prepare()` 的实测时间，也不是服务端进程年龄**。包括 Go test 编译/依赖、测试初始化、cluster 启动、readiness、600 个 channel 的造数、runtime eviction、48 个 warmup 请求及 scrape。product step 还先执行一次默认测试，而 comparison step 直接执行 e2e；第一组 log 明确记录了一次 websocket 依赖下载，但没有每段编译时间。工作流编排见冻结 [conversation-qps-diagnose.yml:98](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/.github/workflows/conversation-qps-diagnose.yml#L98)。

日志还能证明差异不全来自编译。`--- PASS ... (Xs)` 给出单个 test 函数耗时，三个版本依次为 **231.11 / 294.22 / 224.28 s**；首窗开始至独立 profile 窗结束分别为 **193.809 / 191.802 / 191.855 s**。二者相减得到 test 内首窗前耗时的上界 **37.301 / 102.418 / 32.425 s**，因为剩余还包含 profile 后处理/cleanup。用 step 结束为 test 结束的上界，可夹出约 **36.53–37.30 / 101.42–102.42 / 31.86–32.43 s** 的前缀范围；GitHub step 时间只有秒级精度，应为这些边界另留约 1 秒余量。

因此同机 beta.24 与 b469 的 test 内首窗前工作相差约 **70 秒**；这是实际被测过程前缀差异，不能只称为“第二次编译缓存更快”。但这个前缀仍混合启动、readiness、造数、eviction、warmup 等，没有单独计时，**不能断言省下的 70 秒全部来自某个产品修复**。

源码确有能够改变初始写入阶段的差异：

- beta.24 到 b469 将 cold metadata batch 的收集等待从 **500 ms 改为 20 ms**，见 [meta_create_batcher.go:26](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/pkg/cluster/channels/meta_create_batcher.go#L26) 与该文件两 SHA 的 diff。
- commit coordinator 等待窗到期后改为收取一个有界的已排队请求快照，见 [coordinator.go:479](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/pkg/db/internal/commit/coordinator.go#L479) 与 [新收集函数:513](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/pkg/db/internal/commit/coordinator.go#L513)。
- fixture 通过 4 个 worker 调用公开建 channel 与 SEND 接口创建持久历史，见 [conversation_qps_test.go:149](https://github.com/WuKongIM/WuKongIM/blob/b469c6e5e2b4ff65611a8fe3389fb5034de3f152/test/e2e/message/conversation_qps/conversation_qps_test.go#L149)。

这些事实说明“fresh cluster”只保证重新创建进程与 fixture，**不保证首窗时拥有相同进程年龄、相同写入时间分布或相同后台稳定状态**。已有 receipt 没有捕捉精确 process-start 时间、造数分段耗时、首窗之前的 GC/compaction 时间线，也没有共同 idle-settle 条件，所以后台差异仍是未排除的混杂，不是已证实原因。不能凭准备更快就推断 GC 或 compaction 导致 P99 上升。

反序 pair 能检验 runner 执行位置，但若 beta.24 无论先后都具有较长 test 前缀，它仍不能单独区分“读取路径版本差异”与“版本改变准备过程从而改变读窗初态”。解释新结果时应保留这个限制，不自动增开实验。
