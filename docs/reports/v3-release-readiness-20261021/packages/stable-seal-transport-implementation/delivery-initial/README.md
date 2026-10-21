# Stable 外部封存与组合运输计划：本地交付收据

最终源码是 `final-freeze-v2/`，15 个文件、5 个生产辅助脚本；其余为测试和文档。基线为 c8aae37，父任务确认 #72 合并 main 3fe0413 与其树相同。本工作树尚未提交或推送，后续只应搬运本切片，不重复携带 #72 提交。

本切片复用已有 bind/seal/readback 与 planner：stable 首次上传前验证历史 Git 的 10 个控制文件、12 个 producer、祖先与当前 main，重取来源七资产并实际调用既有 attestation/native metadata 验证器及候选验签；最终 PATCH 仍复核同一来源和 main/tag/policy。原 candidate tar/receipt/null IDs/签名原字节不变。TEST ONLY 仅 loopback、禁止所有 redirect 与代理配置；默认 preview 路径和 schema 保持原样。组合计划保留全部输入代，不上传、不激活、不删除；本地 seal JSON 不是已认证的 GitHub 事实。

## 验证代次

- v1：468 项完整 Python 回归通过（1 项既有本机 RPM skip），4 项 contract 与 contract.sh 通过；真实 pinned Linux 工具容器通过 31 candidate + 8 transport 用例。
- 独审 R1：resolution 路径在 hash 与 JSON 解析间被替换可造成锚不一致。保留 `35-resolution-race-red.log`；v2 仅从已读 raw 解析，并保留 duplicate/canonical 检查。独立替换 probe 已确认关闭。
- v2：3 项 composite + 31 项既有 planner 回归通过；最新真实 native 再次通过 31 + 8 用例（56.924 秒），TEST ONLY 私钥树和 scratch 清理均通过。没有把 v1 的完整 suite 冒称在 v2 上重跑。
- 38 个保护文件原字节未改，其中含全部 12 个候选 producer；最终源码与冻结 manifest 再核一致。

初次 native 运行 29.266 秒以 137 结束，事件记录同一容器发生 TERM/KILL，发起者未知，不能认定 OOM。事件与宿主文件时间相符；之后一次隔离复验通过。后续 native-final、native-source-anchor、native-resolution-anchor 分别对应新增 retained 用例、来源字节锚修正、独审 R1 修正，均有独立源码摘要和运行收据，不是覆盖首失败取绿。

## 重放与边界

Python 命令见 `37-generation-2-regression.json`；完整套件使用 `python3 -B -m unittest discover -s tests -p 'test_*.py'`，contract 使用 `bash tests/contract.sh`。所有先红后绿日志独立保存。

最新 native 命令及固定 image digest 见 `native-resolution-anchor-result.json`，输入脚本摘要见同名前缀 `inputs.json`，运行器见 `run-native-resolution-anchor.py`。容器采用 linux/amd64、network none、read-only、drop ALL、nonroot、1 CPU、512 MiB、128 pids；仅挂本任务代码、公开可信脚本和输出。Mac arm64 通过 Docker 运行该固定 amd64 镜像；匿名空 Docker config 拉取，未改变登录认证。可信工具文件来自 manifests/trusted-toolchain.json 指定的 WuKongIM ed7e67d8f781b4f92872e42116c11b304fe03b5c，各脚本按该 manifest 校验。重放需要将收据中的本机绝对路径调整为审阅的本地 checkout/scratch；source_sha 在本地证据中只是基线标签，实际未提交代码身份以 inputs/freeze 摘要为准。

真实 stable 来源的联网 production gate 未在本次执行，TEST ONLY 明确跳过其 GitHub provenance 声明；preview 仅为已有结构 fixture。本切片不提供 stable enabled、生产 workflow 接线、真实 GitHub seal、OSS/CDN 写入/激活、公网四客户端验收或 GA 发布。仍只支持既有首代 add_release candidate；不是新的签名交换协议。

归档仅使用 `ARCHIVE_ALLOWLIST.txt`，完整性见 `SHA256SUMS`。不包含测试 tar/二进制、任何私钥、缓存或 Docker 配置。
