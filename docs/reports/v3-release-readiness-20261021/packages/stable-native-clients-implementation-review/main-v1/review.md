# Stable native clients 主实现独立复核（v1）

结论：**请求修复 2 项 P2，再做 v2 差量复核和首次 Linux native CI。** 本轮未运行 Docker、GPG、产品编译或原生包管理器，没有任何公网/云/发布修改。方案列明的 47 个 native cases 尚未执行。

基线 `335cd2569d2b62de577916ed86c097fd3a22547e`，实现 manifest SHA256 `88e3cfb7e27b340eab43281491e01b88adfa12f4d8b142cda1746943ca7c7c6b`。开审及归档时均验证全部 10 文件 size/SHA 一致，原字节保存在 `source/`。沿用 `fixture-freeze-v1/review.md` 两文件独审，不将其文件 I/O 探针误报为 native 签名验证。

## 发现

1. **[P2] 不确定的首次 Docker 创建会被误报为清理完毕。** `tests/native_stable_clients_drill.py:418–420` 在 `checked(docker create)` 完整返回之后才登记 ID；`:243` 又只在已有登记 ID/network 时查询本次 label。若 daemon 已创建容器，CLI 返回被 TERM/超时中断，集合仍为空，`:255` 生成 `verified: true`。`probe.py` 调用实际 `exercise/create/cleanup`，只替换 native 命令与编译/平台依赖，复现 daemon 有一个本任务容器、没有任何 cleanup 查询、receipt 却 verified=true（`uncertain-create-result.json`）。这也使旧 fixture 可能继续存在。创建前登记明确的尝试/唯一名称，未知结果按本次 label/name 找回并核对实际 ID；只要尝试过创建，必须完成有界的零遗留检查，不能从空集合推断未创建。不要全局 prune 或清理外部 sentinel。

2. **[P2] RPM 的持久缓存挂载没有落在 DNF 实际缓存目录。** `tests/native_stable_clients_drill.py:517` 挂载 `/tmp/dnf-cache`、`/tmp/dnf-state`，而生成的 `RPM_SCRIPT` 使用 `/tmp/client-root` 作为 installroot。DNF 官方文档说明 cache 跟随 installroot，官方 4.14.0 的 CLI 对 cachedir/persistdir 都加 installroot，实际目录是 `/tmp/client-root/tmp/dnf-cache` 与 `/tmp/client-root/tmp/dnf-state`。当前三个 cache cases 没检查缓存目录是否真的产出/复用，因此可仅用三个冷下载而假通过。调整 tests-only mount 到实际目录，保存首次 cache 文件和后轮使用证据，不能只改 case 名称或降低 DNF 签名检查。依据：[DNF 命令文档](https://dnf.readthedocs.io/en/latest/command_ref.html#options)、[DNF 4.14.0 CLI](https://raw.githubusercontent.com/rpm-software-management/dnf/4.14.0/dnf/cli/cli.py) 的 `_read_conf_file`、[配置实现](https://raw.githubusercontent.com/rpm-software-management/dnf/4.14.0/dnf/conf/config.py) 的 `prepend_installroot`。这是官方行为和当前参数/挂载组合的静态结论；本轮未声称运行了 pinned 镜像中的 DNF。

## 范围差异

generation-2 明确要求真实 installed 的旧版本、错 SHA、缺 wkcli 负例。目前 47-case driver 仅有真实 wrong-commit；`reject-exact-B-target-with-A-entrypoint` 在 InRelease 前置读就拒绝，并未执行已安装旧版本。旧 `test_installed_cli_acceptance.py` 的 identity 字段检查使用替身，也未替代真实 missing-wkcli。应补充这些最小原生负例，或明确记录缩减并保留验收缺口；不能宣称完整 failure matrix 已闭合。此项属于计划覆盖不足，不是已证实的 production validation 绕过。

## 已核对的实现边界

- 独立 AST 比对：全部既有 helper/class 未变；旧 `validate_clients` 的 preview 执行体保持逐 AST 相同，只前置 closed stable 分流；parser/main 只接入新参数。34 项 preview+stable admission 测试独立通过。
- stable 调用 `make_composite_plan → _stable_snapshot → verify_candidate`，真实 APT/RPM 验签仍来自原引擎，未相信调用方 `verified` 字段。重新提取 archive 后检查 candidate hash/size/snapshot，固定 stable descriptor、严格 target/source，installed CLI 强制开启。TEST ONLY candidate null provenance 未回填，receipt 明示 GitHub/publication 未验证。
- 生成的 RPM shell 与 STABLE_LAYOUT 的名称/配置一致，真实 mirrorlist、gpgcheck/repo_gpgcheck/sslverify 保持，无 baseurl。APT stable bootstrap/source 路径、suite 与 keyring 一致。生产参数没有额外 CA/私网 override；私网 runner 仅在测试代码注入本次 CA。
- A/B server 仅按 planner object map 读 bytes；A response 选定后切 B，保留 A immutable objects。成功下载 hash 独立锚定候选，不能以观测下载反向设预期。by-hash 和 mirrorlist 请求证明以及 exact-target-B 拒旧 A 检查存在；304 独立 TLS probe 明确与 native 缓存轮区分。
- ordinary download negative 必须非零并排除工具/超时/usage/Traceback退出，不以 Docker 的启动失败算通过；实际原生诊断文字适配仍须首轮 CI 核实。HTTP worker 内部错误会使请求证据检查失败。
- 新 workflow 继承 contents:read，无 secret/environment/publication 调用，固定产品源/Go/actions/images，旧 job 内容不变。artifact 限于 JSON 和请求日志，不上传 package/private trees。超时与受控 TERM 路径有设计，但必须修复上述不确定创建空窗；SIGKILL/host loss 限制已如实说明。
- 独立运行 10 项新轻量 drill/fixture boundary 测试和 12 项既有 installed CLI tests 全通过（工具输出保存在 `.log`）。这些通过并不能覆盖上述缺陷，亦不是 47 个原生案例的运行证明。

## 标准与剩余验证

未发现超出 10 文件允许范围或修改 producer/keys/channels/生产 workflow 的情形。该 packages checkout 无 AGENTS.md/FLOW.md；遵守委托携带的工作约定与 generation-2 合同。没有修改任何源码。

两项修复先写失败回归后落实现；v2 只需差量独审，随后第一轮真实 Linux A/B、四 distro installed CLI、cache 与所有失败案例，保留首失败及清理结果。最终仍须真实公网 exact-version 验收，TEST ONLY 通过不等于可以正式发布。
