# M2 全失败矩阵（计划，尚未写测试或执行）

先冻结真实 route readback 和整个矩阵，再写所有回归并记录 RED；缺编译能力/缺集成 driver 都必须真实失败，不 mock 签名或候选验证。

| 边界 | 首红/期望 |
|---|---|
| 完整 178 native routes | 当前一条一规则因 50 cap 失败；不得裁剪，准确最终条数待读回 |
| ≤50 原路径 | 函数数组/顺序 byte-compatible；旧 22 SDK case 保留 |
| 原 snapshot/public alias/current entrypoint/cross-generation alias/root/mirror | 全部枚举映射到 exact key，source audit/member、hash/size/cache 不变 |
| 规则语言 | 独立 grammar 逆展开 exact set equality；丢行/重复/重叠/额外分支/错误 capture 都失败 |
| URI 边界 | 每条相邻 suffix、slash、newline、query、percent/double encoding、dot-segment、double slash、case、metachar，以及非列路径均不匹配；这不是 live HTTP 拒绝 |
| 1024/50 边界 | 精确 1024 字节可表达、1025 要拆或拒绝；50规则可接受、51拒绝；单行过长拒绝；不改上界 |
| 不可分组/冲突 | 不同目标关系保持 literal；同URI不同facts拒绝；不得靠samebytes丢身份；超限unsupported |
| 稳定性 | >50规则编译重复相同；输入顺序合同明确；<=50旧顺序不变；sequence唯一、break固定 |
| Native seam | 缺 preview/retained/receipt/control/resolution、archive或source漂移均由原helper拒绝；完整验签不替身 |
| Object upload | 全153每项唯一PUT+EOF GET；校验forbid-overwrite、长度、hash、cache、源路径/别名；漏对象/额外PUT失败 |
| Reuse/unknown | exact exists成功；不等existing、截短/多字节/中断/编码GET失败；timeout-before/after只GET无第二PUT |
| 双绑定与fresh | changed source/input/plan/preparation/resource/control、非never-versioned/principal/bucket漂移在下一写前拒绝；先前副作用保留 |
| Configuration | body/sha/domain/resource/managed boundary tamper写前失败；每批20；0/缺/重复/错ConfigId失败；未知/部分写incomplete，不refresh |
| Existing config | unrelated fields不变；old literal→grouped所需删除仍拒绝；A/B各独立正向，不伪称A→B转换成功 |
| Refresh | exact完整URL集合、批次≤50、task查询≤10；foreign/missing/duplicate/changedURL/task状态拒绝；root/目录不支持显式失败，不能剔除 |
| Authority | 实际 TEST ONLY fresh verifier仍拒绝；activate仍拒绝；原production/wire/publicclient false字段保留 |
| SDK/CI/cleanup | 真SDK序列化响应+实例adapter，无socket/credential discovery/global monkeypatch；缺依赖/零tests/skip/超时非0；所有子进程终态，公开receipt不含私钥/签名URL |
