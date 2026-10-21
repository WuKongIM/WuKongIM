# 证据报告事实复核

**未发现不一致。** 审阅精确 Git HEAD `4d8f8e21facd5251074ac3a9040fdb8c8d676351`，报告新增事实截至 2026-10-21 11:46Z。

- native 首次主审确有两个 P2（不确定 Docker create 清理、DNF installroot 缓存挂载）和两类真实 installed 负例缺口。34/10/12 均明确为轻量检查，没有改写为尚未执行的 Linux native 验收。两份缺失日志已由 4d8 提交纳入 Git。
- 云 Go/consumer 仅分段无阻塞；8 个实际 Bash/jq 探针使用 authority 替身，报告明确其不能证明真实 GitHub/旧 artifact/Quote 认证。最终 helper 与组合验证未被提前宣称完成。
- 原 unknown 失败保留，旧完整 Quote reserve 明确是保守预算预留，实际账单仍未知；未宣称新采购、派发或正式发布。
- `540bef079` 修改 release-readiness 报告及证据 README；`4d8f8e21f` 仅补两份测试日志。stable-key-preparation 报告在这两提交均未变，独立截至时间仍为 09:06Z，未套用 11:46Z。

顶层 `SHA256SUMS` 的 **3583/3583** 项均存在于上述 Git tree，直接读取 Git blob 校验全部通过；总字节 **63,268,822**，无缺失、摘要不符、重复、危险路径或非普通文件。校验未使用工作区、忽略或未跟踪文件补齐。清单自身 SHA256 为 `c32af9a380f9add39d414542b98fe5950a23cdd7d29ccaaa89d2040eeb9468eb`。

详见 `fact-review.json`（事实及引用字节摘要）和 `git-manifest-verification.json`（Git 对象校验收据）；两个 patch 保存本次增量。未修改源码、运行新测试/原生工具、调用云/API或访问任何私密材料。
