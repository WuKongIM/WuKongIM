# VCS stamp 与实际编译源的边界纠正

本说明优先于 `provenance-addendum` 中可能被读成“实际编译源就是a804”的表述；旧文件保持原字节。**a804只是二进制内嵌的VCS标记，不能单凭该标记判定实际编译源。** `vcs.modified=false` 同样不能替代完整构建来源链。

本轴确证产品SHA c253…、CLI SHA e36e…、内嵌Go1.25.11、a804 stamp及所列build settings；a804与79a两端源码的85项路径差异、33文件逐SHA相同也仍成立。它们均不自动证明构建时实际读取了哪个checkout。

root报告CLI已有build cwd79a和source-freeze证据，独立审计正在核对Go嵌套worktree的VCS发现、已有构建receipt/frame paths；本轴不扩搜、不预判。产品prebuilt override初始完整链仍不足，故当前不能声称已验证79a候选复现。也不应仅因stamp不匹配撤回c253此次CPU/指标描述，或擅自认定一定运行了a804源码。

此处没有新增构建、产品启动、负载、profile或云操作。待完整来源审计建立后再判定候选资格；本轮仍不能证明cloud性能或message_loss根因。
