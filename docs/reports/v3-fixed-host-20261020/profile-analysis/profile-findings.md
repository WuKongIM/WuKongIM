# 固定主机短 profile 离线分析（run 39453556254）

结论：未找到足以归因到候选新增热点、并支持特定产品代码修复的证据。同一 B 二进制短采样的 CPU 与延迟已经有明显波动；这些结果不能证明或排除小幅回归，更不能确认历史 7763 环境的失败根因。C 完整门禁已通过属于另一个独立证据；本分析只检查已有短 profile。

本次离线分析检查三项假设：① 候选新增热点应在归一化函数成本上持续超过 A/B；② 若驱动争用相关，驱动 JSON/响应处理与等待信号应随变化；③ 若短采样自身不稳定，同一二进制 B4/B5 应出现相近波动。这里只检验已有证据，不新运行负载。

## 身份与归一化

- A：`4d44b39dda6fdaa7dafb637ca6cfb718e9530f77`；B：`b469c6e5e2b4ff65611a8fe3389fb5034de3f152`；C/H：`ef798352c40a94e57f768f76b2f4713712f827fe`。脚本逐一核验实际产品和驱动 binary SHA256。B4/B5 使用同一字节摘要 `e64cf368464e1bb38907f9805b22178e21847eb2c540b2cb6c892db88901c1dc`。
- CPU“核数”=采样 CPU 纳秒 / 各自真实 capture 时长；server 核数是三节点各自归一化值之和，不把三个同时运行的 duration 相加后当单一墙钟时长。累计函数值可重叠，不可相加。
- “CPU ms/请求”和“分配字节/请求”取 report.profile_window 的共享 server metrics / 2600 个完成请求。该 10 秒窗 list=2000、sync=600，五组均无 errors/drops；它不包含 driver CPU。
- alloc pprof 使用每节点 after − before 的 alloc_space；这是统计采样估计，快照跨度还包括 warmup/抓取开销，因此不冒充精确 10 秒窗的每请求分配。

| 块 | driver CPU核 | server CPU核 | 10s server CPU ms/请求 | 10s 分配 MB/请求 | 短窗 list/sync P99 ms |
|---|---:|---:|---:|---:|---:|
| 03-A | 0.4243 | 2.6099 | 10.9808 | 2.361464 | 26.456 / 55.108 |
| 04-B | 0.3630 | 2.3835 | 10.0192 | 2.361245 | 20.008 / 44.861 |
| 05-B | 0.4171 | 2.5665 | 10.8192 | 2.361506 | 27.882 / 59.930 |
| 06-A | 0.4279 | 2.5824 | 10.9154 | 2.359652 | 30.064 / 63.791 |
| 07-C | 0.4497 | 2.4816 | 10.5423 | 2.357982 | 243.138 / 144.447 |

## 可支持与不可支持的归因

1. B4→B5（完全相同二进制）：server 归一化采样 CPU +7.68%，driver +14.89%；10 秒窗 CPU/请求 +7.98%，分配/请求仅 +0.0111%；list/sync P99 +39.36% / +33.59%。CPU 与延迟变化不能都解释成版本变化，且原始三窗 A/A 重复性不满足预声明容差。
2. 五组主要 server 热点相同：RPC 服务、readSelectedConversationHeads/readStoredConversationHead、mallocgc；分配平坦热点仍为 JSON Marshal、channelRegistry.acquire/retainWarmLocked、会话头组装和响应缓冲。未见只在 C 出现且达到 0.01 采样 CPU 核的累计函数。这个 0.01 仅用于列举新热点，非验收阈值；低频新函数不能据此断言为零。
3. C 的 server 归一化采样 CPU 位于 A/B 范围内，10 秒每请求 CPU 也在其范围内，分配约 2.36 MB/请求几乎不变。C 的 HTTP 累计栈略高，而 RPC/存储/运行时在其他块也波动，未形成稳定、独有的新增热点。这里没有足以选择某段代码做修复的相对回退证据。
4. driver 的 JSON Unmarshal 占自身 CPU 约 58%–66%，五组一直存在。C 短 profile 窗 list P99=243.14 ms，其中 driver_wait P99=202.05 ms、request P99=67.38 ms；这与短时调度/排队变化相容，但 CPU profile 只记录运行时间，无法证明 off-CPU 等待原因。request 还包含响应读取、JSON 反序列化和身份校验，不是纯服务器耗时；两个 P99 不可相加。

## 捕获范围与限制

所有 CPU/alloc 抓取发生在三次原始 60 秒窗口之后。server CPU 请求 8 秒、driver capture 约 11 秒；节点 capture 覆盖 warmup 和约前 7.x 秒的额外 10 秒窗，不覆盖全部额外窗，更不覆盖原先 180 秒正式混合读窗口。详见 profile-comparison.json.coverage。不能把某个短 profile 的函数占比用于解释原始三窗的尾延迟或历史失败。
样本较短且没有精确逐请求 CPU attribution。alloc_space 有采样误差与 GC 更新时点影响；前后差值不能冒充全部真实分配。一次 C 短采样也不足以证明稳定提升或回退；现有证据支持继续保留“根因未定”，不支持仅依据这些 profile 修改产品实现。

## 重现与原始来源

输入：`download/conversation-fixed-host/blocks/{03-A,04-B,05-B,06-A,07-C}/report.json`、`driver-cpu.pprof`、`node{1,2,3}-{cpu,alloc-before,alloc-after}.pprof`，以及对应 `binaries/{A,B,C}/wukongim-e2e` 和 `harness/conversation_qps.test`。
运行 `python3 profile-analysis/analyze.py --artifact-root <解压根> --repo <冻结 H 仓库> --output-dir /tmp/new-profile-analysis`，然后 `python3 profile-analysis/summarize.py --analysis-dir /tmp/new-profile-analysis`。output-dir 必须是新目录，拒绝覆盖已有证据；repo 缺省为当前目录；需要 Go >=1.25，若 PATH 的 go 较旧可传 `--go /path/to/installed/go`。extract.go 使用仓库已锁定的 google/pprof parser；工具链必须已在本机安装，重现显式禁用模块/校验和网络和隐式工具链下载；同时保留 go tool pprof 对真实 Linux binary 的文本输出、完整命令、输入 hash 与解码 JSON。未执行 artifact 中的任何产品或 driver binary。
