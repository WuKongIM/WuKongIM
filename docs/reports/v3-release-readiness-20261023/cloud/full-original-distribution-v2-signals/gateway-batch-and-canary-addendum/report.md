# Gateway 实际批量与 canary 的有限离线补充

本补充只读取原始 v2 终态已有六份本地指标、固定三份服务日志、安全身份派生摘要，以及旧云终态八个 metrics 分区。源码冻结为 `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06`。未启动服务、负载、profile、SSH 或云查询；输入大小和 SHA 见 `summary.json`。正在另行执行的 batch128 对照不属于本补充输入。

## 实际批量

六个 node/cut 的 `wukongim_gateway_async_send_batch_records` 均满足 sum = count = le=1 bucket。终态三节点分别 4185、4202、4202，共 **12589 个批/记录，实际平均批量均为 1**，与物理尝试总量一致。

独立 permission cohort 已有合并：终态每 cohort 约 5.95–6.04 个 accepted request、20.32–20.64 个 facts。因此 gateway cap=1 不表示权限层没有批量。`gateway.session_batch_max_records=128` 只规定上限；10k 用户的稀疏 per-session 到达仍可能产生实际单记录批。配置差异本身不足以提高该因果假说的置信度。

原云有限归档只有 presence、delivery、up、runtime、channel_worker、process、counters、go 八个分区，无 gateway 分区。八个 selector 均不覆盖 batch cardinality 或 permission cohort，实际相关序列为零。**云实际批量与 cohort 形态为 missing，不能填为 1、128 或 0。**

## Canary 能确认的边界

本地配置 1 个 100000 成员固定群、60 秒 cadence。终态 primary=12000、canary=1、sent=ACK=12001，聚合计数支持该 canary 获得成功 SENDACK；没有逐 canary 包级时间轨迹。

源码构造/重置以 engine generation 起点加 cadence 计算 nextCanary，未明确以最终报告 measured window 起点计时；因此 measured 仅 13.330 秒不能用来否认已发生的 canary。**确切 send、ACK、recipient delivery 时间、canary 冷 presence 重建耗时和其占用队列量均未保留。**

安全解码证明 3 个 expired 样本全部为 ordinary_group。`internal.app.delivery.plan_incomplete` 日志字段是 `Failure.RecipientUID`，不是 sender；按源码固定 roster 和本次 namespace 在内存中严格分类，256 个失败 recipient 全属其他固定 roster，0 属 very-large 固定 roster，unknown namespace 为 0（presence=254，owner_push=2）。公开摘要不包含 UID、namespace、channel、client_msg_no 或日志正文。

这削弱“记录到的失败直接发生于 canary recipient”假说；**不能排除 canary fanout/冷路由间接挤压普通群队列**。roster 分类不能替代 per-message 因果关联，也不能把 32 个 permission cohort 前置拒绝、256 个 delivery timeout、3 个已持久化 expired 样本自动连接起来。

## 已运行单变量对照的解释约束

应首先检验 batch128 对照中的实际 histogram 与 cohort requests/facts/cohorts。若实际批量仍全为 1，cap 修改未产生所需的批量处理差异；即使症状变化，也不能仅据此归因 batch shape。若实际批量增加，再比较权限 cohort_busy、permission/barrier latency、recipient 队列及普通群样本，结论限于此次共享 Mac 条件。若 harness observation/finalize 无效，保留无效终态，不报告验收通过。该约束不要求新增负载。

本补充独立冻结，不修改主 signals 目录既有 7 项 payload 和 manifest。固定输入均再次核对大小与 SHA；实际云 GOMAXPROCS 仍未知。完整首败保存在私有原始目录，有限公开只使用本目录 allowlist。
