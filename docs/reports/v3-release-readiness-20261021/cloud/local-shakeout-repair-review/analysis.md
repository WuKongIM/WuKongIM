# ed705 本地 shakeout 首失败独立分析（修复前）

判定：已找到与现场错误相容、无需产品延迟即可稳定触发的 harness 配置预算缺陷。原固定 local profile 的 2,500 在线用户在 bootstrap 产生的强制初始单聊活动，超过 100 SEND/s 在五分钟 eligibility 窗口内可服务的预算。现场仍归类 `harness_invalid`；本分析不把该失败认定为产品丢消息或容量失败，也不宣称已捕获原 sequence 315 的精确分支栈。

## 证据与边界

产品基线为 `ed705c0fbffdea485bbb65608d6e340bb049d97e`。原运行 measured start 为 2026-10-21T06:23:53.630033Z，最后完整 cut 为 06:29:03.664644666Z；worker 2 的 sequence 315 随后在 ApplyGrant 返回 `offered_load_under_delivery`，wrapper exit 8。最后 cut 31,005 SEND / 31,000 ACK（5 条在途），first failure、retry、terminal、loss、duplicate、corruption、sequence regression 均为 0。最后 120 秒没有新增超过 400 ms 的 hot SENDACK 样本。worker 2 为 833/833 online。现场没有完整的失败瞬间 CPU/IO/pprof，且最后 cut 早于首失败，不能据此证明所有产品行为健康。

独立审查读取 rc_cloud_plan 先于实现保存的真实工厂诊断，不重复执行负载。该诊断加载原 local YAML、真实 worker factory / Bootstrap / CoordinatorGrantPlan / ApplyGrant，仅将传输、full sync 与时间替换为立即成功和 10 ms 虚拟时钟。v1 三次重复的自然 TTL 与一小时 TTL 两臂全部失败；实际日志为 virtual **317–318 秒**、sequence **318–319**，并非每次恰好 318 秒。v2 两个 100-rate 臂在 318 秒失败；只把一小时 TTL 臂的 global rate 改成 200，运行到 340 秒通过且 300 秒时活动队列为 0。该对照仅证明 grant/活动机制，不包含真实 RECV、网络、磁盘或性能验收。

## 调用路径与假设排除

1. `worker_server.go:1563` → `Engine.ApplyGrant` (`engine.go:1233`) → `admitGrantedTraffic` → `routePersonGrantWithAdmission` (`engine.go:3956`) → `expireActivity` (`engine.go:4146`)。非首个 grant 只做 admission；只有首个 grant 还同步 Advance。因此常规 dispatch 延迟分支（`processWork`）不是这个非首 grant 返回错误的直接路径。
2. `finishBootstrap` / `refreshBootstrapActivityEligibility` (`engine.go:2130/2148`) 已把 bootstrap 活动 due 重基到 first-grant barrier + 原激活间距，并给出 due 后完整五分钟。既有 `TestEngineFinishBootstrapPreservesRelationshipActivationSpacing` (`engine_test.go:5509`) 约束这一点。将活动 deadline 再次重置或在排队时推后，会隐藏义务，不能作为修复。
3. v2 在 barrier 前有 16,746 活动。首 grant 后仍有 16,715（异步 settle 下不硬编码成固定接纳 30），remaining due 为 0..41.850406029 秒，deadline 为 300..341.850406029 秒。发送者及其 login generation 在失败时仍 eligible，`ActivityUnderDelivered` 从 0 增为 1，activity 减 1，canary unavailable age 为 0。这把诊断失败定位到强制活动到期 admission 分支。
4. 自然 TTL 延长到一小时后仍失败，排除了 TTL 到期作为该机制的必要条件；未出现不可用 canary，排除该诊断的 canary 连续五分钟不可用分支；即时 ACK 消除了真实产品反压作为触发此机制的必要条件。现场是否有同时发生的其他瓶颈仍未被证明。

## 预算与契约

worker 2 的平均单聊预算约 30/s，16,746 / 30 = 558.2 秒，已经超过最晚 341.85 秒 deadline，尚未计任何后续活动。更宽松地将该 worker 每秒全部 34 个 grant 都当成单聊、允许到第 342 秒共 343 个桶，最多也只有 11,662，仍小于 16,746。这不依赖产品速度或 90/10 分配细节，足以证明本次冻结 bootstrap 队列在原预算下无法全部按时完成。

设计规定每条新关系有 2–8 条初始消息并保留完整义务；bootstrap barrier 开始 measured grants，不包含免费预排空 SEND。活动 origin 现有实现与该契约相容。问题是已提交 local YAML 的 2,500 online 与 100 SEND/s 不匹配，且静态验证没有识别这种组合。

不能把 `ceil(users/workers) × 5 × max_messages > floor(rate/workers) × person% × window` 当作数学不可行证明：左边是义务上界，右边是服务下界。rate 200 对照已经是该规则会误拒绝可行配置的反例。将其放进通用 Config.Validate 会引入新的保守支持范围，超出这次修复。精确的通用可行性检查需要 seed、worker 分区、实际关系/消息数量、due/deadline 及服务曲线；不应为此次局部修复新增模拟抽象。

## 已同意的最窄修复与审查要求

仅将固定 local YAML 和 native helper 默认改为 500 SEND/s，max_global_burst 保持两秒速率即 1,000；保留 2,500 在线、全部初始义务、五分钟 eligibility、400 ms latency 与正式 profile。同步使用文档及 CHANGELOG，不改变 Engine、通用 Config.Validate 或云控制。该选择增加 offered load，不降低验收标准；它是否满足真实主机性能仍需新的已授权运行证明。

真实 factory + committed YAML + coordinator grants 的回归至少越过已知最晚初始 deadline（建议虚拟 360 秒），使用自然 TTL，断言原 bootstrap 强制活动全部结束且无 UnderDelivery，并保留原 100-rate 负例机制。不可只测一个预算公式或 helper。还需固定脚本 dry-run/help 默认可见行为，以及实际渲染 default/override 配置：helper 当前 sed 精确匹配旧 `100/200` 文本，YAML 改动后必须同步，否则 `--send-rate` 将静默失效。现有 fake-process integration seam 可验证渲染 500/1000 及 override 400/800；单独 dry-run 不执行 sed，不足以验证渲染。

规则选择的 named checks 为 `go-unit`、`go-vet`、`go-format`、`go-mod-tidy`，修改 `.sh` 同时触发 `scripts-integration`。不改受保护 selector/policy；聚焦回归不能代替之后必须的完整检查。本报告是修复前独立诊断，后续实现 review 另存文件，不覆盖原失败。
