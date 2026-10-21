# PR #1020 首轮 CI 公开证据

固定 head `0cdfaa37b00c58819895dee54029c27c83c9caa9`，base/远端 main `ce3c4d9110f17bb71dc99a89dc3c09758fabf980`。实际 checkout 为 PR merge `922bc66a9cebcbe09bef5fc43afc0593c4f07fb8`，tree `36f96357c967418517a00444e52ae03f77c3ff90` 与提交完全一致。

5 个自动工作流首次 attempt 1 全部 completed success；21 checks 中 19 success、2 非 PR 诊断/夜间任务按条件 skipped。没有 rerun、手动 dispatch 或云操作。

- Linux `go-unit`：219 有测试包通过、15 无测试包；`scripts-integration`：633 顶层通过、1162 pass 事件、6 既有可选 skip、0 fail/unfinished。外层 595.434 秒、包 470.805 秒；所有 85 新续跑测试事件通过。三个远端摘要 SSH 子例实际执行并通过。执行器均 complete cleanup、零遗留、输出未截断。
- 迁移五拓扑全部通过；跨节点 4 例通过（既有 opt-in 2000 QPS 比较未启用）；自聊 1/3 节点集群跨重启两例通过。构建和三组测试的独立 cleanup 均完成；binary VCS=实际 merge、modified=false。
- 三个 500 QPS seam 各 3×60 秒窗口共 9 窗均通过，错误/压测请求丢弃为 0、P99 <400ms。chat correctness 的90秒测量 clean，final 为协调 operator_stop，不冒充正式时间窗完成。
- iOS/Android/Flutter 发布包互通验收、DEB/RPM metadata/四发行版安装及生命周期均 success。原生包仅 PR preview 演练，不是稳定渠道生产发布。

本地历史仍保留：go-format、flow-doc-contracts、workflow-contracts、go-unit、go-vet、go-mod-tidy 通过；Mac scripts-integration 首轮9分钟超时，owned session 已清零。完整本地 go-unit 属于 freeze-v2；v3 窄 helper/集成回归另有证明，不能把它们合称本地同源全部首绿。本次最终 tree 的完整 Linux go-unit/scripts 独立首次通过。

尚未证明真实旧请求 continuation authority/live cloud、旧实际账单、2h→72h/容量/恢复，生产 stable 发布也不在本证据内。此次 CI 不能授权或替代云启动决策。

原 ZIP 已下载的 7 个产物均核 API SHA256/字节数/ZIP CRC；大 ZIP 和二进制不在此包。Native preview ZIP 仅保留 API 元数据（未下载），四发行版的公开 job logs/步骤记录证明实际执行。日志以 `.gz` 保留，经行级认证关键词脱敏；原日志 SHA/字节数与变换在 `archive-transforms.json` 及 job receipts，完整原文仅在本机私有原始目录。JSON/业务报告使用精确白名单，不含配置、私钥、解密凭证、缓存和 payload 二进制。`SHA256SUMS` 校验本目录；`ARCHIVE_ALLOWLIST.json` 是完整文件列表。

一次只读 jobs API 获取、一次只读 log 下载曾失败，后续有界 GET 成功；不是 CI retry，原观察错误已保留。初始离线 E2E 分析器对所有 skip 作拒绝，发现既有 opt-in 比较后以精确名单记录此限制；没有修改测试。
