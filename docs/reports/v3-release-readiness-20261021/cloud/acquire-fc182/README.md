# 预演 Acquire 阶段

唯一采购run39582604546 attempt1成功，receipt state=active。source/control=fc182，bundle digest73a5d1e…1eeb88，request/Lease/plan/account与获准Quote完全一致；Acquire产物的Quote字节与原Quote相同。

收据有四实例（3service+1load）、8盘（各40GiB系统盘，service500GiB/load200GiB数据盘）、1EIP及原网络资源，所有资源tag身份匹配。CNY1500/1350及committed0均封存。created_at10:11:00.703367749Z，expires_at22:02:56Z；注意expiry从早于采购的计划时刻计算，不改成采购后12h。

expected-selector.json从该已验证收据字段构造，供后续清理精确比较；未调用云API重查。采购成功不代表部署/两小时预演通过；下一部署run39582829931的终态与原件另存。实际账单尚无数据。
