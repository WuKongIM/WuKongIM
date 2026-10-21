**Stable 离线签名请求与回执协议提案 — packages@45619df（2026-10-21）**

这是可拆成两项 PR 的准备方案，未实施、未生成密钥、未运行测试或原生工具。初版仅接受 `test_only`，拒绝生产签名／发布；现有 `channels.json` 的 stable `enabled:false / object_storage_required`、preview 默认及生产 Workflows 均不变。不需要生产签名负责人即可先完成这两个切片，但它们不等于 stable 发布器。

**1. 已有能力与真正新增的边界**

- `resolve-source-release` v1、`verify-source-attestations` v1、`verify-source-package-metadata` v2 已支持显式 `--channel stable`；必须保持 numeric Release／asset ID、七资产 bytes 闭包、tag/source SHA/main ancestry、固定 source workflow provenance。[合同 L89–153](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/docs/release-contract.md#L89-L153) 离线 JSON 摘要本身不证明这些 GitHub 事实；以后生产接入需要独立、可信的预检结果输入和上线前再确认。
- 现有 APT signer 已逐字比较 clearsign 原文，RPM signer 已以独立 key-only RPM DB 找出实际验证子钥，并要求 SHA-256。[APT L322–361；RPM L445–599](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/sign-package-family.py#L322-L361) RPM 目前在同一执行边界签包、生成 metadata、签 repomd；仅检查新签包发生变化，不独立比较签前／签后的主 header＋payload。[L694–778](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/sign-package-family.py#L694-L778) 对外部签回件必须补上这道内容身份校验。
- 现有 family receipt/v1 是公开 JSON 事实，audit receipt/v2 将它与 archive/control/plan 相互校验；这不等于离线操作者对回执的签名。其 schema 字段集合及路径明确限定 preview，不能把它直接改名当 stable 成功。[receipt L724–851](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/package-audit-receipt.py#L724-L851)、[L339–368](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/package-audit-receipt.py#L339-L368)。新协议独立命名，不更改旧 schema。

**2. 精确数据边界（以下是提案，不是已存在的合同）**

复用 canonical JSON 编码（排序 key、紧凑分隔符、UTF-8、末尾换行）、`{path,sha256,size}` artifact 类型、严格未知／重复字段拒绝。仓库目前用 Python `*_FIELDS` 和校验函数定义 schema，没有独立 JSON Schema 目录；不要新增并行的另一套 schema 权威。[composer L148–239](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/compose-package-site.py#L148-L239)

| 对象 | 建议精确字段及约束 |
| --- | --- |
| 外部 session descriptor/v1 | `schema, mode, session_id, context, certificates, inventory`。`mode` 初版只允许 `test_only`；`session_id` 为调用方提供的 32-hex 唯一值。context 绑定 source 的 repository/release_id/tag/version/source_sha；三个预检收据及八文件 attestation evidence 的 digest；packages `control_sha` 与原始控制／工具脚本 bytes inventory；toolchain image digest、workflow SHA、manifest digest；`audit={kind,id,base_id}`。初版 `kind=test_only`、`id` 为合成标识，`base_id=null`，绝不冒充 GitHub Release ID。未来 real audit 分支必须另行实现数值 ID、受保护 control 和 tag 的独立锚定。 |
| 每族 request/v1 | `schema,mode,session_id,request_id,descriptor_sha256,family,phase,parent_verified_receipt_sha256,inputs,expected_outputs`。`request_id` 为新的 32-hex 值；不包含自己的 digest。family 与 phase 为固定组合：`rpm/rpm_payloads`、`apt/apt_release`、`rpm/rpm_metadata`。inputs/expected_outputs 唯一路径排序，角色集合由 phase 固定，禁止命令、URL、任意输出路径。 |
| 每族 response/v1 | `schema,mode,session_id,request_id,request_sha256,family,phase,signer,outputs`。`signer` 只是待核对的 full fingerprint 声明；outputs 为 exact artifact inventory。建议增加 `response.json.asc`：由该族 current 子钥对 canonical response 原文作 SHA-256 detached signature。这是为离线交接新增的设计选择：通过 request digest 把返回 bytes 绑定到 source/control/audit/phase；包签名或未签名 JSON 单独不能证明这一关联。 |
| verifier result/v1 | 由无私钥验证端生成：`schema,mode,request_sha256,response_sha256,response_signature_sha256,verified_signer,verified_outputs,checks`；`checks` 明确记录 `public_key_policy/signatures/content_identity/closure=true` 与 `github_revalidated=false, cloud_written=false, publication_authorized=false`。失败不产出成功回执。|

包内的 descriptor、证书、source 证据全部视为不可信复制件；验证命令另收 `--descriptor` 和调用方已冻结的 `--expected-descriptor-sha256 / --expected-request-sha256`。这两个 SHA 是调用方提供的外部身份锚，并非自动具有密码学信任；只有审核过的来源／控制决定其可信性。包内字段不得替代外部锚；整体联改 request＋response＋archive 而未改外部锚必须失败。初版 descriptor 的 fixture 身份不得输出真实 GitHub provenance 成功。

**3. 公共证书握手，不进入私钥导入路径**

证书先经最大 1 MiB 的单硬链接普通文件校验，再拒绝 secret packet／`sec,ssb`，在新建临时 public-only GnuPG home 中读取实际拓扑。复用 `inspect_public_certificate` 的 public-only 检查以及 `parse_colon_keys`、纯函数 `validate_arguments`/`validate_public_key`：certify-only primary、sign-only current/next/historical、全部 full／短 ID 跨族唯一、30/45/180 天政策、RPM 全拓扑 RSA 3072/4096。独立证书 descriptor 只复用这些公开字段，**不照抄 preview Environment/secret 字段**。[public inspector L159–216](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/verify-production-package-site.py#L159-L216)、[public validator L396–479](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/validate-signing-material.py#L396-L479)

不得调用 `validated_signing_session` 或 `validate_material` 做此预检：前者 L714–715 已开始读取私钥和口令；也不能把 `IsolatedGPG.run` 原样当有 deadline／输出预算的公共工具 runner，它当前没有这些上限。[L249–289、L691–740](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/validate-signing-material.py#L691-L740) 新入口使用空凭证环境、固定工具路径、临时 HOME、无网络；公共原生命令统一 120 秒 deadline，输出写受预算约束的临时文件（不能先无限 PIPE 再检查）。证书含私钥时仅报拒绝，不回显内容，不导入。

**4. 两阶段交接**

1. **阶段一，仅新 RPM payload。** 请求携带原始 unsigned RPM、source receipts／public certificates／控制证明，不带待签 repomd。无签名字段的原生 metadata 查询和源文件 SHA/size 两次校验沿用 source preflight。离线端只在副本上按 exact current 指纹及 SHA-256 签包；返回 signed RPM＋其 response＋response 签名。验证端先验证 response 实际签名者，再验证 RPM 实际签名者及内容身份。不得由 response 自报“validated:true”跳过验证；使用 primary/next/historical、只有短 ID 匹配或 hashed issuer 冒名均拒绝。初版只处理一套新产品 DEB/RPM，不支持 base、轮换、retirement 或 bootstrap 生产内容；收到这些字段即拒绝，不能静默遗漏。
2. **阶段二，先物化 metadata，再签 exact bytes。** 验证端从阶段一唯一已接受 signed RPM 创建 metadata，固定命令复用现有 `createrepo_c --simple-md-filenames --no-database` 行为；无签名、active-only 视图，保留完整 hash/size inventory。请求分别为 APT 与 RPM 两族，两者均绑定同一个阶段一 verified receipt digest。APT 的 DEB 从源复制不变，生成 Packages/Packages.gz/by-hash/Release；只签同一 `Release` 原文形成 `InRelease` 和 `Release.gpg`。RPM 则签已经对最终 signed RPM 计算过 checksum 的 `repomd.xml`。**不能在 RPM 签包前冻结 repomd，也不能接受签名端偷偷重建 metadata。** 同一个 phase 的 metadata bytes 只生成一次并固定；重试不得靠重建时间戳内容伪装成同一请求。
3. **签回后二次无私钥验证。** APT 两个签名都验证为 current/SHA-256，clearsign 提取结果逐字等于原 Release，closed headers、Date、四种 checksum、Packages.gz 解压相等、索引 payload 闭包继续检查。RPM repomd 为 current/SHA-256，三个 primary/filelists/other 的压缩与展开 SHA-256/长度、路径／引用／NEVRA／active 包集合均校验；metadata 指向阶段一已接受的 exact signed RPM SHA，不是 unsigned SHA。[APT L995–1044](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/verify-production-package-site.py#L995-L1044)、[RPM L1213–1330](https://github.com/WuKongIM/packages/blob/45619df14b931ef0da3eae7a6e3d5402307daa60/scripts/verify-production-package-site.py#L1213-L1330)

可信源 builder 已冻结在 `ed7e67d8…`，其文件 SHA 与 trusted-toolchain manifest 一致。它支持 `--apt-suite stable/--rpm-channel stable`，但同时生成的初始 RPM metadata **不是阶段二可签输入**；RPM 必须在签回后按上文重建。它也不是现有 stable composer/bootstrap/public verifier 支持完成的证明。[固定 builder L82–107](https://github.com/WuKongIM/WuKongIM/blob/ed7e67d8f781b4f92872e42116c11b304fe03b5c/scripts/build-native-package-repositories.sh#L82-L107)

**5. RPM 内容身份的最小新增检查**

不能只比文件清单、NEVRA、`PAYLOADDIGEST` 自报值或解包后文件；这些会漏掉脚本、依赖、header 变更。建议新增仅识别已支持 RPM v4 framing 的有界 bytes helper：验证 lead／signature header／padding／main header 边界及文件长度；签前后 **lead 相等、完整 main header 原始 bytes 相等、全部压缩 payload bytes 相等**，仅允许 signature header 及其 padding 变化。记录各部分 SHA-256＋长度和签前／签后完整文件 SHA。原生 rpm 查询和 `rpmkeys` 仍承担格式语义及密码学验证，此 helper 不替代 RPM parser。

依据：[RPM v4 四段布局及 signature 8-byte padding](https://rpm.org/docs/4.20.x/manual/format_v4.html)、[header framing](https://rpm.org/docs/4.20.x/manual/format_header.html)。官方建议用 RPM library；这里额外读 framing 只为比较原始 bytes，不能凭它宣称 RPM 有效。解析不支持的格式、截断、溢出长度或异常区域一律拒绝。主 header 同一性自然包含 NEVRA、依赖和脚本，再用原生 metadata 查询核对目标身份。不能以 `rpmsign --delsign` 后重序列化相等替代，因其输出是否逐字还原尚无证据。新近 `rpmpart` 命令也不能假定已在现有固定 Ubuntu toolchain 中提供。保留旧 RPM 时未来仍须逐字保留、按已审阅 current／有效 historical 核验，不能自动重新签名；该分支不塞入首版。

**6. 重放、资源与失败原子性**

- 每个本地 session 独占注册 `request_id → request_sha256`。外部冻结 descriptor／control／audit／parent receipt／family／phase 有任何不同即拒绝；已接受请求绑定唯一 response digest。相同收据重复验证最多返回 `already_verified`，不覆盖或再次签名；同 ID 不同 bytes／第二份不同签名回执冲突失败。取消／过期／旧 current 的 session 不可激活。这个本地状态不能证明多机器全局防重放；将来生产控制必须统一登记并再次校验 main，不能靠时间戳或随机 ID 冒充线上锁。
- 初版固定上限建议：JSON 8 MiB；公钥／每份签名 1 MiB；metadata 压缩／展开各 64 MiB；单 RPM 1 GiB；单请求传输总量 800 MiB（实际 min 约束）；成员 1,024、包数初版 1。复用既有常量数值时由新协议唯一引用，不修改现有 publisher。每项 lstat／fstat 的实际 size 必须在 hash／读取／解压／工具执行前同时满足声明值和硬限额；不能先完整 hash 再比较。按记录长度累计真实值，总限额包含返回对象及输入，拒绝超限回复；执行环境还须限制临时盘／进程及 deadline。
- directory tree 或 canonical tar 只接受普通单硬链接文件和目录；相对规范路径、唯一 sorted inventory；拒绝 symlink、hardlink、device/FIFO、重复／额外／缺失成员、绝对／`..`／反斜杠路径、输出与输入重合。复用 canonical archive 的安全包装能力及 composer file helpers，但必须另做该 phase 的完整闭包。临时路径由工具独占创建；验证全部成功才原子导出本地结果，失败仅清理自己创建的文件，不覆盖已存在结果。

**7. 先写失败病例，再写实现（两切片）**

| 切片／具体文件 | 验收及必写失败病例 |
| --- | --- |
| **A：公开证书握手＋RPM 签回核验。** 新增 `scripts/offline-signing-exchange.py`（descriptor/request/response schema、public-only cert preflight、prepare-rpm/verify-rpm、独占 session 状态），`scripts/rpm-content-identity.py`，对应两个 `tests/test_*.py`，`docs/offline-signing-exchange.md`；README 只加 TEST ONLY 指引。若复用 public signature helper 需要注入有界 runner，只对 `verify-production-package-site.py` 的相关 helper 加显式可选 runner，保持当前调用默认不变并运行相关回归。metadata 子命令此切片明确未支持。 | 先黑盒 CLI RED：未知／重复字段、bool-as-int、错误 mode/phase/family；secret 混入公钥、错误 cert bytes／拓扑／RSA／有效期；联改包内 request/receipt 保留外部锚；错 source/control/audit；反斜杠／链接／重复／超限实际文件在读取前失败；input/output 冲突。原生 TEST ONLY：合法签包仅 signature 变化通过；同 NEVRA 改 header/依赖/脚本/payload 后合法重签仍拒绝；伪 issuer、next/historical、非 SHA256、response 未签／签错钥、重放冲突全部失败。 |
| **B：metadata continuation＋两阶段 TEST ONLY 演练。** 扩展 `offline-signing-exchange.py` 的 prepare/verify-metadata 和 native protocol test；新增 `tests/offline_signing_exchange_drill.py`；对既有 `verify-production-package-site.py` 只提取可显式传 suite/repository 的 family metadata validator（preview 默认与旧 CLI 不变）；扩展其相关现有测试，更新同一文档。不改 channels/keys/生产 workflow，不把 TEST ONLY 文件变成生产 key adapter。 | 先 RED：RPM 签前 metadata／wrong parent／跨 session 拼接；APT clearsign 规范化导致 bytes 不同、两签使用不同钥、extra Release header、Packages.gz 不一致；repomd 缺／重复／escape 引用、压缩或 open hash/size错、解压炸弹、metadata 指到 unsigned／另一轮 signed RPM；plain gzip metadata 合法闭包接受，不错误要求 digest 文件名；stage2 错请求签名／超时／中途失败不暴露结果。正向跨进程走三份 request/response，断开私钥目录后 verifier 仅靠公开证书和请求完成验证，输出可重放 JSON 收据和 SHA256SUMS。 |

计划的未来回归入口为 `python3 -B -m unittest discover -s tests -p 'test_offline_signing_exchange.py'`、`… -p 'test_rpm_content_identity.py'`；原生握手／签包演练为 `python3 -B tests/offline_signing_exchange_drill.py --test-only --output <new-scratch-dir>`，只在既有 digest-pinned Linux toolchain、无网络／凭证下执行。测试用 tiny data-only 包及临时 TEST ONLY keys，绝不安装／执行包；原生密钥仅留临时容器，artifact allowlist 只含公钥、请求／回执、哈希与小日志，结束前检查无 secret packet。这里列的是**待新增命令**，本次未创建这些入口、未执行任何测试。涉及 native 变更后保留现有 signing/source metadata/site/receipt 回归；不为方案文本发明新的 GA 测试门禁。

**交付边界：** 两切片完成可证明“固定输入 → 离线签回 → 独立核验”的协议，在没有生产私钥的情况下把错误路径跑实。生产 stable 公钥／设备适配、stable bootstrap、真实 audit/control schema、publisher/object-storage 接入及公网 APT/RPM 验收仍沿既有阻塞项继续完成，不能将 TEST ONLY 结果接入当前 preview family receipt 或声称正式发行完成。
