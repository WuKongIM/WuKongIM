# Stable 客户端兼容性六文件独审（第一代）

基线为 `4ba39668c892ca17eb33b165afe993ccc683161f`，审查未提交六文件冻结见 `source-freeze.json`。完整 patch SHA256 为 `c87bf9b7b69bcd97d6d8abac2ddeab85eb0aa382a9f7879be718d9985a1ebc3b`。仅只读审查及无网络、无密钥的 Python 控制流探针；未跑完整 suite、GPG、Go、Docker、原生客户端或云操作。生产/证据工作树没有被本审查修改。

## Standards

**S1，流程偏差：五个 guard 测试后补，不能声明全量 tests-first。** `tests/test_composite_storage_plan.py:122–195` 五项 guard 在 `05-guards.log` 首次出现；同一完整 discover 的 `04-alias-green`（12:29:50）已有实现并仅运行4项，`05-guards`（12:30:49）才9项。父项目 AGENTS 要求 “NEVER write unit tests after you write code”。如实记录偏差，可对冻结旧实现回放补充回归敏感性；回放不能追认原顺序合规。该历史流程发现不要求重写历史或新增发布门禁。standards 由独立子审完成。

另已逐项核对39个保护文件原字节，包括12个 producer、生产客户端 validator、workflow/control/public keys/site；均等于精确基线。没有 ForceHash、TLS/签名弱化、channel 启用或49项清单缩减。相关边界与变更语义相符，未提出结构重构或风格阻塞。

## Spec

**R1 [P2]，by-hash 负例仍可能把未发送的响应当已发生故障。** `tests/native_stable_clients_drill.py:205–217` 在发送 HTTP headers/body 前写入请求日志；`:220–221` 吞掉 BrokenPipe/ConnectionReset。新增 `verify_apt_by_hash_fault`（`:111–120`）只看这条预发送记录；`:744–753` 仍让一般 SSL/connection 诊断满足负例。`fault-delivery-probe.py` 直接抽取冻结源码实际 `do_GET` 和 actual negative download branch，令 `send_response` 在任何 headers/body 发送前抛 BrokenPipe，随后模拟 client exit100 + “SSL connection failed before response”。missing/corrupt 两分支均被接受且没有 server-error 文件，结果见 `fault-delivery-probe.json`。这证明控制流假绿边界，不证明真实 Linux 出现过该情形。

最窄修复可让 fault 证据只来自 `wfile.write`/flush 成功后的完成记录，并要求该状态；或为两种 by-hash 负例要求明确客户端404/hash/size证据，使纯TLS错误不足以通过。先固定上述失败，保留本代原冻结后再窄修/复核。无须重造transport或客户端。

SHA512 planner其余部分未发现阻塞：完整 candidate native 验签在别名生成之前，既有 `verify-production-package-site.py:1043–1112` 验证签名、两索引及 SHA512 闭包；新别名使用同一次有界 `read_checked(expected=files[name])` 得到的原字节，保留原 SHA256/size/member。虚拟URI未伪装archive member，所有代的alias仍经同一conflict gate。仅 `_stable_snapshot`、`make_composite_plan` 的AST改变；preview-only函数未改。原archive/签名字节不改，输入变化/限额/冲突拒绝有聚焦证据。

TLS修复自身是有界2秒 unwrap→TCP close；原真实TLS红绿证明完整认证HTTP后缺close_notify、修复后成功；错误CA/SAN和缺7字节负例仍拒绝。没有新增实际Linux验收声明。

## 验证边界

已读并绑定原始证据：严格TLS先红后绿、55相关测试、9 composite与31 preview测试。SHA512结构测试隔离native verifier，不能代替密码学/四客户端验收。没有重复运行作者全量检查；下一精确head Linux 49项仍需执行并保留首失败。

结论：Standards 1项历史流程偏差；Spec 1项可复现P2负例假绿，修复前不建议将这六文件delta视为全部审查通过。保护文件39/39相同、planner默认路径不变；本代收据保留，后续修复另封存。
