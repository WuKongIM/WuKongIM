# Mac stable 密钥已生成并完成恢复验证

按用户“放 Mac 或极空间”选择，采用 Mac 本地保管。实际目录为 `/Users/tt/Library/Application Support/WuKongIM/stable-signing`，本机钥匙串账户标签 `tt`，两项服务 `WuKongIM-Stable-APT` / `WuKongIM-Stable-RPM`。账户标签不是另行取得的组织负责人声明。未访问或复制到 NAS，未配置 CI 私钥，未发布软件包。

15 项 fake-command 测试通过后，先执行一轮 TEST ONLY 真实演练（29.524 秒），再用完全相同 SHA 的一次性脚本生成两套独立 production 身份（29.142 秒）。两轮均成功；TEST ONLY 的两条精确钥匙串项和任务临时私钥树已清理，收据保留，不承诺介质级安全擦除。

每套为 RSA3072 certify-only 主钥、current/next 签名子钥，期限分别365/90/180天。恢复从保留的加密完整备份开始，在新的临时 home 核对三角色私钥；current 和 next 各完成错误口令拒绝、正确签名及独立公钥 SHA256 验签；主钥仅在恢复 scratch 中完成新的自签证明。原备份和公开证书未被改写。之后现有签名材料 validator 验明仅 current 的签名输入。

公开生产主钥：

- APT：`F68735072235EDC3BEBA83A55F19C486C2108B64`
- RPM：`4C76B7B37EBDAF05EB44976F96EC1B2D5976F45D`

当前签名子钥分别到期于2027-01-19 08:08:49Z、08:09:04Z。所有完整/16位/8位指纹与两个 preview 家族均不冲突；具体current/next指纹、期限、公开证书哈希和验证结果见 `production-public-receipt.json`。这证明本地生成/恢复及原生OpenPGP兼容性，不代表 stable 控制已注册、Linux生产包已签发或公网渠道上线。

归档仅包含脚本、测试、公开收据、两份 production 公钥及公开nonce/签名证明。加密私钥、current导出、吊销材料及口令没有复制到本证据目录。脚本原参数/边界和官方依据保留于 `README.pre-execution.md`；原初稿执行状态已由本页更新。
