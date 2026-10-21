A — Budget v2 Standards 差量复核：无新增 finding；v1 已知 P2 在本窄切片中关闭。

冻结身份：base `ff0aa096fe1bd125be70fc1844b2f45cc7f0115e`，patch SHA `9c6fa1bb330b10f79359af1905e74b47b04adff34de8e1b7b865e563ef1ceef3`。独立核验全部 27 文件、改动集合及 patch；相对 v1 恰好三文件变动，其余 24 文件字节一致。identity.json 保存逐项结果。

两 finalizer 仅在 zero-proof 上传条件加入 `always()`（rehearsal:518；formal:456），仍要求 `cleanup.outputs.zero == 'true'`。上传名称/目录、权限、资源操作以及前序预算拒绝 exit 1 未改，不把失败运行升级为资格通过。对应静态合同明确检查失败门禁仍在、不可 continue-on-error；这是 Workflow 配置合同，未冒称真实 Actions 执行。

先行测试收据 12 的 rehearsal/formal 两臂均因原始上传条件缺状态函数而失败，随后 13 无失败/skip、scripts 包通过；两者时间顺序及 SHA 已重算。10 的测试定位问题、11 的未完成修复失败没有算作绿测。tests-first 没有新增可证实违规，且本次无需为两个配置条件增加新抽象、扩大源码或重写文档。

本审查只复核 v1→v2，保留 v1 其余 Standards 结论与限制；没有重复 Spec probe、重跑测试或触发外部操作。完整 named checks 仍以 root 的独立收据为准。旧 fc182 产品仍不能接收 lower，新候选 Linux CI/云验收尚未由本审查证明；无新付费启动授权。
