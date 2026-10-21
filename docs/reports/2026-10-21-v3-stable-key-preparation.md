# Stable 签名密钥准备单

证据截至 **2026-10-21 08:40Z（北京时间16:40）**。**Mac独立stable APT/RPM生产密钥已生成、完成加密备份恢复及签名验证；生产渠道仍未上线。** 本地签名输入和保管约定的PR #69已合并，公开证书登记PR #70经批准已合并，两项PR CI和合并后400+3项检查均已通过，部署步骤跳过。没有向NAS复制备份、没有将stable私钥放入CI，也没有发布正式软件包。

## 保管决定和历史材料

前期用户指定目录只有preview备份，README的APT/RPM primary/current/next六个公开指纹均匹配 `packages@feb7f21503d35133cf23df7d7ab250c5d4129e71` 的preview manifest；此核对没有读取preview私钥或吊销文件，也不证明旧备份可恢复。当时“没有独立stable密钥”的结论正确，现由本次新建身份更新，不能继续作为当前阻塞项。

用户随后明确要求“就放Mac或极空间里”，本任务按此选择采用Mac本地保管和签名，极空间可作备份；不再要求外置盘、第二台电脑、硬件密钥或断网仪式，也不将在线模型标为离线签名。见[直接决定](v3-release-readiness-20261021/packages/stable-local-custody-decision.json)。早期[离线/硬件环境方案](v3-release-readiness-20261021/packages/stable-environment-options/report.md)仅作为历史背景；独立stable身份、APT/RPM验签和CI不托管stable私钥的边界继续保留。

## 工具交付已完成

[PR #69](https://github.com/WuKongIM/packages/pull/69)将Mac保管约定写入仓库，并把既有validator的私钥文件/口令文件输入暴露给family signer，保留原CI环境变量输入；APT仍签原Release，RPM仍在同一受控调用内签payload、重建metadata、签repomd并验签。不新增descriptor/request/response/session ledger系统。

head `c21736edcf67bac4b5c1c495d1cd18490b1d154a` 的387项Python测试、3项workflow检查、真实metadata7/7与本地签名10/10通过，五条正向路径在删除测试秘密后独立公开验签，完整TEST ONLY APT/RPM仓库构建、签名、验证及安装实际执行。首轮失败及修复历史保留，最终通过只绑定该head，见[验收收据](v3-release-readiness-20261021/packages/pr69-ci-c217/README.md)。

用户批准后，#69于08:19:40Z正常rebase合并为 `8b3ae9a08b943e51aa614779a58baab204c584ca`，完整tree与已验收head相同。[合并后main CI 39568424562](https://github.com/WuKongIM/packages/actions/runs/39568424562)于08:21:30Z首次成功，387+3测试；Pages配置、上传及部署均跳过。没有启用频道或把TEST ONLY结果报告成生产包签发。

## Mac生产身份与恢复验证

一次性本地工具先经过15项fake-command回归和真实TEST ONLY演练，再以完全相同脚本SHA执行生产准备，08:09:17Z完成。生产执行exit0、29.142秒；操作脚本、公开证书和公开验证收据见[Mac准备记录](v3-release-readiness-20261021/packages/mac-key-preparation/README.md)。

每个家族使用独立RSA3072 certify-only主钥及current/next签名子钥，期限分别365/90/180天；所有完整、16位和8位指纹与另一家族及preview均不冲突：

| 家族 / 角色 | 完整公开指纹 | 到期时间（UTC） |
| --- | --- | --- |
| APT primary | `F68735072235EDC3BEBA83A55F19C486C2108B64` | 2027-10-21 08:08:48 |
| APT current | `870709103947DCA5C19D76A817A8FB409BB05E51` | 2027-01-19 08:08:49 |
| APT next | `46F98702B783D39F2009B75B3528CA2EEEFC54DF` | 2027-04-19 08:08:50 |
| RPM primary | `4C76B7B37EBDAF05EB44976F96EC1B2D5976F45D` | 2027-10-21 08:09:03 |
| RPM current | `7C090D6807F29854C25824B0C620CF61C8256051` | 2027-01-19 08:09:04 |
| RPM next | `333EB560B893835062CE28332022BAA4F4BE83D1` | 2027-04-19 08:09:05 |

恢复从保留的加密完整备份开始，在新临时home核对三角色私钥库存。current和next各完成错误口令拒绝、正确签名和独立公钥SHA-256验签；primary只在恢复scratch中完成新的认证自签证明，保留的原证书及备份未改写。随后现有current-only签名材料validator也通过，不能把后者单独说成完整primary/next恢复证明。完整公开结果见[生产收据](v3-release-readiness-20261021/packages/mac-key-preparation/production-public-receipt.json)。

私密本地目录为 `/Users/tt/Library/Application Support/WuKongIM/stable-signing`。两个独立口令项保存在Mac登录Keychain，服务名 `WuKongIM-Stable-APT` / `WuKongIM-Stable-RPM`，状态均为created_and_verified；账户标签 `tt` 是本机账户信息，不冒称另行取得的组织负责人声明。归档只包含公钥、公开签名证明和公开收据，不含私钥、口令、current秘密导出或吊销材料。TEST ONLY临时私钥树及对应的两条测试Keychain项已清理，不承诺介质级安全擦除。本报告更新只读取这些公开证据，没有访问私密目录或Keychain。

## 公钥登记已验收，频道保持禁用

[PR #70](https://github.com/WuKongIM/packages/pull/70) head `0c26496b624058db813aa1206a7910f0abae6b13` 登记一份public-only manifest及两份完整公开证书，复用公钥validator检查角色、算法、期限和跨家族/preview的完整及短指纹唯一性。历史控制快照可整套缺省，新套件部分缺失会拒绝。现有channels、preview、公网publisher及生产workflow未改。

08:28Z的draft及[08:32:32Z复核](v3-release-readiness-20261021/packages/pr70-review-state.json)的ready待批准状态保留为历史。用户随后批准，08:34:33Z观察到合并成功：merge `51bfedbe88c78758fa05bfb87b13541b4bca4796` 的完整tree与评审head一致（`2c4f7d3a9b24a6f67c18b114b8a5c5f1f5f6fa5e`）。[合并后39570313491](https://github.com/WuKongIM/packages/actions/runs/39570313491)于08:36:26Z首次成功，400项Python测试102.131秒及3项workflow检查通过，Pages配置、上传、部署均跳过。[39568724449](https://github.com/WuKongIM/packages/actions/runs/39568724449)首次通过400项Python及3项workflow检查，Pages部署跳过；[39568724312](https://github.com/WuKongIM/packages/actions/runs/39568724312)于08:27:57Z首次通过metadata7/7、本地签名10/10及完整TEST ONLY仓库构建/签验/安装。五条正向路径在测试秘密删除后独立验签。生产私钥没有进入这些CI；这是公开身份登记验收，不是stable生产发布。

## 剩余交付

- 继续接入stable来源控制、bootstrap、审计、快照及OSS/CDN发布路径，并验证跨代缓存闭包和失败/回滚行为。现有preview运输计划器只输出plan_only，不能当生产发布器。
- 在产品候选和既定RC验收条件满足、目标版本确定后签发精确正式版本，完成四发行版公网APT/RPM安装及CLI验收；本地密钥恢复和TEST ONLY签包不替代这些条件。

NAS仍可用于后续备份，但此次未访问、未复制；不把NAS离线性或新增硬件当放行条件。stable仍disabled/object_storage_required，当前总发布结论仍为NO-GO。
