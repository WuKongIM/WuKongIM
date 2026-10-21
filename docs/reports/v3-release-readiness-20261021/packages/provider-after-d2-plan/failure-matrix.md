# D3 first-red matrix（计划，未执行）

每行须明确预期失败阶段、SDK 调用计数、已发生副作用和不能到达的后续阶段；先保存真实红，再写实现。不能通过删除 verifier、改 test_only=false 或调大期限造红/绿。

| 反例 | 必须验证的结果 |
|---|---|
| v2 缺 current/retained、preview/stable 混淆 | 原 parser/verifier 拒绝，零 SDK 写 |
| 真实 APT/RPM 签名或 native metadata/package 改一字节 | 真验签/闭包拒绝，无 mock，无上传 |
| source/candidate/seal/executor SHA 或 audit asset ID 混用 | 保持各身份，错锚拒绝；不能只比 digest 标签 |
| 同 inputs 路径换字节、plan 重算漂移、materialized 文件替换 | 上传前拒绝；后期漂移保留已有 uploads，不 config |
| alias A payload 位于 B prefix、重复目的 key、遗漏/多对象 | 精确 source audit/member/字节/cache 绑定；对象集合全等 |
| 账户/principal/bucket/versioning/config body/domain 变化 | 相应首次写前拒绝，原完整观察摘要检查不绕过 |
| PUT exact exists / 无法判定 timeout | 只 full GET 决定；最多一次 PUT；不同字节拒绝 |
| GET 早 EOF、超长、hash/cache/encoding 差异 | 关闭 stream，receipt incomplete，无 config |
| 某对象成功后下一对象失败 | 精确 partial objects 与真实已写记录；不删、不伪 rollback |
| 完整 route table >50、未知函数、非受管ID、顺序/管理冲突 | compile/apply 明确拒绝，不用分批绕总额或通配放宽 |
| config 第二批 partial/timeout/ConfigId0/readback drift | 完整 attempted 状态和读回；不刷新、不声称 activation |
| fresh 控制锚在上传与配置之间漂移 | 无配置写；完整 fresh-v2 正向若无可信 fixture 标为未覆盖 |
| refresh 外来/缺失/重复ID、URL重绑、未Complete/超时 | 仅查询本次IDs，incomplete；不触发任何 public-success |
| TEST ONLY 或旧receipt被当生产授权 | fresh verifier/activate 继续拒绝；全部原 false 字段保留 |
| 客户端请求exact version/source/inputs/plan被替换 | 绑定检查失败；callback返回success不能充当真实公网结果 |
| 依赖缺失/zero tests/skip/socket attempt/真实凭据发现 | 独立job非零，零provider网络；无静默跳过 |
| 中断、期限、清理失败或public日志含敏感内容 | 有界终态及真实cleanup；证据缺失不能当通过 |

正向最小例：完整两代 native stable+preview v2（构造可行性待核）→原 plan/materialize→真实官方 SDK 序列化/解析 transport fixture→每对象完整读回→同路由配置/刷新 transcript；`activate()`仍拒绝，记录 TEST ONLY integration 成功而不是 production/live/public-client 成功。
