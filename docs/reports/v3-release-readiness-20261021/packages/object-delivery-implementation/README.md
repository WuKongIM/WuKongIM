# 原 D 离线交付核心：本地实现冻结，待独立双轴审查

工作树 `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-object-delivery`，分支 `codex/stable-object-delivery`，基线 `983e567f155a982c45ca066e4dc67d33851f0d24`。本次不 commit/push/PR/CI，不调用真实云、外部网络、签名私钥、Keychain 或 NAS。#74 已由父任务确认原生 49 项通过并同 tree 合入；本切片没有重跑或修改该工作树。

## 实现范围

仅五文件：新 `scripts/prepare-object-storage-delivery.py`、新 `tests/test_object_storage_delivery.py`、新 operator 文档，以及 README/Unreleased changelog。

- 调用原 planner 严格重算 v1 preview / v2 composite，核对原 canonical digest，保存原 schema/bytes，不把 v1 伪装成 v2。未知资源保持 null，生产始终 disabled/false。
- 每个原 archive 用现有安全 extractor 展开一次。objects/aliases 按真实 source audit/member取字节，generated mirrorlist 精确 UTF-8；物化前后重验 planner/输入。B路径的A包保留A来源。
- exact URI 请求表：entrypoint、immutable_aliases、原 snapshot；root映射preview index，/repo默认preview，cache分类保持原值。没有prefix/regex全路径改写，不声称真实CDN规则数量或语义已验证。
- 函数级 in-memory seam 演练 create-only Put/GET、同字节冲突/未知超时恢复、全量对象确认后激活、旧受管路由 diff/显式删除选择、禁止未知配置、源/控制/config漂移、部分激活、刷新/回源/客户端失败。不存在可装载真实SDK的CLI入口。
- 复用原 verifier 的调用 kwargs；保留其 receipt 及 false 字段，外层只可能 simulated_complete/incomplete。记录已经观察到的对象和配置副作用，不假称回滚或原子切换。

## 测试先行与结果

适用规则及已批准五个窄 seam 见 `rules-freeze.json`。完整16项失败库存先于实现冻结。按用户要求先写全部相关测试，明确偏离 TDD 技能的逐项切片默认。

`first-red.json/log`：实现文件尚不存在时，20个测试方法全部失败。原始测试 bytes 留在 `first-red-tests.py.txt`。实现过程中修正两个测试 fixture事实错误（已记录 `fixture-correction.txt`）：默认 source SHA 本来是全0，负例改为d；原 RPM文件名是下划线格式。没有放松验收断言。

- 新20项：通过，7.224秒，零跳过。
- 原 planner/composite 与新核心直接相关69项：通过，12.713秒，零跳过。
- `tests/contract.sh`：控制校验及4项现有workflow合同通过。
- `git diff --check` 与CLI `--help`：通过。

`simulation-examples.json` 保存实际本地预览：preview成功、composite成功及部分激活incomplete；只有小型归一化回执和跨代alias源绑定，没有包、缓存、私钥。测试复用既有structural composite fixture的native-verification seam，因此不证明密码学签名或真正公网客户端。

## 明确未完成

原 D 真实 OSS/CDN adapter、wire配置等价编译/实际provider限额、fresh GitHub/current-control预检、生产激活控制、云资源身份/权限/证书/DNS与公网四客户端运行仍未接通。当前代码不是 production uploader。¥100/7天资源提案未获批准，不能使用ECS预算替代。

本地request表上限是资源界限，不是声称Alibaba支持10000条规则；没有开始规则合并或猜API。Mac保管与独立生产公钥沿用既有决定，没有新增保管要求。

精确文件SHA/patch在 `implementation-freeze.json` 与 `implementation.patch`；冻结后请由根任务安排标准/规格两轴独审，再决定后续改动。其余 tracked 文件与基线一致，producer/channel/trust/Pages未改变。
