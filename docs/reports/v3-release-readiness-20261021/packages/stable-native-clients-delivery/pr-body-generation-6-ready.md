为 stable 候选增加精确版本、来源提交和安装后 CLI 验收，并从固定 fc182212 产品源码构建两代 TEST ONLY 包，验证 Ubuntu、Debian、Rocky、Alma 的下载、安装、缓存及版本切换。stable 保持关闭。

新增独立只读 Linux job，要求 49 个原生案例通过，覆盖签名、TLS、元数据、旧目标、错来源和缺失 wkcli 等负例，并核验本次 Docker 资源零遗留。运输计划补齐 stable APT SHA512 索引别名，以及已明确保留代之间的 RPM payload 地址闭包；元数据始终绑定所选代，跨代别名绑定已验签原 archive member 和精确 SHA/size。原签名、archive、preview 默认行为和生产控制保持不变，包含 CHANGELOG 条目。

前五代首次 CI 失败均保留，未重跑旧 head：固定 Rocky 镜像、TLS 关闭通知、SHA512 地址、DNF 两次调用间的旧包 404，以及真实缓存绑定已逐项修复。第五代 Pages 518项（Linux零skip）和旧 native 完整演练通过；新案例执行24/49，23通过，在缓存取证助手处失败，随后全部容器/网络清零。真实 DNF 下载和签名验证已成功，但原收据没有被拒文件的stat。

固定 Rocky/Alma 的独立 signed Repo.load 重放复现同类失败：GPG在pubring留下五种Unix socket。该file://最小重放不是原CI文件的追溯，也不代表完整49项通过。本次窄修只略过已实测canonical cache父路径中的这五名、socket类型且nlink=1/size=0的条目；保持其余类型/链接/大小限制，以及metadata hash/inode/真实atime读复用要求。未知文件错误现在含有界路径和stat，未通过删除socket、改变原生命令或减少案例取绿。

最终本地528项Python通过（Mac 1项既有native RPM条件跳过）、4项合同通过；本轮10项新回归全部在实现前冻结，保留原4fail/4error和修后0skip通过，相关41项通过。规范与行为双轴独立复核均无发现。最终head `983e567f155a982c45ca066e4dc67d33851f0d24`、tree `7a498c42b4f5b0e6f86a524878fde651d7301371`。40个保护文件（含12个producer）及driver除inventory之外所有函数/类保持原字节或AST。该 head 的首次 Linux CI（attempt 1）已全部通过：528 项 Python 零 skip、4 项合同、旧 native 完整签名安装演练，以及 49/49 项 stable 原生场景。Rocky 与 Alma 的首次缓存和重复读取收据确认 metadata 的 device/inode/SHA/size 不变且真实 atime 前进；cleanup verified、errors=[]，容器与网络零遗留。实际 checkout 1861383911303229fba4cd65eb75019ff26f6a32 与候选完整 tree 相同。

前一修复中5项planner guard测试事后补充的顺序偏差仍如实保留，不追认为tests-first。这些TEST ONLY验收不替代生产来源认证、对象存储/CDN接入或正式版本公网APT/RPM安装验收。
