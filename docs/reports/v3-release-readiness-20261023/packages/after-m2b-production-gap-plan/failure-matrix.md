# 最小失败矩阵（后续实施前全部先写）

本轮未运行测试。复用 M2b/原 SDK 原有负例，不复制其34/新refresh case；这里只测生产组合接缝。

| ID | 触发 | 必须观察到的结果 | 可离线 / 最终外部 |
|---|---|---|---|
| P1 | manifest disabled/null/unknown field；保存JSON verified=true；生产输入带TEST ONLY | 不构造 credential-bearing SDK、不发生 PUT/config/refresh；不能通过更换入口绕过当前控制 | 离线先红 |
| P2 | 执行HEAD/current main/manifest/输入/plan不一致或checkout脏 | 原 fresh validator/clean gate 拒绝，历史 seal/candidate控制与executor不得混用 | 离线mock只读GitHub；真实生产重核 |
| P3 | public仓库读可用但attestation/native metadata命令失败；source/audit/tag/asset变更 | 不用旧receipt/metadata或普通Release HTTP200替代，后续写阶段停止 | 离线fault；真实source tuple |
| P4 | 上传后、配置前fresh(previous)漂移 | 已上传对象effect保留，不配置/刷新；无删除或自动重试 | 离线whole flow |
| P5 | 两个角色principal混用/跨account/bucket/domain/CNAME，或null intent digest当真实resource digest | 不写；分别绑定uploader/activator资源与相同目标桶域名，原dry-run JSON不改 | 离线；真实STS/资源 |
| P6 | 任一对象冲突bytes/cache不同、未知PUT后GET不完整，或objects/routes manifest漂移 | 不进入配置；保存逐对象已尝试/已读回事实 | 复用SDK cases，新增阶段stop断言 |
| P7 | CDN current-config/managed IDs漂移，非管理rewrite或需要delete，实际layout超编译限额 | apply前拒绝；不增加通配规则、删除权限或限额 | 离线；实际config |
| P8 | 配置分批失败/读回失败，refresh提交不确定或聚合ID/URL覆盖不全 | incomplete，保留部分effect及真实ID；不盲重提、不进客户端 | 复用M2b refresh并测编排停止 |
| P9 | 配置readback正确但Host/SNI/证书/私有回源失败，或响应字节/cache错 | 不能以API成功替代wire/public验收 | 离线HTTP seam；实际域名 |
| P10 | alias误回源原snapshot、根遗漏、邻接/编码/query落入意外映射 | probe明确失败；不归咎客户端或删该URL继续验收 | 离线closed table；实际PCRE/CDN |
| P11 | M2b测试callback/替身client被接入production；任一发行版/安装CLI/exact source/version失败 | 不出完成结果，调用原真实verifier，四份结果全部检查 | 离线；最后真实四client |
| P12 | stable target相同但回执input/plan来自另一composite、验收后current-main变更 | 原receipt保持不变，外层拒绝发布完成 | 原digest负例复用+生产阶段断言 |
| P13 | 原preview/public `/repo`被stable目标覆盖，或公开stable后退回preview-only | 拒绝该目标/回退，保留上一完整composite | 离线；实际preview迁移 |
| P14 | 凭据/debug/raw SDK异常进入receipt，或超时/终态/cleanup不明 | bounded redacted incomplete；无原始token/签名URL/私钥归档 | 原guards复用+外层receipt |
| P15 | hosted stable验收误调用preview-only workflow，或新SDK suite未执行/0case/skip | 不能算稳定版验收；独立明确suite receipt和原stable CLI | 离线CI wiring |

生产正向只能在：真实且新鲜GitHub/source/control + 精确对象/readback + 实际wire/TLS/cache + 公共四客户端全部齐全后报告。
这沿用已存在的发布完成要求，不增加硬件密钥、多保管人、新采购产品或通用授权平台。
