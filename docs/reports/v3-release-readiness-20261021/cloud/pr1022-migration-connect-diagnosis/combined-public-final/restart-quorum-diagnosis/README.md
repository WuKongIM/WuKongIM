# 新首红：1→3 全节点重启后的历史读取

冻结源 `9a4900dc67ac674791edba11ea9a719cd91ec570`，产品二进制为该 clean 源构建；场景 v1 source freeze/patch 与原始日志保留在 `../repair-v1/`。原 Linux 首次 WKProto refusal 与本次重启后 HTTP400 是两处独立失败，均不得覆盖。

## 已证实

- full-five 首次运行 exit1，102.620s；1→3 失败，另四拓扑通过；SID41084 cleanup complete，零剩余。
- 1→3 导入后每节点3行及真实协议追加后每节点4行均通过；全节点重启后，node1 第一条 `/channel/messagesync` 返回 HTTP400，仅 `msg`/`status`，没有 `code:unavailable`，原严格 fixture 没有重试该错误。
- 错误为 `internal/message: append failed: channel replication: durable quorum unavailable`。失败诊断时三进程 PID41246/41247/41248 均 running，HTTP readyz 均200。保留的是各节点有截断标记的 stdout tail，含 Slot 恢复与 leader 变化；无 Channel 逐 voter completion。app/error 文件当时不存在；临时目录已由原 cleanup 删除，无法补回原完整日志。

## 源码闭包与 #1016 的区别

- `internal/access/api/channel_messagesync.go:70` 先用 `message_read_error.go:15` 分类；未识别则走原 HTTP400。
- `internal/infra/cluster/message_reader.go:67/75` 将 committed read 错误交 `error_map.go:28`；缺少明确 typed readiness 则在43行包装 ErrAppendFailed。
- `pkg/cluster/channels/service.go:979–1009` 对 cold/recovering Leader 调 `activateColdReadMetas`，1295行 ApplyMetaContext；reactor quorum 安装错误经 pending futures 原样传播。历史读取可以为证明既有提交范围写 native recovery barrier，不代表此接口执行了业务 SEND。
- `pkg/channel/replication/quorum_log.go:100–105` 的 #1016/98c250 修复只识别 `errRecoveryProbeIncomplete`、`errRecoveryQuorumUnavailable`；192–194行之后的 `writeCurrentTermBarrier` 错误原样返回。
- `recovery_barrier.go:53–58` 的 durability round 可返回另一 sentinel `errDurableQuorumUnavailable`，未进入上述 readiness 包装。
- 不能仅按该 sentinel/text 扩大 transient 集合：`quorum_round_state.go:214–268` 汇总未知/远端冲突/本地非durable等结果；现存 `quorum_round_test.go:230` 明确证明 local sync failed 也落入同 sentinel，253行 unknown 亦如此。实际失败缺少逐 voter cause，暂时还是永久原因未定。

## 三项可证伪假设与最小下一实验

1. **暂时 barrier peer quorum 不足未保留 readiness**：真实 Install seam 中，先为3 voters种同一非空、较旧 authority前缀，仅使 barrier两remote返回已知临时 unavailable；应失败且不开放读写，再只移除该fault后应完成安装并保留旧业务内容。当前源码预测首错误不含ErrNotReady。
2. **本地持久化/非法proof问题被相同sentinel汇总**：同一seam只将local barrier Sync换成typed storage error（另设local conflict、remote conflict负例），应该保留原cause/冲突且不能获得临时readiness身份。当前源码对local非conflict错误会丢cause，需要实证；不能将H1单一正例外推为全部可重试。
3. **错误来自别的read路径/authority状态**：源调用闭包支持barrier分支，但实际节点无堆栈/voter证据；在上述seam记录阶段以及回传真实read/RPC envelope，再确认它与观察到的400匹配。若无法匹配，禁止按推断改product。

建议下一步仅写上述实际 `quorumLog.Install` seam 的有限同步失败用例（复用 `quorum_log_test.go:238` seeded harness，不延长真实时间、不全suite扫频），先保留首红及false-positive controls，再决定最窄产品修复。若要提供新typed failure，应只由实际durability结果得出，不能用HTTP字符串规则或fixture新增400重试。此文仅提议，尚未新增/运行这些测试、未修改产品。

## Fixture 修复状态

原独立 WKProto readiness 修复 v2 冻结于 `../repair-v2/`；root review指出v1两处diagnostic参数 eager执行，v2已改成仅失败分支采集。v1 focused1→1通过、full4/5；v2尚未运行负载，按指令先调查本次新失败。生产源码、原HTTP readyz、业务Connect/SEND及所有deadline/assertion未改。
