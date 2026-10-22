# Independent static challenge: cold recovery admission

**建议有条件推进 tests-first 的冷 UID 隔离候选；目前不能认定原云端 message_loss 的正式产品修复。** 源码明确把每页涉及的 HashSlot 模32 lanes 全部持有至 owner I/O 与安装完成；当前 RED 用可用 owner 端口隔离了这个机制。500 QPS 的一次快照提供现场相关性，不提供五个过期样本与这条等待路径的身份、时长或因果关联。

没有找到同时保留批量读取、同 UID 合并和任意不同 UID 隔离的更浅替代：UID 哈希或增加 lanes 仍有碰撞；逐 lane/target 分拆 owner 查询会重付 unavailable-owner timeout；直接放开锁会重复同 UID owner 读取。建议把完整 distributed target+UID flight 索引作为 `AuthorityRecovery` 内一个私有机制，保持 Directory、owner adapter 与 RPC 协议不变，避免增加通用调度器、结果缓存或 Directory pinning 层。

最小正确形状：短 mutex 内按页原子地区分已 proven、已有 flight 与新 UID；新 UID 作为一个512 UID/256 target以内 cohort 先走现有 owner batch，逐成功 target 安装；**立即完成该 cohort 的 flight、移除 keys 并释放 cohort/bytes 预算，然后才 join 更旧 flights**。完成信号不应延后到整个调用完成，否则 `B={old x,new y}` 仍会让独立 `C={y}` 被 x 拖住。纯 join 不占新 cohort 配额；hot proof 保持旁路。32 cohorts、16,384 keys 和实际复制 UID bytes 应统一预留，超界闭合返回 unknown，而非新建无界等待队列。

需要在实现前固定的风险：

| 风险 | 最小验收性质 |
| --- | --- |
| 一个 cohort 有多个 target，部分失败 | flight 结果按 target/key 保留，不用一个 cohort-wide error 覆盖成功 siblings；错位/缺失 owner 响应不得产生任何负证明。 |
| 旧 owner caller 取消，仍存活的 follower join | follower 取消不取消 owner；owner caller 取消时，存活 follower 不应误收自己的 `context.Canceled`。按现有 parent-live child-error 语义返回 retryable unavailable，不能偷偷新建 owner retry 或后台延长工作。 |
| proof 发布后被1024项 FIFO 淘汰 | 更高同 HashSlot 并发会扩大这一窗口。join 完成后重读 Directory、检查 fence/tombstone；缺证明仍是 ErrRouteNotReady，不能当 offline，也不能直接回传共享缓存 routes。测试 late waiter+并发 FIFO eviction，明确不承诺成功 flight 永远意味着当前 proof 存在。 |
| target key 错误 | key 包括 HashSlot、SlotID、LeaderNodeID、LeaderTerm、ConfigEpoch；RouteRevision/AuthorityEpoch 按现有合同是路由/本地观察元数据，不应误作跨节点 fence。新 term/epoch 的同 UID 不得 join 旧 flight。 |
| 容量与数据所有权 | 在 clone 前预留32/16384/byte预算；索引 key 必须拥有 UID 字节，避免小 substring 留住大 backing allocation。取消、error、panic 路径均释放并唤醒；不保留 per-waiter/per-UID goroutine 或无界 waiter 列表。 |
| 饱和时过度承诺 | owner admission4仍可成为真实排队点。独立 UID250ms性质只适用于 admission有余量且 owner 可用的控制；四个 owner cohort 真饱和时不保证该时限。 |

还应补两项组合反例：A/B 部分 UID 重叠时，先发布各自新 UID 再等待对方，不能形成占有等待环；原分组含重复 UID、跨页1025 UID时，保持原结果顺序/重复次数及已完成页结果，不因 dedupe 新索引而丢失。

**fake 与生产的重要差别：** 受控 fake 只暂停 A 的某一 owner page，并保证 B 的独立 owner 查询可以立即成功。若生产故障是 owner 全局不可用、registry 全局锁或共享 TCP 饱和，去掉 coarse lanes 不能自动恢复投递。当前只有 node2 单点的2个 owner RPC等待与45个 lane等待，没有 owner 服务端或全部3节点的对齐时序。这个 RED 不证明原 cloud1个 expired 或本地5个 expired 的根因。

**因果闭环仍缺：** 至少一个实际过期 sample 的 MessageID/recipient fingerprint/精确 target，关联到 cold admission wait 起止与 owner call 起止；以及同一二进制、配置、负载、固定 owner stall 条件下仅改变 admission 的对照。也须区分 owner RPC 本身无响应、owner admission4饱和和 coarse lane 排队，不能从47个栈或291个 idle worker推断修复效果。可用有限私有 trace/聚合等待时长，不增加 UID/target 高基数 metric labels。RED→GREEN 可证明新隔离性质；还须 exact MessageID join 与有效前后过程 E2E 对照，再决定是否作为正式产品修复。即使随后本地500 QPS通过，也不能替代原云端根因证据或正式 Linux/长稳门禁。

本次仅静态读取指定两份证据、适用 FLOW、生产合同与测试；未运行 Go、构建、测试、CI、云、profile、服务请求或进程操作，未编辑任何源码/公共 worktree。
