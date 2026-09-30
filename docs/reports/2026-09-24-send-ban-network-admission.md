# 远端权限准入饱和与 voter 丢失

状态：真实三节点 E2E 通过，38.42 秒。256 Hash Slots、12 物理 Slots、
每 Slot 两个 voters。通过公开 placement 证明 UID Slot owner=3、另一
voter=1，非副本接入节点=2。正常发送建立持久化历史正向对照。

停止 voter 1 后，owner 3 和非副本 ingress 2 均保持运行；48 条 HTTP SEND
由共享 gate 同时发出，每条只调用一次，不重试到成功。它们全部返回 503
且无消息 ID/序号。接入端公开计数增加 48 个远端权限信封，owner admission
busy 增加 32；50ms 采样执行中信封峰值 16，未超过 16，结束后为零。
两副本失去一个 voter 用于制造不可用屏障，不声称这种配置能容忍 voter 丢失。

重启 voter 并等待公开 readiness、实际 Slot Leader 稳定后，下一次发送
成功，完整群历史严格只有 `network-warm` 和 `network-recovered`。
本项补齐真实网络、真实失去 quorum 时的有界准入和失败关闭证据；
精确 4096 facts/1MiB、独立取消和失败子组重试仍见隔离边界报告。
采样峰值不是任意瞬间的穷尽证明，硬上限另有实现层 gate/race 证据。

```sh
WK_E2E_SEND_BAN_REPORT=/tmp/send-ban-network.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestRemotePermissionOverloadFailsClosed$' -count=1 -timeout=3m -p=1 -v
```

本次未设置 WK_E2E_BINARY，采用默认 harness 从 63e946469 的工作副本
构建 `go build -tags=e2e`，并立即保存实际二进制及 buildinfo。生产 Go 输入
与 1ba4a6352 相同，后续相关差异只有测试/文档。默认临时 JSON 已复制到
[产物清单](assets/send-ban-network-admission-20260924/manifest.json)；源码
与实际二进制 SHA256、冻结上下文和完整 51 条观察均保留。无生产修改。
