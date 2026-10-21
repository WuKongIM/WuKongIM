# Stable 签名密钥准备单

2026-10-21 状态：**用户已明确目前没有 stable 公钥/独立密钥；现有目录是 preview 密钥备份。** 本单只准备流程，没有生成、导入、读取或上传任何私钥。候选测试通过不替代签名渠道的准备。

**最新保管决定：用户明确要求“就放 Mac 或极空间里”，不再要求离线或硬件环境。** 本任务据此采用 Mac 本地保管与本地签名，极空间可作备份；同步修改下述旧合同和工具，保留独立 stable 密钥及 stable 私钥不进入 GitHub CI 的边界。此决定不是正式发布授权。下文离线/硬件材料只作为前期方案背景，不能再作为必须采购设备或等待离线环境的阻塞。见[直接用户决定](v3-release-readiness-20261021/packages/stable-local-custody-decision.json)。

## 已核实的现有材料

用户指定目录的 README 中，APT、RPM 各自 primary/current/next 共六个公开指纹都与 `WuKongIM/packages@feb7f21503d35133cf23df7d7ab250c5d4129e71` 的 `manifests/preview-signing.json` 相同，并含 preview 标记。备份文件的私钥和吊销内容未读取。它可以属于已有 preview 恢复材料，不能据此确认备份可解密/恢复，不能作为独立 stable 密钥。

当前仓库 [release-contract](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/docs/release-contract.md) 写明 stable 私钥不进入 GitHub-hosted CI，采用离线或硬件签名，并与 preview 使用独立密钥。按用户最新指令，将明确修改其中的保管方式；Mac 本地模型不能标记为已经完成离线签名。preview 已有在线工作站、加密备份和操作系统凭据存储的约定，可复用其本地工具而不复用密钥。

## 已选择 Mac 本地保管

使用 Mac 上独立的 stable 目录和本地签名工具；现有 preview 备份保留原身份。不要求外置盘、第二台电脑、硬件密钥或断网仪式，也不再就保管位置重复询问。公开证书、指纹和验签收据可进入仓库；私钥、口令和恢复材料留在本地保管流程。

工具仍须满足 APT/RPM 算法与证书兼容要求，并通过 TEST ONLY 演练；无需用户在聊天中提供私钥、口令或恢复材料。

## 极空间 NAS 的用途

用户已说明当前电脑连接了极空间，可用于存储。它可以作为备份位置；本地工具完成备份后再按明确目标路径保存。此次选择允许联网 Mac 保管，不再将 NAS 是否离线作为放行条件。没有访问 NAS 或写入任何备份。

用户进一步确认只有 Mac 与极空间、没有外置介质，随后明确选择本地保管。[早期环境方案](v3-release-readiness-20261021/packages/stable-environment-options/report.md)保留为历史资料；其中待离线设备/仪式的状态已被上述选择取代。准备单不把“有 NAS”记录为已完成备份或恢复演练。

## 当前实施顺序

已核实[最短实现路线](v3-release-readiness-20261021/packages/stable-local-signer-route/local-signer-shortest-route.md)：现有 family signer 已支持 stable 路径，并在同一调用内完成 RPM 签包、生成元数据、签 repomd 及验签。**不再实施先前提出的 descriptor/request/response/session ledger 系统。** Mac 可运行现有固定 Linux 工具容器，是否就绪须实际检查，不等于要求新增设备。

1. 给既有 signer CLI 暴露 validator 已支持的本地私钥文件与口令文件参数，保持原 CI 环境变量输入兼容；先跑失败用例，再用真实 TEST ONLY 小型 DEB/RPM 演练，确认签名与独立公钥验签有效。
2. 在 Mac 独立 stable 目录分别生成 APT、RPM 身份，保持与 preview 分离。冻结 primary/current/next 拓扑、算法和有效期；RPM 全拓扑遵守既有 RSA3072/4096、SHA-256 兼容合同。保管采用本地文件、加密备份和现有操作系统凭据工具，完成一次恢复核验。
3. 只将完整公开证书和 primary/current/next 指纹纳入包仓库；实际检查 public-only、算法、有效期和跨家族/频道唯一性。生产秘密和恢复材料保存在 Mac；可随后复制备份到用户指定的 NAS 目录。
4. 接入 stable control、bootstrap、审计、快照和 OSS/CDN 发布路径，再完成精确目标版本的四发行版公网 APT/RPM 安装及 CLI 验证。本地签包成功仍不等于正式版发布完成。

## 当前可继续与待确认

已完成：来源分类 → provenance → native metadata 的只读预检与真实 Linux fixture 验收，[PR #67](https://github.com/WuKongIM/packages/pull/67) 已合并；合并后 main 检查通过，没有部署。

无需生产密钥的后续实现：现有 preview 的离线运输计划器 [PR #68](https://github.com/WuKongIM/packages/pull/68) 已经用户批准合并为 `45619df14`，真实 beta.24 快照的结构/字节验证、373 项完整测试及 TEST ONLY 原生包 CI 已通过；它不认证签名或执行上传。客户端切换演练与 stable 本地签名的原生 TEST ONLY 验证仍待完成；本准备单不是已完成签名系统。

待落实：将用户已选定的 Mac 本地模型写入包仓库合同，完成本地工具及 TEST ONLY 签包/验签后，再生成独立 stable 材料并接入渠道。当前没有生产 stable 密钥，不再等待外置设备或离线环境。不会用复制/改名 preview 证书来满足独立 stable 身份。
