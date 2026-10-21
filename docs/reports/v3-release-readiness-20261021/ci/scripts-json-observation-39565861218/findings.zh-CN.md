# scripts-integration 新诊断结果（已失败，未重跑）

本轮固定 600 秒外层门禁再次超时，已有 **10 个根测试断言失败**，另有 15 个根测试未完成。不能把本轮视为仅仅运行时间不足，也不能据未出现失败的名字推断通过。完整原日志未截断，SID 清理收据确认无剩余进程。

身份：PR head `eda77697db42ba14f14bfad58cdd3557342b9989`；实际 checkout `abd628769baf7552a8cf8abb29a7ba63e52c3326`，其两个父提交为 `ed705c0fbffdea485bbb65608d6e340bb049d97e` 和上述 head；tree `773a7f5a6d6f89160ad71e7c6fdfb557eb42239e`。[本次 job](https://github.com/WuKongIM/WuKongIM/actions/runs/39565861218/job/119107852968)。未修改 base policy：9m、parallel=2、外层600s、8MiB；诊断环境仅增加 GOFLAGS 的 `-json`。

## 实测时间，不将推断当根因

| 阶段 | 墙钟时间 | 边界与限制 |
| --- | ---: | --- |
| job command 开始 → scripts package start | 约120.49秒 | 包括下载、构建、vet及启动；现有事件不能独立拆分这些成本 |
| scripts start → 首个顶层 CONT | 216.34秒 | 顶层串行阶段，占已观察包时长45.1%、600s总预算36.1% |
| 首个顶层 CONT → 最后事件 | 263.47秒 | 包含并行运行和锁等待；不能相加各例Elapsed推导总耗时 |

已结束的根测试为489 pass、6 skip、10 fail，共505/520；未完成根测试15个。`TestChatLifecycleShakeoutScriptIntegration` 本轮真实90秒工作量在内总计123.64秒，必须保留其真实运行与串行约束。`TestWukongIMThreeNodeLocalE2EBuildUsesSelectedWorktreeRevision` 的独立空 GOCACHE 构建耗时113.94秒并失败，明确是可单独处理的重复成本。`NativePackageBootstrapFailureDiagnostics` 20.43秒和 `CloudDeploymentExecution` 10.39秒在串行阶段；若后续改调度，先核对每个顶层/子测试资源所有权，不能批量添加 parallel。

修复监控的三个相同 `-trimpath` build 尚未消耗本轮大头：第一个相关测试直到最后一个事件才 CONT，另外两个仍 PAUSE。不能用它们解释本次已发生的超时。Mac warm 两次仍超过9m，因此 cold build也不足以解释整体门禁。

## 已知失败与证据范围

精确10个根失败、5个子失败及各自断言正文在 `assertion-failures.json`。完整事件可从已通过 GitHub artifact API digest/size 校验的 `artifact.zip` 重放。

- HotProfile：shell明确报 `rg: command not found`，这是当前 runner 缺少依赖的证据。
- SelectedWorktreeRevision：诊断 GOFLAGS 的 `-json` 同时改变了子 `go version -m` 输出；JSON 内的 vcs.revision 实际等于所期待 checkout。该项是诊断观测参数对文本消费端的影响，不是 VCS 绑定失败。
- 其余8个根失败涉及 signal/join 与 analysis close cleanup，由独立 rc 审查；此报告不把候选 shell 语义假设当已证实因果。

## 对此前报告的明确更正

此前 observation 方案声称 `go version` 不支持/会忽略 `-json`，该判断不正确。Go 1.25.11 `cmd/go/internal/version/version.go` 明确提供该选项。本轮远端真实失败以及本地仅对既有Go可执行文件读取元数据的两次probe证明：继承 `GOFLAGS=-p=2 -json` 得到JSON；显式 `go version -json=false -m` 恢复文本。probe每次小于0.02秒，无构建、无服务启动；见 `version-format-probe.json`。旧封存文件保留不改，以本代更正为准。

## 最窄下一步

先修明确消费端格式，并保留外层JSON。随后对空GOCACHE用例保留实际launcher命令、错误GOWORK、嵌套worktree与精确VCS断言，改用共享编译缓存和每次唯一link buildid；验证旧产物不能冒充新link。具体先失败清单在 `repair-failure-inventory.md`。先解决当前断言，再以新提交跑原门禁一次，不提高超时、不反复重跑取绿。

限制：没有完成全部scripts门禁；没有新Linux性能验收结论；本诊断代次不包含后续修复；未修改生产脚本、未重新运行门禁。随后获授权的单文件 VCS fixture 修复与本地 focused 验证独立存放在相邻 `vcs-build-repair/`，保留本次失败及原始 identity。
