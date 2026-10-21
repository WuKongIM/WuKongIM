**现有 Mac／极空间方案与已提交工具的复用边界（最新用户策略）**

用户已选择常规 Mac 本地保管，极空间可备份。无需外置盘、第二台机器、硬件令牌或断网流程；这属于本次明确批准的保管政策变化，不能称为完成 offline/hardware ceremony。生产私钥仍不进 GitHub CI，APT／RPM 使用独立 stable 证书。当前只做方案，未生成密钥、未读取 Keychain／私钥／NAS、未改变系统。

| 已存在的依据／工具 | 可以复用什么 | 不可声称已完成什么 |
| --- | --- | --- |
| packages `release-contract.md` L348–360 | preview 已接受单操作者在线工作站模型：0700 目录／0600 加密全量备份、独立口令保存在 OS credential store、restore check。stable 可采用同一操作模型并独立目录／证书，不新增多层隔离要求。 | 这些是已记录的操作约定，不证明本轮 stable 密钥／备份已存在。现有 preview 备份不能充当 stable 身份。 |
| `validate-signing-material.py` L396–479、L691–804 | 纯公开拓扑／RSA／有效期校验立即可复用。以后局部复用其加密 current-subkey 导入、错误口令拒绝、签名证明与清理逻辑。 | `validated_signing_session` 会读私钥／口令；它只验证 current-subkey import，**不是全量 primary＋next 备份恢复检查**，也未实现 macOS Keychain 存取。 |
| WuKongIM `scripts/generate-native-package-test-keyring.sh` L6–10、L64–104；packages `tests/test_validate_signing_material.py::TestKeyFactory` | 临时测试目录、GPG 生成／导出结构、独立 APT/RPM、测试 teardown 可作为 TEST ONLY 回归素材。packages TestKeyFactory 已有带 TEST ONLY 口令的 full-secret／subkey 导出。 | shell 工具明确一日、无保护测试子钥；不能直接改名字当生产生成器，且不满足生产 current/next 寿命拓扑。测试工厂的固定口令和私钥输出不得进入生产。 |

在两个仓库所查已提交的 `scripts/`／`.agents/` 及 packages 源文件中，未发现可直接调用的**生产**“生成 stable＋macOS Keychain 写入口令＋加密全量备份＋完整 restore-check”一体脚本。关键字检索中生产 Keychain/backup/restore 要求出现在合同，执行代码只有测试工厂及 current-subkey 验证器。因此切片 A 只做公共证书／请求预检，切片 B 只做 TEST ONLY 本地签名回执；生产生成工具不能以“已有脚本”名义贸然执行。root 后续选择独立 Mac 私密目录并推进生成即可，本轮无需再次询问位置。

需同步的是 `release-contract.md` L362–365、`keys/README.md` L41–44 的 preview CI 适用范围和根 `README.md` L73。现有验签器未强制要求专用机器或断网；保留 exact signer、独立 key、RSA/SHA-256、有效期和 source/control/audit 一致性校验即可。此轮不修改 stable enabled、CDN 或正式发布流程。

源码固定链接：[合同](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/docs/release-contract.md#L348-L365)、[签名验证器](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/validate-signing-material.py#L691-L804)、[TEST ONLY 工厂](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/tests/test_validate_signing_material.py#L124-L225)、[root TEST ONLY 生成器](https://github.com/WuKongIM/WuKongIM/blob/ed705c0fbffdea485bbb65608d6e340bb049d97e/scripts/generate-native-package-test-keyring.sh#L6-L10)。本机仅保存方案和源码摘要；冻结详见 source-freeze.json。
