Standards v2：0 项新增硬违规，0 项可报告的 possible smell；v1 结论与失败历史保留。

AGENTS.md:129–137：先行矩阵及冻结存在；精度夹具假红不计为有效 RED，已用精确 v1 helper 重放 11 场景，8 个目标失败、0 error。终版两 Go 入口、20 Python 方法/79 进程场景通过，均在 integration 层。

AGENTS.md:86–91：terminal-evidence.py:68–136 用单一闭集校验和英文注释说明时间/阶段边界，未扩通用框架；原采集预算不变。integration_test.go:18–42 仅注入子进程环境并核收据，避免污染产品 WK_ 配置。知识文档同步，无 protected 变更。

Mac 12 分钟补充 integration 审读时仍运行，不能替代 Linux 原 policy 9 分钟检查；未复跑、未读 Spec 轴。
