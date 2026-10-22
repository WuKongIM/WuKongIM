# v3 发布复核证据：2026-10-23

结论及尚未完成的实际交付见[本次复核报告](../2026-10-23-v3-release-readiness.md)。本目录不改变[前一检查点](../v3-release-readiness-20261021/README.md)及其失败记录。

- [包交付实现、独立复核和本地验证](packages/README.md)：仅按有限公开清单复制，逐项源/目标/字节/哈希见 [COPY_RECEIPT.json](packages/COPY_RECEIPT.json)，整体校验见 [COPY_SHA256SUMS](packages/COPY_SHA256SUMS)。其中每份记录的 `pending` 均指该记录的历史冻结状态。
- [第二批收据](COPY_BATCH2_RECEIPT.json)：原请求的启动/handoff 和 #79 首轮 CI，复制范围及摘要分别记录在 [清单](COPY_BATCH2_ALLOWLIST)和[校验表](COPY_BATCH2_SHA256SUMS)。
- [第三批收据](COPY_BATCH3_RECEIPT.json)：本轮预演失败、双轴离线分析、未进入 MCP 的单次分析尝试，以及 #79 合并和 #80 首轮失败。原含 UID 应用日志和二进制 profile 不入库，保留原摘要与有限脱敏派生；排除范围、变换及来源均在收据中。见[清单](COPY_BATCH3_ALLOWLIST)和[校验表](COPY_BATCH3_SHA256SUMS)。
- [第四批收据](COPY_BATCH4_RECEIPT.json)：按用户要求结束租约，独立 Release 于 **02:14:57.935** 证明 12 个资源范围零库存；同时保留 GitHub 阶段总状态陈旧的认证限制。见[清单](COPY_BATCH4_ALLOWLIST)和[校验表](COPY_BATCH4_SHA256SUMS)。

云 RC、实际生产来源/签名/审计、OSS/CDN 激活和公网客户端验收分别需要自己的实际收据。TEST ONLY、局域 TLS 或 SDK adapter 的成功不能代替这些结果。
