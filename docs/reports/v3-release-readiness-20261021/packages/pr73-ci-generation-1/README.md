# Packages PR #73 首次 CI 验收

结论：两条自动 CI 的首次 attempt 均成功。PR 保持 draft，本审计未执行外部写入、重跑、dispatch 或本机 Docker。

- PR: https://github.com/WuKongIM/packages/pull/73
- Head: `59549efe1074562928b8ae175faeda8778849e0e`；base: `3fe04138403a7c9443e4ede457dccfa16dff2b00`。
- 日志实际 checkout 为 PR synthetic merge `873b760b734ace5d19d7f56d90ee053c83a3abac`。Git API 证明其 tree 与 head 均为 `6237c709d8cca42cc716a7aee796760c3cd867a9`。原生 receipt source_sha 也为该 checkout；13 项输入 control size/SHA 与 head Git blob 一致。

| Run / 检查 | 实际结果 |
|---|---|
| [Pages 39587825259](https://github.com/WuKongIM/packages/actions/runs/39587825259) attempt 1 | 468 Python 测试，102.264 s，0 skip；4 合同测试；10:54:51Z workflow success |
| [Native 39587825081](https://github.com/WuKongIM/packages/actions/runs/39587825081) attempt 1 | 4 合同、7 metadata、10 local signing、6 bootstrap、31 stable candidate、8 transport 全部通过；10:58:56Z workflow success |
| 原生完整仓库演练 | 10:57:50Z 开始；10:58:26.560Z 输出正式成功 marker，APT 下载、RockyLinux 9 RPM 安装及 release build_source 检查实际完成；tracked-tree 与全部 post steps 成功 |
| 发布 | Configure Pages、Upload complete Pages artifact 与 Deploy complete Pages snapshot 全部 skipped；不计作已发布 |

31 candidate 用例包括 10 正例、21 预期拒绝。8 transport 为 stable-bind、stable-seal、stable-resolve、composite-plan、composite-repeat、composite-retained 六项 exit 0，缺少 TEST ONLY opt-in / 改变 retained input 两项 exit 1 预期拒绝。独立检查这 8 项全部 passed，未仅依赖 workflow 的原 31-case 条件。

候选收据确认 scratch_removed、secret_tree_removed_before_verification、checked_in_control_unchanged 均 true；运输收据确认 2 assets、candidate_bytes_preserved 和实际 stable 签名校验 true。HTTP POST/PATCH 仅在 TEST ONLY loopback fixture，public_api_writes=false；production_keys_used、published、source_provenance_verified 均 false。preview composite fixture 明确为 structural-only。

完整旧仓库演练来自固定可信工具/产品 source `ed7e67d8f781b4f92872e42116c11b304fe03b5c`，并非对当前产品 main 的重新构建验收。它校验 file: preview APT/RPM；本轮不证明真实生产 seal、云/CDN 运输、stable 激活或公网安装。

唯一读取故障：下载 stable candidate artifact 首次 GET 返回 EOF，原 stderr 保留，后一次 GET 成功。首次 collector 未记录该 subprocess 数字 exit code，收据如实标注未知；这不是 CI 失败，也没有重跑 CI。四个小型 ZIP 全量下载后重算 SHA256 均匹配 API digest，成员经 ZIP CRC 校验并另记 SHA/size。归档只留 JSON 成员和下载收据，未保留 ZIP 或产品二进制。

`allowlist.txt` 是精确归档清单；`archive-sha256.json` 覆盖清单和所有列出的 payload（不自哈希）。原始 API、完整两份 job log、规则、Git 身份、4 个 artifact JSON 与摘要均在本目录。
