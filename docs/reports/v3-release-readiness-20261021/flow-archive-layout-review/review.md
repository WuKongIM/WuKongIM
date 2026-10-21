# 历史 FLOW 归档格式只读复核

**建议同意：将恰好6份历史归档改名为 `FLOW.md.source`，原字节不变。仅改5份仍会失败。** 当前 `scripts/flowcheck/main.go:291` 以精确文件名发现 FLOW；`:150–166` 仅在invalid为0后才比对生成index。原5个缺引用错误隐藏了第6个历史FLOW导致的index不一致。6个归档移出导航后，剩余发现路径88项与现有 `docs/development/FLOW_INDEX.md` 的88项逐一完全一致。无需修改校验器、policy、生成index或真实运行源码。实际named check仍须root应用后运行，本审查未执行。

## 精确实施范围

`suggested-path-map.json`列出6个原归档路径、新路径、size和SHA256，均已读取并核对目标路径尚不存在。它是建议输入，不是已执行收据。root可新增一份正式 `archive-path-map.json`，schema与字段保持简单；每条记录以证据根相对路径绑定 `original_evidence_path`→`archived_path`、bytes、sha256。只做这6个一对一改名，不保留原名副本或软链接。保留原failed named check收据。

第6份是 `cloud/released-continuation-go-review/files/internal/usecase/chatlifecyclerun/FLOW.md`。它目前有邻接源码而可通过单文件检查，但仍是历史审查副本；一致改名避免将它冒充主仓库模块导航。

## 历史清单与链接影响

- `cloud/source-b469-manifest.json`及顶层历史 `audit-archive-manifest.json` 的4个FLOW路径继续保留原字节，属于原始采集布局。
- `cloud/released-continuation-go-review/{SHA256SUMS,archive-sha256.json,ARCHIVE_ALLOWLIST,source-freeze.json}`继续不改；其 `files/.../FLOW.md` 通过新mapping可恢复。
- `cloud/rc-next-execution-plan-post1020/{SHA256SUMS,ARCHIVE_ALLOWLIST.json,source-and-rule-freeze.json}`与 `cloud/post1020-next-plan-copy.json`原记录不改。source身份/path是历史事实，不重写成`.source`，也不将旧工作树head标签升级成文件的已提交来源证明。
- 本次扫描没有发现指向这6份归档的直接Markdown链接。FLOW内部的原相对引用保留，不为满足当前archive布局改源码。原历史JSON、patch、log中路径仅作事实记录，不批量替换。
- 当前顶层 `SHA256SUMS`更新为6个新目的路径，添加mapping与说明自身；此清单验证现在的存储布局。所有旧历史清单的文件SHA仍不变。

## README应写清的恢复边界

`.source`是原始证据字节，并非活跃FLOW。当前归档检查使用顶层清单；旧子清单不能直接在重命名后的目录原地运行并声称成功。需要校验旧布局时，在**仓库外新临时目录**复制相应公共证据子树，校验mapping的SHA/size后把6个`.source`复制回原证据路径，拒绝不同内容的已有目标，再按原根执行原清单验证。不要在工作树内恢复原名或创建FLOW软链接；不执行历史脚本。

恢复名字只恢复历史清单布局，不能补齐当时没有归档的源码邻接文件。因此完整历史FLOW导航/运行测试仍需要相应完整原源码树，不能声称这份局部archive单独具备该能力。实现一个泛化迁移工具并非必要；简单mapping和明确恢复说明足够。

## 应用后有限验收

1. 六个新文件逐字节等于原size/SHA，原路径不存在；其余原历史payload/manifest以及真实FLOW/index/policy字节不变。
2. 顶层清单覆盖新路径与新增说明；所有被引用文件真实跟踪，Markdown链接复核。
3. 运行原命名 `flow-doc-contracts`，预期88项导航、0invalid，既有20warnings不升级为新失败。原失败保留，新结果绑定新源码/归档身份。

没有产品或安全规则变更，没有新增发布门禁。本次只读审查未改任何仓库文件。
