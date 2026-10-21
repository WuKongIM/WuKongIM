为 stable 候选增加精确版本、来源提交和安装后 CLI 验收，并从固定 fc182212 产品源码构建两代 TEST ONLY 包，验证 Ubuntu、Debian、Rocky、Alma 的下载、安装、缓存及版本切换。stable 保持关闭。

新增独立只读 Linux job，要求 49 个原生案例通过，覆盖签名、TLS、元数据、旧目标、错来源和缺失 wkcli 等负例，并核验本次 Docker 资源零遗留。运输计划补齐 stable APT SHA512 索引别名，以及明确保留代之间的 RPM payload 地址闭包：两次 DNF 调用切换 mirrorlist 后，旧版本包仍可从计划明示的另一代前缀下载，来源保持已验签的原 archive member 和精确 SHA/size。元数据始终绑定所选代，原签名、archive、preview 默认行为和生产控制保持不变。包含 CHANGELOG 条目。

真实 CI 已定位并修复固定 Rocky 镜像不可用、HTTPS 关闭通知、默认 SHA512 地址缺口，以及 Rocky 两次 DNF 调用之间的旧包 404。第三代执行 23/49 个案例、22 通过，四发行版两代的八套完整安装后 CLI 通过；首次失败和清理记录全部保留，未重跑旧 head。本次修复保留实际调用顺序、barrier、缓存和全部 49 案例，并将原全 A 前缀假设明确改为“精确 A 元数据 + 已验证 A payload 字节的显式别名”。

本地最终 de43579a 的 518 项 Python 测试通过（Mac 1 项既有 native RPM 条件跳过），4 项合同检查通过；规范、行为两轴独立复核无发现。本轮 planner/driver 完整新测试在实现前冻结并保留红绿结果。前一修复中 5 项 planner guard 测试事后补充的顺序偏差仍如实保留，未追认其为 tests-first。第四代 Linux 514 项和旧 native 流程通过，Rocky A→B 下载也已通过；随后 cache-A-first 因夹具遗漏实际 repo cache 挂载而失败（24 执行、23 通过、25 未执行）。固定 Rocky/Alma 镜像实测确认 early persistor 与最终 CLI cache 路径分裂，现补齐四个独立的缓存/状态绑定并保留 inode/SHA/atime 真实读取要求。该四文件修复完整测试先行，65 项相关回归和双轴独审无发现；49 项真实 Linux 全套等待本次提交的首次自动 CI。

这些 TEST ONLY 验收不替代生产来源认证、对象存储/CDN 接入或正式版本公网 APT/RPM 安装验收。
