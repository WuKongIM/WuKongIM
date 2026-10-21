# Bash 清理补丁独立审查

**审查范围内未发现阻塞问题。** 基线 `eda77697db42ba14f14bfad58cdd3557342b9989`，冻结patch SHA256 `21c51ebae7ac2c78de39699840be5206eaf710e7414c6442efd5f93261b6485c`；3个源码文件逐一独立核对SHA一致。审查者仅阅读源码/差异/收据，没有编辑实现或重复执行测试。

行为：`scripts/cloud-sim/analyze.sh:181–207` 的4个成功/无操作路径显式返回0，避免Bash5在失败EXIT trap内继承原状态而提前结束；关闭失败仍输出原有警告。`cleanup:223` 先捕获退出码、关闭EXIT trap，按原顺序处理bridge/close/worktree/temp/tools，最后显式退出原码。补丁没有改变其顺序或关闭次数栅栏。

`run-wukongim-three-node-chat-lifecycle-local-baseline.sh:464–474` 只改变首个signal handler的成功返回；first status和signal名称在转发前固定，active-child ownership、转发栅栏、后续信号分支及wait/reap循环均原字节保留。既有真实TERM回归验证一次转发、joined标记、退出143、进程消失和证据封口。

测试证据：相同Bash5.2.21 focused命令先红后绿；新8子例覆盖原17/143 × 成功close/无open/已有close尝试/close失败29，断言完整cleanup trace、私有临时文件清理及失败警告。修复后的Bash3.2新8例+baseline也通过；Bash5的9个顶层现有失败回归全部通过。具体log/receipt的独立SHA见review.json。新测试有integration标签、先写失败库存、复用既有有界调度和helpers，未添加新云调用或放宽门禁。

局限：实际Bash5是Mac上从GNU源码构建，与Ubuntu具有相同宣告版本但不是相同OS/发行版。Linux observer exit255仍未定位，原observer源码未改，Mac pass不能覆盖原失败。完整Linux scripts门禁仍待整合后新head验收。根AGENTS要求user-visible条目，已提醒root在整合CHANGELOG中记录本修复；3文件代码审查本身无缺陷发现。
