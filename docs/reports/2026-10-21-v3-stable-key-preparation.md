# Stable 签名密钥准备单

2026-10-21 状态：**用户已明确目前没有 stable 公钥/独立密钥；现有目录是 preview 密钥备份。** 本单只准备流程，没有生成、导入、读取或上传任何私钥。候选测试通过不替代签名渠道的准备。

## 已核实的现有材料

用户指定目录的 README 中，APT、RPM 各自 primary/current/next 共六个公开指纹都与 `WuKongIM/packages@feb7f21503d35133cf23df7d7ab250c5d4129e71` 的 `manifests/preview-signing.json` 相同，并含 preview 标记。备份文件的私钥和吊销内容未读取。它可以属于已有 preview 恢复材料，不能据此确认备份可解密/恢复，不能作为独立 stable 密钥。

仓库 [release-contract](https://github.com/WuKongIM/packages/blob/feb7f21503d35133cf23df7d7ab250c5d4129e71/docs/release-contract.md) 要求 stable 私钥不进入 GitHub-hosted CI，采用离线或硬件签名，并与 preview 使用独立密钥。现有 preview 在线工作站保管模型不自动成为 stable 的离线模型。

## 先确定保管环境与负责人

建议由明确负责人在经审阅的离线环境生成并保管新密钥；若已有受支持的硬件密钥设备，则采用该设备的生成、备份与签名仪式。合同并未要求第二台或专用电脑，专机只是隔离建议。当前联网工作站上的现有 preview 备份不能直接转为独立 stable 根。

需要确认的只有实际可用环境和负责人；不需要给 agent 私钥、口令、吊销证书、恢复种子或硬件 PIN。选择具体硬件时再核对 RPM 所需 RSA 能力，不能仅凭“支持 OpenPGP”判断兼容。

## 极空间 NAS 的用途

用户已说明当前电脑连接了极空间，可用于存储。它可以作为**加密备份的一个存放位置**；联网 NAS 本身不能替代上述离线或硬件签名环境。备份先在签名环境内加密，再转移到 NAS；解锁因素与备份分开保管。签名仍在选定环境内完成，不能因为 NAS 可用就直接在联网电脑上生成生产 stable 私钥并宣称符合离线模型。

用户进一步确认：**目前只有这台联网电脑和极空间，没有已提供的离线电脑或硬件签名设备。** 这表示签名方案尚未选定并验证，不意味着必须另买电脑。现有 Apple silicon Mac 可考虑独立外置 macOS 环境，密钥介质在回到联网日常系统前移除；外置介质、实际离线启动和密钥隔离流程仍待落实。临时断网也未被合同明文禁止，但不能仅凭断网动作认定仪式完成。详见[一页方案与官方依据](v3-release-readiness-20261021/packages/stable-environment-options/report.md)。具体 NAS 路径、备份保护措施和负责人尚未确认，也没有访问 NAS、读取/迁移现有私钥、生成或上传新密钥。准备单不把“有 NAS”记录为已完成备份或恢复演练。

## 拟实施顺序

1. 在包仓库先提交稳定签名控制与离线请求/返回协议，并用 TEST ONLY 密钥验证。正式生成前冻结 APT/RPM 两个信任根、primary/current/next 拓扑、算法、有效期、轮转和恢复要求。建议沿用现有短期子钥的有效期边界，但它仍需成为受审阅的 stable 控制，不能用 preview manifest 冒充。
2. 负责人在选定离线/硬件环境分别生成 APT、RPM 密钥。RPM primary 与全部签名子钥满足现有 RSA3072/4096、SHA-256 合同；APT 的算法需通过目标客户端验收。每个信任根预置当前及下一签名子钥，指纹和短 key ID 均不得跨家族/频道复用。
3. 负责人将加密备份、吊销材料、解锁因素分别保管，并在隔离环境完成一次恢复或硬件恢复演练。公开收据只记录完成状态、日期、负责人和公开身份，不记录恢复材料。
4. 只导出两份完整公开证书及 primary/current/next 指纹供本任务核验。拟定公开文件名 `keys/apt-stable.asc`、`keys/rpm-stable.asc`；提交前实际检查 public-only packet、算法/大小、认证和绑定签名、有效期、撤销状态、全局指纹/短 ID 唯一性与当前/下一子钥关系。缺失或不兼容时保持 stable 禁用。
5. CI 从精确不可变 source/audit/control 生成待签清单；负责人核对清单摘要后离线/硬件签名。APT 返回对应 Release 的 InRelease/Release.gpg；RPM 返回签后包与随后生成的仓库元数据签名。验收端绑定原 unsigned payload、NEVRA、源 provenance、实际签署者、签后摘要与控制 SHA，拒绝跨家族或跨频道签名。
6. 使用 TEST ONLY 完整流程与四发行版验收后，再以真实公开证书和签名收据进行生产预置。正式发布仍须完整快照、不可变审计、OSS/CDN 发布与精确版本的四客户端安装/CLI 验证。

## 当前可继续与待确认

已完成：来源分类 → provenance → native metadata 的只读预检与真实 Linux fixture 验收，[PR #67](https://github.com/WuKongIM/packages/pull/67) 已合并；合并后 main 检查通过，没有部署。

无需生产密钥的后续实现：现有 preview 的离线运输计划器 [PR #68](https://github.com/WuKongIM/packages/pull/68) 已经用户批准合并为 `45619df14`，真实 beta.24 快照的结构/字节验证、373 项完整测试及 TEST ONLY 原生包 CI 已通过；它不认证签名或执行上传。客户端切换演练、stable 签名请求与回执协议及 TEST ONLY 测试仍待完成。协议和 stable 公钥校验器尚未实现，本准备单不是已完成签名系统。

待落实：满足合同的离线或硬件签名仪式，以及实际保管负责人；可在现有 Mac 上准备合适的隔离方案，不增加第二台电脑采购要求。目前只有联网电脑与 NAS，不再按“稍后提供现有 stable 公钥”等待。实际生成密钥应在冻结的方案得到确认后进行；本任务不会用复制/改名 preview 证书来满足 stable 要求。
