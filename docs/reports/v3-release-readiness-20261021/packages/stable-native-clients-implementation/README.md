# Stable native clients：实现交付（尚未原生验收）

基线 packages main `335cd2569d2b62de577916ed86c097fd3a22547e`；worktree
`packages-stable-native-clients`，未提交/推送/创建 PR。按已独审 generation-2
范围完成10文件实现。唯一生产入口是显式 stable package client validator；
必须有 composite transport inputs、canonical origin、exact release target 和
完整 installed CLI 门禁。稳定频道仍disabled，12producer/生产发布workflow/
公开路由/真实密钥均未修改。

`final-freeze-v1/manifest.json` 固定10文件及review.patch，另核34受保护文件。
原native workflow job原字节保持；只新增独立contents-read TEST ONLY job。
`fixture/` 保留子agent两文件先行红绿、默认等价/搬迁检查；其6项轻量测试中
5项已进入新默认test文件。首次原生命令异常清理的单独边界例原样留在fixture/
（依赖该证据目录已冻结的公开trusted builder，不增加repo测试data文件）。
独审另由stable_storage_design执行。

验证：全Python482项/1既有skip，112.586s、exit0（19）；contract4项和shell
合同exit0（20）。随后tests-only driver的cleanup/source收据和错SAN/真实TLS
conditional304收尾，最终相关10项exit0（24），actionlint exit0（25）。完整
suite不是该最后小delta后的复跑，不能混称同一冻结全套；源码最终覆盖由完整
suite加相关复验组成。首红与中间失败均保留；06虽然文件名带green，实际是测试
在rpm-switch状态误期待normal304的失败，07显式配置normal后通过，没有覆盖06。

新native固定47例，但尚无一例实际执行。包括两代各四次下载及四次完整installed
CLI阳性、真实请求屏障/持久缓存、两类TLS错误、保留依赖破坏/redirect/断流、
实际TLS conditional304、exact目标和实际commit负例。最后5例是原已批准失败矩阵
的四个wrong-SAN与独立真实TLS缓存探针，区别于客户端本身的缓存请求记录。
真正的fixed fc182 Go编译、A/B签名导出、host GPG/RPM再验、四Linux客户端和owned
Docker清理全部待独审后的首次Linux CI；本机没有Docker/产品编译/新fixture真实
GPG执行。旧默认Python自身的既有工具测试不算新原生验收。

已知未验证边界见implementation-summary.json；无已知可忽略的失败。不得将当前
实现称为公网分发、global CDN原子切换、正式版发布或正式Release来源证明。
