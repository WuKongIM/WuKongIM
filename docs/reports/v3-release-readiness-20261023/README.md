# v3 发布复核证据：2026-10-23

结论及尚未完成的实际交付见[本次复核报告](../2026-10-23-v3-release-readiness.md)。本目录不改变[前一检查点](../v3-release-readiness-20261021/README.md)及其失败记录。

- [包交付实现、独立复核和本地验证](packages/README.md)：仅按有限公开清单复制，逐项源/目标/字节/哈希见 [COPY_RECEIPT.json](packages/COPY_RECEIPT.json)，整体校验见 [COPY_SHA256SUMS](packages/COPY_SHA256SUMS)。其中每份记录的 `pending` 均指该记录的历史冻结状态。
- [第二批收据](COPY_BATCH2_RECEIPT.json)：原请求的启动/handoff 和 #79 首轮 CI，复制范围及摘要分别记录在 [清单](COPY_BATCH2_ALLOWLIST)和[校验表](COPY_BATCH2_SHA256SUMS)。
- [第三批收据](COPY_BATCH3_RECEIPT.json)：本轮预演失败、双轴离线分析、未进入 MCP 的单次分析尝试，以及 #79 合并和 #80 首轮失败。原含 UID 应用日志和二进制 profile 不入库，保留原摘要与有限脱敏派生；排除范围、变换及来源均在收据中。见[清单](COPY_BATCH3_ALLOWLIST)和[校验表](COPY_BATCH3_SHA256SUMS)。
- [第四批收据](COPY_BATCH4_RECEIPT.json)：按用户要求结束租约，独立 Release 于 **02:14:57.935** 证明 12 个资源范围零库存；同时保留 GitHub 阶段总状态陈旧的认证限制。见[清单](COPY_BATCH4_ALLOWLIST)和[校验表](COPY_BATCH4_SHA256SUMS)。
- [第五批收据](COPY_BATCH5_RECEIPT.json)：#1026 指标采集修复、#80 Linux 权限交接修复的独立复核、首次 CI、普通合并及后续 main CI。含原始失败记录；self-chat 原始身份报告及其 ZIP 排除，仅保留有限安全摘要和完整性引用。见[清单](COPY_BATCH5_ALLOWLIST)和[校验表](COPY_BATCH5_SHA256SUMS)。
- [第六批收据](COPY_BATCH6_RECEIPT.json)：四代小群本地实验、原分布本地预检失败和自然失败、只读三副本核对、配置差异及离线信号分析。配置 addendum 和批量对照提案是独立补充，尚不包含该对照的执行结果；原始配置、数据库和含身份日志不入库。见[清单](COPY_BATCH6_ALLOWLIST)和[校验表](COPY_BATCH6_SHA256SUMS)。
- [第七批收据](COPY_BATCH7_RECEIPT.json)：单变量 batch128 对照的失败和终态缺口，以及 #1027 两项观测修复的测试先行、实现和双轴复核。该批未含 Linux CI 或新 profile；受控测试 fixture 与真实运行身份已在收据中区分。见[清单](COPY_BATCH7_ALLOWLIST)和[校验表](COPY_BATCH7_SHA256SUMS)。
- [第八批收据](COPY_BATCH8_RECEIPT.json)：#1027 首轮 Linux CI、普通合并和清理；finalize 诊断缺口提案；有界 CPU 采集提案、实际本地运行及离线信号；来源声明的逐层纠正和独立二进制审计，共 10 个独立来源组。子清单分别核验，原始 profile、可执行文件、数据库、运行身份和私有配置不入库；嵌入 stamp 不等于实际编译源码。该批不含后续清洁来源构建或新负载。见[清单](COPY_BATCH8_ALLOWLIST)和[校验表](COPY_BATCH8_SHA256SUMS)。

后续 #1027 已于北京时间 **04:01:25** 合并，首次 CI 为 17 项成功、2 项条件跳过。本地实验的来源声明、二进制嵌入 stamp 和实际编译内容必须分开核对：Go 1.25.11 在嵌套 worktree 的 Git 根查找可取外层 a804 标记，而反汇编已见 79a 工作树路径及新增分支；不能仅凭 stamp 判断执行了旧源码。最初产品 override 构建链仍不完整，精确候选资格暂未验证。当前来源纠正及后续诊断见[复核报告](../2026-10-23-v3-release-readiness.md)；旧批次冻结材料中的初步解释保留原字节。

云 RC、实际生产来源/签名/审计、OSS/CDN 激活和公网客户端验收分别需要自己的实际收据。TEST ONLY、局域 TLS 或 SDK adapter 的成功不能代替这些结果。
