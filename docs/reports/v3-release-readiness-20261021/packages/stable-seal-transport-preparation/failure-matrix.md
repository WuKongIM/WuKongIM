# 必须先写的失败库存（提案，尚未执行）

基线 #72 `c8aae37d13787a0aceceac751d6d35bbc1d89c35`。以下以真实 CLI + 可观测 loopback API 为 seam；TEST ONLY 的 API 数值 ID 是 fixture，不是公开 Release。通过离线 fixture 不能改变 `github_provenance_verified/cloud_verified/activation_authorized=false`。

| ID | 触发 | 必须观察到的结果 |
| --- | --- | --- |
| I01 | legacy preview 命令不传新 flags | 原 tag/asset names、schema、输出 bytes与请求形状保持；stable candidate拒绝 |
| I02 | stable opt-in 不完整、unknown/重复字段、bool-as-ID/size、非法hash、prerelease/build version | 非零且无任何写 API/输出 |
| I03 | archive内部 null ID 或 control/source SHA被回填；候选与外部plan一起改但独立digest未变 | raw hash或外部计划锚拒绝，不重新生成“等价”archive |
| I04 | test_only候选进入真实 writer/生产stable descriptor；preview来源冒充stable | 前置拒绝；loopback test入口不能成为生产绕过flag |
| I05 | sealed tag control与candidate历史control不同但关系清晰；或互相顶替/未核实 | 正例分别保留两个角色；错配拒绝；不重写candidate control/producer |
| I06 | candidate producer清单与实际core bytes不符；从control/source目录放置Python | 失配拒绝，不导入数据目录代码 |
| R01 | wrong numeric Release ID、preview tag/name、annotated/wrong commit tag、mutable published、draft+immutable、不合法日期/分类 | resolver拒绝；不可把draft解释为封存 |
| R02 | 缺一个asset、receipt-only、extra asset、重名/重复id、非uploaded、错版本/快照名 | stable immutable读取只接受exact two-assets；draft恢复仅允许既有合法状态 |
| R03 | API size/digest不符、下载截断/超声明长度、asset ID与path不符、redirect到不受信origin | 有界失败、清理本次部分文件；授权头不转发到redirect |
| R04 | 读前/读后 Release/tag/asset变化 | 无可信resolution输出；保留首失败不自动重试成绿 |
| B01 | 非空draft、不同控制已存在reservedtag、伪detached tag、有真实对应ref | binder拒绝；不delete/force-update/rebind |
| B02 | 正确tag重入、正确empty detached transition | 只做既有允许的最小恢复；preview名字不被stable覆盖 |
| S01 | 同名远端asset相同bytes vs不同bytes | 相同可精确恢复；冲突不覆盖/删除/rename |
| S02 | archive上传后失败、receipt上传后中断、已immutable重入 | 仅缺失允许asset可补；immutable只重读；不多次publish |
| S03 | immutable policy关闭/未知、main推进、tag变化、发布前增加asset、发布响应mutable或错误id | 写前停止或写后失败检测；不声称外部事务原子 |
| S04 | 非canonical tar/路径/链接/duplicate member、oversized文件、解压超限 | 复用现有有界canonical/metadata校验，在写 API前失败 |
| S05 | candidate receipt/plan/control/source/family签名/实际包不一致 | 现有独立验签拒绝；不能仅通过字符串current fingerprint |
| S06 | writer stdin/argv/输出记录含token/私钥；API替身请求中出现DELETE/任意路径 | 测试失败；只能固定数值 GET、既有精确tag bind、2asset POST、一次指定 PATCH |
| P01 | 仅自报 seal JSON=true、未绑定原两asset digest、错source/control/plan/snapshot | descriptor一致性拒绝；即使结构一致，offline仍不标online provenance true |
| P02 | candidate source/control根symlink、硬链接、路径穿越、输出祖先alias重叠、existing输出 | 内容读/写前安全拒绝；输出不能影响输入 |
| P03 | 虚报archive小size但实际巨大、所有代累计超2GiB、超过8snapshot | 内容哈希/解压前拒绝；不能逐代重置总预算 |
| P04 | retained代坏archive、缺控制/旧byhash/RPM依赖，且digest在别代已见 | 仍验证该代并拒绝；不得seen-digest skip |
| P05 | 重复auditID、同ID跨channel、同URI不同bytes/size/cache、漏current preview | 明确拒绝；不能靠dict last-write-wins |
| P06 | current两个根metadata冲突 | 根只由preview拥有；stable原成员留snapshot，不覆盖root |
| P07 | stable mirrorlist有第二行/外部host/query/HTTP/浮动baseurl/错误snapshot前缀 | 拒绝；唯一固定HTTPS指向完整stable snapshot，no-store且正文hash入inventory |
| P08 | Packages.gz或普通RPMmetadata错分immutable；缺旧代alias | 分类/retain验证失败；plain metadata每代原bytes独立保存 |
| P09 | 改retain顺序或两相同bytes descriptors排列 | 输出canonical相同；不得把输入顺序当冲突胜负 |
| P10 | 输入在validation后替换/增删；生成镜像列表未入对象inventory | 导出前重核拒绝；没有缺依赖的activation-ready输出 |
| P11 | unsealed/test_only v4输入直接喂原preview生产消费者 | 默认拒绝；不添加schema自动fallback |
| P12 | 封存/plan后源文件/archive不变 | 对照原archive、原receipt、signedAPT/RPM逐项SHA全部相同 |
| E01 | loopback完整bind→seal→read→verify→composite CLI路径 | 精确2asset、真实TEST ONLY签名验证、可重放archive/plan摘要、无外部网络/secret输出；不冒充生产成功 |

范围外但接口必须保留：真实CDN A/B/缓存收敛、四公网clean clients/installed CLI、live源Release新鲜性、生产schema启用/资源角色及收费/DNS授权。它们不是本次mock通过的含义，也不是新增产品性能门禁。
