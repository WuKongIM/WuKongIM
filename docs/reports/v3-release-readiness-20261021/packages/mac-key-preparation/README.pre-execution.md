# 一次性 Mac stable key preparation（待 root 审查，未执行）

这是 `/tmp` 中的单次操作脚本，不修改产品/包仓库、不发布、不上传密钥、不建立交换协议。
当前仅写好失败库存、fake tests、实现和说明，并用 Python `compile()` 检查语法；
**未运行测试、GPG、Keychain、Docker 或任何真实生成。当前不能称 red/green 或原生验证通过。**
测试初稿先于实现保留在 `tests-before-implementation.py.txt`，Keychain 精确路径绑定的
增量测试也在对应实现前留存。第一次实际 fake tests 和 TEST ONLY 演练等待 quiet 结束。

## 实际操作范围

默认新目录：`~/Library/Application Support/WuKongIM/stable-signing/`（0700）。
默认两项服务：`WuKongIM-Stable-APT` 和 `WuKongIM-Stable-RPM`，account 为 `getpass.getuser()` 的本机账户标签（本机已知为 `tt`），不据此声称组织保管负责人已确认。
先读取 default-keychain 的公开路径，之后存在性检查、创建和读取都绑定该精确 keychain。
如果其路径不能表示为简单 ASCII token，本一次性脚本直接拒绝，由 root 查看实际路径；
没有泛化交互命令转义系统。

每族用独立随机 64 字符十六进制口令（256 bit 随机量），先写本次新 Keychain 项并核对，
再创建 RSA3072 certify-only primary365d/current sign90d/next sign180d。不读取旧 preview
私钥；只读取公开 preview fingerprint 清单做完整/16/8hex 去重。秘密通过 stdin 传 GPG
及 security，不进入 argv、stdout 或公开回执。GPG secret packet 保留口令保护；base64
仅编码，不当作加密。公开证书与原 backup 都在 primary scratch 证明前冻结。

每族保留（全部处于 0700 根目录中，文件0600）：

- `apt/` 或 `rpm/` 下的 encrypted `full-secret-backup.gpg`、`current-secret-subkey.b64`、
  `revocation.rev`。完整备份含 primary/current/next；签名导出用 exact `CURRENT!`。
- `public/` 下的完整公钥和随机 nonce/签名证明，可由 root 另行分享公开部分。
- 根下 `public-receipt.json`：仅公开身份、指纹/有效期、哈希和结果；stdout 也仅输出此类 JSON。

完整恢复从 retained full backup 进入新的短路径临时 home，核对三个 local private roles。
current 与 next 各做错误口令拒绝、正确口令 nonce 签名，再在独立仅公钥 home 按 exact
fingerprint/SHA256 验签。primary 仅在恢复 scratch 上从365d改到366d，核对新有效期
及可验证 SHA256 自签；不更改 retained backup 或生产公钥。三角色证明后，current-only 由精确
SHA 绑定的已有 validator 验证（native I/O 仅通过本地有时限 runner；验证逻辑未改）。

## quiet 结束后的验证顺序，由 root 执行

先跑 fake tests（只用假命令、临时普通文件，不访问 Keychain/GPG）：

```sh
python3 -B /tmp/wukongim-v3-readiness-20261021/packages/mac-key-preparation/test_prepare_stable_keys.py
```

初始红灯可在独立 `/tmp` 目录放入 `tests-before-implementation.py.txt` 作为测试文件、
不放实现，验证原缺失入口失败；保留原始结果。现有代码通过与否必须由实际执行证明，
不能由静态编译推断。

之后先审查脚本/实际 native tool 身份，再做一次新路径的 TEST ONLY 原生演练：

```sh
python3 -B /tmp/wukongim-v3-readiness-20261021/packages/mac-key-preparation/prepare_stable_keys.py --execute --test-only --output /tmp/wukongim-stable-keyprep-TEST-ONLY-20261021
```

**这个 TEST ONLY 命令也会创建两条新 Keychain 项**，名称包含 TEST-ONLY 和输出路径摘要；
它不会使用生产两项名称或默认生产目录。生成临时目录已存在则拒绝；请不要复用旧输出。
演练成功后，root 再决定生产执行（无须再次审批用户已选的 Mac 存储位置）：

```sh
python3 -B /tmp/wukongim-v3-readiness-20261021/packages/mac-key-preparation/prepare_stable_keys.py --execute
```

没有 `--execute` 只返回 `executed:false`，不执行操作。Keychain 解锁/UI 提示应由操作员处理；
本脚本不解锁、重置 Keychain 或改访问权限。现有稳定目录/服务不覆盖，不提供自动重生/重试。

## 已知边界，不能提前声称完成

- `security -i` 一进程只有一条 add 命令、恰好末尾一个换行，然后 EOF；不能附加空行/help
  掩盖最后状态。不用 `-U` 或 `-v`。创建成功后仍须对本次精确 item 执行 `find -w` 内存比对。
  元数据 probe 的44仅代表未找到可见项，Apple旧实现会折叠部分搜索错误；真正防覆写边界是
  add 不带 `-U` 的原子 Create。显式 search-creation 错误会拒绝。
- Native runner 的300s是每次调用时限，2MiB是返回数据长度检查，**不是流式内存限额**；
  输入均为本脚本固定的本地原生命令/本次小型密钥。不能宣称已经验证所有 native 输出/取消行为。
- 异常不自动删除 persistent target 或新 Keychain 项；公开收据保持 incomplete，原始阶段错误
  与 cleanup 问题一并保留。root 应按 receipt 精确身份检查，不要盲目重跑或广泛删除。
- normal/SIGINT/handled SIGTERM 会尝试关停自己的 scratch GPG homes；SIGKILL/宿主丢失可能
  留下临时材料，不能宣称硬终止零遗留，也不能承诺 Mac/SSD 删除实现安全擦除。
- Python 明文副本无法保证全部擦除；可变口令缓冲最后尽力清零。不会把测试密码或测试密钥
  转为生产材料。完整生成、恢复、Keychain、验证兼容性全部等待实际 TEST ONLY 证据。

## 公开依据

- [GnuPG key management](https://www.gnupg.org/documentation/manuals/gnupg/OpenPGP-Key-Management.html)：
  quick-gen/add-key、loopback/passphrase-fd、quick-set-expire 的公开参数语义。
- [GnuPG DETAILS](https://github.com/gpg/gnupg/blob/master/doc/DETAILS)：colon 签名状态、issuer fingerprint、hash 字段。
- [Apple security.c](https://github.com/apple-oss-distributions/Security/blob/main/SecurityTool/macOS/security.c#L1094-L1137)：
  interactive 逐行执行并返回最后命令结果，verbose 会回显参数。
- [Apple keychain_add.c](https://github.com/apple-oss-distributions/Security/blob/main/SecurityTool/macOS/keychain_add.c#L173-L179)：
  无 `-U` 不进入 update。
- [Apple keychain_find.c](https://github.com/apple-oss-distributions/Security/blob/main/SecurityTool/macOS/keychain_find.c#L423-L456)：
  item-not-found 返回及相关搜索错误折叠限制。
