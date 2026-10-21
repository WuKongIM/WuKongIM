发现 2 项 P2，均在 scripts/chat-lifecycle/terminal-evidence.py：

- 293 行：纳秒边界误拒 Prometheus 毫秒首点并丢弃有效分片。探针纳秒边界失败、毫秒对照通过，违反 failure-matrix.md:8 的覆盖要求。须匹配查询精度并保留原终态锚点。
- 370–374 行：非字面 pass 配 attempted=false 均记为 formal_failed_before_capacity。探针中成功的 passed_with_capacity_warning 和非法 outcome 均被豁免，违反 failure-matrix.md:4 的明确阶段证据要求。须校验合法失败状态及阶段。

七文件前后哈希一致，无范围扩张；未读 Standards，未重跑全套。复现见 spec-probes/results.json。
