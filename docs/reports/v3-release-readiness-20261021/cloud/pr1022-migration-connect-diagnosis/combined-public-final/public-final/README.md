# PR1022 首次迁移连接失败诊断

结论：历史 Linux `connection refused` **尚未定因**。固定三轮 Darwin 1→1 观测均通过，不能将它们当作原故障已修复；不增加次数、不重跑 CI。现有证据确认了一个夹具就绪条件缺口，最小修复建议尚未实现。

原运行 `39625524702` / artifact `12356928133` 的首个 1→1 场景先通过 HTTP imported history 的完整 3 rows 校验，约 2.21 ms 后首次 WKProto TCP `Connect` 对 `127.0.0.1:27802` 被拒绝。尚未进入 SEND。时间是两条 Go JSONL 输出事件的间隔，不是精确服务器事件间隔。其余四个迁移拓扑通过，后续 cross-node/self-chat 未执行。原 wrapper 清理完成且无剩余进程。

失败断言没有附节点诊断，公开制品仅保留命令、JSONL、外层结果与构建身份；不存在可以事后补取的原 PID、stderr/app log 或监听状态。不能排除目标进程退出、启动回滚或端口冲突。

## 已证实的源码关系

均绑定提交 `9a4900dc67ac674791edba11ea9a719cd91ec570`，逐文件摘要见 `source-reference-sha256.json`。

- `ordinary_history_test.go:110–111` 启动已导入数据的进程后，仅调用 `WaitHTTPReady`；168 检查 imported history；171–174 第一次真实 retained-token WKProto Connect。
- `suite/migration.go:101` 的 `StartPreparedCluster` 仅启动节点，不内置协议等待。
- `suite/runtime.go:402–429` 明确区分 HTTP `/readyz` 和包含真实 WKProto handshake 的 `WaitClusterReady`。
- `internal/app/lifecycle.go:197–227` 启动顺序是 API→Manager→Prometheus→Gateway；普通静态拓扑的 `readyzReport:843–855` 只要求 cluster write routing。seed-join 的特有 gatewayStarted 条件不适用于本例。
- `pkg/client/client.go:155–159` 拒绝来自原 TCP dial，发生在 CONNECT packet 写入之前。

`hypotheses-and-experiment-v1.json` 在实验前冻结 H1 就绪窗口、H2 退出/回滚、H3 端口/配置三个可证伪假设和六项观测失败模式。

## 固定三轮实验

产品在干净 9a490 源上构建一次（Darwin arm64，e2e 标签），然后只在隔离 worktree 临时记录 `/readyz` 返回与原始 DialContext 的时间/进程状态。原失败判定、3 分钟场景 context、12 分钟 Go 总时限和业务断言保留。成功轮不增加连接；若原连接失败，才观察同一 PID/端口最多 1 秒并保留原错误。没有修改冻结的 shipping worktree。

三轮原始连接均成功；HTTP 就绪返回至 dial 为 4.511、4.051、4.175 ms，dial 为 0.762、0.491、0.260 ms，原 PID 当时均存活。包 46.357 s，外层 51.167 s。SID40182 清理完成且再次只读核对为空。没有触发失败后探针，也没有生成原始节点日志目录。完整结果见 `observation-v1/` 与 `observation-v1-summary.json`。

临时观测已经保存为 patch 并从诊断 worktree 移除；两个 worktree 均保持 9a490 干净状态。该本地循环是 red-capable（原 Connect 错误仍立即令测试失败），本次未得到红，不能宣称完成确定性复现。

## 最小修复建议（未实施）

保留原 HTTP 就绪与首次 cold imported-history 读取顺序。只在 retained-token Connect 前复用 `node1.Process.WaitWKProtoReady(ctx, node1.GatewayAddr())`，再按原逻辑认证迁移后的 Alice 凭证、发送消息和核验全部历史；专用 readiness UID 与原迁移 UID 不同。连接失败增加现成的 bounded `DumpDiagnostics`。不改变产品 `/readyz`、不重试业务 SEND、不增大 deadline。

这个建议修复已确认的 fixture readiness gap，不证明历史 Linux 根因。原 Linux 首红已经在真实场景的正确调用点发生；若批准修复，仍需原 1→1 与完整五拓扑回归、新 head 首次 Linux 门禁，以及之后原本未执行的跨节点/自聊场景。现有 helper 的鉴权与退出测试只能佐证 helper 语义，不能替代 Linux 历史证据。

`public-final/` 只包含显式白名单的公开摘要、源/规则摘要、临时补丁和本地测试日志；不归档产品二进制、缓存、凭据或私有原始节点日志。
