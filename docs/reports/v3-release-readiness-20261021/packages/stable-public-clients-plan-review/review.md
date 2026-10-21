# Stable 四客户端 / A-B 计划独立复核

截至 2026-10-21T11:15:20.711132+00:00，**generation-2 无剩余设计阻塞，可进入测试先行实施**。这是只读方案复核，不是实现、原生 CI 或公网发布通过。

基线 packages `59549efe1074562928b8ae175faeda8778849e0e` / tree `6237c709d8cca42cc716a7aee796760c3cd867a9`。独立逐项核对第二代清单 10 项，manifest SHA-256 `40aad480d58a1809eba758a81122f6ed056d5fb6333f46fca6cf61008e0bca06`；首稿及首轮意见完整保留。产品 fixture `fc18221282f289088ec9b4282fda8e0bfc67a3b8` 的 Git tree `3254faa77819a1dc7c4b7230b567ec1115be5a7a` 与四个声明文件摘要也独立吻合。

## 问题闭环

- **R1：首稿相同版本 bootstrap 同字节假设已修正。** 现有首代 candidate 强制 bootstrap 为 new，独立 rpmsign 没有保证两次签名的 bytes 相同；同版本对应同 URI，差异会被 planner 正确拒绝。第二代仅在 scratch 分别使用 1.1.1 / 1.1.2，公钥和仓库配置一致，各自 new unsigned 输入真实签名。现有 SemVer 规则支持此测试数据，checked-in 1.1.0、producer 和不可变冲突校验均保持。此结论来自静态源码核对，尚未执行原生测试。[首代约束](https://github.com/WuKongIM/packages/blob/59549efe1074562928b8ae175faeda8778849e0e/scripts/compose-package-site.py#L917)、[bootstrap 读取规则](https://github.com/WuKongIM/packages/blob/59549efe1074562928b8ae175faeda8778849e0e/scripts/build-repository-bootstrap-packages.py#L655)、[冲突拒绝](https://github.com/WuKongIM/packages/blob/59549efe1074562928b8ae175faeda8778849e0e/scripts/plan-object-storage-publication.py#L436)。
- **R2：执行位置已明确。** 新 Linux job 准备并记录 host 原生验签工具，stable adapter 同进程复用 composite verifier；缺工具在启动客户端前拒绝。签名仍用既有 pinned image，host 工具版本不冒称与镜像一致；无新 IPC、相信自报 verified JSON 的旁路或客户端 Docker socket。[原生验证调用](https://github.com/WuKongIM/packages/blob/59549efe1074562928b8ae175faeda8778849e0e/scripts/plan-object-storage-publication.py#L343)。

## 范围和验收判断

唯一生产代码范围是现有 client validator 的显式 stable 分支；其余为 tests-only fixture、只读 CI 和文档。计划不修改 12 producer、频道、密钥、发布器或公共路由。普通模式拒绝测试来源/CA/网络 override；测试二进制 SHA 只属于 TEST ONLY 构建检查，不能回填 candidate 的 null source 或覆盖生产身份。

stable 身份来自显式 descriptor、原 candidate 和 snapshot 路径，不能借根 preview status/manifest 授权；既有 `/repo` 仍属于 preview。mirrorlist 保持实际原生路径，不换 baseurl。[composite 路由](https://github.com/WuKongIM/packages/blob/59549efe1074562928b8ae175faeda8778849e0e/scripts/plan-object-storage-publication.py#L440)。

A/B 要求两份实际不同 payload/index/archive，共用测试公钥；服务端在 A InRelease / A mirrorlist 返回边界切 B，并记录真实 by-hash 和 immutable RPM snapshot 请求、响应摘要及客户端结果。完整旧 A 只证明一致性；调用者要求 B 时必须拒绝旧 A。固定四镜像的真实下载与 fresh installed CLI 均纳入验收，复用原 version/source/release、bench/query/migrate 及只读数据检查。[固定镜像](https://github.com/WuKongIM/packages/blob/59549efe1074562928b8ae175faeda8778849e0e/scripts/validate-production-package-clients.py#L117)、[installed CLI](https://github.com/WuKongIM/packages/blob/59549efe1074562928b8ae175faeda8778849e0e/scripts/validate-production-package-clients.py#L1372)。

私网 A/B 与允许正常依赖下载的 installed 容器分开统计；签名、TLS、摘要、依赖和原 CLI 检查不绕过。计划不声称本地 resolution JSON 是 fresh GitHub 证明，不声称 Docker fixture 证明真实 CDN 原子切换或正式发布。

本轮未修改仓库、运行测试/容器/编译、访问私钥或调用云。实施仍须先红后绿、保留首轮失败、完整相关回归和真实 Linux 四客户端/安装/清理证据；这些属于待执行验证，不是已完成事实。
