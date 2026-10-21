# Stable clients v3 最终差量复核

**结论：原 v1 两项 P2 和 v2 追加网络清理 P2 均在实现层关闭，未发现剩余阻塞，可进入首次真实 Linux native CI。** 这不是 49 个原生用例通过或正式发布通过结论。

已独立核对全部 10 文件与 v3 manifest 一致，SHA256 `90c77a141e13f3a177f2ed51ad2afd186a80e098c6262c526bc1810c10cc1c55`；相对 v2 只有 driver/tests 两文件变化，delta SHA256 `8ee54c78c7da4366373642b0021f3a2f4e62be491804cd5248376391fd154b18`。生产 validator、其全部旧 preview helper、workflow、fixture 两文件保持已审字节。

v3 只在 Docker remove 明确成功后从活动集合退役对应 ID；创建尝试名称历史不丢失，后续仍按 UUID label 查询初末库存。没有扩大忽略 inspect/removal 错误的范围，没有以“network not found”笼统吞掉其他失败。

独立执行从冻结 v2 失败探针演进的同一网络场景：首次 remove 成功，第二阶段只继续 label 查零，不再 inspect 旧网络 ID；first/final 都 verified=true、资源确实为零、只发生一次 network inspect、两阶段累计 8 次库存查询，尝试名称仍在最终收据。使用 root 已实测的缺失网络诊断作为替身；本审未调用 Docker。

另外精确运行 3 项相关回归：两阶段退役 ID、首阶段失败不能被最终成功洗白、不确定容器/网络创建仅清理本任务资源，全部通过。未重跑 unchanged 全套或原生测试。

继承 v2 关闭证据：正确 DNF installroot cache 挂载、非空元数据及独立读信号、root offline cache helper 的受限写入/清理、fixture host 非 root UID、受控 TERM 原 failure 保留、两类新增实际 installed 负例都已接入。49 个 native cases、Linux cache atime/权限、实际 APT/DNF 诊断及最终清理仍须首轮 CI 验证并保留首失败。

v1/v2 原失败和本审中网络替身覆盖不足的更正记录原样保留，没有将任何本地轻量通过提升为签名、安装或公网发布通过。本轮不修改源码，不运行 Docker/GPG/产品编译/云/发布操作。
