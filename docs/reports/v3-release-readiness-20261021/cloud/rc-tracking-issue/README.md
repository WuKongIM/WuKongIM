# 首次云 RC 的补建跟踪 Issue

原用户付费授权明确包含 tracking Issue。首次运行前遗漏创建的事实保留；没有以补建记录改写运行时历史。

2026-10-21 10:42:38 UTC 一次 `gh issue create` 成功创建 [#1018](https://github.com/WuKongIM/WuKongIM/issues/1018)，请求 exact title、原 source/control 和预算不变，正文链接固定证据提交 `88c1d311d5604e77215c1e2275503f76c066d769`。创建前完整 search 返回零项；没有评论、标签/Issue Agent授权命令、云 dispatch 或新采购。

第一次 readback GET 非零，调用在写收据前断言退出，原 stderr 未保留，原因未知；该局限单独记录。第二次只读 GET 成功，逐字核对正文及 open/unlocked/exact identity，没有重试创建。原始第二次公开 GET 留在本机，归档只含其必要字段投影与命令收据。

随后运行新 `comment-request-issue.sh --preflight` 一次，对真实 Issue 读取成功，输出 ready/issue_ready/1018。仅此脚本的精确 SHA 和实际 stdout/exit 在收据中；这不等于完整 orchestration、云启动或未来评论权限的验证。生产 source/code 未切换，原失败和清零证据不变。
