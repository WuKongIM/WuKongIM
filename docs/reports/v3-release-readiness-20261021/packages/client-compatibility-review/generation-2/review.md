# Stable 客户端兼容性复核（第二代）

原六文件审查与 R1 失败保留在父目录；本代只审两个测试文件增量。作者 manifest SHA256 `47a10f17635dc5a25cefb08299f7a6c239e8a192be630c20c059ddd8aa3b706b` 的15项原字节全核，delta SHA256 `59bef77fed3ad3591d9472b5d755d406527a6b12b11f361d597272479eeb4b3e`；当前工作树与这两文件冻结一致，其余原四文件未改。

## Standards

原 S1 的五个 guard 后补偏差已如实记录（standards-deviation.md），未追认全程 tests-first。此次 R1 失败用例在实现前记录；作者先红后绿记录保留。无新增规范发现，不引入额外发布门禁。

## Spec

**R1 已关闭，无剩余阻塞。** Handler 将记录放在发送尝试后的 finally，只有 `wfile.write` 和 `flush` 完成才置 `response_write_completed=true`。故障验收同时要求该标志、原故障path/status/body digest，以及对应的APT 404或hash/size诊断；纯SSL、泛化fetch/resolve诊断不足以通过。标志明确仅证明服务器写完成，没有宣称客户端已收到全部字节。

独立重放原实际 Handler/negative branch 探针，两个 headers 前BrokenPipe的原假绿均拒绝且保留false记录；另在实际negative branch运行48项无网络控制矩阵（SHA256/SHA512、标志缺省/false/true、SSL/fetch/hash/404），结果全部符合预期。合法completed+hash继续通过；missing允许completed+404，corrupt不允许404。探针仅验证实际控制流，不是原生客户端证据。

已有的非零/timeout/tool/signal/harness拒绝检查仍存在；TLS有界关闭、alias身份与跨代冲突、preview/12 producer不变的第一代结论继续适用。作者57相关检查收据已读，未重复全套/真实TLS或native。49项Linux验收仍待下一精确head CI，不能由本审查或轻量结果代替。

Standards：原历史偏差已记录，新增0；Spec：原1项P2关闭，剩余0。本审查未改源码/证据分支，未执行网络、GPG、Docker、Go或云动作。
