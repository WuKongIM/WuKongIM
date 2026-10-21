# PR #1020 合并交付

人类已明确批准“同意合并 #1020”。2026-10-21 12:43:39 UTC 使用普通 `gh pr merge --merge --match-head-commit 0cdfaa37b00c58819895dee54029c27c83c9caa9` 合并，没有 `--admin`、保护修改、Review Agent 评论或手工 Actions dispatch。

- 新 main / merge：`e30299fa59ea6669ea71024eabe209840f241f19`。
- Parents：`ce3c4d9110f17bb71dc99a89dc3c09758fabf980` 与已验收 head `0cdfaa37b00c58819895dee54029c27c83c9caa9`。
- Full tree：`36f96357c967418517a00444e52ae03f77c3ff90`，与首轮 Linux CI 的候选及 synthetic merge 完全一致。
- Fresh 保护 JSON 前后完全相同。非 draft、mergeable clean、无阻断 Review；22 checks 中20 success与2条件skip。比最终CI快照多一个ready事件的成功 credential-free wake-up，不是CI rerun。
- 拉取远端 main 并验证 contains 原tip后，专属 `.worktrees/v3-rc-released-continuation` 和本地 `codex/v3-rc-released-continuation` 已普通删除，未用force；清理前tracked/untracked/ignored均为空。
- 用户 primary main 仍为 `a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83`，primary status逐字不变；未修改其他worktree、packages或证据分支。
- 自然 postmerge Docs run `39601918104` attempt1 / exact merge SHA：构建、部署、direct-origin验证、既有bounded CDN refresh四job全部success。四份完整公共日志经行级认证关键词脱敏并gzip，原始字节数/SHA和脱敏行摘要保留；raw日志仅本机私有目录且未归档。

原 Mac scripts-integration 首轮9分钟超时及所有CI原始证据继续保留于前代收据，不被合并成功覆盖。合并不等于正式v3发布：未采购新的RC主机、未重新Quote/dispatch，也未创建产品tag/Release或生产stable包发布。本次只完成已批准PR合并和既有自动文档流程。

使用 `SHA256SUMS` 核对完整目录。`ARCHIVE_ALLOWLIST.json` 列出公开文件；不含私钥、解密云凭证、构建缓存、二进制或原始认证日志。扫描仅为固定私钥/PAT形状及日志关键词过滤，不是完整秘密审计。

补充 `publication-receipt.json`：构建/源站验证/CDN job的 `git log -1` 实际stdout均为exact merge；Pages artifact12346885726的上传日志digest/size与API一致（未重新下载42.7MB归档），部署成功和5路径origin_ready均保留原行。CDN task33641885707/request01A15E80-49E3-5A19-BF57-241915012149只证明4URL刷新已受理，不能声称所有边缘节点已完成刷新。v1原封存保持不变；v2增加这些精确公共收据。
