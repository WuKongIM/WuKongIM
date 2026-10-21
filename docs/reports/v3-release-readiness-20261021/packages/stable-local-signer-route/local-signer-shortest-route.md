# 收窄后的 Mac 本地签名最短路线（只读评估）

**结论：复用现有 family signer，不新增请求／签回／session ledger 框架。** 用户已直接授权普通 Mac 本地保管；NAS 可用于加密备份。当前工作树仅有 README、keys/README 和 release-contract 三处 custody 草稿（23 行增加、9 行删除）。没有实现脚本、没有测试运行，没有读取私钥／Keychain／NAS，也没有启动 Docker。

## 已有路径足以承担本地签名

固定源码 packages `45619df14b931ef0da3eae7a6e3d5402307daa60`：

- `scripts/sign-package-family.py:827–862` 将输入复制到自己创建的 stage，在单个 `validated_signing_session` 中签名／验证，最后原子输出并返回既有 family receipt。异常清理本次 stage。`870–894` 的 CLI 未将路径硬编码为 preview。
- `sign_apt:322–361` 可使用 `--apt-release dists/stable/Release`；生成 InRelease 和 Release.gpg，核验 exact current signer、SHA-256，并逐字比较 clearsign 原文。
- `sign_rpm:631–778` 可使用 stable RPM 目录；先核对三个 allowlists 闭包和 unsigned 状态，签新 RPM，核实际子钥，再从已签 RPM 重建 metadata，最后签并验 repomd。没有跨机签回时，不需要另建两阶段交换协议来完成这个顺序。
- `scripts/validate-signing-material.py:691–804` 使用单族 current-subkey 导出与独立临时 GNUPGHOME，并清理 agent/home。它不接受 full primary+next material 作为 current signer，也不等于完整备份恢复检查。
- `manifests/signing-toolchain.json` 固定 image `ghcr.io/wukongim/native-package-signing-toolchain@sha256:769d49a4882439a35fdf4497a4b97f09843a9dc92f4074447d48e43b2614d922`，workflow SHA `1db16ff24b066d14cbfd312b2d92e3331d052150`。`toolchain/native-package-signing/Dockerfile` 包含原生 RPM/APT/GPG 工具，默认 UID/GID 65532。现有 workflow `native-package-publish.yml:593–632` 是可类比的只读控制／输入挂载、独立输出、禁网容器调用证据，不运行或修改该 workflow。

## 真正的小缺口及最多两步交付

1. **本地凭证输入薄适配＋原生 TEST ONLY 演练。** family signer CLI 目前只暴露 `--secret-subkey-base64-env` 和 `--passphrase-env`，尽管其 validator 已有互斥 file/env/stdin 私钥输入及 file/env 口令输入（`validate-signing-material.py:130–177,815–845`）。最小代码变更是让 signer CLI 直接暴露同样互斥选项并删除硬设 file=None 的 defaults；保持 Environment 参数兼容、signing session 原样复用。文件模式已经有 0600、单链接、大小和 base64 检查。无需复制 GPG/签名验证逻辑，也无需添加新 schema。Mac runner 仅将单族 current 导出/口令作为私密本地输入送入同一临时容器；镜像、控制及输入用 exact SHA 固定，公开输出与凭证目录分开。是否采用只读私密文件挂载或临时进程输入须用 TEST ONLY 原生演练验证 Mac Docker UID/权限映射，不能预先声称已经可用。

   先写失败例：互斥/缺少输入、权限过宽/链接/错误 base64、错误口令、full primary/next 导出误用、公钥不符、跨族短 ID、existing output、输入改变、原生命令失败均无成功收据。复用 `tests/native_source_metadata_drill.py:41–75` 的 tiny data-only DEB/RPM builder；复用测试 key factory，补充 next 公钥后生成两族独立 TEST ONLY 证书，仅在 scratch 中保留秘密。用真实 gpg/rpmkeys/rpmsign/createrepo_c 调用现有 signer，独立公钥验证 APT 两签、signed RPM 和 repomd；不编译产品、不安装／执行 payload、不启用 stable。既有 `tests/test_sign_package_family.py` 的 RPM 是 fake，不能当原生 RPM 验收。

2. **生产 Mac 密钥准备／恢复与同一 runner 适配。** 原生 TEST ONLY 路径成立后，准备单独的本地生产目录、独立 APT/RPM stable primary/current/next、公钥导出、加密完整备份／撤销证书及恢复检查，再由 reviewed public fingerprints 驱动同一 signer。仓库尚无可直接复用的一体生产生成＋Keychain＋完整备份恢复工具；测试 factory 的固定 TEST ONLY 口令／UID 不能用于生产。这是下一步具体实现工作，不是要求用户再买设备或接受额外审批。实际生产生成由 root 后续推进，本阶段不访问现有 preview 秘密或 NAS。

这两步只让本地 stable 签名路径可用；stable control/bootstrap/composer/object-storage 发布接入及公共 APT/RPM 验证仍按已有阻塞项完成。既有 preview 默认与所有 manifests/workflows 保持原样；本地签名成功不能冒充完整 v3 发布。

## 未验证且不假装已验证

Docker daemon、固定镜像缓存/获取与在本机执行仍未测试（当前 quiet）。不保证普通 Mac 删除临时文件等于 SSD 零残留，也不把用户接受的本地保管写成离线完成。当前没有 stable production key。所有多机 descriptor/test 草稿已保留在 `superseded-request-draft/`，仅作历史，不再作为实现待办或发布门禁。
