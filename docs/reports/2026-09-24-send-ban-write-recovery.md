# 禁令写入响应丢失与并发元数据更新

状态：真实三节点 E2E 通过，39.24 秒；`flow-doc-contracts` 命名检查通过。
本轮没有修改生产代码。利用性能对照的冷启动准备阶段完成验证；两次用例
终止时，正在运行的旧基线第二轮均尚未输出 cold-prime 完成记录，证明其
测量窗口还未开始。阶段快照保留于产物中。

## 固定失败场景

三节点集群，256 Hash Slots，12 个物理 Slots，三副本，Gateway Token
认证开启，辅助权限缓存 1h。

1. 先以 HTTP 成功发送建立消息历史正向对照。
2. 通过仅用于 E2E 的回程故障代理，把用户/频道禁令 POST 转发到真实服务。
   代理读取真实响应状态后，截留全部响应字节直至调用方的 2 秒 deadline。
   调用方必须超时，而代理必须实际观察到成功响应；不能将写入前取消伪装成
   已提交响应丢失。该故障不模拟 Raft 内部提案超时或请求未送达。
3. 从另一节点 GET 查证禁令和版本 1，随后用旧版本 0 请求解禁，必须得到
   CAS 冲突且不改变禁令。调用方不能把超时当作回滚。
4. 八轮并发执行 Token 更新、部分 Channel 元数据更新、成员添加/删除以及
   带版本 1 的幂等禁令写入。每轮结束后从全部节点查证两种禁令仍为 1，
   版本仍为 1；再执行一次发送，必须拒绝。
5. 用户仍禁发时，在全部节点用更新后的 Token 登录成功。
6. 先解除用户禁令仍被频道禁令拒绝；再解除频道禁令后发送成功。完整成员
   历史必须严格包含故障前与全部解禁后的两个成功请求。

四类写入通过每轮共享的开始 gate 并发发起，并在查证前全部 join。本项
观察每轮后置条件；不声称仅靠采样证明任意交错时刻的线性化。原子 FSM
约束仍由已存在的命令级边界证据支持。

## 结果及重跑

两个真实成功写入的响应被截留，调用方均得到 deadline exceeded；随后
跨节点查证策略为 1、版本为 1，使用旧版本 0 的解禁请求均返回 409。
八轮并发写入完成，每轮全部节点读取到两种策略仍为 1、版本仍为 1。
用户在三个节点均能使用保留的 Token 认证。九次被禁发的 SEND 均无消息
ID/序号；最终完整历史严格包含 `atomic-warm` 和 `atomic-recovered`。

首轮 36.03 秒失败于 Token 登录：夹具使用 `device_flag=1`（WEB）注册，
却用 `frame.APP=0` 连接。禁令、CAS 和并发后置条件已通过，但该轮整体
保留为失败。将注册设备类型改为 `int(frame.APP)` 后完整重跑通过。
这不是 Token 被禁令写入覆盖的产品缺陷。

```sh
WK_E2E_SEND_BAN_REPORT=/tmp/send-ban-write-recovery.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestSendBanWriteRecoveryAndConcurrentMetadata$' -count=1 -timeout=3m -p=1 -v
```

新增 helper 位于 `test/e2e/suite/http_response_loss.go`，只使用真实公开 HTTP；
请求和代理共享取消，丢弃的响应体最多读取 64 KiB，不保留 Token 或响应正文。
故障代理不用于产品路径。Go 1.25.11 / Darwin arm64；生产二进制与
`1ba4a6352` 的生产输入相同，本轮基础提交为 `458a5ca8c`。
见[源码身份](assets/send-ban-write-recovery-20260924/source.json)、
[结果及首次失败清单](assets/send-ban-write-recovery-20260924/manifest.json)。
