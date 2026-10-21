# 实际 GitHub Issue API 字段验证（只读）

2026-10-21 10:38:04–05 UTC 对现有公开 Issue #1015 执行一次 search 和一次 exact GET，均 exit 0。两者均包含 id、number、title、state、locked、comments、repository_url、url、html_url，且不是 PR；comments=3，repository_url=https://api.github.com/repos/WuKongIM/WuKongIM。

这证明新预检所依赖字段存在于实际 API 返回；不是本 request 的预检通过证明。Search 为 schema 观察只取 per_page=1，total_count=601，因此不满足预检的完整唯一搜索条件；正式 --preflight 仍须查询自己精确 title 并验证完整结果及 exact GET。未创建 Issue、未读评论内容、未发表评论或修改资源。仅归档筛选后的公开必要字段，不含 token 或原始无关 payload。
