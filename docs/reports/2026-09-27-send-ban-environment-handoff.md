# Send ban 剩余性能验收的环境交接

后续更新：已找到并实际验证本机 Docker Linux/arm64 路径，见
[Docker 验证](2026-09-27-send-ban-docker.md)。下文“没有可用环境”描述的是
交接当时状态；目前构建和小规模三节点验证已通过，原定持续场景在
277.717 秒 EOF 失败，Linux 三组对照已完成但没有完整 fresh 配对，
不再以缺少外部服务器作为阻塞条件。性能验收结论仍待完整结果。

**功能实现保留，完整验收仍未通过。** 本文固定新环境的复现输入和判定
规则，不代表已在目标环境执行。当前没有可用的专用环境规格与访问方式；
本机重复压力失败及无效 L0 候选已完整归档，不再重复同条件碰运气运行。

## 需要补齐的信息

需要可重复、资源可核对的测试环境：访问方式、操作系统/CPU/内存、数据盘
及文件系统、可用空间，以及是否与其他负载共享。建议使用已有专用 Linux
机器；Linux 不是本功能的新增语义要求，其他足够隔离且有稳定基线的环境
也可评估。尚未证明 Mac 磁盘饱和，不能把环境假设当作根因。未授权采购，
不购买云资源，也不扫描个人凭据或自行选择生产集群。

## 固定源码与构建身份

| 对照组 | 输入 |
| --- | --- |
| 旧基线 | `64f73d99b3b0cb8960d40825f76053f5a0000dbd` 的完整生产输入 |
| 节点聚合 + fresh barrier | `25892c51c`；生产代码与既有九轮对照的 `a1e06e54c` 相同 |
| 逐 Slot + 同等 fresh barrier | 同一节点聚合源码，仅应用下述单文件信封分组补丁 |
| 共用 harness | 在 `25892c51c` 构建的 `test/e2e/message/medium_recipient_hotpath` |

[参考补丁](assets/send-ban-environment-handoff-20260927/reference.patch.json)
以 JSON 的 `patch` 字段保存，只有 `pkg/slot/proxy/send_permission_rpc.go`
变化；已经验证能应用到当前源码，未修改当前工作副本。另保留
[既有参考测试补丁](assets/send-ban-environment-handoff-20260927/reference-validation.patch.json)，
只将两 Slot 的预期信封数改为两个，仍断言同样两个 fresh barrier。两份
补丁仅用于参考工作副本；目标执行前先运行对应的参考契约。参考程序必须放在
独立工作副本。不能把原旧基线的缓存读取当作同等 fresh barrier 参考，也
不能将已撤回的 Darwin L0 候选带入。

统一使用 Go 1.25.11，在目标架构原生构建，保存所有源码输入摘要、参考
补丁、实际 `go version -m` 输出与二进制 SHA-256。历史旧基线报告中的
`vcs.modified=true` 限制保留；新构建按实际状态记录，不复制旧二进制摘要。
在所有构建和小规模正向验证完成后，再开始隔离的正式测量。

在节点聚合工作副本中，以下是 R2 的构建/执行命令模板。先清理与本场景
无关的 `WK_*` 环境覆盖；仍须核对每个运行节点公开渲染的有效配置，不以
命令行意图代替验证。输出目录必须是新目录，不能覆盖既有失败产物。

```bash
send_ban_run_dir=$(mktemp -d /tmp/wukongim-send-ban-acceptance.XXXXXX)
GOTOOLCHAIN=go1.25.11 GOWORK=off go build -o "$send_ban_run_dir/wukongim" ./cmd/wukongim
GOTOOLCHAIN=go1.25.11 GOWORK=off go test -c -tags=e2e -o "$send_ban_run_dir/permission-soak.test" ./test/e2e/message/medium_recipient_hotpath
GOTOOLCHAIN=go1.25.11 go version -m "$send_ban_run_dir/wukongim" > "$send_ban_run_dir/product-buildinfo.txt"
WK_E2E_BINARY="$send_ban_run_dir/wukongim" \
WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK=1 \
WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION=30m \
WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS=5000 \
WK_E2E_MEDIUM_RECIPIENT_QPS=4500 \
WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS=32 \
"$send_ban_run_dir/permission-soak.test" \
-test.run='^TestCloudMediumPermissionSoak$' -test.count=1 -test.timeout=40m -test.v=true \
> "$send_ban_run_dir/e2e.log" 2>&1
```

目标机执行前还要记录机器规格、空闲内存、磁盘空间和共用负载，并设置
按该环境核定的资源保护；达到保护边界属于未完成，不能写成通过。运行
失败或超时后先确认精确进程树是否已退出，保留日志，不盲目重启。历史
诊断 runner 的 `/tmp` 二进制路径及 Darwin `iostat` 参数不能直接照搬。
上面的命令只经过语法审查，尚未在目标机运行。

## 拓扑、对照与判定

R2 保持 3 个真实进程、256 Hash Slots、10 个物理 Slots、3 副本，
5000 频道、4500 SEND/s、30 分钟目标，gateway 128 workers、队列
131072、batch32、append workers128。短时对照只能验证环境及采集能工作。

R5/R6 沿用已登记的 5000 频道、1200 SEND/s、60秒诊断窗口，三个组各
运行三次，顺序为 baseline/slot/node/node/baseline/slot/slot/node/baseline。
同一 harness、节点资源和配置；不同时运行构建、其他压力、测试或额外
profile。三组全程保存成功及失败，不用重试替换原失败。若改变机器、
负载或采集方式，整组重新编号并保留原组，不能混拼成三次有效配对。

报告每组吞吐、SEND P50/P95/P99、权限查询 RPC、Raft 信封与字节、CPU、
分配和 GC；缺失指标保持不可用。5% 比较使用完整成功窗口中的逐 Slot
fresh 参考与节点聚合，不能拿失败窗口较小的累计值推导收益，不能删掉
超限配对。旧基线也必须如实报告。若仍无稳定基线，则性能未验证，不能
发布；不恢复陈旧读、降低同步、缩小原压力档位或降低门槛来通过。

完整验收以[矩阵](2026-09-24-send-ban-acceptance-audit.md)和设计第11/12节
为准。本说明不替代功能/故障证据，也不把三进程同机实验宣称为三台物理
机器的容量结论。
