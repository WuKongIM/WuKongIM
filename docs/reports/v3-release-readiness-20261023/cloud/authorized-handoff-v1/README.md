# 79a 云 RC：部署和已认证远端交接

唯一 request `chat-20261021T214505Z-00299e37`、产品/控制 `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06` 的初始启动已完成。顶层 [39820950900](https://github.com/WuKongIM/WuKongIM/actions/runs/39820950900) 于上海 2026-10-23 01:35:31 首次成功退出；所有关联子运行都是 attempt 1，未重复 dispatch 或重购。基础准入、报价、采购与预算证据见同级 `startup-public-v1`，其 SHA256SUMS 摘要 `bf305a54b37f9feee68f4b215842d666e85d589fa3ae05a92c26070b1e22e6cf`。

**已认证的计时开始**：上海 2026-10-23 01:35:17.578381188，预定结束 03:35:17.578381188，generation 1，精确相差 2 小时。原源码只会在准备完成、10,000 全量同步与首个完整 2,000 SEND/s grant 接受后写出这份 typed run-start；该回执不是每秒实时计数快照。2h 结果、72h、容量、恢复均尚未观察，不构成 v3 正式发布通过。

100 项独立交叉检查通过：

- handoff artifact 12446490247 的实际 35,013 B ZIP 与 GitHub SHA256 `d5b73cf3baf33ea1e7397c5919322a67c47ba281b345ada75d6973c8b121d716` 一致，41 成员逐一哈希、CRC、名称和尺寸检查完成；仅抽取 15 个固定公开 payload。
- 父链明确指向 bundle 39821046654、Acquire 39822336878、Deployment 39822605006；源、bundle digest、首次 attempt、generation、原预算和初始 prior=0 逐项绑定。
- 已观察到顶层实际 `release-selector.json`：request、Lease、provider、region、repository、plan digest 与已验 Acquire Receipt 精确相等；不能再用单纯 Receipt 身份推测 selector。
- 部署 artifact 12445382317 的实际 ZIP 大小/SHA/CRC 通过，四份公开回执与 handoff 相同。部署 readiness 于 01:32:06 通过：3 节点、256 物理槽领导与副本健康、12 逻辑组、3 worker、7/7 Prometheus target、待处理 Controller 任务 0。
- 部署 Plan 摘要重新计算，4 个 host/disk/resource 逐一绑定原采购，私有地址、文件系统最低容量、拓扑、租约 expiry 和激活时间一致。
- 诊断公钥与加密访问 recipient 指纹一致；部署 envelope 绑定原 request/Lease/plan/expiry/公钥，两个 envelope 从部署到 handoff 字节一致。只验证公开身份和密文摘要，未解密或复制密文。
- 真实 systemd 空 Job 字段被原解析器接受为 Job=0；active/running、完整字段且无 invalid/missing。原 empty-Job 修复在这次真实输入上生效。
- Quote 与 Acquire 原字节在 handoff 中未变；新 request 硬上限 ¥1276.5672 / 停止线 ¥1126.5672、外部历史报价预留 ¥223.4328 保持不变，实际账单未知。

交接后的只读检查：[#1025](https://github.com/WuKongIM/WuKongIM/issues/1025) 为 open，原工作流已写 `rehearsal_running`；rehearsal finalizer 与通用 Release 均 active。最近已完成的 finalizer 39823023114 创建于 01:28:40，早于这次 handoff，不能作为交接后远端健康或终态证明。尚未观察到新终态或失败原因，远端后续健康需要后续正式回执确认。

后续责任仍由原安全调度执行：采齐终态、精确 Release 至 12 scope 零库存，只有 passing rehearsal 才能消费原 transition 获得 fresh formal Lease。未手工触发新 finalizer、第三次 live MCP、SSH、额外云写入或 stable 发布。完整 ZIP、原 job 日志、私钥和 encrypted envelope 均保留在公开封存之外。
