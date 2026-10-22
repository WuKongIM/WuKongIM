# 观察及43项首红补充复核

**结论：可以按现有最小设计实施，无外部信息或权限设计阻塞。** 无需等待本附录或增加provider全局deadline。根任务已收到结论，作者已直接收到以下最小先红增强项。

已核对root封存的真实UI观察，manifest `f6630eb65c38160857514435837baf8db78befc9232b60a527b2b39e022efea2`、三份payload均匹配。任务33643947491的TotalCount4、四行共享TaskId、四个不同URL、file/Complete/100%均与报告一致。这更新主报告中“尚未独立读原UI档案”的读取状态，不改动此前冻结件；仍不是raw HTTP capture。

作者43项review RED的四份文件已hash绑定。新增严格int（拒bool/string）、Tasks/row类型、全局合法但不在当前组的ID、空?/#/Tab/space在静态源码中可见，provider仍按作者收据绑定base7255102。本代理未执行这些用例，也不把预期RED当实现通过。

尚建议加入三个区分性测试，直接扩既有场景，不扩实现范围：

1. 前50条合法，第51条非法，要求0次Refresh提交。当前两条URL的小输入不能区分全量预检与逐批预检。
2. 两查询组交换完整合法row，使全局IDs/URLs依然齐全且每URL一次，只有所属group错误。单改首行ID还可能因全局某ID缺失被拒，不能独立验证组归属。
3. 两批部分重叠且顺序非单调ID，例如[601,501]、[501,701]，最终first-seen[601,501,701]。现全部同ID/顺序递增用例不足以区分正确first-seen和错误排序；可同时验证每ID只查询一次。

CR可顺补进现有控制符表。重复URL、非对象row、缺行、TotalCount不符、映射漂移等原矩阵保持。

时限说明接受root确认：session connect5/read10、response读取30秒为现有单请求防护，driver外层300秒是另一层边界。1000URL最大调用数增长须文档如实说明；本次不要求新增全局deadline，不把单请求/poll_interval说成总耗时承诺。

以上是设计与测试切口意见，未替作者修改源码、变更保护文件、运行SDK或操作CDN。实际实现与完整RED/GREEN及driver首轮仍需后续验证。
