# Send ban 插件入口与在途验证

本轮在 `codex/issue-973-send-ban-design`、基线 `823e8d15c` 上继续实施
[设计](../specs/user-channel-send-ban.md)，没有缩小完整验收范围。

## 缺陷与修复

真实 `.wkp` 进程使用 `/message/send`：正常发送取得消息 ID 后，通过公开
HTTP 禁言发送者。相同插件再次发送，旧实现返回 RPC StatusOK 和 MessageId=0，
没有错误。消息用例已经拒绝；插件宿主适配忽略 SendResult.Reason。

插件用例现在把非成功 SEND 转成 `SendRejectedError`，保留内部原因。
入口才映射公共协议原因；用户/频道禁令均为
`message send rejected: reason=25` 的 RPC error。成功 protobuf 不变，
底层错误保留，业务原因 128–255 保留，未知内部原因映射系统错误。
内部成功值是 0，不能把内部枚举直接当 wire reason；PDK Send hook 原有
允许值使用 0，本轮不改动其既有原因编码契约。

隔离失败用例在修复前执行并失败，覆盖禁令、终态、禁止发送与不支持模式。
入口测试覆盖完整公共原因映射与包装错误。实现后相关用例、race、vet 通过。

## 真实进程证据

一个主场景 `test/e2e/plugin/send_ban`，运行单节点与三节点集群，256 Hash
Slots、12 初始物理 Slots、辅助权限缓存 TTL=1h；插件只在发送接入节点启用。
三节点从节点 1 写入策略，节点 3 的实际插件发消息。Gateway Token auth 在
受控本地测试中关闭；这不是认证功能验收。

- 每个拓扑有 9 条插件发送结果：前后成功、用户群/私聊禁令、非持久化禁令、
  频道禁令、默认系统发送者禁令、在途场景后的新请求拒绝。
- 插件 hook 在成功路径实际修改 payload，并尝试替换发送 UID/频道；收件人与
  完整历史证明只有 payload 生效，发送者和目标保持原值。
- 接收端先后收到成功控制消息并发送 RECVACK，禁令期间有界等待无新增投递。
  随后的成功控制也检查没有排队泄漏。完整历史恰有四条允许消息，无被拒绝项。
- 通过 sandbox gate 将 HTTP 消息停在真实插件 hook 中，再完成用户禁言，释放
  hook 后该已准入请求成功；禁令后的新插件请求失败。JSON 保存发送开始、
  hook 到达、禁令完成和 SEND 完成时间，顺序由事件控制，不依赖固定等待猜测。
- 最后在用户仍被禁言时读取完整历史成功，证明禁言没有阻断历史查询。

`SkipPluginHooks` 没有 PDK 请求字段；本场景不声称可通过该公共协议设置它。
内部显式标志、多设备/其他频道类型和其余故障矩阵仍按完整验收表继续。

## 重跑与产物

```sh
GOWORK=off go test ./internal/usecase/plugin ./internal/access/plugin -count=1
GOWORK=off go test -race ./internal/usecase/plugin ./internal/access/plugin -count=1
GOWORK=off go vet ./internal/usecase/plugin ./internal/access/plugin
WK_E2E_PLUGIN_SEND_BAN_REPORT=/tmp/plugin-send-ban.json GOWORK=off go test -tags=e2e ./test/e2e/plugin/send_ban -count=1 -timeout=4m -v
```

实际执行 Go 1.25.11 darwin/arm64，使用独立工具链与缓存，E2E 使用本轮构建的
`WK_E2E_BINARY`。Mac race 链接器有既有 LC_DYSYMTAB 警告；测试返回成功。
FLOW 变更运行授权命名检查 `flow-doc-contracts`，索引同步生成。

[产物清单](assets/send-ban-plugin-20260924/manifest.json) 包含失败/成功结果、
源码与二进制指纹、冻结上下文、日志摘要及验证命令。原始日志在本机忽略目录
`tmp/send-ban-validation-20260924-plugin/`。结果不等于完整性能或发布验收；
100k 准备失败、三组对照和其他未完成项见[完整核对表](2026-09-24-send-ban-acceptance-audit.md)。
