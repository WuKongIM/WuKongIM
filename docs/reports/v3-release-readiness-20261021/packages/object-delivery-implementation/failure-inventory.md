# 原 D 离线首切片建议先行失败库存（未执行）

不是新增产品/GA门禁；均落实续审 README:27–29、43–48、56，原 design:112，resource proposal:22–38、47–48。先测试再实现；保持 v1/v2 原 schema/摘要与生产禁用。

| # | 输入或阶段故障 | 最小可观察结果 |
|---|---|---|
| 1 | 合法 v1 preview、没有任何 stable descriptor | 能生成同核心的 preview 对象/路由预览；原 v1 bytes/digest 不变，无假 stable/生产启用 |
| 2 | v2 缺 preview 或 stable；混用 schema/channel；未知字段 | 原 planner 拒绝，不借内部归一放宽 |
| 3 | TEST ONLY/fake API 成功、伪造 verified/resolution、缺 fresh 生产前置事实 | 只准模拟；绝无生产请求/production-complete |
| 4 | 输入/archive/source/control 读取期间或恢复前改变 | 停止；不使用旧 journal 免验 |
| 5 | alias B/A.rpm 被误从 B archive 查找；成员缺失/错源/SHA/size | materialize 拒绝；仅从指明的 A 成员取 bytes |
| 6 | 同 object key 不同 bytes/size/cache metadata；generated UTF-8 改字/换行 | 拒绝；entrypoint 必须引用已存在的精确物理对象 |
| 7 | Put成功/同字节冲突/超时未知；GET错误、截断、digest/size/cache metadata不同 | 只有实际精确读回可标对象完成；全部确认前绝无激活；不覆盖/删除 |
| 8 | versioning不适用/未知/旧观察；bucket/endpoint/domain换成其他资源 | 真实执行拒绝；fake仅显式模拟，不扩 IAM/任意endpoint |
| 9 | snapshot通配规则先吃掉跨代alias；所有Packages盲加public | 路由表检查失败；A/B原对象与跨代别名各到精确key |
| 10 | `/` 漏映射、`/repo` 被stable抢占、浮动Packages.gz/mirrorlist长期缓存 | 路由/cache表失败；默认preview不变 |
| 11 | 路由合并后邻近/编码/穿越URI误命中、规则数超已声明能力 | 编译拒绝或保持安全的未执行状态，不拓宽读取面 |
| 12 | 旧受管规则残留、未受管配置被覆盖、ConfigId无效、仅部分更新/回读不符 | 记录已观察配置副作用，incomplete；不新增权限“补齐” |
| 13 | 激活前source/audit/control/current-config漂移 | 不执行新的激活动作；不改历史candidate/control |
| 14 | 刷新超时或缓存命中200掩盖回源错误 | incomplete，不声称全球切换/回源验证 |
| 15 | 公共入口或四客户端任一失败/拿到合法但非exact target/CLI身份不同 | 外层不能complete；保留原client receipt的false provenance/publication字段 |
| 16 | stable已公开后回滚到preview-only目标 | 拒绝该回滚；仅原受审阅完整前代composite可供真实回滚验证 |

轻量 seam 测试使用临时目录/固定 fake API 日志，不用 Docker、生产签名私钥或真实云账户。已有签名验证和四客户端的真实运行仍由各自既有入口负责；不为这个切片再创造“完整 49 必须重跑一次”门槛。
