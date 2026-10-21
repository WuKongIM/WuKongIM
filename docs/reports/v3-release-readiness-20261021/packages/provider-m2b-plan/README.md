# M2b 实施前准备：先全对象/配置，完整 refresh 仍阻塞

**仅只读方案，未实施、未测试、未运行容器。** 基于已审 #78 `1fb1c6ea077fd260c373434c1a3c368a7952defd` / tree `cd49a22102235d60bda1a703a5b1145ab112c40e`。必须等 root 给出实际 merge SHA、核合并 tree 后，在新 `.worktrees`/`codex/` 分支实施；#78 当前工作树不改。M2a 的通过范围不因新 refresh 发现而重写。

## 可做与不能承诺的结果

可先交付 **TEST ONLY 全对象 SDK + 配置 + 完整刷新拒绝证据**：原 native preview/stable A/B 全量 materialization，各 153 objects / 178 routes，经官方 SDK 实例 HTTP adapter 执行每个对象的 create-only PUT 和完整 EOF GET，再验证全部 17 配置规则及 A→B ConfigId 复用。完整链的收据只能是 `incomplete`，阶段明确 `refresh_unsupported`；测试驱动本身可在“准确观测到预期拒绝、无漏项/错误副作用”时通过。不能命名为 M2b delivery complete、真实 CDN、生产来源或公共客户端成功。

本次静态读取固定 JSON：A/B 路由 URI 集相同；各 26 entrypoint（含 `/`）、32 alias、120 snapshot；77 no-store、101 immutable。各对象累计 433040 bytes，最大 10442；A/B 共 154 distinct keys，其中 152 共享，只有各代 mirrorlist key 不同。对象/路由完整集合和原 client 请求身份在 `observed-gaps.json`，没有执行 compiler 或 verifier 来产生这些数字。

## 三个独立 refresh 缺口

1. **覆盖集合与根路径。** client_requests 仅携带两份客户端验证参数，不是已定义的 refresh URL 清单。建议 TEST ONLY 采用保守的全 178 个 public URI 作完整性检查；26 浮动入口也必须含根。现 `Provider.refresh` 明确拒绝 `/` 和目录路径（provider:409），原 SDK 负例锁定此行为。不能省为 177 文件、25 入口或用 `/index.html` 代替 `/`；相同 object 不等于相同 CDN cache key。最终生产 refresh 集合仍需合同确认。
2. **实际 API 基数。** root 新核中文官方 [RefreshObjectCaches](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-refreshobjectcaches)：同域同秒 URL 可按最多 2000 条合成一个任务 ID。现 provider:414 要求 `len(ids)==len(urls)`，423–425 又要求任务单个 ObjectPath 的集合等于 URL 集。这会 fail-closed 拒绝 API 允许的聚合响应。官方 [DescribeRefreshTaskById](https://help.aliyun.com/zh/cdn/developer-reference/api-cdn-2018-05-10-describerefreshtaskbyid) 仅描述字符串 ObjectPath/TaskId 和单 URL 示例，尚不足以确定聚合任务的覆盖编码。不得猜换行、多行重复 ID 或把一个 Complete 当所有 URL 完成。
3. **测试 Wire 的独立 bug。** 现 Wire 每次从 501 生成 task IDs，并返回累积 tasks。它不能正确证明连续 refresh 批次的 ID 归属；即便把 Wire 改成单调唯一 IDs，仍不解决真实 API 基数问题。第一窄片保留 full-refresh 拒绝，不为了完整正向伪造一对一 API。后续要测试非根多批原语时，再先红修其 tests-only 状态模型，明确仅是该响应分支。

[官方刷新说明](https://help.aliyun.com/zh/cdn/user-guide/refresh-and-prefetch-resources) 的根目录 Directory 会影响子目录；它不是精确 File 根 URL 的自动替代。根 File 行为和聚合覆盖合同未知时，完整刷新必须 unsupported；不增加权限或创造批准系统。

## 最小剩余范围：建议六文件

旧七文件方案中的 compiler 已由 M2a 完成，本片无需改任何 production script；M2a fixture/34 旧 SDK 方法保持。

1. 新 `tests/provider_sdk/native_object_delivery_sdk_drill.py`：显式 TEST ONLY `prepare` / `sdk` 两阶段入口，原 native helpers + 官方 SDK，保留有限 transcript；无 credential discovery、真实 SDK endpoint/network 模式或生产 CLI。
2. `tests/provider_sdk/test_aliyun_object_storage_provider.py`：保留原 34 方法，先写完整新 driver / HTTP 故障回归。复用 Wire；新增故障按 exact operation index 注入，可用 driver 的窄 TEST ONLY adapter 子类，禁止 mock provider 方法/verify_candidate/验签。此片不必为被拒绝的刷新添加伪完整成功 Wire。
3. `tests/test_object_storage_delivery_inputs.py`：原 10 方法保留；先加两阶段 CI 接线、命令/资源/receipt/零 skip/cleanup 的默认静态合同，不能以字符串合同代替 SDK/native 执行。
4. `.github/workflows/native-package-repository-drill.yml`：**在现 M1 步骤及其原收据检查之后添加步骤**，直接复用同 job 的 `$RUNNER_TEMP/native-delivery-fixture/fixtures`，原所有步骤和期限字节保持。旧计划另起 SDK workflow job 无法直接得到 public archives：现 artifact 仅上传 result.json 与 fixture-export.json（232–236），M2a JSON 也不含对象 payload。复用当前工作目录可避免新增 archive 上送/跨 workflow 凭据/重复 signer。现 SDK workflow 34/input10 步骤及 hash lock 完全不改。
5. `docs/object-storage-delivery.md`：两资源身份、native→SDK 绑定、expected unsupported、尚未证明范围。
6. `CHANGELOG.md`：明确全量 TEST ONLY SDK 覆盖，完整 refresh/activation 仍拒绝。

这是下一片建议范围，不是已开始的编辑授权。若同 job 时间预算不能容纳新增步骤，先记录实际阶段时间再评估独立 job 的公开 artifact 传递；不静默增加现 60m job 上限或重跑 M1 以掩盖 CI 缺口。无需改 M1 exporter/29-case driver、31/49、producer/control、key/channel、planner/materializer、provider/fresh validator、policy、lock 或 Pages。

## 两阶段具体接线

**Native prepare**：消费已成功 M1 的公开 export；核 exact source/tree、export 测试标记、secret_tree_removed、120 public input hashes及真实 archive/receipt。新驱动不能调用 exporter 或 signer。使用原 Preview、seal_generations 的显式 real preview、unchanged planner 与 prepare CLI重建 A/B；复验实际 native signatures/metadata，逐对象 source audit/member/hash/size/cache 与 plan 相等，保留 exact inputs/plan/delivery bytes。原 fixed signed snapshot bytes 不改，新的临时路径/loopback resolution digest如实绑定，不假称等于旧读回 JSON。

持久化仅同 job-owned scratch，用相同绝对挂载路径使 native 生成的 input roots 在 SDK 阶段仍可读；不改写路径后冒称原 digest。工作目录限定 regular files、无 symlink/hardlink逃逸，固定小型 manifest；完成或失败后清理全部 stage payload，公开 artifact 只留有界 transcript/manifest。临时 signing keys 已由原 M1 删除，不读取或重新生成。

**SDK phase**：延迟导入 SDK，在原 lock venv运行。复用 Provider 与现 Wire 的 instance adapter，socket connect guard 必须覆盖整个 SDK 阶段；native helper 所需 loopback只属于前一 network-none 容器，不把“native可loopback”混同“SDK允许network”。每代完整遍历153对象，逐一 put_create_only（它自带 resource/never-versioned检查和 full GET），记录所有306逻辑操作。A后B应有152 exact-existing conflicts/readback与1新 mirrorlist；不省略重复对象验证。然后用178原rows编译17rules/apply/readback；A→B source IDs复用，恰两target更新只是当前fixture事实。

**双绑定和 fence**：分别记录 disabled/null resource_intent canonical digest与显式 fake Provider resource digest；不能强行使二者相等或将 null 改真。另绑定 executor source/tree、input/plan/preparation/route/object manifests以及独立 TEST ONLY current-control observation。历史 preview control、stable seal/candidate control、product source、executor/current 分列。原 delivery.activation_control_sha 为 null的历史证据不填值；可在新prepare用显式声明的 TEST ONLY anchor形成新的delivery digest。每个下一次写前重读已验证输入/物件/资源/current observer，改动即停；这些是fixture drift断言，不是新的生产auth正例。

完整 URL set 预检作为独立用例在任何 refresh请求前拒绝 `/`；完整流在 all-objects/config后终止为 refresh_unsupported，不删该行继续刷新。为避免消费者误读，收据分列 objects_verified、configuration_readback_verified、refresh_coverage_complete=false、fresh_production_auth_verified=false、activation_authorized=false及所有原false字段。真实 verify_delivery_inputs(TEST ONLY) 和 activate() 均实际调用并断言拒绝；不能 catch 后标生产成功。

## 资源、验证与产物

Native新增读回复用已 pin image/trusted tools，`--init`、network none、read-only source/public input、nonroot65532、cap drop ALL、no-new-privileges、1CPU/512MiB/128pids/512MiBtmp、15m外层＋10s kill。SDK lock安装单独240s（公共依赖准备可网络），SDK阶段禁sockets、总300s、有限20-case集/每代153/178/17计数，阶段工作文件最多64MiB，JSON/log每文件最多8MiB；这些新界限是提案，首次实测不足则失败留证，不擅增。现有 native job 60m上限不变；SDK host不伪称容器cgroup隔离，固定数据/输出上限和deadline必须实际生效。

先写 failure-matrix 全部回归和接线测试，保留 unchanged source真实RED，再写driver/Workflow。新case数与所有旧34/10/29/31/49单列。SDK及native等效命令首次实跑、focused/default/contracts与CI首次结果均保留；缺依赖、zero cases、skip、未终态/cleanup失败均失败。公开收据绑定 exact source/lock/image/tools、每个object及观测PUT/GET、全部config before/after、URL完整集合/拒绝原因、部分副作用和cleanup。不保存 Authorization/token/secret/raw signed请求；fake常量也明确 TEST ONLY。

完整 refresh 若今后获确定 API 覆盖合同，是独立后续改动，须新 tests-first matrix和显式review；本计划不会用无限refresh重试、Directory全域失效或虚假的任务形状消除阻塞。
