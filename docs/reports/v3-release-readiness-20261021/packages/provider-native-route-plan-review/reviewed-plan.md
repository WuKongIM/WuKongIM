# M2a 真实 native route 读回（只读完成，provider 未实现）

基于 PR #77 精确 HEAD `19ce29fb2233b3039e29c503963e27bd1fab972a`、tree `8275ccb92c310f488d911b5aa077a962e1faad5c`，第一次有界读回即通过：**A/B 各 153 objects、178 routes**，exit 0、无 timeout/OOM、scratch 删除、容器 cleanup 0。33.177 秒。工作树始终干净，120 个已封存 M1 公开输入在前后均逐 hash 一致。

容器固定原 signing image digest，`--init`、network none、非 root 65532、只读 source/fixture、cap drop all、no-new-privileges、1 CPU、512 MiB memory、128 pids、512 MiB tmpfs、900 秒整体期限。`readback.py` 是外部证据驱动，只调用原 Preview wrapper、真实 preview verifier、原 `seal_generations`、planner 和 prepare CLI；不调用 exporter/signing，不生成或读取私钥。原 helper 的 audit HTTP 只在容器 loopback TEST ONLY fixture，不是云 API。没有 SDK、CI、provider/生产网络、资源或源码操作。

## 已封存原始结果

`first-command.json` 保留 exact command、deadline、真实输出、exited container 状态和 cleanup；`first-run/result.json` 保留原子命令及阶段结果。每代保留未经重写的 `inputs.json`、`resolution.json`、`transport-plan.json`、`delivery.json` 和派生 `routes.json`。完整物化大包树已随 scratch 删除。JSON 中 `/tmp/wk-m2a-...` 路径是本次观察的原样历史路径，不是可直接复用的生产请求；其 inputs/plan/resolution 是本次原 helper 重建结果，不冒称与 M1 已删除临时 JSON 相同 digest。

原 preview 验签、stable 候选验签/metadata、完整 v2 planner 与 materializer 都实际运行，无 verifier mock。153 个物化对象每个均逐文件 hash/size 检查，并与原 plan 全字段比较，保留 audit/member/cache provenance。当前客户端请求仍仅 preview 10 与当代 stable 4242/4243；retained 不变成当前发布目标。所有 production/auth/cloud/public-client/activation false 状态保留。这个读回不是重跑 29/31/49 suite，也不是 SDK 正向。

## 有限表达的实际分布

| 固定映射组 | 路由数 | 候选规则数 |
|---|---:|---:|
| preview 浮动入口 → 当前 preview `/site/` | 17 | 1 |
| stable 浮动入口 → 当前 stable `/site/` | 7 | 1 |
| exact alias URI → `/public` + URI | 32 | 4 |
| 原 preview snapshot 同名路径 | 38 | 3 |
| stable A snapshot 同名路径 | 41 | 3 |
| stable B snapshot 同名路径 | 41 | 3 |
| `/` 和生成 stable mirrorlist，独立 literal | 2 | 2 |
| 合计 | **178** | **17** |

A/B 都是 17 条，最大 source expression **1017 UTF-8 字节**，无需改任何物理对象/签名字节、源身份或上界。candidate JSON 保留所有实际表达式。表达式仅 `^escaped_prefix(escaped_suffix1|escaped_suffix2|...)\z`，target 为固定前缀 + `$1`；根与 mirrorlist 保持 literal，固定 break 和 unique sequence。不存在动态 wildcard。`analyze-routes.py` 是证据分析构造器，不是交付到仓库的编译实现。

`route-analysis.json` 的 17 是可实现上界，也是**当前六个固定分组、literal 例外及固定源前缀**下的字节容量下界：每组 `ceil(sum(escaped_suffix_bytes+1)/(1024-fixed_overhead+1))`，实际 lexicographic next-fit 分块恰好达到它。未证明任意 PCRE/任意重分组的全局最小规则数；没有必要为更小数字扩算法。

独立逆解析候选的封闭 grammar，将每个完整 literal alternative 展开，得到与原 178 URI→target **完全相等**的有限集合，无丢行/额外路径/重复匹配；再连接原对象清单验证 hash/size/cache，原 source identity 已在读回阶段逐字段验证。Python regex 的正向 capture 输出及每代 1602 个相邻字符串拒绝另有检查。它们证明本地有限表示，**不证明 Alibaba PCRE 或真实 HTTP 拒绝**。

这对具体 A/B 的 source expressions 和 sequence 完全相同，仅 stable 浮动入口 target 与 mirrorlist target 两项不同（`A-B-candidate-delta.json`）。所以本夹具的候选不需新增/删除 rule；原计划关于“可能要求删除”的拒绝边界仍须保留，不能推及所有未来 generations，且本轮没有执行 SDK apply/update。

## 最小 M2a tests-first 实施建议（尚未执行）

缩小到 **5 文件**，先只实现编译能力；完整对象 SDK fixture 仍是后续 M2b：

1. `scripts/aliyun-object-storage-provider.py`：只改 compile_rewrites。现有 safe_path、dedup/conflicting facts 检查先执行；unique≤50 时返回现有逐 literal 的完全相同 bytes/order。>50 才按上述实际既有布局分组；不能证明关系的行保持 literal，结果>50或expression>1024明确拒绝。
2. `tests/provider_sdk/test_aliyun_object_storage_provider.py`：保留现有 22 case，新增完整先红矩阵与官方 SDK request serialization/readback 的有限规则断言。现有“51个无法分组的 /pN→同一个key”负例仍应拒绝，不借任意同目标分组合并去改变原能力。
3. 新 `tests/provider_sdk/fixtures/native-composite-routes.json`：从本次 A/B routes 原字节/事实导出一个有限数据 fixture，附源 delivery/plan/archive/receipt 与本 evidence manifest hashes；保留完整 178 行和 object/source 锚，不用手写近似 51-route toy 替代真实证明。该 fixture 是 TEST ONLY 已验字节锚，不是 fresh auth。
4. `docs/object-storage-delivery.md`。
5. `CHANGELOG.md`。

已有 SDK workflow 的 `scripts/**`/`tests/**` trigger 与 mandatory SDK runner 已涵盖这次 source/fixture，故 M2a 不必改 workflow/lock。原 22/10/29/31/49 的任何命令、计数合同与生产拒绝均保持。测试新增计数单列。若需要超出此五文件，先给具体复用/CI 合同依据，不自动扩成 publisher。

实施顺序：先将真实 fixture 的 provenance/全量行校验与全部新 failure tests 写好；在未改 provider 上实际红（178 literal 触及50cap），保留 fixed source/hash、真实失败退出；再窄改并跑原22＋新编译/SDK序列化合同及相关默认合同。A/B source-set 相同这一性质应成为实际 fixture 正例，不应被编译器假定为一般事实。不把此次证据分析当正式 tests-first 首红。

## 新回归矩阵

- 真实A/B：全178行准确覆盖、17规则能力、最大1024、inverse finite language完整相等；all objects/source/cache 不变，当前与retained不交换。17可作已冻fixture结果，不规定所有输入必须17。
- ≤50：原 literal 输出字节及顺序保持；已有原22 case不删。>50无法分组输入继续拒绝；input空/冲突/unsafe/unsupported namespace与大小限制不变。
- 组关系：snapshot/public/current-prefix可证明；root/generated保持literal；不同targetprefix不混合，source prefix/targets不靠宽泛后缀猜测；新布局没有隐式fallback。
- grammar：literal metachar escaping、一个capture、目标仅$1、完整\z、无wildcard/flags/可选分支；强制逆展开拒绝漏行/重复/额外行/错误捕获和目标。
- 边界：1024/1025 expression bytes、50/51 output规则、长singleton、重复一致route、冲突SHA/size/cache、capture歧义、sequence唯一/稳定、break固定、跨代alias源不改。
- 邻接：每条 suffix/slash/newline/query/fragment/dot/double-slash/percent及double-encoding/nonlisted paths拒绝；其中不支持输入仍先由原safe_path拒绝。
- SDK：真实官方SDK将全部17规则序列化到同一正确BatchSet请求，响应和完整配置readback绑定；A→B同source-set两target更新正确；超限/tampered before/domain/resource/unmanaged/drift在write前失败，ConfigId0/缺/重复/timeout保持incomplete，不重试/refresh/activate。此处测试只用既有Wire实例边界，不做全量153对象SDK闭环。
- 保持禁止：TEST ONLY fresh和activate仍拒绝；provider/object/resource/never-versioned/fullGET/logging guard不改，不开生产CLI/新schema/authority。

## 未证明事项

真实 Alibaba PCRE 的 capture/\z、query和URI规范化、未匹配回源、cache key与传播、批次/POP副作用、provider数据与公网客户端仍待外部授权和live验收。有限语法中邻接字符串不匹配，不等于 CDN 已返回拒绝。原 ≤50 输出可兼容并不授予旧literal配置删除权。后续M2b仍须完整153对象create-only/fullGET、双resource/input/current-control绑定、配置副作用/refresh精确ID与URL、fresh/activate拒绝及原所有suite；此次不实现。

`terminal-check.json` 确认 source 与公共输入未变、容器无残留、全部命令终态。有限公开清单与 manifest 不含私钥、真实凭据或大包树。历史 M1/D2 原失败及 tests-after-code 过程记录保持原目录不动。
