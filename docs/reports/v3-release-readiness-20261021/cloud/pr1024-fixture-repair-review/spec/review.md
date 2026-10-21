Spec 发现：0。缺失/部分要求、额外范围、实现错误均未发现。

- 计划第 23–25 行要求记录实际命令并拒绝错误边界。fixture 第 61–77、121–156、187 行从真实 NUL 命令记录重建 calls，按实际请求裁剪；15 项独立 adapter 探针确认错 start/end/step、未知 query 拒绝，stage/family 分派、partial、timeout、missing、stderr canary 保持。
- 计划第 26 行要求原方法/输入/断言逐字节不变：独立核验全部 20 方法一致；仅该文件 110 增/67 删，无 untracked，collector、Go 入口及规则哈希未变。
- 按计划第 30 行，独立比较 79 场景，先验证双方原 manifest 字节数/SHA，再仅归一 observed_at 及其派生 SHA，其余完全相等。已读首次 focused 两入口通过收据。

源码前后均为 3db0a415…ecc2b1。未读取另一轴、未改源码、未运行完整 fixture/full suite/CI。历史 Linux 超时仍保留，本复核不证明 Linux 门禁恢复。
