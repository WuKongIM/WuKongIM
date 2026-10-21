# scripts-integration 超时：只读诊断与最小观测补丁

结论：这次门禁仍未通过。Mac 两次都确实耗尽 `scripts` 包的 540 秒执行预算；Linux 则耗尽整个命令的 600 秒预算。现有证据支持“累计工作与调度预算不足”，不支持把它简化为某个用例挂死、共享 runner CPU 争用或依赖下载卡住。不能声称任何优化已解决问题。

## 固定身份与现有事实

- 本地审读源码 `0672c115950f65e6d92381aa5b3ea9f0a20fc790`。GitHub API 确认 Linux run `39563185818` 的 merge SHA `753005853ea3253d9d1ce6c525a3c3ba8c2f5c09` 与它具有相同 tree `ad05497c99502a4d076bacb7dee150d10e939d28`；两个 parent 是 ed705 和 0672。
- 未改变的 policy SHA256 是 `2ee1b3162b431d77bec8cf68acc5b8aec0eede735f03c9f89d6c42e1d86cb0f9`。原命令完整覆盖 `./scripts/...`，`-count=1 -timeout=9m -parallel=2`；外层 600 秒、8 MiB，`GOFLAGS=-p=2`。
- Mac 首轮包耗时 540.594 秒，尾部两用例各运行 1 秒；隔离复验包耗时 540.957 秒，尾部两用例仅运行 8、9 秒。两轮均有大量 `T.Parallel` 排队，终止位置不同。不能从缺少失败行推导其余用例均已通过。
- 隔离复验 54 次约十秒间隔的进程样本中，07:20:43 已看到 `scripts.test`；07:23:26 至 07:25:17 的样本看到真实 shakeout 的 server/worker 进程；26 个样本含 sleep，3 个样本捕获编译或链接。采样不是精确阶段计时，也不能用 `ps %cpu` 的平均值证明 CPU 饱和。原日志与摘要的 SHA 已冻结。
- Linux job `119099050076` 使用 Ubuntu 24.04、Go 1.25.11；600 秒后外层清理完成，无剩余本次 SID 进程。5206 字节输出全是依赖下载，未给出测试阶段或逐例时长。当前 runner 没有阶段时间戳，原 Go 非 verbose 输出又会缓冲，因此“只有下载行”不等于“十分钟都在下载”。

## 排序后的假设与可证伪边界

1. **累计工作/串并行安排耗尽包预算：最强。** 同一 `scripts` 包静态有 520 个顶层测试，其中 219 个来自 integration 文件。普通重型用例需经过 `T.Parallel`、2 槽信号量及 RWMutex 读锁；exclusive 用例再独占同一个锁。[调度 helper](https://github.com/WuKongIM/WuKongIM/blob/0672c115950f65e6d92381aa5b3ea9f0a20fc790/scripts/script_test_helpers_integration_test.go#L18)。真实 shakeout 明确在 Go 串行阶段运行，先构建两个二进制，再启动集群并做 90 秒工作；它拥有 8 分钟自己的上限，不能被当作“通常只占 90 秒”。[用例](https://github.com/WuKongIM/WuKongIM/blob/0672c115950f65e6d92381aa5b3ea9f0a20fc790/scripts/wukongim_three_node_chat_lifecycle_script_integration_test.go#L396)、[构建](https://github.com/WuKongIM/WuKongIM/blob/0672c115950f65e6d92381aa5b3ea9f0a20fc790/scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh#L352)。这证明当前机器上的实际组合不适配预算；尚未证明任何 Linux 机器上该包的必然最短时间都超过 9 分钟。
2. **可消除的重复构建：源码已证实，耗时占比待测。** `wkchatlifecycle` 在 repair tests 的 19/124/226 行分别同参数构建；`wkcli` 在数据库烟测的 18/55/86 行分别同参数构建，真实 shakeout 另构建一次。当前没有共享 build-once fixture。Go 缓存能复用编译单元，不能把多次独立命令直接等同于零成本。[repair](https://github.com/WuKongIM/WuKongIM/blob/0672c115950f65e6d92381aa5b3ea9f0a20fc790/scripts/chat_lifecycle_repair_integration_test.go#L19)、[数据库烟测](https://github.com/WuKongIM/WuKongIM/blob/0672c115950f65e6d92381aa5b3ea9f0a20fc790/scripts/wkdb_import_smoke_script_integration_test.go#L14)。可在保留每例独立数据目录和全部断言的前提下复用只读二进制；尚不能承诺这一步就能省足预算。
3. **表驱动测试绕过调度入口：源码已证实，收益待测。** `repoRoot` 对名字含 `/` 的子测试不装 gate；例如 `TestCloudDeploymentExecution` 直接循环十个子用例、子用例内才调用 `repoRoot`；六例原生包故障诊断相同，含真实超时/清理等待。[入口限制](https://github.com/WuKongIM/WuKongIM/blob/0672c115950f65e6d92381aa5b3ea9f0a20fc790/scripts/script_test_helpers_test.go#L38)、[十例](https://github.com/WuKongIM/WuKongIM/blob/0672c115950f65e6d92381aa5b3ea9f0a20fc790/scripts/cloud_deployment_execution_integration_test.go#L21)、[六例](https://github.com/WuKongIM/WuKongIM/blob/0672c115950f65e6d92381aa5b3ea9f0a20fc790/scripts/native_package_bootstrap_diagnostics_integration_test.go#L18)。最窄候选是确认隔离后在顶层进入现有 gate，保留子例串行。不能全局删除 `/` 判断，否则嵌套并行/锁和 `Setenv` 等约束可能被破坏。候选列表只是静态筛选，不是已测优化清单。
4. **Linux 冷下载/编译消耗外层预算：合理但未定量。** `cache: false` 与下载日志确认它未使用持久缓存；首次包 `start` 时间缺失。Mac 复验使用已有缓存仍超包时限，所以“只预热”不是已经成立的完整修复。单一挂死或平台 CPU 争用的解释更弱，暂无因果证据。

## 最短下一步：先让一次新 revision 给出可用进度

已在 `observation-patch/` 准备三个文件的提案：仅 Linux scripts job 的 GOFLAGS 改为 `-p=2 -json`，同步其现有静态契约及工作流说明。不修改 policy、runner、源测试集合、600 秒/9 分钟/parallel=2、输出上限、缓存或自动重试；不触碰运行中的 worktree。

**为何不是 `-v`：** 同版本 Go 源码 `cmd/go/internal/test/test.go:1424–1463` 明确表示，多包且 `BuildP=2` 时 `-v` 仍进入内存 buffer；`testJSON` 才会即时输出并缓存。[官方同版本源码](https://github.com/golang/go/blob/go1.25.11/src/cmd/go/internal/test/test.go#L1424)。外层先结束进程时，单用 `-v` 可能再次丢失全部进度。

`-json` 的 TestEvent 提供 `Time/Action/Package/Test/Elapsed`。若 scripts 包根本没出现 `start`，才有理由继续量化构建阶段；若出现 `run/pause/cont/pass/skip`，可以排序真实运行耗时、还原串行阶段、排队与最后活动用例，再选择上面一项窄修复。BuildEvent 只有 `ImportPath/Action/Output`，不提供编译器级耗时；不保证这一个补丁能细分每个链接或下载的成本。[编码定义](https://github.com/golang/go/blob/go1.25.11/src/cmd/go/alldocs.go#L2169)。JSON 放大输出仍受原 8 MiB 限额约束，达到上限仍须失败。

**对子命令的影响：** Go1.25.11 的 `go build` 支持 `-json`；当前真实 build 调用成功时不解析 CombinedOutput，失败时会保留 JSON 诊断。`GOFLAGS` 只对命令实际支持的 flag 生效，`go version` 等不支持者忽略它。[flag 注入](https://github.com/golang/go/blob/go1.25.11/src/cmd/go/internal/base/goflags.go#L90)。检索到唯一真实 `go list -f` 在 gofail 构建脚本，但当前脚本测试只做其 `--dry-run`，没有执行该分支；不扩大为对任意未来 Go 子命令的兼容承诺。

测试失败库存和契约编辑先于 workflow 实现。随后 root 明确允许仅执行静态 Python 契约：旧 workflow + 新契约先红（8 项中 1 项失败，正是缺少 -json），候选再绿（8/8）；日志分别保存为 `01-static-red.log`、`02-static-green.log`。Python/内嵌 Python AST 解析和独立副本 `git apply --check` 也通过。没有运行 Go、后端或负载。下一次远端运行应是此诊断改动的新提交，不是重跑旧失败 revision；首失败证据继续保留。当前没有放宽门禁，也没有通过证据。
