# 先行失败矩阵

所有条目先声明再实现；本文件为计划，尚无红/绿执行记录。

| 族 | 先证明的失败/阳性对照 | 核验点 |
|---|---|---|
| 默认兼容 | 现有 preview local/remote/installed CLI 与 receipt v4 完全继续；裸 stable输入、channel typo、schema fallback拒绝 | 真CLI parser+原单测，runner调用前拒绝 |
| host 验签工具 | 缺 gpg/gpgv/rpm/rpmkeys/dpkg-deb/zstd、工具失败/非法结果 | stable 在Docker client前拒绝，不能跳过验签；记录实际host版本，preview不增依赖 |
| stable 身份 | current stable外部锚缺失/plan或receipt不同、null真实源、不完整/可变seal、testonly入生产 | 复用已有 descriptor/crypto，不读根preview status作授权 |
| 输入稳定性 | archive/plan/control/read buffer在检查间变化、越界/链接输出、大小上限 | 原共享helper事实边界不放宽 |
| stable bootstrap | 错族公钥/包名/文件、direct/indexed bytes不同；bootstrap重复安装和preview共存 | 正常source/suite/mirrorlist及签名/TLS选项逐字校验 |
| 真实产品 fixture | 无wkcli、dummy输出、version/SHA/build_source不同 | 固定fc182实际编译两binary；真实CLI receipt不能由runner假值替代 |
| A/B 实际不同 | 同archive仅换ID、A/B same payload/index bytes、同版本bootstrap两次签名发生不可变URI冲突 | A=3.0.0/B=3.0.1真实metadata和binary哈希不同；同公钥，scratch bootstrap A=1.1.1/B=1.1.2独立new签名；checked-in 1.1.0不变 |
| Ubuntu/Debian | baseline A/B；A InRelease后切B浮动文件；旧 by-hash保留/缺失/损坏 | 必须见native GET by-hash；正常完整A，破坏时认证失败，不回退trusted |
| Rocky/Alma | baseline A/B；A mirrorlist后切B；缓存重读；A repomd配B.asc/缺metadata | mirrorlist实际GET且全immutableA prefix；不得改baseurl/关gpgcheck |
| exact target | 已签A而expectedB；wrong source SHA | 正常validation拒绝，安全旧代不算发布成功 |
| HTTPS/HTTP | 错SAN/不信任CA、redirect、404/304交错、断流、恶意mirror目标 | SSL/digest不绕过，服务器只allowlist；禁止逃出私网 |
| installed 4发行版 | 每代下载bytes真实安装；有效/无效bench/query/migrate，原source不变target未建 | 复用原30s/600s，version+commit+release 全要求，不启动集群负载 |
| 边界与清理 | preflight/tool失败、客户端非零、超时、TERM、HTTP worker异常、receipt写失败 | 原失败保留；本次owned container/network清零收据，外部sentinel不碰 |
| CI权限与发布 | workflow只contentsread；无secret/environment/publish；38旧控制/12producer diff核 | 原31+8/7/10/6及全Python仍真跑，不替换为新套件计数 |

正/负用例名称与期望退出码在实现前进一步固定到native CASES。不得把工具缺失、usage或Traceback当目标负例。A/B流程中预期安全拒绝与正常exact-target通过分别统计。真实公网、全球CDN缓存/收费资源、真实Release封存均不是本矩阵的通过项。
