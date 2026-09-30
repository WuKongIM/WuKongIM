# 禁令随策略 Slot Leader 切换保持生效

状态：三节点进程 E2E 通过（29.64 秒），没有生产代码变更。使用新缓存
修复二进制、256 Hash Slots、12 物理 Slots、三副本和 1h 辅助权限 TTL。

公开 placement 确认用户策略在 Slot 1，源频道策略在 Slot 11。分别设置
禁令后，通过 Manager 发起手动转移：用户 Slot 实际 Leader 从节点 2
变为 3，频道 Slot 从节点 1 变为 2。测试要求任务结束并由全节点公开
Raft inventory 证明稳定领导关系，不把 preferred target 当作结果。

每次转移与三个入口各 16 次 SEND 并发发起，共 96 次请求均返回 Reason
25、零 MessageID/Seq。本轮没有观察到 503；记录保留开始、结束及转移
接受时间，但不宣称每条请求恰好穿越某个内部路由/fence 更新瞬间。
真实转移完成后，三个入口都继续拒绝；由旧 Leader 接入点解禁立即允许，
由新 Leader 重新禁言再次拒绝。最终完整历史严格等于八条成功对照。

共 129 条观察包含策略版本、接入节点、转移与稳定状态、发送结果和完整
历史。此项补齐真实手动路由切换证据；缺失、重复、错误 fence 回包与精确
失败子组重试仍由原隔离 RPC gate 证明，不能称为本进程测试注入的故障。
该功能验证不替代尚未通过的持续性能资格或三次 5% 对照。

重跑：

```sh
GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestSendBanSurvivesManualLeaderTransfer$' -count=1 -timeout=4m -v
```

[清单](assets/send-ban-leader-transfer-20260924/manifest.json)记录源码、
冻结上下文、真实测试结果与二进制身份；本轮使用 WK_E2E_BINARY 指定
新缓存修复二进制，生产改动身份独立记录，不假称测试发生于未来提交。
