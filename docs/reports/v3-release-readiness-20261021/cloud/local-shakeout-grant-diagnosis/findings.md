# 本地 shakeout 的 grant 预算诊断

原始产品/工具源为 `ed705c0fbffdea485bbb65608d6e340bb049d97e`。原实机失败仅运行一次，全部原收据保持不可变；本目录是后续独立诊断。没有重跑原 100 SEND/s 实机负载，没有修改 runtime deadline、路由、正确性或延迟阈值。

## 原始信号及缺失信息

`../local-shakeout-ed705/run/logs/worker-3.log:1` 保留 worker 2、grant sequence 315 的 `offered_load_under_delivery`；coordinator.log:63 保留 `harness_invalid / grant / delivery / report=unavailable`。最后完整 cut 是 06:29:03.664644666Z；没有 final.json，不能声称最终排空/正确性通过。

06:27:03.664721208Z 至最后 cut 的约 120 秒（25 cuts）中，SEND 增加 12,002，ACK 增加 12,006（包含上一区间在途 ACK），接收和接收 ACK 均增加 15,609；各失败、重试、终态、丢失、重复、损坏和序号回退增量均为 0。热延迟样本增加 2,640，超过 400ms 的增量为 0；这不是精确 P99。最后累计 SEND 31,005、ACK 31,000，仍有 5 条在途。

首次自然过期的 cut 为 06:28:48.664656125Z。最后 worker 2 在线 833/833、过期累计 6、planned cancellation 0；全局过期 24。时序相邻并不能证明过期导致错误。

没有 pprof。主机资源只保留启动前的 warmup-before 快照：06:23:02Z 的内存约 56.13–56.17%，首个 CPU 利用率样本缺失，物理 I/O 信号标为不可用。06:29:06–07Z 有最后一份累计进程 CPU counter 和 RSS：三个产品进程 RSS 708,608,000 / 700,121,088 / 698,925,056 字节；worker RSS 258,736,128 / 257,245,184 / 266,584,064 字节。这些不能还原失败前两分钟 CPU 压力、瞬时停顿、主机内存或磁盘 I/O。

`evidence-excerpt.json` 保留全部 25 cuts、最后三 worker、资源原字段及源 SHA256。`extract-evidence.py` 是只读提取器，`--output` 必须为新文件。原凭据扫描仅引用通过/是否命中布尔值和原扫描范围，不输出凭据。原清理收据已证明 12 个记录 PID 退出、64 个预留端口关闭。

## 真实调用路径

在 exact source 中，coordinator.go:794 的计划分配进入 :2083 `deliverGrant`；worker_server.go:666 调用 generation.ApplyGrant；:1563 进入 Engine.ApplyGrant（engine.go:1233），然后 :1268 / :1326 执行 admission。只有第一次 grant 同步推进 due work；第 315 次 grant 的同名错误来自 admission 路径。

直接候选分支是 person mandatory activity 的到期（engine.go:3957、:4146）、active person 候选耗尽（:4039）、due canary 连续不可用（:4344）。`processWork` 中 100ms dispatch deadline (:2838) 是另一个 tick/advance 路径，不能仅凭相同错误码替代原 grant 调用栈。原事件没有具体分支 discriminator。

## 假设与可重复结果

父任务在执行 probe 前声明四项假设：自然过期使 sender 不可用；pending activity/canary 状态问题；调度迟到；产品/transport 背压。测试使用真实 worker factory、已提交 YAML、CoordinatorGrantPlan、固定时钟和 10ms 推进，fake ACK 完成后才推进时钟；不启产品进程、端口、云或 wall-clock sleep。

- `probe-v1.log`：原 YAML worker 2 在虚拟 318 秒、grant 319 报相同 admitted underdelivery。仅把会话固定为一小时的对照同样失败。`probe-v1-repeat.log` 三次重复的六个分支均失败，位置 317–318 秒、grant 318–319。启动异步完成顺序使精确秒数不同，不能称每次完全相同。
- `probe-v2.log`：一小时会话不变，只把测试内 global SEND 从 100 改为 200，走完 340 秒且 300 秒 activity 已清空；100 分支仍失败。此控制只用于机制辨别，不改变任何已验收 workload 或生产阈值。
- `probe-v3-500.log`：保持自然会话，仅500 SEND/s控制三次均走完340秒。
- `focused-green.log`：修正固定默认后，正式回归覆盖全部三个 worker 到360秒；240秒时初始 activity 队列为空，最终 Engine ActivityUnderDelivered/HarnessInvalid 都为0；原100负例仍捕获 admission activity expiry。该负例不依赖调用前 heap head 的 deadline，因为同一批 grant 可能在路由后续 activity 时失败。

这些 probe 稳定复现配置机制，不证明历史 grant315 的逐条状态。历史失效分支未保留，bootstrap 的真实 CONNECT/同步完成时序也与 fake I/O 不同。fake ACK 没有真实 RECV 或产品延迟覆盖，结果不得用于产品正确性、实机容量或正式资格证明。

## 预算解释及最小修复

该 seed/worker 的初始 mandatory activity 共 16,746 条。首 grant 重基后的最迟截止为 341.850406029 秒。在原100 SEND/s分配中，每秒给该 worker 的 primary share最多34条；即使把所有 primary都当person，并宽松给足343次分配，也最多11,662条，仍少于16,746条。实际90% person share约30条/秒。因此在“会话始终可用、不取消义务”的控制中，原预算不能履行生成的义务；不需要 CPU 停顿或产品背压才能失败。

真实 admission 返回错误时 activity 计数减少且 ActivityUnderDelivered/HarnessInvalid 各增1；结合调用路径，将 probe 定位到 mandatory activity expiry。自然过期、canary缺成员、调度延迟和产品背压都不是这份 probe 的必要条件；不排除历史实机有额外因素。

修复仅修改固定 `local-shakeout.yaml` 的 SEND100→500、burst200→1000（原2s信用不变），同步 helper默认/帮助/渲染锚和文档，加入真实默认预算回归及实际shell配置渲染默认500/1000和override400/800检查。保持2500在线、启动义务、5m窗口、400ms、全部正确性与资源门槛。没有将最坏工作上界加入通用 Config.Validate：这种保守条件会误拒 probe 已通过的200，并非必要条件。

formal/rehearsal YAML实际仍10000在线、2000 SEND/s；本次未改。旧设计/runbook中25/50 bootstrap logins/s与当前配置不同的陈旧叙述不在修复范围。

## 验证与待办

首红收据：`default-red*`、`config-example-red*`、`render-red*`；focused green和render green已通过。所有完整named checks单独保留在 `named-checks/`，从冻结 policy动态取命令。原诊断probe源码逐代存为文本；仓库只保留最终默认场景回归。

修复后的30分钟实机验收必须在完整检查及提交冻结后，使用新输出目录和一次独立运行完成。当前机制绿不证明本机能承受500 SEND/s，也不关闭正式2h/72h、capacity/recovery资格要求。若新实机负载失败，原样保留，不降门槛或反复重跑。

## 完整检查发现的前存测试问题

首轮 `go-unit` 全部通过，随后 `go-vet` 在未修改的
`pkg/transport/internal/buffer/retained_test.go:7` 拒绝值遍历：`slabClass`
包含 `sync.Pool`，复制类值同时复制其 noCopy 标记。修复前文件与基线
`ed705c0fbffdea485bbb65608d6e340bb049d97e` 字节完全一致（见
`vet-failure-inventory.json`）。保留 `named-checks/go-vet.log` 的第一次失败，
只把测试改为索引加指针遍历，不修改 transport 产品代码。该包的 vet/test
补证和完整 policy `go-vet` 结果分别在 `named-checks-vet-repair/`。
最终 11 个改动文件的策略选集在 `final-policy-selection.json`；新增测试路径
未增加另一类 mandatory check。全量单元测试之后仅该测试遍历语法发生变化，
因此以该包测试补证，不重复无关全量负载。

## 脚本集成总时限仍未通过

首轮完整 `scripts-integration` 在固定九分钟整包时限终止（exit 1），
不等于产品或本次默认预算断言失败。完整栈见
`named-checks-remaining/scripts-integration.log`；退出时两项用例各只运行
一秒，另有多项仍在并行队列中等待。既有真实 90 秒测试在整个套件的
串行阶段运行，但没有逐用例日志，不能据此把该用例单独宣称已通过。
SID 40338 的退出后清单为空。没有增加时限、关闭测试或自动重跑。
原始超时是当前尚未完成的 named gate，必须在最终交付中保留。

一次隔离复验保持相同源码、policy 命令、九分钟 / parallel=2 / p=2 和任务缓存路径，
在 07:29:35Z 再次超时，包耗时 540.957 秒。首次后缓存已变热，两次不能当作
纯单变量争用对照；首轮与补充 vet/test 的实际重叠仅约 7–8 秒，不能据此称其
为超时主因。复验终点是另一组运行 9 秒 / 8 秒的 shell 用例，未观察到断言
FAIL。复验的完整记录在 `named-checks-scripts-isolated/`，包括每十秒进程/CPU
观测。Go 超时后遗留本 SID 内的假 shell 进程已经过即时 SID 复核清理，
cleanup.verified/complete=true，remaining_processes=[]。没有第三次重跑。
最终汇总 `final-check-summary.json` 明确 scripts-integration 尚未通过，
其余五项 selected checks 有绿；当前未提交、未开始新的 30 分钟负载。
