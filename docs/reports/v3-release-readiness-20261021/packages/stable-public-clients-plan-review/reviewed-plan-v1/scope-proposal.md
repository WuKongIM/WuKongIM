# Stable 四客户端与 A/B 切换：下一最小切片

状态：只读设计，尚未写实现、跑测试、启动容器或云资源。基线 packages #73 `59549efe1074562928b8ae175faeda8778849e0e`，tree `6237c709d8cca42cc716a7aee796760c3cd867a9`，其 base 为 main `3fe04138403a7c9443e4ede457dccfa16dff2b00`。实施应从 #73 最终被批准并合并的等树主线开始；不得移动 #73 已验收 head。

## 目标与当前缺口

交付现有 `validate-production-package-clients.py` 的显式 stable 路径和无凭证 Linux TEST ONLY 原生验收：四 clean clients 真正读取 stable APT/by-hash、DNF mirrorlist；两个不同签名代在确定请求边界切换；四发行版实际安装认证下载的真实 server/wkcli 并通过现有全部 CLI 检查。生产 stable 仍关闭；本切片不创建真实 audit Release、不上传/激活、不增加公网路由，不宣称正式版已发布。

已有 validator 在 :117–138 固定四个 amd64 镜像，:1427–1666 复用下载哈希/精确 target 与单独 installed CLI，:1372–1425 已有 600s 容器/30s 命令及 bench/query/migrate 检查。实际差距是 preview 被硬编码在 version、v3 snapshot 正 ID、证书路径、bootstrap 包名、APT suite 和 RPM baseurl。#73 planner :441–453 明确把 `/status.json`、`/bootstrap/manifest.json`、`/repo` 留给 preview；stable 不能借这些对象冒充自身身份。31+8 中 retained 是同一 archive 的不同 TEST ONLY observation ID，并非不同 bytes 的 A/B，且没有四客户端。

## 最小生产接口

保留所有未传 `--channel` 的 preview 参数、命令、v4 receipt 与现有测试行为。新增显式 `--channel stable`，使用 `--transport-inputs PATH` 消费 #73 已有 v2 composite 输入（不是新 manifest），`--base-url https://packages.githubim.com` 与必需的 exact `--expected-version X.Y.Z`。复用现有 `make_composite_plan` / stable descriptor 验证和 canonical archive 提取，重新验 APT/RPM 签名、候选/receipt/control/source 锚；不复制闭包引擎。原 candidate null ID 与签名 bytes 不改。

从 current stable descriptor 及其 immutable snapshot 路径取 stable bootstrap inventory、证书、payload 白名单；不能读根 preview status/manifest 作 stable 授权，也不要求不存在的 `/repo-stable`。stable bootstrap 已有独立名称及文件路径（builder :122–136），复用固定 `STABLE_LAYOUT`。安装 bootstrap 并校对原 packaged 配置后，让原生客户端使用它：APT suite stable；RPM 真 `mirrorlist=https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist`，绝不换成 baseurl。mirrorlist 只能指向已锚定 stable snapshot。

普通 stable 模式拒绝 test-only 描述符/测试 CA/私网 override；测试模式必须双向显式 `--test-only`。稳定验收输出独立 stable schema，保留精确 expected target / 下载 bytes / bootstrap / 四客户端 / installed CLI 字段，并绑定输入 descriptor 与生成 plan digest。原 v4 preview receipt 不变。离线 resolution JSON 仍是 caller-reviewed evidence，不能把 helper 重新验签声称为 fresh GitHub provenance；正式外部封存和激活仍由既有 fence/后续生产执行完成。

`--verify-installed-cli` 在 stable 下沿用原强制门禁。生产 expected commit 来自已验证 candidate source；TEST ONLY candidate source ID/SHA 仍按合同 null，因此测试 driver 另提供固定真实产品 checkout SHA，作为二进制检查的预期值，receipt 明确来自 TEST ONLY build，不回填 candidate provenance。该测试输入不得覆盖生产模式的 source SHA。

## 实际产品 fixture（必须完成，不以 dummy 替代）

旧 `manifests/trusted-toolchain.json` 的 `ed7e67d...` 只打包 wukongim，没有 wkcli，不能完成这里的 installed CLI。新测试 job 单独、只读 checkout 产品 `fc18221282f289088ec9b4282fda8e0bfc67a3b8`，不修改该 manifest。固定 Go 版本取其 go.mod（1.25.11 toolchain），编译该源的 `./cmd/wukongim` 和 `./cmd/wkcli`，两代分别嵌入 `3.0.0` / `3.0.1`、同一实际 source SHA、`build_source=release`。保存 checkout/tree、编译参数、go version -m、两个二进制 SHA 和包 metadata。版本是 TEST ONLY 构建参数；它不表示存在相同版本的正式 Release 或 attestation。

用当前 native fixture 原生 dpkg-deb/rpmbuild 路径增加显式真实 binary payload 输入，构建各代 DEB/RPM，覆盖两个实际可执行文件，保持所需 systemd 依赖和原生包元数据；默认 tiny fixture 不变。不能修改 beta 包元数据冒充 stable，不能写假 CLI 来返回预期 JSON。两代共享一次生成的独立 TEST ONLY 两族公钥/同一 bootstrap bytes，分别走实际 prepare/sign/compose/archive/audit；不是复制同一 archive 改 ID。抽取现有 tests-only fixture 函数，留存公有产物前先确认 GPG 私钥树销毁。十二个 producer 脚本不改。

四 clean clients 每代先证明产品未安装、安装 data-only bootstrap、认证下载精确 A/B bytes；随后另起四个 fresh 容器原样安装已校验的包，调用不改动的 `validate-installed-wkcli.py`，校验 server 和 CLI version/commit/release，以及有效/无效 bench、offline query、完整 migrate diagnose、源不变/目标未创建。对旧版本、错 SHA、缺 wkcli 明确负例；不可只检查 version 或只 mock 调用。

## 网络、镜像与生命周期

复用 `APT_CLIENTS` / `RPM_CLIENTS` 的四个完整 linux/amd64 digest（精确值在 source-freeze.json），不引入 mutable tag；signing fixture 复用现有 pinned image `sha256:769d49a4882439a35fdf4497a4b97f09843a9dc92f4074447d48e43b2614d922`。拉镜像/Go依赖准备在测试前完成。

A/B 阶段：唯一具名 `--internal` Docker 网络；受限 HTTPS fixture 服务仅此网络、DNS alias `packages.githubim.com`、SAN 同名一次性 TEST ONLY 证书。四客户端保留原 URL/正常 TLS 校验，仅只读挂载这次 CA（APT CaInfo；DNF sslcacert，sslverify/gpgcheck/repo_gpgcheck 均开）；无宿主端口、host networking、代理、宿主 Docker socket或真实凭证进入客户端。服务器只提供 planner 精确 allowlist 的原 bytes/Cache-Control/ETag，路径穿越/任意文件拒绝。测试 coordinator 可通过现有 injected fetcher 在服务容器内做同域名真实 TLS GET，主机不改 DNS，不把本地结构读冒充公网请求。该 adapter 仅测试代码，普通 CLI 使用现有 HTTPS fetcher。

实际 installed CLI 容器与 A/B 网络证据分离：沿原合同允许发行版正常源下载 Python/systemd 等依赖；产品只装已经下载验明的本地 package，RPM localpkg_gpgcheck 保持。这些正常依赖流量不作为 A/B 证明。不为了私网环境跳过依赖检查或 CLI cases。

所有新容器/网络均具本次随机唯一名和 label，记录实际 ID，只清这些 ID，finally 尝试清理并检查零遗留；不用全局 prune/pkill。沿用每 installed client 600s、每 CLI command 30s，新增 download client 固定上限及有界日志，外层 job 最多 60m，不自动重试。TERM/异常/断流需轻量进程/runner测试；硬 kill/runner 丢失不能宣称 finally 保证。公开 receipt 写失败也不能翻成通过；always 上传 JSON/有界请求日志，不上传私钥或整个包树。

## 确定性 A/B 与版本判定

服务端在精确请求屏障切换，无靠 sleep 猜时间：返回 A InRelease 后切当前入口 B，Ubuntu/Debian 必须实际请求 A 指定 SHA 的 by-hash 并下载 A；返回 A mirrorlist 后切 B，Rocky/Alma 后续 repomd/.asc/repodata/RPM 必须仍来自 A snapshot。B 的 clean round 必须下载 B。记录每条实际 URL、所选代、响应 size/SHA、status、缓存头和客户端结果。

固定正例要求精确指定代。测试“缓存仍命中完整 A”时只能记录一致性/旧代可用；若调用者目标为 B，即便 A 签名合法也必须失败。另覆盖持久化客户端缓存、304/ETag、缺旧 by-hash/metadata、错误 repomd.asc、错误 mirrorlist/redirect、TLS/下载断流；失败必须出在目标校验层，不能把 usage/工具缺失视作预期拒绝。没有跨对象/全球 CDN 原子性声明，也没有 GC。

## 固定文件范围（预计 10 文件）

1. scripts/validate-production-package-clients.py：唯一生产入口变化，closed stable adapter、复用旧检查。
2. tests/test_validate_production_package_clients.py：先红 preview 保持/新模式拒绝与绑定。
3. tests/native_stable_clients_drill.py（新）：A/B server、实际四客户端/installed CLI、公有收据与 owned cleanup。
4. tests/test_native_stable_clients_drill.py（新）：模式/固定镜像/私网/上限/cleanup/receipt 静态及轻量失败合同。
5. tests/native_stable_candidate_drill.py：最小 tests-only 公有 fixture seam，原31+8行为不变。
6. tests/native_source_metadata_drill.py：显式真实 binaries 输入；原 tiny metadata7行为不变。
7. .github/workflows/native-package-repository-drill.yml：新增独立只读 test job（contents:read、无 Environment/secret），固定源码及 action pin、always 公有artifact；不改原 job。
8. docs/release-contract.md：stable client 与真实/public界限。
9. docs/stable-audit-transport.md：输入/路由/A-B合同。
10. CHANGELOG.md：新增稳定渠道客户端验证能力，不声称已激活。

不改 channels、keys、audit/tag/permissions、生产 publish/reverify/CLI workflows、12 producer、schema/archive bytes、composite planner、/repo或preview历史快照。全仓 CODEOWNERS 原样正常审阅；本范围不涉及 WuKongIM 的 protected Issue/Review Agent 控制。工作流仍需按release contract读写合同及静态检查，不能把「只测试」当免审。

## 交付顺序与边界

先落 failure matrix；先跑 parser/fixture缺口红灯再实现；原 preview client suite 全回归、全部 Python+contract，通过后 draft PR 首次 Linux native 四镜像与 A/B/installed真实验收。保存首次失败、源码/镜像/请求/下载/CLI/cleanup收据，再独审。不先改 enabled，不自动合并。实现若需要改任何 producer/公共路由/降低 CLI 门禁，应先回报缩减或重划范围；不能藏在 fixture 修复内。

本 PR 可闭合四原生客户端的 stable 路径与 TEST ONLY 切换/安装能力；真实 packages.githubim.com、OSS/CDN、真实 source attestation/immutable audit 与 exact正式版本的公网四客户端仍须后续执行，才可称发布完成。
