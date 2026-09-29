# 独立禁令与 hook 跳过边界验证

本轮只补测试与验收证据，生产实现保持 `1ba4a6352`。设计 F2 的独立禁令
组合和 F3 的 `SkipPluginHooks` 内部边界通过；不代表整体功能、故障及
性能资格完成，剩余范围仍按[验收核对表](2026-09-24-send-ban-acceptance-audit.md)执行。

## 真实进程矩阵

单节点集群和三节点集群均使用 256 Hash Slots、12 初始物理 Slots、1h
辅助权限缓存。写入节点轮换，每次写入成功后立即从全部接入节点发送。
保存每一步的策略版本、请求标识、原因码、序号与时间。

| 状态顺序 | 用户禁令 | A@B 与目标群禁令 |
| --- | --- | --- |
| 预热允许 | 0 | 0 |
| 仅用户禁发 | 1 | 0 |
| 两者禁发 | 1 | 1 |
| 先解除用户禁令 | 0 | 1 |
| 再解除频道禁令 | 0 | 0 |
| 再次同时禁发 | 1 | 1 |
| 先解除频道禁令 | 1 | 0 |
| 再解除用户禁令 | 0 | 0 |

每个状态验证 A→B、B→A、A→C、A→目标群、B→目标群及 A→其他群。
总计 192 次 SEND，96 次成功、96 次原因码 25 拒绝；拒绝均无消息 ID
与序号。两个拓扑各四个频道的完整历史严格等于成功请求集合，共 96 条，
无多余记录、重复记录或未读页。该项证明持久化侧无拒绝记录，不额外声称
已覆盖所有拒绝类型的在线投递侧矩阵。

整个主流程 E2E 通过，耗时 94.76 秒（单节点 23.70 秒、三节点 71.06 秒），
同时保留此前类型 3–12、既有连接、重启、Leader 丢失、quorum 失败关闭
和 RPC placement 检查。

首次运行失败：新增夹具用 `messages` 解析 Manager 实际返回的 `items`。
192 次决策已符合预期，但没有完成历史核对，因此保留该轮为失败；修正
JSON tag 后完整重跑通过。没有因等待失败降低历史集合要求。

## 内部标志边界

`SkipPluginHooks` 不在 PDK `SendReq` 中公开，直接在消息用例边界验证。
跳过/执行 hook × 单条/双条批量 × 群/信息频道/显式接收者 × 八种连续
策略状态，共 96 个状态检查。允许请求以实际 submitter 结果为正向对照；
拒绝请求不能调用 hook 或 submitter。跳过 hook 时 payload 保持原值；
普通 hook 路径确实修改 payload。显式接收者只受 UID 禁令约束。

所有 `TestSendBan*` 普通测试（0.313 秒）与 race（1.374 秒）通过。
Mac 链接器产生既有 `LC_DYSYMTAB` 警告，未影响测试结果。本轮不修改
生产行为，因此无新增 CHANGELOG 条目，也没有重复运行无关压力场景。

## 重跑与身份

```sh
GOWORK=off go test ./internal/usecase/message -run '^TestSendBan' -count=1 -v
GOWORK=off go test -race ./internal/usecase/message -run '^TestSendBan' -count=1
WK_E2E_SEND_BAN_REPORT=/tmp/send-ban-combinations.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestUserAndChannelSendBan$' -count=1 -timeout=5m -p=1 -v
```

实测使用 Go 1.25.11、Darwin arm64 和上一轮同一预构建二进制。
[源码与二进制身份](assets/send-ban-combinations-20260924/source.json)记录基础提交、
测试文件摘要与二进制 SHA256；其生产输入与该提交一致，未伪称二进制的
构建 stamp 来自此后的干净提交。完整原始结果、首轮失败、冻结上下文及
摘要均列在[产物清单](assets/send-ban-combinations-20260924/manifest.json)。
