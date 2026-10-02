# 禁发和 quorum 拒绝的无投递验证

状态：真实三节点黑盒 E2E 通过，44.86 秒；没有修改生产代码。
256 Hash Slots、12 物理 Slots、三副本、辅助缓存 1h。公开 placement 定位
发送者 UID Slot owner，发送和接收连接在禁发前建立，并在该 owner 上保持。

用户禁发和频道禁发分别覆盖私聊/群、HTTP 三个接入节点及既有 WKProto
连接，包含普通消息、NoPersist、SyncOnce。共 48 次明确禁发拒绝，均无
消息 ID/序号；每种禁令后接收端等待 2 秒，没有任何 RECV。解禁后再次
通过两种协议发送，必须精确收到当前成功请求，禁止旧拒绝消息排在前面。

随后停止另两个进程，让 UID Slot 失去 quorum，但保持接收连接所在进程
存活。一条 HTTP SEND 必须返回 503 且无 ID/序号；接收端仍为空。重启
节点、等待公开 readiness 和实际 Slot Leader 稳定后，原连接收到成功
对照；最后再次执行空读。完整私聊和群历史各严格包含八条成功请求。

产物共 70 条观察：48 个禁发、16 个已发送且已接收成功对照、四个有界
空读窗口、两份完整历史。另一次 quorum SEND 的 503/零 ID/序号由用例
直接断言，空读及恢复后精确历史也排除它的副作用。该测试对观测窗口
负责，不声称有限等待能证明任意未来时刻永不产生异常投递。

```sh
WK_E2E_SEND_BAN_REPORT=/tmp/send-ban-delivery.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestRejectedSendHasNoDelivery$' -count=1 -timeout=3m -p=1 -v
```

Go 1.25.11 / Darwin arm64。测试源码基于 2f49a2869 加未提交测试，
生产二进制输入仍对应 1ba4a6352。前一持续压力运行已经终止后才编译和
执行。源码及 harness 摘要、冻结上下文、完整 JSON、日志见
[清单](assets/send-ban-delivery-20260924/manifest.json)。
