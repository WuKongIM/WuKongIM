# Linux scripts 门禁的 shell trap 诊断与最小修复

基线 `eda77697db42ba14f14bfad58cdd3557342b9989`，原 Linux run `39565861218` / job `119107852968`。本目录不修改此前本机 30 分钟结果，也不把本次聚焦回归当作 Linux 全门禁通过。

## 已证明的原因与修复

`analyze.sh` 的 EXIT trap 已保存原始失败码，但空 bridge、无需关闭、已经尝试关闭、成功关闭分支的裸 `return` 在 Bash 5 中继承 trap 原状态，`set -e` 会提前退出。只修空 bridge 时，远端关闭 fixture 已调用，后续本机 worktree/temp/tools 清理仍会遗漏；把四处成功/空操作明确为 `return 0` 后，清理执行到底，最终仍由既有 `exit "$exit_status"` 返回原错误。

baseline 的首个 operator signal handler 同样在转发 TERM 后裸 `return`，导致 Bash 5 提前退出而没有 join 已启动的子进程。修为显式 `return 0` 后，既有等待循环完成 join，再以既有 operator status 返回 143。没有修改失败码判断、超时、负载或门槛。

只改三个文件：`scripts/cloud-sim/analyze.sh`、`scripts/run-wukongim-three-node-chat-lifecycle-local-baseline.sh`、新 `scripts/shell_trap_cleanup_integration_test.go`。完整源摘要和 patch 在 `shipping-freeze.json` / `shipping.patch`。

## 先红后绿

- `focused-red`: 实际 Bash 5、修复前，新 8 子例（4 分支 × 原码 17/143）全部缺少 cleanup trace；既有真实 TERM baseline 子进程未完成 join。
- `focused-green`: 同一个命令，8 子例及 baseline 全通过；原码保持 17/143，关闭失败 29 的 warning 保留，精确 owned temp 删除，worktree/tools cleanup 执行。
- `bash5-regression`: 9 顶层 / 15 子例通过，覆盖原 CI 的 6 类 Analysis 关闭失败、baseline、新 test、未改的 observer test；无 skip。
- `bash3-green`: 实际 `/bin/bash` 3.2.57，8 子例及 baseline 全通过；无 skip。
- `static-validation.json`: 两种 shell 的语法、gofmt、局部 diff 检查通过。

新 test 提取实际生产 cleanup 函数，只有云关闭与本机 checkout/tool 操作使用 fixture；没有调用真实云或凭据。143 cleanup 子例是显式 exit 143；baseline 回归才是真实 SIGTERM 及 child join。

## 未关闭的边界

`TestSingleNodeTerminalObserverJoinSurvivesParentSignal` 原 CI 退出 255 仍未定位。相同源码在实际 Bash 5 的 20 次有界 probe 和既有测试均通过，不能据此宣称 Linux 异常已解决。未改 observer 源码或断言。

job 的 setup log 指向 ubuntu-24.04 image `20261018.339.1`。该版本官方 image 软件清单记录 Bash `5.2.21(1)-release`，但 job 未直接执行 `bash --version`。临时 GNU 官方源码构建采用相同上游版本、Darwin arm64 平台；它证实 shell 语义差分，不等于 Ubuntu 内核与发行包的精确复现。`bash5-build.json` 保留 URL、tar 摘要、构建步骤与二进制版本。没有安装或替换系统 shell。

原 CI 10 顶层失败及总 600 秒超时保持原记录。这里只修已复现的脚本控制流；root 另处理缺 rg 和 go version JSON fixture，完整 Linux scripts 门禁仍需后续执行验证。

## 复查入口

`focused-red.json`、`focused-green.json`、`bash5-regression.json`、`bash3-green.json` 保存实际 argv、shell 版本、时间、cache 路径、返回码；对应 `.log` 保存完整 Go JSON 事件。`bash3-first/receipt.json`、`bash5-first/receipt.json`、`bash5-v2/receipt.json` 保存修复前与单变量控制结果。

复跑需使用同版本源码及摘要核对后的临时 Bash 二进制，把其所在目录放到 PATH 首位以覆盖 fixture shebang，然后执行对应 JSON 的 argv（保留 `GOWORK=off GOENV=off GOTOOLCHAIN=go1.25.11 GOFLAGS=-p=2 LC_ALL=C`，使用独立 GOCACHE）。不要覆盖首红证据。原始源码 tar 和编译产物可按 `bash5-build.json` 重建，不属于轻量归档。
