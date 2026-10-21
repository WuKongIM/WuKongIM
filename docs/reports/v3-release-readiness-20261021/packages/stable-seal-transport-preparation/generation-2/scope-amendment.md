# 封存写前来源闭合与 TEST ONLY 出口：第二代方案

本补充取代第一代方案中“生产来源检查留待后续 activator”和“只检查双 loopback origin 即足够”的部分；其余两资产、不改 candidate bytes、双 control、composite 路由约定不变。与 ci_audit 共同核对；当前仅方案，不创建实施工作树，不执行 GitHub API、凭据、签名或生产写入。基线为 `c8aae37d13787a0aceceac751d6d35bbc1d89c35`。

## 共同推荐：4 个既有 helper + 1 个窄只读预检

新增 `scripts/verify-stable-seal-inputs.py`（建议名），供既有 bind/seal 直接调用；现有 resolve-audit-release、bind-audit-release、seal-audit-release、plan-object-storage-publication 仍为其余四处修改。新增模块职责仅为把**真实 GitHub 读取结果**与 candidate 的原字节及已有校验器连接，不定义新的 signing 交换协议。强行保持四文件会把来源验证、Git tree/blob 解析堆进 writer 或 audit reader；增加一个独立只读入口更容易测试和审阅。

所有现有 #72 producer 核心、控制 JSON、公钥、生产 workflow/权限、频道状态保持不变。新增模块由已审精确 checkout 的固定路径加载，永不导入 archive/control/source 目录中的代码。source-read token 复用现有调用者提供的只读环境变量；本切片不创建 App/Environment/secret，不把 token 写入 receipt。实际 production workflow 接线与实际运行授权仍不在这次实现中，但将交付不依赖“调用者声称已预检”的完整生产写前检查能力。

## 顺序与证明边界

1. **先限制出口。** stable 正常模式固定 `https://api.github.com` 作为 API/download 初始 origin；不接受任意 base override。新 `--test-only` 模式只允许 loopback API/download，binder 仅需 API；缺 test opt-in 的 loopback 请求前失败。两个模式不能互相 fallback。TEST ONLY 收到任何 301/302/303/307/308 均立即失败，不读取 Location，不继续请求；包括 API、numeric asset download、writer response。现有 preview 默认行为保持。
2. **历史 control 必须真实存在并匹配 bytes。** 新预检从固定 `WuKongIM/packages` 读取 fresh main，核对 seal 的 `expected_control_sha`；以真实 commit/tree/blob 身份证明 candidate 历史 `control_sha` 为该 main 的祖先或相同提交。只读取 #72 已固定的 10 个控制路径与 12 个 producer 路径，拒绝 symlink/submodule、重复/缺失 entry、截断 tree、错误 size/base64/blob SHA/原 SHA256。每个远程 raw blob 与外部文件及 plan 对应 facts 一致；producer 也与当前执行的受审本地 sibling 原字节一致，不执行下载内容。允许 regular blob 的实际 mode（100644/100755）；绝不能把所有脚本误限成 100644。读取 main 两次并与现有 writer fencing 对齐，不能凭本地 checkout 的 commit 字符串声称认证完成。GitHub 的 tree/blob API 提供这些原生对象，无需自建 Git 协议。
3. **bind 只改变空 draft/tag。** 复用同模块的 control-only 阶段和既有 empty-draft/create-only/tag/main fencing。此操作没有 candidate 资产、也不产生 source 已验证声明，因此不重复下载七份 source。它不能绕过 seal 的完整预检，不能把 bind receipt 当 seal 放行凭证。
4. **seal 首次资产写入前执行完整预检。** 在当前已审代码中调用 `resolve_source_release(... channel='stable')`，按候选的 numeric source Release ID fresh 下载原七资产并做 Release/tag/main ancestry 双读；将其不可变身份（Release ID、tag/version/classification、source SHA、各 asset ID/name/size/SHA、checksum closure）与候选原证据比对。随后对这些 fresh bytes 调用现有 `verify_source_attestations`，实际执行固定 workflow、source ref/digest、deny-self-hosted 的 `gh attestation verify`；调用 `verify_metadata` 验证真实 unsigned DEB/RPM。再复用 #72 candidate audit verify 核查原 archive、external plan/control/source、实际 APT/RPM 签名与 payload 闭包。执行来自固定受审 checkout，工具使用既有受控路径/环境；GH_HOST 固定 github.com，隔离不相关 gh 配置，避免继承企业 host 改写来源。任何步骤失败都必须保持零 asset POST、零 publish PATCH。
5. **不改原证据来匹配 fresh 输出。** GitHub main 可以合法前进，新 gh attestation JSON也不保证逐字节复现；fresh 检查只比较上述稳定身份，分别保存新观察摘要。原 candidate source-resolution/attestations/metadata、archive、receipt 和 null IDs 保持原字节，不能用新结果替换原成员。candidate 的 producer 若与受审当前可执行代码不匹配则拒绝；不自动执行历史 archive 附带代码。
6. **写与最终封存有独立 fence。** 通过预检的内存结果只在该进程中消费，不接受 caller 传入“verified receipt”代替执行。沿用每次写前后 main/tag/policy fencing。最终 `draft:false` PATCH 之前重新轻量读取 source Release/asset tuple、tag/ancestry，并比对同一源身份；不需要第二次下载大资产或重做全部签名。读回变化则停止，保留可恢复 draft，不能删改已上传资产。重启恢复仍重新执行完整预检；不把旧 stamp 当永久授权。

现有源解析器、attestation verifier、metadata helper可直接复用；**没有现成 Python helper 已经认证上述历史 10+12 原字节**，这是唯一新增的来源控制逻辑。现有 publish workflow 有通过固定历史 commit `git show` 提取 preview 控制的模式，可参考其信任边界，不能把那段未执行的 shell 当本次已验证。

## 必须补充的失败场景

| 边界 | 先红案例与必要断言 |
| --- | --- |
| 自报证据 | archive/plan/control/receipt 全部联改为一致且实际签名有效，但历史 commit 不含这些控制 bytes：首次 asset POST 前失败；自报 verified=true 无效。 |
| 历史 control | commit 不可达、main 变化、blob 与 plan/本地 sibling 不同、symlink/submodule、tree truncated/重复/缺失、base64/size/SHA 错误和超限：拒绝且零写。 |
| 来源绑定 | stable numeric ID 与候选不符，tag/source SHA/asset tuple 或 checksum closure 改变，prerelease/draft/immutable 条件错误：拒绝且零写。 |
| 实际 provenance | 原 JSON 声称 attestation 成功，但真正 verifier 拒绝、workflow/ref/digest/runner 条件不符、超时或无工具：拒绝且零写。 |
| metadata/签验 | source native metadata 或 unsigned 条件失败；candidate 签名、payload、external plan/control/source 原字节不符：拒绝且零写。 |
| fence与恢复 | 首次预检通过后 source/main/tag/policy 在 final PATCH 前变化：不得封存；archive-only 恢复需新预检；不可重复覆盖/删除资产。 |
| TEST ONLY 外网 | 任一非loopback origin、无opt-in loopback、含credential的URL、loopback→HTTPS外网、loopback→loopback及第二跳 redirect均拒绝；记录目标 listener 零请求，零 token 泄露。 |
| 模式隔离 | 正常 stable 使用假 API 或 test candidate失败；TEST ONLY保留 null source IDs，所有输出显式test_only，正常planner/后续activator拒绝。不得为测试伪造production来源。 |
| 不变性 | 预检、封存、数值readback后原 candidate pair 的大小/SHA完全不变；source证据不因fresh main/attestation JSON变化被回填；preview既有回归不变。 |

生产预检分支用受控 API/gh/native seam 负例测试；外部 CLI 不新增绕过生产 origin 的测试后门。真实 tiny native端到端继续明确 TEST ONLY，用 loopback 两资产状态机验证字节与运输闭包，不声称建立真实 GitHub provenance。正常 stable writer 若尚未完整接通上述检查，就必须在任何非loopback请求前拒绝；不能先留下危险写分支再写“后续加检查”。

## 源码与官方依据

- [source resolver：fresh 七资产、tag/ancestry 双读](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/resolve-source-release.py#L392)
- [attestation verifier：固定工作流与真实 gh 验证](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/verify-source-attestations.py#L386)
- [metadata：现成 native 查询入口](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/verify-source-package-metadata.py#L59)
- [candidate 仅重建/验证本地来源声明](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/compose-package-site.py#L1980)
- [candidate audit 复用实际签验](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/scripts/package-audit-receipt.py#L1089)
- [已有历史 preview 控制提取模式](https://github.com/WuKongIM/packages/blob/c8aae37d13787a0aceceac751d6d35bbc1d89c35/.github/workflows/native-package-publish.yml#L1781)
- [GitHub Git tree API](https://docs.github.com/en/rest/git/trees#get-a-tree)说明 tree entries 的 path/mode/type/SHA 与截断状态；[Git blob API](https://docs.github.com/en/rest/git/blobs#get-a-blob)返回原 blob Base64。该方案通过这些固定仓库对象比对 bytes，不下载执行代码。
- [GitHub compare API](https://docs.github.com/en/rest/commits/commits#compare-two-commits)用于两个精确 commit 的关系核对；这仍是对 GitHub API 的信任，不等价于独立签名提交证明。
