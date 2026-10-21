# VCS 构建回归用例窄修

仅修改现有 `scripts/wukongim_three_nodes_build_integration_test.go`。显式固定 `go version -json=false -m` 文本输出；继承编译缓存，并为该次实际 launcher 构建设置独立随机 link build ID，实际二进制必须报告同一 ID。原 worktree 拓扑、错误 GOWORK、buildvcs=true/e2e、精确提交号断言均保留。

测试先行：原Linux失败和元数据probe已证明格式故障；先保存 `failure-inventory.md` 与新 build ID 断言的 `assertions-first.patch`，再实现。昂贵的断言中间态未执行，不报告为red。只执行1次实际focused集成回归：PASS，用例18.22秒，包18.736秒，整体31.099秒，日志无失败。命令使用 GOFLAGS=-p=2 -json、9m/parallel2，外层600s；未运行完整suite。

Go1.25.11源码依据：`cmd/go/internal/work/exec.go:1352–1404` 将 linker flags 纳入 link action 身份；`gc.go:648–653` 让用户 linker flags 覆盖自动 buildid；`buildid.go:691–694` 保留显式用户 buildid。新ID不会污染测试进程或其他子命令的环境。当前没有删cache、改launcher或提高门禁。

Mac本次18.22秒与Linux之前113.94秒来自不同环境；临时Bash编译可能并发，因此不作为性能节省比率或正式Linux通过证据。完整Linux gates仍失败，需其余修复后在新head继续验收。源码验证后未再变更；未commit/push。

重放：`python3 -B run-focused.py` 会实际编译此单个回归（需先确保正在使用相同源码和没有冲突任务），不是无负载的收据检查。`SHA256SUMS`用于不执行测试的文件校验。归档只使用 allowlist，排除任何生成二进制/cache。
