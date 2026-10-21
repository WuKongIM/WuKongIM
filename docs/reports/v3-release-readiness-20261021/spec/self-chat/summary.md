# Self-chat coverage result, 2026-10-21

新增 self_chat 专项已通过，运行 54.65 秒；不改产品代码、不增设原有 GA 强制门禁。产品二进制 SHA256 `a6b6d20a8d60cc38f164a6479f34557f698fd9400892d8ea05f9f64051db9b05`，构建 revision `98c25091bb9587a303af982bb14bf4cedd9aa110` 的 cmd/internal/pkg 与当前 `32d402f7625c36f5667a290ec95c045f5f35937c` 相同。

- 256 Hash Slots，single-node cluster 与 three-node cluster，生产默认 Token 认证；两个未注册 Token 拒绝对照。
- 合计 12 条 SENDACK/HTTP ACK（8 条 WKProto、4 条 HTTP），同 UID APP/WEB 收发，8 个发送会话抑制观察窗，每窗 1 秒。
- 合计 2 次全部节点停止、join 后重启；先读取全部旧历史，再登录并新发消息，凭据不重新创建。
- 12 次完整历史检查（1 节点 3 次、3 节点 9 次），28 页、48 行观测；逐条比对 ID/序号/client_msg_no/发送方/频道/类型/载荷/持久标志，严格单调与 More 终止，无历史 unavailable 重试。
- 32 个 RECV 帧含 16 个相同 MessageID 的重复帧；精确校验并 RECVACK。公开契约要求客户端去重，未承诺 exactly-once wire，不把该观测升级为 GA 阻碍。

两次夹具修正均保留原始失败 JSON、日志及当次完整测试源和 SHA256：
1. `first-run` 把双零边界误当从最早历史读起；当前既有 `page_reader.go:258–281` 明确双零取最新页。只改为显式 inclusive start=1；完整内容、顺序与终止断言未放宽。
2. `explicit-start-run` 两拓扑全部业务阶段完成（各 6 ACK、12 次完整历史合计全部完成），但手动 Close 后 Cleanup 再 Close 得到公开 client 的 ErrClosed。只加每设备 close-once 保护，首关错误仍断言；`final-run` 业务与清理全部通过。

可复制的 4 个仓库文件（未提交）：
- `test/e2e/message/self_chat/AGENTS.md`
- `test/e2e/message/self_chat/self_chat_test.go`
- `test/e2e/AGENTS.md`（新增一行 catalog）
- `test/e2e/message/AGENTS.md`（新增一行 catalog）

复现：`GOWORK=off WK_E2E_BINARY=/absolute/frozen/wukongim WK_E2E_SELF_CHAT_REPORT=/absolute/self-chat.json go test -tags=e2e ./test/e2e/message/self_chat -count=1 -timeout=4m -p=1 -v`。
