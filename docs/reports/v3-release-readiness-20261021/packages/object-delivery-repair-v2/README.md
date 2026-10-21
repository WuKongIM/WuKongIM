# Original D 离线交付核心 — Spec v1 两项 P2 修复（v2）

源码冻结于 `983e567f155a982c45ca066e4dc67d33851f0d24`，分支 `codex/stable-object-delivery`。
此目录只增加 v2 证据；原 `object-delivery-implementation/` 和 v1 Spec/Standards 报告保持不动。
完整五文件 patch、source 副本、规则与 protected supporting source 摘要均单独冻结。
`implementation-freeze.json` 是复审源锚；完整 suite 的最终结果使用独立回执，源锚不改写。

## 修复范围

- 当前 activation-control 由独立 fake `read_activation_control()` 读取，不接受 expected 参数。
  PUT 前和 apply 前分别比较 reviewed SHA；原历史 source/candidate/seal 控制字段保持独立。
  缺失、null、格式错误、不同 SHA、非 simulated 观察都会阻止写入；PUT 后仅当前控制漂移
  会保留已有对象 GET 证据并阻止 apply。两次实际观察写入模拟回执。
  dry run 仍可保留 null 控制锚；该未知值无法满足模拟写入的控制围栏。
- stable 原客户端回执的 `transport_inputs_sha256`、`transport_plan_sha256` 必须精确绑定
  当前 preparation 的输入 bytes 与原 planner digest。相同 stable candidate 的另一份
  composite（preview 或 retain 不同）也会拒绝。只增加外层比较，原回执内容和摘要保留。
- 现有 v1/v2 planner/schema、对象物化、路由优先级、四客户端接口均不改。
  README 保留 v1 引导；交付说明和 Unreleased 条目解释两项新检查。

## Tests-first 证据

`failure-inventory.md` 先列出 8 种失败/成功边界；随后补完全部 6 个测试方法和 fixture。
`first-red-tests.py.txt` 是首红前的测试原文。`first-red.json` 证明实现仍是 v1 SHA，
26 个方法首红出现 20 个断言失败子项和 1 个缺少观察字段错误，明确复现两项 P2。
第一次实现后 focused：25/26 方法通过，单一断言误把 c×64 snapshot digest 当作 c×40
控制 SHA。已按既有测试本意改为比较真实控制字段，详情见 `fixture-correction.md`。
没有修改实现、添加新测试或盲重跑 focused。相关 suite 首跑覆盖全部 26 个 focused 方法。

- related：75 项通过，无 skip；`related-tests.json` / `.log`。
- 合同：生产控制检查与 4 项 source-preflight 测试通过；`contracts.json` / `.log`。
- 完整 Python suite 首跑：554 项，553 通过、1 项跳过，123.035 秒，exit 0。
  唯一跳过是既有 real RPM build tools unavailable；未改变环境或文件描述符限额。
  回执为 `full-suite-first-run.json` / `.log`；结尾的 false JSON 是预期失败路径 fixture 输出。
  平台、原文件描述符限额、未修改环境均在 start/final 回执记录。
- 源码 diff 与 patch 反向适用性检查通过；最终结果汇总见 `completion.json`。

## 边界与公开归档

所有模拟回执的 production/publication/cloud/GitHub provenance flags 均为 false。
资源 identity 仍 unknown；stable disabled/object_storage_required 不变。
本 PR 范围不包含实际 OSS/CDN/GitHub adapter、真实上传、生产激活、签名或公共客户端执行。
没有访问生产私钥/Keychain/NAS、云 SDK/云 API，没有提交/push/PR/CI 或更改权限与 workflow。
完整 suite 包含仓库既有 TEST ONLY 本地测试；未重复 49 项真实 Linux native 演练。
未来 PR 自然触发的 native workflow 未被禁用。

仅 `ARCHIVE_ALLOWLIST.txt` 所列小型公开文本可归档；校验见 `SHA256SUMS`。
不要归档工作目录、fixture 缓存、对象/候选 archive、大二进制、临时 TEST ONLY 密钥或环境变量。
