# D3 只读可行性结论

**四文件范围尚未证实闭合；缺口是实际 preview 公开归档夹具，不是额外生产授权系统。**

方案 README:19、25–27 要求完整 preview＋stable v2 且不替身验证器。现有 `native_stable_candidate_drill.py:204–355` 只导出两代 stable：真实签名、删除私钥后验签和审计；没有 preview。`native_stable_clients_drill.py:615–664` 的 seal_generations 明确用 `InputSnapshot` 补 preview；该类 `test_plan_object_storage_publication.py:38–56` 使用 deb-v1/signed-* 测试字节及结构化源证明，不能称原生 preview 签验。planner:401–445 仍要求 preview，:230–246 的 receipt＋闭包核验不把这套结构夹具升级为真实密码学来源。原 native workflow:224–234 调用另一产品仓库的完整签验演练，但此处未证明其输出具有本 planner 的 canonical audit/descriptor 接口。

**实施前先补一个真实 preview canonical audit export 正例**：真实 DEB/RPM、APT/RPM 签名及默认原生校验、归档/外部 receipt/历史 control 锚；销毁临时私钥后，与现有 stable A/B 输出一同通过 unchanged planner 和 materializer，并保留 TEST ONLY 身份与生产 fresh verifier 拒绝。先冻缺 preview/混入结构字节的红例，不能用 verifier mock 或改生产拒绝消除。

若新 driver 可直接组装上述公开接口，四文件仍可能足够；若须给现有 fixture 增加 export，第五个 tests-only 文件须先报具体合同。三分钟静态检查未证明可直接复用完整 preview 导出，故不批准“现成四文件已足够”的断言。真实 Git/attestation 生产认证正向、live CDN/公网验收仍可后续处理，不是本 D3 必要新增门禁。未执行测试、容器、网络或秘密访问；不声称原生通过。
