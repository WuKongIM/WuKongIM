# NeedFrom-only 最窄候选补充复核

本附录保留上层 review.md/manifest 原字节，收窄其推荐为 root 提出的既有 result 计数方案；不需要实现新的通用错误类型/原因列表或修改 wire6。源码仍为 9a4900dc。本次仍只读，未测试。

建议仅在 current-term barrier 返回原 `errDurableQuorumUnavailable` 时判定：`localDurable && durableVotes < writeQuorum && durableVotes + len(repairs) == len(authority.Voters)`，然后保留原 sentinel 并赋予该特定 barrier 失败 ErrNotReady。成功、取消、其他错误先按原语义返回。

证明来自 quorum_round_state.go：

- 54–73：start 拒绝零/重复 voters、缺本地 voter、无效 quorum；followers 只来自冻结 voters。
- 83：每次调用新建 completed 与 result；repairs 不是修复 owner 历史队列。
- 115–120、137–142：reserve/dispatch 把 completion 绑定本轮具体 voter。
- 215–221：terminal 或已完成 voter 的回调直接丢弃；同一 voter 不能先 repair 后 durable 双重记账。
- 224–229：validRepair 要求远端、follower==该 voter、DefinitelyNotWritten、typed errReplicaNeedsRepair、正偏移，才加入 repairs。
- 239–246：durable/unknown/conflict 分支互斥；只有 durable 增加 durableVotes。repair outcome 非 durable，故两类互斥。
- 248–269：足够票+本地durable立即成功，失败耗尽才产生 sentinel；unknown/conflict/提交错误不会增加两类计数。因此等于全部 voters 意味着除 durable 外全部是已验证 NeedFrom。任一混合永久/未知票使总和不足，拒绝推广。
- 281–295：完成发布一次；其后 onResult 的 terminal 检查拒绝修改 result；返回 repairs 不会被迟到证明改成“先前历史”。同步 callback 的 starting/dispatching 门槛也在发布前收敛。

无需新增 follower membership/唯一性重复扫描：绑定闭包、completed 和 follower==voter 已证明。唯一可补的精确无效输入边界是 `needFrom <= proposal.last`：真实 peer_batcher.go:590–591 已验证它，但 round 的 validRepair 目前只校验 >0。若 barrier 以后以该字段为错误分类依据，建议在原 validRepair 加这个上界或在 barrier gate 检查一次；不需要两层新抽象。

失败模式与先行回归可压缩成同一个真实 Install 表：local durable+所有其余NeedFrom 是唯一新暂态阳性；NeedFrom+conflict、NeedFrom+unknown、local failure+NeedFrom、Backpressured（即使全部远端）、malformed/越界NeedFrom均不新增NotReady；cancellation不改。另独立重复/迟到回调可复用已有round测试验证不能充票，正常quorum早退必须保持成功。

失败后 Ready/HW/frontier 仍不得发布。清除缺口后重新 Install 应通过原 recovery+barrier机制，旧业务记录身份不变；不应添加任何请求循环或让既有HTTP400文字成为重试依据。

该选择避开已发现的 Backpressured 宽编码边界，因此本切片不用修远端错误协议。它是安全的窄支持能力，但现有 E2E 日志未保留本次各 voter 原因，不能宣称本次400已证明属于 NeedFrom-only，也不能推断旧CI连接拒绝的根因。
