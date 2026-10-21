# M2 最小方案：有限精确路由 → 官方 SDK HTTP fixture（未实施）

建议先完成 **M2a 精确 route 读回与有限编译**，可证明后再接 **M2b 完整对象/配置/刷新 SDK fixture**。这是同一个七文件窄切片的先后里程碑，不是生产 publisher。当前没有改源码、执行测试/容器/负载、调用 provider 或触发 CI；没有费用、资源、凭据或生产 activation 授权。M1 冻结不变，本方案不向 M1 追加验收要求。

## 已知事实与尚缺的证据

M1 的无插桩结果给出 A/B 各 153 objects、178 routes；当前 `compile_rewrites` 是每 URI 一条 literal，必定超出 50 条。官方规定单域最多 50 条，按 sequence/order 执行，`break` 停止后续改写，PCRE 可捕获替换；URL rewrite 不改变 cache key。1024 字节是当前代码的**本地能力上界**，不是已证实的阿里云参数上限。[官方 rewrite 合同](https://help.aliyun.com/zh/cdn/user-guide/rewrite-urls-in-back-to-origin-requests/)，[官方字段合同](https://help.aliyun.com/zh/cdn/developer-reference/parameters-for-configuring-features-for-domain-names)。本次只读重新打开上述官方页面；其事实也由独立 `provider-next-route-gap/observation.json` 绑定。

代码表明路径主要属于三种固定关系：原 snapshot URI 对应同名 key；immutable alias 对应 `public` + URI；浮动入口对应当前 snapshot 的 `/site/` + URI。根 `/` 和生成 mirrorlist 是单独 literal。这给出一个可证明的有限表示候选，**本次没有得到精确压缩条数，也不声称 178 路由已成功编译**。M1 只保留了 composite plan/delivery 的 digest 和计数，临时完整 JSON 已删除；公开 archive/receipt/control/source 齐备，但 stable resolution 和完整 inputs 需原 helper 重建。不能从计数或自己模仿 planner 拼出受验 route table。

因此实施前第一条证据应是一次有界、尚未接 SDK 的 Linux 读回：使用封存公开 fixture，在现有 pinned image + `--init` 中复用 `seal_generations(..., preview=真实公开preview)`、unchanged v2 planner/materializer，保留 exact inputs/plan/delivery 的小型 JSON 和 hashes，完整大包树仅留受控 scratch。复验原生签名/metadata，不 stub verifier，不生成或读取生产私钥。记录 descriptor 到新 loopback resolution/plan 的关联，不把它冒称与已删除 JSON 必然同 digest。此后用实际 178 行逐项决定可表达性。若仍超过 50 或某行无法安全表达，结束为明确 unsupported；不增加预算、cap 或改变布局。

## 最小编译策略

保持去重、冲突、safe-path 和完整 object facts 检查；独立 object/source/cache 清单不压缩。unique routes ≤50 时继续当前 literal 输出和顺序，保证原成功输入字节兼容。

仅当超过 50，按已存在的、可逐行证明的前缀替换关系分组。组内每行必须满足 `public_uri=P+s` 且 `'/' + object_key=Q+s`。P/Q 来自现有 snapshot/site 或 public 前缀结构，不接受任意用户 regex。将一组输出为 `^escape(P)(escape(s1)|escape(s2)|...)\z`，目标为 literal Q + `$1`。只允许一个 capture，每个 alternative 是完整 literal suffix；不得使用 `.*`、字符类、可选段、量词、反向引用匹配、flags 或递归 rewrite。`/`、generated mirrorlist 和不能证明关系的行继续单条 literal。快照同名路径也保留有限 literal 清单，不能通过省略 snapshot routes 假装压缩。

按固定排序及完整 UTF-8 expression 字节数确定性分块，每块 ≤1024，规则总数 ≤50，sequence 唯一且为 1…N，固定 `break`。长的重复 snapshot 前缀在 capture 之外；必要时按固定路径分隔符细分前缀以减少重复，不引入任意 trie/优化平台。不追求所有可表达集合的最优压缩；窄算法不能表示时失败。

证明不能只靠若干正例 regex：从编译规则的封闭 grammar 独立枚举其完整有限语言，必须与去重后的原 route URI 集合完全相等；每个捕获替换目标、object key、SHA/size/cache/source identity 与原行逐项相等，无 overlap、丢行、多行、循环或新 namespace。再做实际本地 regex/capture 边界检查，但不得把 Python `\Z` 模拟叫阿里云 PCRE 验收。证明无论规则何序，互斥输入最多匹配一条，并保留稳定 sequence/readback 顺序。

## SDK 接线与限制

新 TEST ONLY 驱动分两个明确阶段：native 阶段在原 pinned 容器生成/读回受验 preparation；SDK 阶段在已锁 venv 使用真实官方 SDK、仅实例 HTTP adapter，禁止实际 sockets。独立绑定 disabled/null resource-intent digest 与显式 fake SDK resource digest；它们本来不同，不能改前者为启用，也不能拿 JSON success 当 fresh authority。transcript 绑定 exact source/input/plan/preparation/route-set/resource、每个对象 audit/member/hash/size/cache。每代完整遍历 153 对象，调用现有 create-only PUT + 完整 EOF GET，不取样；重叠物件保留 exact conflict/readback，未知 PUT 结果只 GET，绝不重复 PUT。每个 PUT 的原 resource/never-versioned 检查不变；当前控制/输入若漂移，不发下一次写；已有 side effects 原样记 incomplete。

配置使用同一全部 route table 的有限规则，通过 SDK 真实序列化和响应解析。保留完整 before body/hash/domain/resource 及 managed ID 双绑定、写前 fresh configuration/resource fence、每批 ≤20、ConfigId 正数/唯一、完整 after readback 和无关配置不变。旧 literal 配置迁移至压缩规则可能需要删除，现有 API 会拒绝；本切片不新增删除许可。完整 A/B 分别从空 managed rewrite fixture 验证；A→B 若需要删除必须保留拒绝，不能称已实现热切换。中途 drift/部分批次/超时应保留实际已尝试批次及未知副作用，不自动回滚、重试或 refresh。

Refresh 使用 preparation 的原 exact current-client 目标所需 URL 集，先证明集合完整，按原每次 ≤50、task 查询 ≤10 约束分批并绑定返回 ID/URL；`/` 和目录形式不在当前 refresh 能力内，若需求集合包含它们须报告 unsupported，不私自丢掉。批次均成功后才能完成 TEST ONLY transcript；其结果仍非公开客户端成功。现有 fresh verifier 对 TEST ONLY 的拒绝和 `activate` 无条件拒绝必须实际运行。不得改原 false fields，不造批准系统或生产 CLI。

**仍待 live：** 真正 CDN PCRE/`$1`/`\z` 支持、URI percent/query/斜杠标准化、无匹配时回源行为、缓存键和传播、配置跨批次短暂状态。尤其“相邻 URI 不匹配任何候选规则”不等于 CDN 已拒绝 HTTP 请求；未匹配请求可能直接回源。实际 origin 私有布局可否暴露未列路径也需 live 验收。M2 的有限语言证明不能替代这些事实；activation 继续 unsupported。

## 精确预计范围与验收

七文件：
1. `scripts/aliyun-object-storage-provider.py`：只窄扩 compile_rewrites；现有 SDK/object/config/refresh 权限模型不变。
2. `tests/provider_sdk/test_aliyun_object_storage_provider.py`：先写有限编译完整矩阵，复用现有 HTTP Wire；保留原 22 case，不删/改旧成功断言。
3. 新 `tests/provider_sdk/native_object_delivery_sdk_drill.py`：仅 TEST ONLY 两阶段 driver，复用原 native export/seal/materializer 和 Wire，不复制生产引擎。native 阶段延迟导入 SDK，避免改 signing image。
4. `tests/test_object_storage_delivery_inputs.py`：现有 10 case 不变；先加新 job/receipt/零 skip/cleanup 的静态合同。
5. `.github/workflows/object-storage-provider-contract.yml`：新增独立有界无密钥 integration job；原 SDK22/input10 步骤不改。实际安装原 hash lock，校验 trusted tools 和原 pinned image，使用 M1 的 init/资源界，host SDK fixture 禁 sockets。零测试/skip/缺依赖/超时失败，always 小型公开收据；不上传整个 delivery 目录。
6. `docs/object-storage-delivery.md`。
7. `CHANGELOG.md`。

不改 M1 29-case driver/native workflow、旧 31/49 链、requirements lock、planner/materializer schema、bucket/object layout、12 producer、签名/生产 validator、channel/keys/history/control、Pages/production publisher、Issue/Review Agent 保护面。若现有 helper 不能在这七文件内安全复用，应先展示具体合同缺口，不擅增生产编排器。新增 workflow 属只读 safety 自动化，实施前重核适用 workflow README/CODEOWNERS，不能将此方案当云授权。

首红矩阵见 `failure-matrix.md`。交付需保留真实未实现首红、完整 case inventory、固定工具/源码/SDK lock hashes、所有命令期限/终态、无 sockets 证明、all-178 equivalence 和 all-153 upload/readback transcript、完整副作用收据与 cleanup。先冻结真实 route 读回再写算法；本轮不执行该读回，不承诺最终 N。原 22/10/29/31/49 均继续验收，新增数单列；历史 D2 tests-after-code P2 不消失。
