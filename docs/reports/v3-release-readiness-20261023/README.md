# v3 发布复核证据：2026-10-23

结论及尚未完成的实际交付见[本次复核报告](../2026-10-23-v3-release-readiness.md)。本目录不改变[前一检查点](../v3-release-readiness-20261021/README.md)及其失败记录。

- [包交付实现、独立复核和本地验证](packages/README.md)：仅按有限公开清单复制，逐项源/目标/字节/哈希见 [COPY_RECEIPT.json](packages/COPY_RECEIPT.json)，整体校验见 [COPY_SHA256SUMS](packages/COPY_SHA256SUMS)。其中每份记录的 `pending` 均指该记录的历史冻结状态。

云 RC、实际生产来源/签名/审计、OSS/CDN 激活和公网客户端验收分别需要自己的实际收据。TEST ONLY、局域 TLS 或 SDK adapter 的成功不能代替这些结果。
