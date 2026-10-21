# M2b 先行失败矩阵（20 个驱动 case，待实施）

全部计划性条目，未运行；positive 仅限 TEST ONLY object/config，完整 refresh 明确 expected unsupported。实际实现前先完成全部测试和此清单的具体case输入，再对尚未实现driver/旧HTTP契约实际RED；不得把本表或静态文件存在断言当首红。若case inventory需增减，先记录原因与先红。

测试入口：既有 Provider public primitives、真实官方SDK HTTP instance adapter、新TEST ONLY driver。native通过原 CLI/helpers，禁止替换verify/signature结果。原SDK34、input10、M1 29、native31、stable49保持独立。

| proposed case | 失败方式 | 预期可验证行为 |
|---|---|---|
| native-public-binding | 缺失/改动export、archive、receipt、control/source member；TEST ONLY/secret_removed/source身份不符 | 原native verifier及hash绑定拒绝；不接SDK；不重新签名 |
| native-composite-materialization | 真实preview省略成structural、计划digest错、objects/routes缺行或source/member/cache错 | 用现helper正向实际得到A/B153/178；任一缺口停止；原签名/metadata验证不mock |
| sdk-all-objects-A | 只取样、遗漏最后对象、HEAD/ETag代替GET、PUT字节/长度/cache错 | 153个实际SDK create-only与153个EOF readback；精确manifest集合相等 |
| sdk-all-objects-B | 共享object跳过验证、同key不同bytes视conflict成功、mirrorlist未更新 | 152 exact conflicts均GET＋1新key；153逻辑操作；154-key union |
| sdk-object-resource-fence | account/principal/bucket/owner/region/never-versioned变化 | 该次PUT前拒绝；分别记录disabled intent与fake resource digest |
| sdk-local-object-drift | 输入file replaced/linked/size或hash变更；source绑定被替换 | 下次写前拒绝；只接受同一次native preparation固定regular-file集合 |
| sdk-unknown-put-no-retry | timeout-before与timeout-after；未知响应 | 一对象最多1 PUT；只允许完整GET消歧；不能验证则incomplete并保存attempted |
| sdk-full-get-failures | truncation/overlong/hash/cache/encoding/interrupted stream | 拒绝下一写；EOF/size/SHA/cache缺一不可；没有假readback_verified |
| sdk-initial-input-current-fence | input/plan/preparation/route-set/current-control observation不匹配或missing | 任何PUT/config/refresh前拒绝；不拿历史seal充当current |
| sdk-mid-object-drift | 第N个成功后input/current/fake resource漂移 | N个既有effect保留；第N+1写为0；receipt incomplete，不rollback或重复PUT |
| sdk-generation-source-binding | current/retained交换、A/B archive或product source偷换 | 153对象及178routes仍逐source audit/member比对；客户端只preview10＋当代stable |
| sdk-full-config-A | 178行被裁剪、17规则source或target错、读回漏ID/顺序 | 官方SDK真实序列化≤20 functions；全量finite-map与config readback相等 |
| sdk-config-A-B-reuse | ConfigId/source绑定错；把retained变current | 同source-set复用IDs，仅2target变化；原false字段不变 |
| sdk-config-fresh-fence | before体/digest/domain/resource/managed边界/新鲜readback漂移 | BatchSet前0写；从fixture evidence推断不代表production auth |
| sdk-config-removal-unsupported | literal→group、rechunk、route移除需要删除 | 原apply真实拒绝；不新增删除授权或自动清理 |
| sdk-config-incomplete-sideeffects | 0/missing/duplicate/wrong IDs、partial、timeout、readback失败 | 真实attempted batch与after或unknown收据；不重试、rollback、refresh |
| sdk-full-refresh-set-unsupported | 所有178URI集合含/；试图省为177/25或改Directory | 全set预检不遗漏；refresh请求0，或完整object/config流以refresh_unsupported结束 |
| sdk-aggregate-task-coverage-unknown | 合法少量聚合IDs与URL数不同；unknown多URL ObjectPath形状；Wire旧ID复用 | 实际provider当前拒绝为已知限制；不自造响应编码/一对一绿证明全部覆盖；已有refresh请求副作用必须标明 |
| sdk-production-boundaries-refuse | TEST ONLY fresh auth、activate、任一production/cloud/public-client真字段 | 实际调用原verify_delivery_inputs和activate拒绝；不得mock正向/改fields |
| driver-ci-receipt-cleanup | 缺SDK/zero cases/skip、阶段超时、manifest外文件、socket尝试、cleanup失败、日志超限 | driver非零、always有限收据；所有进程终态＋owned scratch移除；原29/31/49命令保持 |

所有危险变体在测试 scratch 内、dummy凭据/网络拒绝下。对象/配置中途错误必须从观测的HTTP尝试记录副作用，不能由异常字符串猜是否写入。生产provider的put/refresh异常并不总返回部分receipt，因此新driver需保留自己有限adapter transcript；这是测试观测，不是新增生产恢复journal。

刷新集合测试与多批原语成功测试分开：根与聚合覆盖未知时不得标完整成功；Wire单调ID修复若后续需要，必须新增独立先红，其绿只证明fixture状态模型。当前片不承诺用177非根URL分50/50/50/27执行完整刷新。
