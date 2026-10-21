# Stable clients v2 差量独立复核

**结论：原两项 P2 的实现已修复；新增两阶段网络清理问题仍需 v3。不能以 v2 进入通过状态。** 本轮只审 driver/tests 与 workflow 的 47→49 计数变化，全部其余 v1 原字节保持。核对的 manifest SHA256 是 `e45c45554d63f9c15f889ccd71a4c7f453c1f7eeec3dac4c7374a76dbbb60a20`，delta SHA256 是 `d08db68f6aa4be0a0b9755f59fc1b78464cc77799244bd6f5ad2a2fc0f2e689b`。

## 新增待关闭项

**[P2] 第二阶段重新 inspect 已成功删除的网络导致误失败。** 第一次 `cleanup(final=False)` 成功删除网络后仍保留 `self.network`。第二次 cleanup 从该字段重新加入旧 ID；真实 Docker 的缺失网络诊断为 `network <id> not found`，不含当前分支接受的 `No such`，因此最终收据错误标记 `network identity query failed`。root 用只读 inspect 实测该 stderr 后提示本审；`network-repeat-probe.py` 在冻结 v2 的实际 cleanup 上用该诊断替身重现：第一次 verified=true、资源已清零，第二次 verified=false。建议成功删除时从活动 ID 集合移除，尝试名称历史继续保留并始终执行 label 查零。

本审最初两阶段替身统一返回 `No such container`，没有覆盖网络真实 stderr，曾发出“当前未见新阻塞”的暂定更新；此处明确更正并保留原探针结果，不能将替身通过当作真实 Docker 行为证明。

## 已关闭的原发现与范围缺口

- 不确定创建：attempt/name 在调用 Docker 前登记；异常结果通过本次 label 查出，进一步核对 ID/name/label 后删除。独立探针调用实际 exercise，在首次创建生效但未返回 ID 时保留原 TERM failure，查询并清掉本任务容器，外部 sentinel 不变。此前空集合直接声称无遗留的路径已消除。
- DNF 缓存：挂载位置已包含 `/tmp/client-root`；缓存首轮必须包含非空 metadata，重复 A 轮必须有同 device/inode/hash/size 文件且 epoch arming 后 atime 前进。独立实文件探针确认仅文件存在不通过，另一个 Python 进程真实读后通过。该信号不要求停止网络刷新，也不将 HTTPS 304 探针冒充 native cache 复用。
- 缓存权限：inventory/arm/remove 由已固定镜像的本任务 root helper 执行，network none、只写单个 cache mount、只读 control，权限限 DAC_OVERRIDE/FOWNER，无宿主 socket/凭证。捕获 atime 在 observer 自身读 hash 之前，hash 后再 arm，避免自身读取造出阳性。
- fixture 导出使用 host UID/GID 且拒绝 root host，输出目录保留非 root 隔离并可由 host 清理。root cache 子树在停止所有 readers 后由 helper 清空，最后再验证 Docker 库存；阶段一 errors 累积进入最终收据，不能被后续零库存洗白。受控第二次 TERM 在清理块内被忽略；SIGKILL/host loss 仍无完成保证。
- 新增 actual installed old-version 与 missing-wkcli 两个调用：都先对真实 A 包进行 hash 校验/安装，然后调用原 acceptance 脚本；old-version 只改变期望版本，missing-wkcli 仅在本次沙箱安装后移除该文件，并要求准确缺失诊断及删除标记。总计变为 49 native cases。这里确认的是实现接入，原生用例尚未执行。

## 本轮证据与限制

16 项相关轻量 tests 独立通过。关闭探针覆盖实际 exercise 不确定创建、两阶段 helper 成功/未知结果、缓存读取与仅存在区分、cache 删除不跟随外部 symlink。所有 Docker、编译、签名操作均为替身或未调用；未运行 Docker/GPG/产品编译/原生包管理器，未修改源码。

独立关闭探针中的两阶段成功结论适用于其容器案例，不能替代后来新增的网络案例。v3 只需修复成功删除活动 ID 的精确回归与小 delta 复核；无需重新审查 unchanged production validator。首次 Linux CI 仍必须实际证明四 distro、49 cases、cache、安装和最终清理，再保留首失败做后续处理。
