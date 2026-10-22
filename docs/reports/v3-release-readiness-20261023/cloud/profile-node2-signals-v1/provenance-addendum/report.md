# 本地 CPU 诊断的二进制来源纠正

**本轮不是已验证的 79a 候选复现。** 实际保留并运行的产品、CLI 二进制都报告构建自 `a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83`，内嵌 Go `1.25.11`、`vcs.modified=false`；原 preflight 的 `source_revision=79a71770…` 是分析/启动上下文声明，`go version go1.25.0` 是主机命令版本，都不能替代可执行文件内嵌元数据。本纠正独立封存，未重写任何原 proposal、原终态或旧归档。

| 可执行文件 | 字节 | SHA256 |
| --- | ---: | --- |
| wukongim | 92887922 | c2531b920ba7b9ff99d777c6bf5d988840426a5dc5f15d66ae0005aa2660b379 |
| wkcli | 59162610 | e36e5151f4424b0485c1f611d2982f76e6dd0ed3b805c478c70a3bbfdb74ba62 |

两者均为 darwin/arm64、CGO_ENABLED=1、compiler=gc、buildmode=exe。完整嵌入设置、主模块版本与两个 Git tree ID 见 `binding.json`；未记录的 build flags 不作假设。方法是只读 `go version -m` 与逐字节 SHA，未执行产品、重建二进制或运行新 profile。

a804→79a 全仓有 85 项路径变化，有限清单逐项记录两端 SHA（新增/删除仅对应存在侧）。其中 `cmd/internal/pkg` 下全部非测试 Go 实现变化为：

- cmd/wkchatlifecycle/main.go
- cmd/wkcli/internal/benchmark/formal_chain_command.go
- internal/usecase/chatlifecyclerun/run.go
- internal/usecase/clouddeploy/deployment.go
- internal/usecase/conversation/legacy_sync.go
- pkg/channel/replication/recovery_barrier.go

后两项为普通消息产品代码差异，不能声称整体产品等价。配置、脚本、Workflow、文档和测试的全部其余变化也保留在 `all_repository_differences`。没有把命令/云工具预算差异等同于全部产品差异，也没有以旧二进制通过或失败代替新候选判断。

本次解释到的 permission cohort/RPC、Slot fresh ReadBarrier、presence recovery/usecase/directory、transport conn、网关错误映射及固定指标解析等源码，以及适用 AGENTS/FLOW，已按表内实际路径逐字节核验 a804=79a=当前 worktree（具体文件与 SHA 见 `analysis_files_verified_byte_identical`）。这只允许解释这些等价文件的机制，不扩张为全仓/全二进制等价。当前 worktree 的 E2E 未提交变更也记录在绑定中，未作修改。

保留的 node2 CPU 为 40474 字节，SHA `d3c5e46713de6add18641f2f4f4a0672cbac6db16322d555cdb0236cf32dcd23`。CPU top/-cum 可描述 **c253 这次实际 node2** 的样本；它不能单独证明等待位置、79a 产品问题或云性能/message_loss 根因。profile/场景有扰动，且 goroutine 捕获缺失，不能作为性能验收配对。

本目录只公开派生元数据、源码路径/SHA 与验证程序；原可执行文件、pprof 二进制、日志、配置、UID、payload 不在 allowlist。嵌入版本是可验证的文件内声明，本任务没有完成可复现构建来进一步认证所有构建输入。
