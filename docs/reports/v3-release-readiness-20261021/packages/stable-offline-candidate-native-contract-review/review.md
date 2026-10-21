# stable 候选原生契约短审

无剩余设计阻塞。审核固定 main `0cd625c` 的既有消费者/签名器及冻结 native-contract；未读取正在编辑的半成品，未运行测试。

发现并已修正 1 项确定接口错误：原第 3 步的 RPM allowlist map / 空对象不符合 unchanged signer。当前稿改为现有 v1 `packages:[{path,sha256,size}]` 和 `paths:[...]` 完整 schema 对象。原稿与修正稿均在此保留，原稿恢复后 SHA 与首读冻结值逐字一致。

未来可以在候选归档之外，将真实 immutable audit Release 和 OSS activation 绑定到 exact archive SHA/size；无需更改已签 APT/RPM 字节。候选内的 null audit ID 和 offline_candidate 状态仍保持原值。回填内部 ID 会产生另一份归档，不能称同一工件。当前 `stage/channel/test_only` 已足以区分未发布、未封存候选，不要求等价重复 booleans。

现有默认消费者受旧 schema、preview 和正整数 audit ID 三类约束。未发现本契约要求绕过这些限制；最终 diff 独审须确认新分支只能由显式 stable + offline-candidate 参数进入，不能按输入 schema 自动 fallback。生产控制和默认 preview 字节回归、旧 verify/audit/transport 拒绝候选仍按原计划验证。

这仅为格式和后续绑定兼容性的设计判断；不证明候选实现已经通过签验，也不代表存在真实 audit seal、OSS 路由或公网发布。
