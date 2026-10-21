# PR #1021 ready 后只读检查

转 ready 后仅新建 PR Signal run [39617990847](https://github.com/WuKongIM/WuKongIM/actions/runs/39617990847)，attempt 1，14:34:25Z 创建，14:34:31Z 成功。原 candidate 与 three-node runs 保持 attempt 1 success。全部 checks 终态，无待运行或失败检查。ready action 是时间及 workflow trigger 对齐判断；REST run 只直接提供 pull_request event。

第一次 PR GET 失败收据保留；原 stderr 私有 0600，未纳入公共清单。mergeable_state 不作为测试失败证据；不猜测其暂态原因。不触发工作流、不改 PR。首轮 CI 归档保持不变。
