# v3 两阶段网络清理窄修复

v2 frozen原样保留。root只读Docker inspect证实已不存在network报`network <id> not found`，v2阶段一删除后仍留active ID，第二阶段误报失败。

先加实际OwnedDocker两阶段行为回归得到01 red，再实现成功remove才移除active ID。attempt/name历史保持，最终UUID两类inventory仍必跑；没有吞掉或扩大匹配inspect错误。首fake将network错误用于两类资源，03在冻结v2上用准确的container No such/network not found分别重证，仍仅network错误；不隐藏首red。

04最终17项轻量测试通过。原34production+12installed相关测试沿用v2已通过记录，未变化。无Docker/GPG/native/产品编译执行，49 native仍待CI。v3仅2文件；workflow49和其余8文件与v2冻结一致，34protected基线原字节。
