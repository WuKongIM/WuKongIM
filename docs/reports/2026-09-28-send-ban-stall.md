# 30 分钟持续验证（写停顿观测版）：447.5s 因权限读准入繁忙失败

源码 `42080a044`，服务端 `wukongim-stall`，harness `permission-soak-stall.test`。
负载 5000 频道、4500 SEND/s、cap32、30m，不注入故障。

- 失败于 447.5s（约 7.5 分钟），`SENDACK wkrc-permission-soak-001146936 reason=ReasonNodeNotMatch`。
- 前 4 分钟每分钟快照 pending=0；无 EOF、无断连。
- 日志 36 条 `send_failed`，错误均为 `route not ready: permission read admission busy`。
  `send_permission_admission_busy` = 4；node-1 37 条、node-2 15 条 `node_not_match` SENDACK。
- 来源：`pkg/slot/proxy/send_permission_rpc.go` 权限读闸门（执行 16、等待 16、最长等待 100ms）拒绝，
  经 `internal/infra/cluster/channel_metadata.go:600` 映射为 `ErrRouteNotReady`，客户端看到 `ReasonNodeNotMatch`。
- 写停顿指标未进入失败快照：harness 的诊断指标白名单缺少 `wukongim_storage_pebble_write_stall*`，
  本提交已补上。因此本轮无法判断停顿是否与权限闸门繁忙同时发生。

结论：本轮不是持续验证通过，也不能证明或排除 memtable 停写。需用补齐白名单的 harness 重跑。

## r2（harness 补齐写停顿白名单，`c2f5bf604`）

- 失败于 1273.3s，`SENDACK wkrc-permission-soak-004872204 reason=ReasonNodeNotMatch`。
- 第 1–18 分钟每分钟快照 pending 均为 0；无 EOF、无断连。
- 54 条 `send_failed`，错误全部为 `route not ready: permission read admission busy`；
  `send_permission_admission_busy` = 7，`sendack_rate_limited` = 0。
- 三个节点 × 5 个 store 的 `wukongim_storage_pebble_write_stall*` 全部为 0：
  整轮没有发生 Pebble 写停顿。`max_message_memtable_bytes` 仍达 128 MiB，
  但未触发停写。

结论：这两轮的失败不是 memtable 写停顿，而是权限读准入繁忙被映射为
`ReasonNodeNotMatch`。上一轮 30 分钟 SENDACK 超时的根因仍未确认，
不能据此归因于写停顿。后续改动：新增 `ReasonSystemBusy`（`4c86f7e9a`）。
