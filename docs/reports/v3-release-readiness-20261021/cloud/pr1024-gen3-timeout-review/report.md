# Gen3 timeout evidence review

**1项P3措辞问题，已由clarification.md澄清关闭；无开放发现或数值/方法阻塞。** 独立核验冻结20份文件、21项原始输入与19份精确44de源码；未复跑测试、构建、CI或实验。

- 从三个原ZIP重新提取安全事件，核得gen2→gen3的两段不重叠增长：step→包标记约14.262748秒，包标记→首个root CONT为18.044390秒，合计约32.307138秒。`README.md:7,21–37`正确保留API秒级精度及非纯编译、非反事实完成时间边界。
- `phase-timing-table.json`使用scripts包的根测试CONT；`generations.json`较早的首CONT包含子测试，不能混用。独立核得collector根CONT排名150/9/179；gen3为29根PAUSE、4根CONT未终态，并非33项都在执行。
- **P3，README.md:16–17：**“are lock waits”“lock waits are directly observed”超出直接观测。实际只有CONT、首child与终态时间，锁源码支持admission/lock等待解释，并无获锁事件。建议概述统一为“consistent with admission/lock waiting”。41–49行已正确限定，成功夹具缺失的build/readiness/drain阶段未被补齐。
- 计数更正成立：gen1为610 PASS/6 SKIP/40未完，gen2为649 PASS/6 SKIP/1 FAIL，gen3为618 PASS/6 SKIP/33未完。附录八个安全测试名均能在原ZIP找到，旧summary原hash未变；不改变首失败和NO-GO。

本审查不评价另行进行的工具替换实验，不支持放宽预算或宣称79场景已在gen3完成。原件与冻结内容在封存前再次验hash一致。
