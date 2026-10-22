# 最小生产接入方案复核

结论：方向可行，但实施前需收窄以下接线合同；无需新授权平台或重写 validator。八文件数量本身不是问题，不能把全部生产闭环当作目前可验收的单片。M2b 三项 P2 仍在修复，未计完成。

1. **先定两操作语义。** 方案 README:46–54 同时提出 `upload|activate` 控制及 `previous=first`。若改控制提交切操作，旧上传收据必因 `executor_sha` 不同被拒（`verify-object-storage-delivery-inputs.py:60–63,110–118`）。建议 upload 只写对象；activate 从当前干净 main 重新 fresh→prepare→全量 GET，旧上传回执仅作历史。`previous` 仅绑定同次激活的写前复核；不修改既有相等规则，也不重 PUT。

2. **拆清离线正例。** 现 fresh 测试替换 Git 与 native subprocess（`tests/test_object_storage_delivery_inputs.py:185–194`），不能证明真实 clean-executor。补临时干净 Git checkout 的真实 HEAD/dirty/源码变更拒绝，与 HTTP-shaped GitHub transport 分开测；原 fresh 函数不替换为 success。共享编排的正例可由 TEST ONLY native 输入＋实际 SDK adapter 完成，始终标 fixture；生产 CLI 必须硬接原 fresh/客户端函数、不暴露 callback 绕过。真实 source/audit tuple 未齐，生产 provenance 正例仍待外部验收。

3. **session 是明确外部输入。** 保留 Provider:229–246 的 exact 比较。先约定两角色的 session 名及期望 ARN，由获准的外部凭据发行过程提供；当前仍 unknown。不能从当前调用结果反填期望，不能增加 wildcard、自动 AssumeRole 或新 OIDC。SessionName 可预指定，但具体调用身份仍须实际精确观察。[官方合同](https://www.alibabacloud.com/help/en/ram/developer-reference/api-sts-2015-04-01-assumerole)

4. **把 TLS probe 定为真实黑盒。** canonical HTTPS URL＋仅改变连接目标可保持 Host/SNI/证书名称；可用 curl `--connect-to`，不能只改 Host。[curl 合同](https://curl.se/docs/manpage.html#--connect-to) 局域测试服务实际记录 SNI/Host，验证正确 SAN 成功、错误 SAN/CA/重定向/超时/超限拒绝；fixture CA/loopback 注入只在测试接口，生产无 insecure/任意 endpoint 开关。此测试不证明真实 CDN PCRE、POP 缓存或 DNS 已切换。

可立即实施最小片：默认 disabled 控制＋拒绝路径、真实 clean-executor 接线测试、同一 probe 的局域 TLS 合同；复用 `_client_result`（prepare:307–326）和原 planner/fresh，不复制闭包验证。M2b v2 验收后再接固定 SDK 编排。真实 publication 仍等待原有资源/权限/来源及发布授权；无新增门槛、采购或测试执行。
