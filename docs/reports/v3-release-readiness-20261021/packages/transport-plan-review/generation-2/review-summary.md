# 第二代独立续审

结论：普通 RPM metadata 文件名的兼容补齐安全，可保留在本次离线 preview 计划范围内；当前没有未解决发现。

上一代真实验收确实失败：完整锚定的 beta.24 audit `402342684` 在 planner `27ddc3fe…` 上 exit 1，拒绝 `repodata/filelists.xml.gz`。上一代合成测试使用 digest 文件名，未覆盖真实发布布局。原审查、真实失败和哈希清单全部原样保留，本代不替换它们。实现方先固定 2 项/5 个失败，再修复；原始红灯见 `../../object-storage-plan/09-plain-rpm-red.log`。

新 planner `ae1ca68e28d6dcf487a2cf56af27944ae92accfa795df35a681e62b0772c3da7` 与上一代逐字比较仅有两处分类变化：移除校验前的强制 digest 文件名拒绝；仅完整 SHA-256 前缀与内容相符的 metadata 进入共享 immutable alias。receipt、repomd 引用、压缩/open 哈希、精确文件闭包、资源上限均未修改。普通 metadata 在完整闭包成功后作为当前 no-store 入口，每代快照分别保留。

独立四次真实 CLI 探针通过：两代 primary/filelists/other 的同名 gzip 文件全部使用不同内容，得到 6 个独立 no-store snapshot 对象、3 个当前入口和 0 个共享 metadata alias；压缩哈希错误、open 哈希错误、保留代缺失引用均拒绝且无输出。2 项新定向测试也独立通过，没有重复全套测试。首次探针记录 `floating-probes-first-new-code-diagnostic.json` 已在新源码上运行，两个失败标记只是审查探针预期错误文案不匹配；真实拒绝正确，修正文案后的最终记录全通过，该记录不充作实现红灯。

真实 beta.24 的同一原始 inputs 在二代 exit 0，计划 SHA-256 `3c1dd765103ede4839f4d213d58bb8674365091d190008bc39ee84a289aed114`，31,484 字节。rc_cloud_plan 从 tar 独立重算 44 个普通文件并核对闭包；本审查另行核对计划哈希、完整 descriptor 身份，以及 3 个普通 metadata 与原公开 RPM receipt 的 size/SHA。真实计划包含 17 个入口、14 个 immutable alias，这 3 个 metadata 均为当前入口与快照 no-store，无共享 alias。

固定 RPM base URL 跨代仍可能混合 repomd、签名和普通 metadata，客户端只能依赖签名/哈希校验拒绝不一致；本计划不保证原子或无缝激活。签名、完整来源、control commit 与云状态仍未验证，stable 仍未启用。A/B 发布演练须另行实现和验收；本次没有云、密钥、流程或生产控制操作。
