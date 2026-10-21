# 首次 go-vet 失败增量独立复核

结论：`pkg/transport/internal/buffer/retained_test.go` 的索引/指针遍历修复保持原测试覆盖，无可行动 finding。

首次 named `go-vet` 原始 exit 1 与日志已保留在 diagnosis/named-checks；提示 range value 复制 `slabClass`（内含 `sync.Pool` / `noCopy`）。独立读取 ed705 文件得到与修复前库存一致的 SHA256 `f751bf595b75fda5bd7a676205716395a41bb1831fb4cc5635e90ea4f5b2032f`，因此确认是基线既有测试问题，不是本次 local profile 改动引入。

增量只把按值 range 改为索引 range，并取 `&DefaultSlabPool.classes[i]`。`n=512..1<<20` 全部 payload 尺寸、class 顺序、跳过过小 class、首个 fitting class 的两倍开销上界断言及 break 均原样保留。读取的是相同 class.size，不复制池、不调用新方法、不修改共享 class。此函数没有 t.Run、闭包或 t.Parallel，故没有循环捕获绑定问题；同文件另一 Get/Release/capacity/retained-charge 测试完全不变。

逐级检查根、pkg、pkg/transport、pkg/transport/internal、buffer：适用 AGENTS 为根文件，无适用 FLOW。已读取实际 slab.go 结构；其生产 Get 实现本身使用同样索引加指针模式。本次不改产品、公共 API、测试预期、阈值或受保护控制。

此记录追加在原 10 文件审查之后，不覆盖原审查或首失败。未运行本地 Go 编译；focused test/vet 及完整 named go-vet 由实现代理负责。本记录不提前宣称它们通过。源码冻结、适用规则 hash、原始日志引用见 vet-increment-review.json。
