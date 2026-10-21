# PR #1016 第一代 CI 审计（2026-10-21）

结论：本代尚不满足候选放行。全量默认测试有两处断言失败，依赖它的迁移/跨节点/自聊 E2E 未执行。其他 5 个工作流成功不能补足这一缺口。

冻结来源：head `5f3ecdf93e762a1b1551b44977a44f2e63c97802`，base `b469c6e5e2b4ff65611a8fe3389fb5034de3f152`。PR 实际 checkout 合成提交 `0ab6ea4bad09c76dae8c71f8ca67c00d4e220b89`，其 tree 与 head 同为 `f030a2fdd558266bc8f3d87f516b9f73273df93a`。全部运行均 attempt 1，未重试。

| 门禁 | Run / job | 结果与实际覆盖 |
| --- | --- | --- |
| 全量默认 + 候选 E2E | [39546957124](https://github.com/WuKongIM/WuKongIM/actions/runs/39546957124) / default 119047184216 | **失败**；go-unit 实际执行 412.69s，exit 1；E2E job 119049325203 **skipped**，没有五拓扑/自聊新证据 |
| PR 三节点与性能 | [39546956929](https://github.com/WuKongIM/WuKongIM/actions/runs/39546956929) | unit/race、arrival saturation、90s correctness、三个 500 QPS seam 和最终聚合均成功；nightly/诊断 job 为不适用 skipped |
| Released EasySDK | [39546956856](https://github.com/WuKongIM/WuKongIM/actions/runs/39546956856) | Android Maven Central 1.0.5、iOS CocoaPods 1.1.1、Flutter pub.dev 1.1.0 均实际运行，三份日志都有双向 `PEER_PASS` |
| Manager | [39546956925](https://github.com/WuKongIM/WuKongIM/actions/runs/39546956925) / 119047183500 | 实际 Chromium 产品场景与超时子进程回收测试均 PASS；仅失败截图上传步骤因成功而 skipped |
| Native preview | [39546956829](https://github.com/WuKongIM/WuKongIM/actions/runs/39546956829) | 构建、测试签名元数据闭包、四发行版安装及 Ubuntu/Debian/Rocky/Alma 真实 systemd 生命周期均成功；这是 unsigned preview 验收，不是正式签名频道发布 |
| 手动同源 500 QPS 资格 | [39546982565](https://github.com/WuKongIM/WuKongIM/actions/runs/39546982565) / 119047268866 | 600s 实测及三个 seam 成功；手动 checkout 精确 head 5f3ecdf |

## 首次默认测试失败

冻结 policy 为 b469，digest `2ee1b3162b431d77bec8cf68acc5b8aec0eede735f03c9f89d6c42e1d86cb0f9`。命令/工作目录/1800s/8MiB 来自 named `go-unit`，`GOFLAGS=-p=2`。输出 88,269B 未截断；没有超时或 runner 错误。Linux cleanup 收据 verified=true、complete=true、remaining_processes=[]，原始失败保留。

1. `TestSourceDrainProgressBeforeSealDoesNotClose/canceled`，`internal/usecase/mqttsession/source_drain_contention_test.go:116`：err chain 仍含 `mqttsession: source drain pending`。
2. `TestRuntimeRetainsEveryFollowerRepairWhenExecutionQueueIsSaturated`，`pkg/channel/replication/runtime_test.go:415`：`retained repairs = 64, want 66`。

原始日志、identity/command/result 在 `artifact-12322262068/`，下载 ZIP 已独立匹配 GitHub API SHA256。内部迁移 v2/v3/usecase 包均显示 ok，但不替代被跳过的真实进程 E2E。

根任务后续诊断反馈：MQTT 是嵌套 root 取消传播 race（3/3 重现）；replication 是测试 router 仅匹配单项请求，合批绕过注入。该分类来自后续诊断，不是本轮 CI 日志单独推导；修复后的新 head 仍需重新验收。

## 性能与资格数值

每个 seam 均为三个 60s measurement window、90,000 次完成、0 error/0 drop，qualification=true。PR 最大窗口 p99：channel append 6.769ms、mixed send 14.155ms、TCP SENDACK 178.080ms；手动精确 head 分别 22.391ms、19.475ms、178.114ms，均低于 400ms。warmup 与 measurement 文件分开保留。

600s 本地资格的 `local-step.json`：测量 sent=ack=300,509、expected=300,000，实际 500.848333/s；2500/2500 连接在线；storage/I/O/product metrics/queues/process continuity/timeline/profile evidence 均 complete，队列收敛。日志正式测量约为 UTC 05:01:52—05:11:52，测量后按协议 TERM，首个 post-drain 样本即归零。

`final.json` 全程 sent=ack=330,511，包含 60s warmup 和边界发送，不能当作纯 600s 数量。terminal/loss/duplicate/corruption/sequence regression/first-attempt failure/harness failure 均为 0；retry_attempts=27；cleanup_error_count=0。hot SENDACK 聚合直方图 p99 上界为 400ms、均值 28.591ms、最大 1482.669ms；有 1 个 hot latency warning，latency anomaly count=0。canonical lifecycle candidates/completed 均为 0，故不能声称已完成冷却重热或正式长稳实验。

## 归档界限

`archive-allowlist.json` 给出建议归档的相对路径、大小及 SHA256。包含本代 API/run/job/source identity、成员 inventory、选取的小 JSON、测试日志与此摘要；排除 ZIP、服务端二进制、native deb/rpm、原始数据库与临时下载工具。

小型 default/seam ZIP 的完整 SHA256 已匹配 API。大型 608MB qualification、230MB correctness、78MB native 包仅按认证 blob byte ranges 读取成员目录和明确的小报告；保留 API 声明的整包 digest，但未声称已独立计算未下载整包的 SHA256。选取报告经 ZIP 成员 CRC 检查。新版 head 应写入独立 final-v2，不能覆盖本代首失败。
