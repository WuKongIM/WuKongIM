# Exact tracking Issue 付费前预检

基线 fc18221282f289088ec9b4282fda8e0bfc67a3b8；仅修改 comment-request-issue.sh 与专用 integration 测试。root/rc 独立负责 stage 接线和 docs，本子任务不提交或推送。

新 CLI `scripts/chat-lifecycle/comment-request-issue.sh --preflight` 只需 GH_TOKEN、GITHUB_REPOSITORY、WK_CHAT_REQUEST_ID。stdout 单行 JSON schema `wukongim.chat_lifecycle.issue_preflight/v1`：status=ready/rejected，reason 固定枚举，repository/request_id/issue_number/issue_url。成功 exit 0；失败 exit 1、Issue 字段为 null；未知或重复参数 exit 2、usage，不访问 GitHub。

固定 github.com 和指定 repository/title，单次 search 限 10 条；incomplete_results 或 total_count 未覆盖全部结果即拒绝。唯一 exact 非 PR Issue 再 GET 一次独立验证数字 identity、URL/repository/title、open、非 locked、comments；无 POST/PATCH/create/comment/retry。原无 flag 实现尾段字节完全一致。预检不证明将来 comment 写权限，不消除后续 close/transfer 竞态，原运行期失败/清理机制仍需保留。

先写失败库存与真实 CLI+外部 gh stub tests，01-red exit1（原脚本要求 ISSUE_STATE，不能输出预检 JSON且未知 flags会访问 API）后才实现。02-focused-green exit0：4 个新测试共 35 个情形、2 个既有合同/评论去重关闭回归。新测试使用 integration tag，无产品编译/高负载/真实 GitHub 或云操作。独立 Go cache、Go1.25.11、-p2；命令、时间、精确源 SHA、完整首红和绿日志已留存。03 记录 bash -n、diff --check、gofmt、原分支字节不变。

测试 seam 是已获授权的 public CLI + gh API 边界 stub。规则/源码原 SHA 在 rule-and-source-freeze.json，无适用祖先 FLOW；测试先行快照在 tests-first.go.txt。生成的 go-cache 非证据、不进入 allowlist；冻结源码/patch 与日志可独立复核。
