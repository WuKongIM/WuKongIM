# ed705 本地 30 分钟 shakeout 首轮失败

结论：**未完成，不能记为本地预检通过**。合并后干净 `ed705c0fbffdea485bbb65608d6e340bb049d97e` 用已提交 helper 原命令 `--stop-after 1800` 在 Darwin arm64 / Go 1.25.11 执行；没有修改100 SEND/s、2500用户、12/256/3/3拓扑、400ms及5%阈值。构建使用独立 GOCACHE，GOFLAGS=-p=2仅限制构建并发。无云操作、无再次运行。

06:21:48Z开始构建；三服务/三worker/四host-metrics准备就绪，06:23:53.630Z进入测量。06:29:11Z wrapper exit 8。worker-3（worker_id=2）在 grant sequence315 报 `offered_load_under_delivery`；coordinator终行是 `harness_invalid / invalid_observation / grant / delivery`，且 `report=unavailable`，没有final.json。未到1800秒自动停机时间，也不是为取绿而人工停止。

最后完整运行cut为06:29:03.664Z（测量约310.03秒）：发送31,005、ACK31,000；首尝试失败、拒绝、重试、terminal、丢失、重复、损坏、序号回退均0。5条仍在途，不能用运行中数字替代停止后的完整正确性证明。hot延迟有5954个样本，8个>400ms；没有完整终局P99，不从计数反推精确P99。24个自然会话过期、1个新会话启动中、2个planned cancellations保留在原日志。

清理独立核验：全部12个记录PID已退出，TCP15000–15063无listener，短TMPDIR为空；源码与冻结文件前后未改。原始本地Bench/worker随机token只在环境中存在；小于64MiB的未压缩日志/config/report/evidence逐字泄漏扫描为空，扫描范围明确留档。日志无panic/fatal匹配。终止期间一条presence RPC rejected的delivery warning保留，不能把它推定为首因。

磁盘前置已恢复并满足原5%门槛；结束后约78.80%可用。最后进程RSS快照保留在summary及原prom文件；由于终局报告缺失，不能宣称完整资源阈值验收通过。此运行只针对本机native预检，不是Linux24/72小时或云侧正式证据。

复核入口：`command.json`、`preflight.json`、`run/evidence/identity.tsv`、`run/logs/worker-3.log`、`run/logs/coordinator.log`、`cleanup-verification.json`、`credential-scan.json`、`summary.json`。原始data、二进制和独立构建cache保留本地，不复制仓库证据；二进制hash与构建身份已单独记录。只归档 `ARCHIVE_ALLOWLIST.json` 内的小文件。任何后续诊断需保留本轮，不能覆盖或重跑掩盖该失败。
