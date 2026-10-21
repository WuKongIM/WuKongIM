# Mac stable 密钥准备：最小后续交付（尚未执行）

用户已选择普通 Mac 本地保管，极空间可作备份。无需离线设备或新硬件，
也不需要再次询问该选择。默认独立目录可以是
`~/Library/Application Support/WuKongIM/stable-signing/`；这只是后续工具默认值，
当前未创建该目录、未生成密钥、未读取 preview 秘密／Keychain／NAS。

## 先闭合当前已有签名路径

当前补丁让 family signer 直接接受 validator 已有的本地文件／stdin 输入；
签名、exact current/SHA-256 验证、RPM 先签后建 metadata 和原子输出全部复用。
先在固定镜像里用 tiny data-only 包、临时独立 TEST ONLY APT/RPM keys 走实。
Mac Docker daemon、镜像和 UID/0600 输入权限映射尚未测试；不能把 CLI 文件存在
或 parser 测试通过当成本机 Linux 工具已可用。固定 30 分钟负载期间不运行它。

## 一个本地 key-preparation 工具，而不是新交换协议

建议新增一个窄 Python 入口和对应测试/操作说明，不改变发布工作流或频道：

1. 在全新 0700 目录中准备 APT/RPM 两族独立 stable primary/current/next。
   复用现有公钥拓扑政策、所有 full/16/8-hex ID 跨族及 preview 去重。
   两族均采用 RSA3072 是当前 RPM 兼容策略的简单统一选择；APT 原合同允许的
   其他算法无需改禁令。primary 为 certify-only，current/next 为 sign-only；
   可用 current 90 天、next 180 天（不超过现有 180 天上限，满足 45 天 runway）。
   不得使用测试 factory 的固定口令、UID 或提前生成的 TEST ONLY key。
2. 通过本地交互或操作系统安全随机源建立独立口令，并使用 OS credential store
   的受控本地接口保存；口令不得出现在 argv、日志或公开回执。具体 Keychain
   调用接口须先用假凭证回归，不把现有 `security` CLI 的未验证选项写成完成。
   本步骤不读取或复用 preview 秘密。
3. 导出完整公开证书、只含 current secret 的加密导出、加密 full-secret 备份及
   撤销证书。秘密和撤销材料只进私密目录（0600），public 输出目录只收公钥。
   NAS 只作为可选的加密备份复制目的地；不以可联网 NAS 冒充离线环境。
4. 从本次加密 full-secret 备份导入一个新的本地临时 home，核对完整公开拓扑和
   primary/current/next 的秘密可恢复性；再从恢复后的 home 导出 current-only
   输入，调用既有 validator 作签验自检。后者单独通过不能替代完整备份恢复。
5. 成功只输出不含秘密的公钥指纹／公钥 SHA／有效期和 restore-check 结果，
   保管目录供后续 reviewed stable signing controls 使用。既有目录/不匹配部分
   结果失败，不覆盖或悄悄重生已有 stable key。清理仅限工具自己的临时 home；
   不承诺普通 Mac/SSD 删除能实现零残留。CI 永不持有 stable 私钥。

先测试失败：目标目录存在、错误权限/链接、任一原生命令失败、空/不合规口令、
跨族/preview ID 冲突、缺 next/非 sign-only/RPM算法不符、只恢复 current 而缺
primary/next、备份损坏或口令不符、公开输出出现 secret packets。测试以隔离
TEST ONLY home 为唯一密钥来源；没有生产生成、NAS 访问或发布动作。

这一工具及未来真实 production key generation 是下一项工作。当前 CLI 薄适配
不宣称生成／保管／备份已经完成，也不宣称完整 stable publisher 可用。
