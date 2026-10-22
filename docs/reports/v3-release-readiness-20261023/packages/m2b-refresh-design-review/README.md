# M2b refresh 最小设计：独立 Spec 复核

**设计方向可实施；尚不是实现或生产验收通过。** Base `7255102de81b45c23d52e908ccdde1b7799ee3f5`。本轮未修改工作树、执行SDK/测试或调用云API。作者正在编写的测试 diff 已读，未当作冻结实现复核。

父任务提供的新观察为现存任务33643947491：TotalCount4，四条不同ObjectPath行共享同TaskId，均file/Complete/100%。这消除了此前“实际查询聚合shape未知”的一个具体样本缺口；本代理未独立读取其原UI档案，`design-input.json`明确标注来源。

### 必须明确的三组条件

1. **原始URL必须保持精确身份。** `urlsplit`会剥离部分控制字符，不能只验证解析后的path再提交原字符串。补原串控制符/空白、空`?`/`#`、authority别名、编码/空段的拒绝。根和尾斜杠仍用File；仅对该边界允许末尾一个斜杠，不能宽松rstrip或修改全局object-key规则。
2. **按查询组验证，再按整轮计完整覆盖。** 每组Tasks需list、每row需dict，TotalCount需真正int（拒bool/string/float）且等于rows。每row的ID须属于本次group，该group全部ID都要使用；先拒URL重复再建URL→ID映射，整轮每目标URL恰一次且无缺漏/外来项。跨轮允许row顺序变化，禁止映射变化。缺页、截断、缺TotalCount均不能视为Complete。
3. **批内ID合法、跨批允许共享，写后失败保留不确定性。** 每提交返回1至该批URL数的ASCII正整数ID，批内重复拒绝；跨批共享first-seen去重。第二批与第一批部分重叠也要覆盖（例如[a,b]、[b,c]）。attempted须在调用前登记，任何提交/查询异常均不盲重提、不继续公共验收，driver保留incomplete与已有证据。

没有必要引入新查询API、额外权限或分页猜测。ById文档没有分页输入；TotalCount不相等时应保持未完成/拒绝，不能只验证第一页然后放行。

### 最小回归范围

`minimum-test-matrix.json`列12组：真实4URL共享ID；178URL跨4批共ID；跨批部分重叠ID；多查询组；旧11独立ID与50/51/1000/1001边界；全量输入预检；ID形状；查询类型/TotalCount；URL/ID精确覆盖；状态/跨轮稳定；写后超时无重试；driver全量scope/失败收据/清理和false权限声明。

作者现有先写测试已覆盖主要正反例。建议补充剩余类型/控制符/空分隔符、跨批部分重叠和跨组错配场景，保留真实SDK边界测试；无需为每条检查造结构镜像测试。

### 保留的限制

≤1000URL、每批50仍有确定调用数，但最坏1000独立ID为20次提交、每轮100次查询：默认3轮300次Describe，max10轮1000次。SDK单次connect5s/read10s和poll_interval不是整次操作的短deadline。TEST ONLY driver须保留原外层截止；是否额外增加provider共享deadline是另行明确的范围决定，不能默默增加无限重试。

跨请求同域同秒聚合或结果暂未完整可造成严格拒绝，这是有意不宣称成功；刷新写可能已经生效。不能把拒绝当无副作用、可再次刷新或生产正向验收。该一个真实样本也不构成完整CDN版本兼容承诺。

`source-freeze.json`绑定base实现、文档及规则；`spec-review.json`保存可机读约束。最终实现、RED/GREEN、driver实际计数和原始UI观察档仍由作者/父任务的后续证据完成，本报告不代签。
