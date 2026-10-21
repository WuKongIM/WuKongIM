# Spec v1 P2 修复失败清单（v2）

仅修复原 D 离线 seam 的两项复核发现；生产、云、私钥、真实 GitHub/API、原客户端实现不在范围。
原 v1 的 16 项库存与首红/首绿保留，v2 是新增回归覆盖。

| ID | 失败方式 | 预期行为 / 先写测试 |
| --- | --- | --- |
| C1 | fake 当前 activation-control 缺失、null、格式错误、与请求 SHA 不同或非 simulated | 初次检查拒绝；不 PUT、不 apply；保留实际观察；生产标志 false |
| C2 | 调用者没有 activation-control SHA | dry run 仍可准备，simulation 无法满足独立控制围栏，不产生 API 写入 |
| C3 | 初次 SHA 匹配，但 PUT 后仅当前控制改变；源、历史控制、config 不变 | 激活前再次读取并拒绝；完整 GET 证据保留，write_attempted false，无 apply |
| C4 | 当前控制一致，且不同于历史 source/candidate/seal 控制 | 接受模拟；分别保留请求锚和两次独立观察，不串用历史控制身份 |
| R1 | stable 原回执缺少任一个或两个 transport input/plan digest | clients 阶段 incomplete；保留已激活观察及原回执，不重写原回执 |
| R2 | stable 原回执任一个 digest 错误或类型错误 | 同 R1；不能只凭 target identity 宣布 simulated_complete |
| R3 | 相同 stable candidate，但另一个 composite 改 preview 或 retain | 两个原 digest 与当前输入/plan 不同，拒绝旧回执；target/audit/snapshot 相同也无效 |
| R4 | stable 原回执双 digest 精确匹配当前 preparation | simulated_complete，原回执与其 digest 完全保留，外层所有生产 flags false |

fake 当前控制读取独立于调用者 expected SHA；真实远程控制真实性仍待 provider/GitHub adapter。
客户端测试回执的双 digest 从原 planner 和输入 bytes 独立计算，不从待验证的 preparation 复制。
