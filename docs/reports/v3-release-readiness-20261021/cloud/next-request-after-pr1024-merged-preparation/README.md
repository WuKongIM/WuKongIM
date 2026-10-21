# #1024 合并后的下一次 RC 审批材料

**计划已冻结，尚未获本次付费启动授权，不能直接执行。** 产品与控制均为 `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06`，完整 tree `a13367b2dc4658e6c27a1992f240544636e5178d` 与已验候选 f419 相同。父任务确认普通合并于 **2026-10-21 20:44:41 UTC / 2026-10-22 04:44:41 Asia/Shanghai**；本地 Git 对象、父提交及 118 个离线工具源文件逐字核验通过。本次未刷新远端 API。

`plan.json` 是可审阅、不可派发的六字段模板：`source_sha` 固定以上 SHA；`operator=tangtaoit`；`request_budget_limit_micros="1276567200"`；`released_preclock_run_id=""`；真实 `request_id`、诊断公钥仍为 null。没有生成运行身份、私钥或新 Issue。只有新的明确付费批准后，才可依既有机制补齐这些字段并单次调用 `.github/workflows/chat-lifecycle-rehearsal.yml`（ref main）；启动前 main 必须仍是冻结 SHA，变化则重新审查。

预算保留整项硬上限 **1500000000 微元（¥1500）**／停止线 **1350000000 微元（¥1350）**。三次历史完整报价保守预留共 **223432800 微元（¥223.4328）**，实际账单仍未知。新 request 硬上限 **1276567200（¥1276.5672）**／停止线 **1126567200（¥1126.5672）**，两条加回历史预留分别恰等于原整项上限／停止线，安全差额仍为 ¥150。新 request 初始 prior 为 0；外部历史预留不能伪装成已认证 prior 或重复计费。正式阶段只继承该新 request 已认证的预演创建至清零费用。

方案保持杭州 **4 台 4 核 8 GiB**（3 service、1 load）、原磁盘/公网 SSH 与 HTTP 范围及清理责任：12h Lease 中完成 2h rehearsal，先完整证据和精确清零，再以同源码/同 bundle/同 request 取得 fresh 96h Lease，执行连续72h、同老化数据容量≤8h、2000 SEND/s 恢复30m。10,000在线、2000 SEND/s、12/256/3/3拓扑及 YAML 全部门槛不变。24h、CI或离线检查均不能代替正式72h验收。

已运行 `offline-cases/results.json` 的9项实际本地 CLI 检查：降预算预演、旧默认、降预算正式结构传递，以及提高／零停止线／非规范预算、错误历史 prior、无证据 attempt2、缺正式 transition 的拒绝，全部符合预期。正式正例使用明确 **TEST ONLY 合成 transition**，仅证明结构传递；测试公钥只是固定格式向量，无私钥，request/bundle均为测试占位，时间已过期。初次默认 Go1.23.4 因版本不足拒绝已保留；改用现有 Go1.25.11、网络禁用后构建成功。工具不是部署 bundle。复算命令见下方；输出必须是不存在的新目录。

```sh
python3 verify-offline-materialization.py --repo <本地仓库绝对路径> --source 79a7177032a6ae1ec7015cefe0e6f94eb0f19f06 --tool <上述源码闭包构建的本地wkchatlifecycle> --output <新临时目录>
```

实际启动仍需既有准入链：身份/公钥配对、完整有界活跃及 cleanup/Stop 库存、唯一 open/unlocked Issue、不可变 bundle、真实 Quote 的容量/配额/价格/预算验证、Acquire 前精确 selector，以及后续完整终态证据和12scope清零。本轮没有请求或伪造 Quote、selector 或 live 权威验证；没有修改 IAM、权限、预算、门槛、工作流状态或调度。失败按原 Stop/Release 收尾，不自动重试运行期/正确性失败，不扩大权限。

旧 request `chat-20261021T163850Z-4ccd7506` 在 **61.212052848s** 报 `product_failure/message_loss=1`，formal未启动；**2026-10-21 17:43:09.4025176 UTC / 2026-10-22 01:43:09.4025176 Asia/Shanghai** 已精确12scope零库存。六份原公开证据哈希重验通过，旧代8项清单未改。此为保留的历史证明，非新provider扫描；旧 complete artifact 的证据是已验选定成员，不冒称本轮完整ZIP重下载。旧已接受 run-start、runtime failure 与 Stop 不符合 released pre-clock continuation，旧授权已消费。

#1024 改善终态证据保留：worker/status 绑定 run、generation、snapshot sequence、terminal cut；指标按原终态时间锚定15分钟，8个固定分区共享60s/24MiB、单文件4MiB，保留原纳秒 cut 和规范毫秒查询边界。缺失、超限、超时仍如实失败，不能保证下一次一定采齐；**没有证明或修复历史 message_loss 根因**。前次两次现场 Analysis 均未连上 MCP，第二次 CLI0.163 的12个 health请求超时原因未知，原授权已关闭；不能假设下次 MCP 可用或暗含第三次尝试授权。

这是既定正式 RC Actions 路径。Direct Lab Skill 明确仅诊断、不是正式证据；其 CNY300／whole-CNY 限制不适用于本方案，也不会改走该路径。下一步仅向用户呈现上述精确源码、风险、整个任务累计预算、临时公网、跟踪与最终释放范围，取得**新的**启动决定。

依据（均由 `rules-and-source-freeze.json` 绑定到精确合并源码）：

- [正式入口、预算、续跑与准入](https://github.com/WuKongIM/WuKongIM/blob/79a7177032a6ae1ec7015cefe0e6f94eb0f19f06/.github/workflows/README.md#L1018)，[正式链与清理](https://github.com/WuKongIM/WuKongIM/blob/79a7177032a6ae1ec7015cefe0e6f94eb0f19f06/.github/workflows/README.md#L1206)。
- [向下预算 materialize](https://github.com/WuKongIM/WuKongIM/blob/79a7177032a6ae1ec7015cefe0e6f94eb0f19f06/internal/usecase/chatlifecyclerun/run.go#L325)，[正式 transition](https://github.com/WuKongIM/WuKongIM/blob/79a7177032a6ae1ec7015cefe0e6f94eb0f19f06/internal/usecase/chatlifecyclerun/run.go#L472)，[远端预算 guard](https://github.com/WuKongIM/WuKongIM/blob/79a7177032a6ae1ec7015cefe0e6f94eb0f19f06/cmd/wkcli/internal/benchmark/formal_chain_command.go#L163)。
- [Quote 后 selector](https://github.com/WuKongIM/WuKongIM/blob/79a7177032a6ae1ec7015cefe0e6f94eb0f19f06/internal/usecase/cloudlease/control.go#L45)，[Quote 成本准入](https://github.com/WuKongIM/WuKongIM/blob/79a7177032a6ae1ec7015cefe0e6f94eb0f19f06/internal/usecase/cloudlease/control.go#L817)，[工作负载与阈值](https://github.com/WuKongIM/WuKongIM/blob/79a7177032a6ae1ec7015cefe0e6f94eb0f19f06/configs/wkbench/chat-lifecycle/formal.yaml#L69)。
- [worker 与 cut 验证](https://github.com/WuKongIM/WuKongIM/blob/79a7177032a6ae1ec7015cefe0e6f94eb0f19f06/scripts/chat-lifecycle/terminal-evidence.py#L125)，[有界 metrics](https://github.com/WuKongIM/WuKongIM/blob/79a7177032a6ae1ec7015cefe0e6f94eb0f19f06/scripts/chat-lifecycle/terminal-evidence.py#L359)。

归档仅按 `ARCHIVE_ALLOWLIST.txt` 与 `SHA256SUMS`；排除离线工具二进制、构建cache、工作树源码、私钥、完整DB及旧大制品。无活动 `FLOW.md` 副本。
