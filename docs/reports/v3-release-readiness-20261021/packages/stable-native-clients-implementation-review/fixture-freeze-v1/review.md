# Stable native clients：两项 fixture 独审

2026-10-21。基线 `335cd2569d2b62de577916ed86c097fd3a22547e`。先在 11:29:31 UTC 保存两文件原字节，再读 generation-2 范围及 fixture 先红后绿收据；本次只审这两项，不把正在实现的 validator/driver/CI 计入通过。

**结论：这两项冻结 fixture 未发现阻塞问题。** 真实原生双代构建、签验、四客户端安装及二进制版本/source 身份仍待完整实现与 Linux 验收；本次没有运行 GPG、Docker、编译、包管理器或产品。

## 已核对

- `native_source_metadata_drill.py` 新增可选 `binary_payloads`，原 data-only 默认不变。独立执行无原生命令的对比：stable 和 preview 两种输入下 staging 文件 bytes/mode、native 命令参数均与 Git 基线相同；原 `main` / `fixture_receipt` AST 不变。
- `native_stable_candidate_drill.py` 仅新增 exporter 与参数化 `fixture_source`；原 `pipeline` / `main` / `generate_control` / `sign_family` AST 不变，原 31 case 列表和 `transport.exercise` 调用保留，因而没有删除原 31+8 入口。这不等于本次重跑了该原生套件。
- exporter 显式接收 A=3.0.0 / B=3.0.1 两套 server/wkcli 输入，scratch bootstrap 分别为 1.1.1 / 1.1.2；每族生成一次 TEST ONLY key，两份 control 复制同一证书。每代仍是新 unsigned→签名→compose，没有将新 RPM 冒充 preserved，也没依赖签名发生在同秒。
- `finally` 先关闭 key factory，再删除包括 passphrase 文件的整个私钥树；之后才执行两代 verify/archive/audit。只选择 control、source、plan、archive、receipt、两族 signing receipt、site 八种公有输出；source ID/SHA 保留 TEST ONLY null。两个代都验证完成前不会写 output_root。
- 独立轻量文件 I/O 探针（全部 native 操作替身）：正常双代导出、B 验证失败、B 复制到一半抛 OSError 三例通过。两种失败均无 A/B 残留，私钥标记树已删除，环境恢复。此探针只证明清理/导出顺序，不证明原生包格式或签名正确。
- 31 个实际保护文件独立比对 Git 基线一致（10 control、12 producer 和 9 个非本任务测试 workflow）；stable 未启用，未改生产 signer/控制/发布入口。未读取任何真实私钥、Keychain、NAS 或凭证。

精确源码快照：

| 文件 | SHA256 |
|---|---|
| tests/native_source_metadata_drill.py | `08c8203fd16124cfec2f3e68972014ad355f29f6d70b9930e01587fc7288259b` |
| tests/native_stable_candidate_drill.py | `7c1b3c474327f906b3cd5dd58005b21a1bac4e5d0632de5fc2f696340c62db58` |

## 留待主实现/真实验收

ELF header/权限/大小检查只证明可打包的输入形状，不能证明 source SHA 或版本；主 driver 必须真实编译固定 fc182 源码，并由已安装 CLI 的现有门禁检查两代 version/commit/release，不能把导出结果里的 source_sha 自报当 provenance。原 candidate null 身份不可回填。最终完整收据还应覆盖所有原 native 套件、真实 A/B 两代不同 bytes 和清理结果。

继承的 TestKeyFactory 调用没有逐命令 timeout，完整 exporter 必须受 driver/容器总期限约束；本切片 README 已如实说明，待 driver 冻结时核对。进程被强杀不能声称 Python 清理一定执行。轻量探针自己的首次断言写错了故障名称匹配，已在 `probe-correction-note.txt` 记录并纠正；未据此产生产品 finding。

原 fixture 首红/绿与 15 项旧 unit/默认一致性/portable-plan 证据由 `review-inputs.json` 冻结；本审增加的实际结果是 `default-equivalence-independent.json`、`export-boundary-results.json`、`static-independent.json`。没有扩大代码范围，也没有对主 validator/driver/CI 作完整通过结论。
