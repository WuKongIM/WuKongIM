# 非副本入口的禁令缓存转换

状态：真实三节点 E2E 通过，最终运行 18.96 秒。无生产代码变化。
三节点、256 Hash Slots、12 物理 Slots、每 Slot 一副本，辅助权限缓存 1h。
通过公开 Manager placement 选择 UID Slot 的非副本接入节点。

先确认远端用户禁令生效，再解禁并读取不存在的群，得到频道不存在。
随后创建该群且设置频道禁发，普通发送立即返回 ReasonSendBan；系统设备
也必须返回相同禁发结果。保持该设备上下文，解除频道禁令后成功，设置
用户禁令后拒绝，再解除用户禁令后成功。所有新增拒绝均无消息 ID/序号。
成员历史同步必须返回完整历史，且严格只有两个成功请求。最终产物记录
七条状态观察。首次版本 19.46 秒通过；增加同一系统设备观察频道禁令的
断言后完整重跑，未修改生产实现。

系统设备在此隔离不存在群时预热的辅助成员缓存。测试证明两种强制禁令
不受辅助 TTL 影响，不宣称改变了成员关系缓存语义。普通设备和既有
WKProto 连接的禁发/解禁已由主场景和独立禁令矩阵覆盖。

```sh
WK_E2E_SEND_BAN_REPORT=/tmp/send-ban-cache-transitions.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestNonReplicaIngressReadsUserSendBan$' -count=1 -timeout=2m -p=1 -v
```

使用 Go 1.25.11 / Darwin arm64。测试基于 fc44be97c 的工作副本，生产
二进制与 1ba4a6352 的生产输入一致。源码摘要、冻结上下文、两次结果和
日志原始摘要见[清单](assets/send-ban-cache-transitions-20260924/manifest.json)。
两次测试均在性能第八轮的准备阶段发起；首次结束快照仍未 cold-prime
完成。最终运行的终止结果在下一性能轮开始后才取得，缺少同时刻阶段
快照，因此不能认证最终运行与性能测量窗口完全无重叠。该限制不影响
功能断言，但必须在性能资格结论中保留。
