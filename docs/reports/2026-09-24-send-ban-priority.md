# 已知用户禁令与频道 Slot 不可用

设计 F7 的真实进程故障优先级验证通过，耗时 23.42 秒。生产二进制与
`1ba4a6352` 相同；本轮基于 `42b291b14` 补测试，没有生产代码变更。

## 拓扑与断言

三节点集群、256 Hash Slots、12 物理 Slots、每 Slot 一个 voter、1h 辅助
权限缓存。公开 Manager placement 确认 UID Slot 12 位于节点 3，频道
Slot 8 位于节点 1；节点 2 为第三方接入。选择单副本是为了确定性制造
一个策略 Slot 不可用而另一个仍可用，不是高可用部署的推荐或承诺。

先成功发送 `priority-warm`，写入用户禁令，再停止节点 1。
公开频道策略查询返回 503，证明目标策略权威确实不可用。

| 操作 | 接入节点 | 结果 |
| --- | --- | --- |
| 用户禁令仍为 1 | UID owner 与第三节点 | HTTP 200、SEND 原因码 25，无消息 ID/序号 |
| 写入用户禁令 0 | UID owner 与第三节点 | HTTP 503，无消息 ID/序号 |
| 再写入用户禁令 1 | 第三节点 | 原因码 25，无消息 ID/序号 |
| 恢复节点 1，确认 Slot authority 稳定，再解禁 | 第三节点 | `priority-recovered` 成功 |

所有拒绝请求仅执行一次，不重试直到放行。禁令写入版本依次为 1–4，
策略切换后的请求立即执行。成员公开 `/channel/messagesync` 返回完整
历史（`more=0`），严格等于两个成功请求，无拒绝记录或重复记录。
此处证明持久化侧无副作用，不扩展为所有在线投递矩阵的证明。

## 首次失败与独立发现

首次运行 33.85 秒失败于 Manager 完整历史查询，策略判定均通过。第二次
保留相同故障和判定，用成员公开同步契约核对历史，并记录三个节点的
Manager 响应：节点 3、2 返回 `404 channel not found`，节点 1 返回两条
完整记录；成员同步从节点 2 同样返回两条完整记录。

因此没有证据表明此处消息丢失。Manager 跨节点查询的不一致是独立待查
问题，记录在 `docs/development/CODE_QUALITY.md`，不能将其标记为已修复。
原失败产物保留；通过的是禁发优先级与公开成员历史断言。

## 重跑及产物

```sh
WK_E2E_SEND_BAN_REPORT=/tmp/send-ban-priority.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestKnownUserBanPrecedesUnavailableChannelSlot$' -count=1 -timeout=3m -p=1 -v
```

使用 Go 1.25.11 / Darwin arm64。报告输出为指定路径的 `.priority.json`
伴随文件，包含完整公开 Slot placement、时间、策略版本、SEND 决策与
历史响应。见[产物清单](assets/send-ban-priority-20260924/manifest.json)与
[源码身份](assets/send-ban-priority-20260924/source.json)。
本项不是性能资格，不改变原 30 分钟、三组重复对照和其他故障验收缺口。
