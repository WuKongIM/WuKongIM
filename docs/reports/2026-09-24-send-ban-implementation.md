# 用户与频道发送禁令：实施与验证记录

核心实现和主要故障验证已完成；完整压力与发布验收尚未完成。此报告不宣称性能 SLO 或发布就绪。

- 方案：[用户与频道发送禁令设计](../specs/user-channel-send-ban.md)。
- 分支：`codex/issue-973-send-ban-design`。
- 起点：`64f73d99b3b0cb8960d40825f76053f5a0000dbd`。
- 测试源码指纹：`c250ea78a116abd3018b1570d348b30da226ecea288a3e7de77bb9a8d6b10a31`。算法和文件清单见 [manifest](assets/send-ban-20260924/manifest.json)；提交前的测试用基线提交与指纹标识，不伪称是在干净发布提交上执行。

## 已实现的行为

`User.SendBan` 按 UID 限制全部设备的发送；`Channel.SendBan` 限制实际源频道，规范私聊频道 A@B 双向生效。两个维度独立解禁，CMD 继承源频道。系统 UID、系统设备和跳过 hooks 都不能跳过新增禁令。请求级 subscribers 发送只有用户维度约束，因为它没有持久源频道。

新增用户/频道禁令 GET、POST 接口，支持可选版本 CAS，管理 API 版本使用十进制字符串。写入在 Slot FSM apply 时原子修改，Token 更新和省略 send_ban 的普通频道更新保留禁令。显式修改才递增版本；重复值不递增，终态和版本溢出不允许修改。新数据目录使用格式 2；没有实现旧禁令语义迁移、混部或回填。

消息用例一次规划 UID、源频道、成员与名单事实；按事实去重，再把同一 Leader 节点的不同 Slot 合为一个 RPC。每个 Slot 仍独立执行 fresh ReadIndex、durable apply、固定快照和前后路由校验，整个读取持有维护准入。只缓存辅助成员事实，禁令不跨批次缓存。

执行有界：4096 个事实、1 MiB 节点请求/响应、每批 4 个 worker、节点 16 个活跃信封（解码前准入）、最多一次失败路由重试。本机 Leader 使用相同校验路径且不走 loopback RPC。只读 RPC 传播取消；同预算的独立取消请求可共享读取而不互相取消。

新增权限/RPC/屏障/拒绝指标，并接入现有消息 Grafana 仪表盘。

## 真实集群验证

测试使用本机独立服务进程、HTTP/WKProto、256 Hash Slots、12 个初始物理 Slot、1 小时辅助权限缓存。覆盖单节点集群与三节点集群，另外以三节点、每 Slot 一个副本验证非副本接入；这些都是集群路径。

[功能结果与时间线](assets/send-ban-20260924/functional.json) 和 [非副本结果](assets/send-ban-20260924/non-replica.json) 均通过，主要断言包括：

- 用户禁令覆盖持久/非持久发送、CMD、请求级接收者、HTTP、已连接 WKProto 设备；仍能接收和确认消息。
- 私聊双向禁令、其他私聊不受影响、用户/频道独立解禁、群禁令与系统身份、预先禁言。
- Token 更新不清除禁令，CAS 冲突，字段省略保留，显式零值解除，非法输入拒绝。
- 进程重启后禁令保留；用户 Slot Leader 停机后，新 Leader 仍返回禁言。
- 保留当前 UID Slot Leader、停止其他两个节点：已预热允许状态不能代替 quorum，SEND 返回 HTTP 503。恢复后，通过完整已提交历史确认拒绝请求不存在，并以恢复后的成功消息作为查询正向对照。
- 非副本节点没有本机用户策略副本，仍立即看到跨节点禁言和解禁；已知用户禁言优先于缺失群错误。

### RPC 计数实证

通过公开 Manager 确定实际 Leader placement，选择不同物理 Slot 的系统 UID 与源频道，在公开 Prometheus 上读取差值：

| 接入位置 | 事实涉及 Slot | 权限节点 RPC | 服务端 Slot 读取 |
| --- | --- | --- | --- |
| 两个 Slot 的远端接入节点 | 2 | 1 | 2 |
| 两个 Slot 所在 Leader 节点 | 2 | 0 | 2 |

这验证了节点聚合与无 loopback，**不是**吞吐提升比例或总网络流量下降比例。Raft 屏障仍按 Slot 执行；重试、分块和实际 gateway 批次分布会改变成本。功能时间线中的单次延迟不作为性能基准。

## 自动检查

| 检查 | 结果 |
| --- | --- |
| 直接相关存储、FSM、代理、集群、用例、API、Manager、app、指标、CLI 测试 | 通过 |
| 代理、消息用例、元数据、FSM 的 race 检测 | 通过 |
| 新 send_ban E2E 与非副本 E2E | 通过 |
| 既有 send_permission、no_persist、terminal_disband E2E | 通过 |
| 修改涉及的 Go 包 vet | 通过 |
| Go 格式与 diff whitespace | 通过 |
| 命名检查 flow-doc-contracts | 81 个 FLOW 合规、0 invalid；保留 10 个超过 100 行建议目标的警告 |
| 仓库全量 unit 首轮 | 3 个包失败，随后按下述原因修正/单独验证；未再次运行完整命令 |
| 仓库全量 vet | 未通过：未修改的 pkg/transport/internal/buffer/retained_test.go:7 存在 range 复制 sync.Pool/noCopy 告警 |

首轮 unit 失败处理：

1. Alibaba SDK 的本机模拟 HTTP 受到环境代理影响返回 502；清除代理变量后整个对应包通过，没有操作云资源或修改产品代码。
2. 迁移独立校验器仍按旧 native 字段集合比较；补齐新字段默认值后重跑完整 migrationv2 包，只剩一个旧错误文案断言。数据格式拒绝顺序变更对应的断言更新后，失败用例重跑通过。这是既有导入工具的结构校验维护，不是旧用户禁令语义迁移。
3. Grafana 覆盖测试提示新增指标缺失；补齐图表后通过。

terminal_disband 的旧全局 CMD 同步断言与基线已有“跳过已解散源频道”行为不一致，已按基线实现修正；没有修改 CMD 产品逻辑。普通发送拒绝、直接历史读取拒绝及成员删除断言保留。

原始日志保留在本工作副本的 `tmp/send-ban-validation-20260924/`，清单与 SHA256 在 manifest 中。上下文来源见 [冻结的 AGENTS/FLOW 摘要](assets/send-ban-20260924/frozen-context.json)。

## 后续完整验收核对

[逐项核对表](2026-09-24-send-ban-acceptance-audit.md) 保留设计第 11、12 节的完整范围与当前缺口。本轮新增单/三节点恢复策略检查、十万成员检查、压力观测字段，并修复检查中发现的目录识别、权限错误映射及 Controller 镜像可见性问题。十万成员场景在建群准备阶段超时，不能记为禁令规模验证通过。最终单节点恢复（142.08 秒）、三节点恢复（429.21 秒）和单/三节点发送主流程回归（75.70 秒）均通过。三节点恢复完成后先检查所有节点公开 readiness，再读取策略，避免把维护退出传播期误判为策略错误。源码、二进制与日志身份见 [本轮清单](assets/send-ban-followup-20260924/manifest.json)。

## 未完成的发布验收

- 旧实现、相同 fresh barrier 的逐 Slot 方案、节点聚合方案三组同机对照，各重复三次。
- 10 万成员、5000 活跃频道、4500 SEND/s 参考负载的吞吐、P50/P95/P99、RPC/Raft 字节、CPU、分配与 GC；尚不能判断相同一致性下的 P99/CPU 5% 门槛。
- 完整插件进程矩阵，以及逐项核对表列出的剩余故障/边界场景。真实单/三节点 backup/restore 已有本轮通过证据；hook 身份变化等较小范围用例仍不能替代真实插件验收。

方案第 11 节保留完整验收标准。这些项目未通过之前，不应把当前功能证据写成性能或发布验收结论。

## 重跑

使用 Go 1.25.11 和全新测试数据目录：

```sh
GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -count=1 -timeout=5m -p=1 -v
GOWORK=off go test -tags=e2e ./test/e2e/message/send_permission ./test/e2e/message/no_persist ./test/e2e/message/terminal_disband -count=1 -timeout=3m -p=1
GOWORK=off go test -race ./pkg/slot/proxy ./internal/usecase/message ./pkg/db/meta ./pkg/slot/fsm -count=1
```

通过 `WK_E2E_SEND_BAN_REPORT` 指定结果路径；`WK_E2E_SOURCE_REVISION` 与 `WK_E2E_SOURCE_FINGERPRINT` 可把源码身份写入测试产物。非副本场景生成 `.non-replica.json` 后缀文件。命名检查的执行参数以仓库 Review Agent policy 为准。
