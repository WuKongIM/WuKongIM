# 实际 gateway 批次的 UID/频道分布

状态：真实三节点黑盒 E2E 通过，56.24 秒；无生产代码变更。256 Hash Slots、12 物理 Slots、三副本、辅助缓存 1h。四种分布分别先全部允许，再设置用户或频道禁令，通过已有 WKProto 连接突发发送。每条 ACK 以 ClientMsgNo 对齐，不依赖客户端 future 完成顺序。

| 分布 | 阶段 | 成功/拒绝 | SEND/批次 | 去重前/后 facts | 节点信封 |
| --- | --- | --- | --- | --- | --- |
| one-user-one-channel | 0 | 32/0 | 32/1 | 192/6 | 1 |
| one-user-one-channel | 1 | 0/32 | 32/1 | 192/6 | 1 |
| many-users-one-channel | 0 | 32/0 | 32/4 | 192/24 | 6 |
| many-users-one-channel | 1 | 16/16 | 32/4 | 192/24 | 6 |
| one-user-many-channels | 0 | 64/0 | 64/1 | 384/81 | 2 |
| one-user-many-channels | 1 | 32/32 | 64/1 | 384/81 | 2 |
| many-users-many-channels | 0 | 128/0 | 128/16 | 768/656 | 32 |
| many-users-many-channels | 1 | 32/96 | 128/16 | 768/656 | 32 |

单 UID/同频道的 32 条请求实际组成一个批次，192 个原始事实去重为 6；单 UID/16 个频道的 64 条请求组成一个批次，384 个事实去重为 81。多 UID 的连接独立形成批次，不跨会话强行合并。本轮总计 512 个 SEND 判定，336 成功、176 拒绝；34 份完整频道历史严格包含 warm 和成功请求，不含任何拒绝请求。

每阶段公开 gateway records 等于发送数、batch count 小于发送数，事实数不超过去重前。节点信封、Slot groups 等指标保留原始范围：后者可能包含其他关联元数据读取，不能直接当作每批的纯强制禁令屏障数。全量 UID/频道规模压力由独立 5000 频道场景承担，本项是分布与对齐验证，不宣称吞吐、CPU/P99 或 30 分钟资格通过。

```sh
WK_E2E_SEND_BAN_REPORT=/tmp/send-ban-gateway-distributions.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestSendBanGatewayDistributions$' -count=1 -timeout=3m -p=1 -v
```

Go 1.25.11 / Darwin arm64。测试基于 2f49a2869 的工作副本，生产输入对应 1ba4a6352。前次性能运行终止后才执行，本次功能运行结束后才启动新的 profile 诊断。源码/harness 摘要、冻结上下文和完整证据见[清单](assets/send-ban-gateway-distributions-20260924/manifest.json)。
