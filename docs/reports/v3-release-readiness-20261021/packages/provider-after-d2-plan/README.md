# D2 后最小下一切片（只读准备，未实施）

基线为已合并 `be32eb375e3c77dc5b3d1070fd798c281ae2d3bc`，tree `e319ee72e8d30f012fd5e6206d131e2b75c47a09`。建议 **D3：真实 native v2 候选 → 原物化 → 真实 SDK 的离线 HTTP 合同集成**，先补字节与阶段绑定，仍不提供生产 writer/CLI。此为约三分钟收束的最小方案；没有测试、负载、provider、凭据、资源或源码操作。未批准的 ¥100/7 天提案仍只是提案。

## 剩余闭环

| 边界 | D2 后事实 | 尚缺 / 本切片处理 |
|---|---|---|
| 完整 v2/native | 现有 native drill 实际签名验真；现有 delivery composite 测试替换了 native verifier（`tests/test_object_storage_delivery.py:198`）；二者尚未与 SDK 同链 | D3 用真实 Linux 原生候选/签名/闭包，无 verify_candidate mock，验证 current preview + current/retained stable 和跨代 alias |
| fresh GitHub/control | `verify_delivery_inputs:120–180` 有原 plan、干净执行树、当前 main、历史 candidate/seal/source/audit 独立验证；测试输入不能进入生产认证 | 保持拒绝。D3 记录 TEST ONLY 的身份链，不把 fake API 的成功写成真实 GitHub provenance；完整生产形态 v2 fresh 正向 fixture 仍需独立补审 |
| plan/preparation/resource | prepare 复算 inputs/plan 并 materialize；SDK resource 是另一形状；没有把双方绑在一起的 writer | D3 的测试驱动逐字段绑定原 inputs/plan、对象源 audit/member、SDK resource digest；不修改原 disabled/null resource intent 以开启云 |
| upload/readback | SDK 单对象 create-only、未知结果 GET、完整 EOF/size/SHA/cache 已有覆盖 | D3 按完整候选对象清单调用真实 SDK + 注入 HTTP transport，并证明没有漏对象/alias/额外 PUT；绝不调用 fake provider 方法替代 SDK |
| config/refresh | 有真实 SDK 请求/响应与 partial receipt、ConfigId/readback/ID 查询；rewrite 仅静态候选 | D3 对同一物化 route table 验证离线批次与反馈、失败顺序和刷新 URL 集合。超过 50 条等仍明确拒绝；这不等于 live route equivalence |
| activation | `Provider.activate:386–388` 无条件拒绝；stable control 仍 disabled；没有生产 authority | D3 必须保留拒绝，不添加 enable flag、任意 JSON approval 或可执行生产 CLI |
| public client | 原四客户端已真实 TEST ONLY 验收，但不经过真实云 SDK 路由；旧 false 字段必须保留 | D3 至多绑定原 `client_requests` 的 exact version/source/inputs/plan；不以 callback 返回通过宣称公网验收。真实 origin/TLS/cache + canonical 四客户端/installed CLI 属后续授权的闭环 |

## D3 的有限实现形态

优先仅增加一个 TEST ONLY 集成驱动和专用 CI 接线，不先造可生产写入的编排器。复用 `native_stable_candidate_drill.export_client_generations` 的公开输出与已有签名镜像；短期测试私钥只在原隔离签名阶段，销毁后才导出公开候选。复用原 planner/materializer，不拷贝签名、归档或客户端引擎。SDK 仍在锁定的宿主 venv 运行，HTTP adapter 只在测试实例注入，禁止真实 sockets、全局 SDK monkeypatch 和凭据发现。

新驱动把完整候选结果接入 prepare，然后逐对象调用 Provider，再使用同一 route table 的配置/刷新原语；一个测试专用 operation transcript 将 preparation、resource、SDK 请求和所有观察串起来。成功状态只能是 `test_only_integration_complete`，所有原始 false 字段原样保存；activation 仍以拒绝作为最终预期。此 transcript 是证据，不是恢复 journal/发布 authority。

**不能假装已闭合的接缝：** fresh verifier 明确拒绝 TEST ONLY、固定测试 source SHA，并调用实际 attestation/source helpers。当前 native fixture 的控制/源为 TEST ONLY，直接输入它必须失败。若要测试完整生产形态 fresh-v2 正向，应先建立所有真实 helper 均可验证的离线 fixture（包括精确 Git objects、七个 source assets、attestation/metadata 与 historical seal），HTTP 边界可替身，验证函数不可 stub；其可构造性本次未核完。不得将两个半流程拼接后称 production-authenticated。该工作可独立成为随后 D4，不能通过放宽 D3 的输入拒绝来完成。

预计 **4 个文件**：新 `tests/native_object_delivery_sdk_drill.py`；现有 `.github/workflows/object-storage-provider-contract.yml` 添加独立有界 Linux integration job 与触发路径；`docs/object-storage-delivery.md`；`CHANGELOG.md`。若 CI setup 无法在此四文件中清楚复用既有 fixture，需要第 5 个窄 TEST ONLY helper，先提出具体复用合同理由；不默认扩大。现有 SDK22/input10 job 和 native49 job都保留，不用 D3 代替。新集成 job 需要真实 dpkg/rpm/GPG/签名镜像并安装原锁，缺工具/依赖、零用例、skip 或超时均失败。

签名公开 fixture export 是否能直接生成 planner 所需完整 preview+stable v2、保留完整 source identity，是实施前第一项可行性检查。现有 native 客户端 `seal_generations` 还使用结构化 preview fixture（`:620`），因此不能直接照搬并声称完整 preview 原生正向。若此处需要非窄 fixture 重构，先收束为失败证据并重新评审范围。

## 先失败与验收

完整矩阵见 `failure-matrix.md`。先写全矩阵/测试，保留未实现接线的真实首红、源码 hash 和命令终态，再实施。优先单一 Linux process-level 驱动；不在实现后补 unit、倒写首红。验证产物包括 exact checkout/tree/lock/image/tool versions、case inventory、候选/输入/plan/preparation/resource digests、对象数与逐对象来源/读回、完整配置观察及尝试批次、刷新 task/URL 绑定、拒绝边界、清理和零 sockets 证明。只公开 bounded JSON/transcript 与 hash，不上传私钥、凭据、签名 URL 或完整包树。红/绿各保留固定命令、期限、真实退出及所有子进程终态。

## 外部步骤与保护面

D3 可在无云环境实现；下载公共依赖/镜像与实际云操作分开记录。真实 principal/resource/CDN 观察需要对应只读访问授权；获取临时权限、创建 bucket/CDN、IAM/回源角色、DNS/TLS/证书变更、真实 PUT/config/refresh 和流量费都需各自外部授权。既有 ECS 预算不可挪用，当前不报价、不申请资源。

后续生产顺序：确立受审生产 authority 与实际资源/权限 → fresh v2/source/audit/control 和 resource 双 fence → 完整 create-only PUT/读回 → 再 fence → 有界配置变更/读回 → exact refresh → origin/TLS/cache/编码查询与真实 PCRE 语义验收 → unchanged canonical 四客户端 exact target/installed CLI。任一部分失败须保留真实副作用，不能报告回滚或原子 POP 切换。preview 同字节迁移与 stable 启用顺序、公开 stable 后回退前代 composite 的合同保留。

不动 12 producer、stable channel/key/history authority、原 simulate、生产 validator、Pages/native publisher 或 Issue/Review Agent 保护代码。预计修改的 SDK workflow 仅 `contents: read`、无 dispatch/environment/secrets/OIDC；可调用它的权限不等于生产授权。是否被远端规则列为保护路径本次未重新查询，实施前按精确 CODEOWNERS/保护合同复核，不假定可直接写保护文件。packages 未发现 tracked AGENTS/FLOW，沿用调用方根 AGENTS 与 Workflow README（摘要已冻结）。D2 历史 tests-after-code P2 和首轮 native 未唯一归因继续保留；不新增发布门槛来掩盖这些事实。
