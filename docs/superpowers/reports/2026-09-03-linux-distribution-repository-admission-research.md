# WuKongIM 进入 Debian、Ubuntu、Fedora 与 EPEL 官方仓库的准入研究

- 日期：2026-09-03
- 仓库修订：`0c7190c72c6433d322fcd8a43779939ec33d99a1`
- 范围：官方发行版仓库的准入条件、流程、维护责任、技术/许可/依赖/安全/更新约束，以及 WuKongIM 当前主要差距
- 来源原则：仅使用 Debian、Ubuntu、Fedora、EPEL 的官方政策、维护者文档和官方基础设施页面；项目判断来自本仓库当前修订

## 结论摘要

可以申请，但不能把当前 GitHub Release 中由 nFPM 生成的 `.deb`/`.rpm` 直接提交给发行版。Debian、Ubuntu、Fedora 和 EPEL 都要求发行版构建基础设施从可审查的源代码包重建，并要求有人承担长期维护责任。

对 WuKongIM，最实际的路线是：

1. 先补齐上游发布合规基础：恢复根目录完整许可证文件，完成 Go 模块和内嵌前端的许可证/源码清单，确保无网络环境可复现构建，并准备稳定版源码归档。
2. Fedora 路线：以 vendored Go 依赖制作可复现 SRPM，通过 Fedora Package Review，再由 Koji 构建、Bodhi 发布。
3. EPEL 路线：通常在 Fedora 收录后再请求 EPEL 分支；确认不与 RHEL 基础包冲突、目标 EPEL 的依赖可满足，并承诺企业发行版周期内的兼容更新。
4. Debian 路线：提交 Debian 原生 source package，经 ITP、赞助者评审、NEW 队列进入 unstable，再迁移 testing。
5. Ubuntu 路线：优先让 Debian 包自动同步进 Ubuntu `universe`；这已经能让用户在新安装的 Ubuntu 上直接执行 `sudo apt install wukongim`。没有必要以进入 `main` 为第一目标。

这不是一次性“申请上架”。四条路径都要求可联系的个人或团队持续处理漏洞、Bug、构建失败、依赖迁移和发行版分支更新。一个人可以启动 Debian/Fedora 的打包和赞助流程，但必须愿意承担长期响应；Ubuntu `main` 还要求由合格的 Ubuntu 团队明确拥有。

## “一键安装”到底需要达到哪一层

只要包进入某个发行版及版本默认启用的官方仓库，用户刷新仓库索引后就可以直接安装：

```shell
sudo apt update
sudo apt install -y wukongim
```

或：

```shell
sudo dnf install -y wukongim
```

Ubuntu 的 `universe` 是 Ubuntu 官方 Archive 的组成部分，默认安装通常已启用；进入 `universe` 足以实现上述体验。`main` 的区别主要是 Canonical 承担的支持、安全维护范围和更严格的 Main Inclusion Review，而不是 `apt install` 语法是否可用。参见 [Ubuntu Archive 组件说明](https://documentation.ubuntu.com/project/how-ubuntu-is-made/concepts/package-archive/) 和 [Main Inclusion Review](https://documentation.ubuntu.com/project/MIR/main-inclusion-review/)。

## 当前项目准备度

| 项目 | 当前判断 | 影响 |
| --- | --- | --- |
| 上游许可证 | **阻塞**：README 声明 Apache-2.0，但当前修订没有被 Git 跟踪的根目录 `LICENSE` | Debian NEW、Fedora Review 和依赖许可证核验都会首先要求可验证的授权文本与版权清单 |
| 从源码构建 | **阻塞**：`.goreleaser.packages.yaml` 的 nFPM 包装的是预构建二进制 | 不能作为 Debian source package 或 Fedora SRPM 直接送审；必须由 buildd/Koji 从源代码重建 |
| Go 依赖 | **待治理**：服务构建图约有 92 个外部 Go 模块 | Debian 需要解决发行版依赖或获准的源码携带策略；Fedora 允许并要求新 Go 包 vendor，但要逐项核验许可证 |
| 内嵌 Web 资产 | **阻塞风险高**：服务嵌入约 72 个已构建的 JS/CSS/字体/图片资源，约 3.2 MiB | 必须提供对应 preferred source、可复现构建路径和完整许可证；不能只提交不透明的 minified bundle |
| systemd 集成 | **较好基础**：已有非 root 用户、sysusers/tmpfiles、配置目录和多项 hardening | 仍需分别按 Debian/Fedora helper、脚本和配置文件规则改写发行版包 |
| 架构 | **不完整**：当前原生包只发布 amd64 | Fedora 若排除其他 primary arches 必须给出技术理由；建议至少验证 arm64/aarch64 |
| 发行版测试 | **不完整**：已有自有包安装测试，但没有发行版 source build、autopkgtest/piuparts/mock/fedora-review 证据 | 送审前需为各目标发行版建立原生构建和升级/卸载测试 |
| 长期维护者 | **尚未形成发行版承诺** | Debian 需要 maintainer 与 sponsor；Fedora/EPEL 需要 `packagers` 成员与 sponsor/reviewer；上架后需持续维护 |

上述数量是对当前仓库构建图和资源目录的审计快照，不是发行版政策中的固定阈值。

## Debian

### 准入条件

- Debian `main` 中的软件必须符合 Debian Free Software Guidelines，允许自由再分发、提供源码并允许修改；包本身及其构建/运行依赖必须满足对应 Archive 组件规则。[Debian Policy：Archive areas](https://www.debian.org/doc/debian-policy/ch-archive)
- 二进制包必须随包安装版权和许可证信息，且 `debian/copyright` 要准确覆盖上游代码和随附第三方内容。[Debian Policy：Copyright information](https://www.debian.org/doc/debian-policy/ch-docs.html#copyright-information)
- 必须提交 Debian source package。构建依赖要在 `Build-Depends` 中声明，构建应当非交互且可以由 Debian 构建系统重建；不能在构建时联网抓取依赖。[Debian Policy：Source packages](https://www.debian.org/doc/debian-policy/ch-source.html)
- Debian Policy 强烈反对不必要的 embedded code copies：已有 Debian 库通常应使用系统版本；需要携带副本时必须可追踪并承担安全更新责任。[Debian Policy：Embedded code copies](https://www.debian.org/doc/debian-policy/ch-source.html#embedded-code-copies)
- 包必须遵守 Debian 的配置文件和服务启动语义；不能用通用安装脚本绕过 dpkg/systemd helper。[Debian Policy：Configuration files](https://www.debian.org/doc/debian-policy/ch-files.html#configuration-files)、[Debian Policy：Starting system services](https://www.debian.org/doc/debian-policy/ch-opersys.html#starting-system-services)

### 进入流程

1. 搜索现有包和 WNPP，提交 wishlist 级 ITP，说明包、许可证和上游下载位置。[Developer's Reference：Adding a new package](https://www.debian.org/doc/manuals/developers-reference/pkgs.html#new-packages)
2. 准备并在当前 unstable 环境测试 Debian source package。Debian 的 Go Team 文档推荐使用 `dh-golang`/`dh-go` 并让可复用 Go 库成为显式构建依赖；module-aware 构建仍需按团队现状协调。[Debian Go Packaging](https://go-team.pages.debian.net/packaging.html)、[Debian Go Team：Module-aware builds](https://wiki.debian.org/Teams/DebianGoTeam/ModuleAwareBuilds)
3. 没有上传权限时，把 source package 放到 mentors，并提交 RFS 寻找 Debian Developer/有权限的 sponsor。Sponsor 会审查、构建、测试并代表维护者上传。[Debian Mentors：For maintainers](https://mentors.debian.net/intro-maintainers/)、[RFS HOWTO](https://mentors.debian.net/sponsors/rfs-howto/)
4. 首次上传进入 NEW 队列，由 ftp-master 检查版权、许可证、DFSG、包拆分和 Archive 组件。[Debian NEW queue](https://ftp-master.debian.org/new.html)
5. 包先进入 unstable；满足无新 RC Bug、架构同步和依赖可安装等条件后才迁移 testing。[Developer's Reference：Testing migration](https://www.debian.org/doc/manuals/developers-reference/pkgs.html#the-testing-distribution)

### 测试、更新与维护责任

- 送审前至少要用当前 unstable 完成安装、运行、升级、删除、重新安装和从源码重建；`lintian` 是基本检查，`piuparts`、autopkgtest 等用于覆盖安装生命周期和系统集成。[Developer's Reference：Testing the package](https://www.debian.org/doc/manuals/developers-reference/pkgs.html#testing-the-package)
- `Maintainer` 必须是真实、可联系并愿意负责该包的人或团队；职责包括处理 Bug、新上游版本、依赖迁移和安全问题。[Debian Policy：Maintainer field](https://www.debian.org/doc/debian-policy/ch-binary.html#the-maintainer-of-a-package)
- 新包通常服务未来的 Debian stable，不会直接进入当前 stable。当前 stable 用户若要官方渠道获取，需要包先进入 testing，再由维护者维护 stable-backports；backport 维护者要承诺覆盖对应 stable 生命周期。[Developer's Reference：Stable backports](https://www.debian.org/doc/manuals/developers-reference/pkgs.html#the-stable-backports-archive)

### WuKongIM 的 Debian 特有难点

最大的工程风险是 Go 依赖闭包。当前服务构建图约有 92 个外部模块；Debian 通常期望使用发行版提供的库或把缺失库分别打包，不能依赖 `go mod download`。需要尽早与 Debian Go Team 确认哪些模块已经存在、哪些需要新包、哪些可以在充分披露后随源码携带。

内嵌的编译后前端资源同样需要对应源码、构建工具链和许可证。仅有 minified JS、字体或图片而无法证明 preferred source 和授权，可能在 sponsor 或 NEW 审查中被拒绝。

Beta 版本并非规则上绝对禁止，但不利于 sponsor 对稳定性和长期维护的判断。若必须提交预发行版本，Debian 版本排序通常应写成类似 `3.0.0~beta.6-1`；更推荐等上游 `v3.0.0` 稳定版后申请。

## Ubuntu

### 推荐路线：Debian 自动同步到 `universe`

Ubuntu 官方文档明确建议新包优先进入 Debian。Debian unstable 中的包在 Ubuntu 开发周期的 Debian Import Freeze 前通常会自动同步；冻结后需要显式 sync/freeze 处理。[Ubuntu：New packages](https://documentation.ubuntu.com/project/how-ubuntu-is-made/processes/new-packages/)、[Ubuntu：Merges and syncs](https://documentation.ubuntu.com/project/how-ubuntu-is-made/processes/merges-and-syncs/)

因此，对 WuKongIM 而言，“Debian `main` → Ubuntu `universe` 自动同步”是维护成本最低、社区接受度最高的路线。`universe` 已能提供官方 `apt install wukongim` 体验。

### 直接进入 Ubuntu 的备选路线

若无法先进入 Debian，则需：

1. 在 Launchpad 创建 `needs-packaging` Bug，提供上游、许可证和包描述。
2. 提交标准 Debian 格式的 source package，并证明可在 PPA/干净环境构建。
3. 通过 `ubuntu-sponsors` 寻找有上传权限的 sponsor。[Ubuntu：Find a sponsor](https://documentation.ubuntu.com/project/contributors/uploading/find-a-sponsor/)
4. 首次进入 Archive 时接受 Archive Admin 的 NEW 审查，包括再分发权、版权准确性、DFSG、lintian、名称、内容和架构。[Ubuntu Archive Admin：NEW review](https://documentation.ubuntu.com/project/maintainers/AA/aa-new-review/)
5. 遵守 Ubuntu 开发周期和 Feature Freeze；错过冻结窗口会推迟到下一系列或需要 freeze exception。[Ubuntu：New packages](https://documentation.ubuntu.com/project/how-ubuntu-is-made/processes/new-packages/)

### 为什么暂不建议申请 `main`

进入 `main` 要在 `universe` 包的基础上发起 Main Inclusion Review。它要求真实需求、合格 Ubuntu 团队负责、活跃上游、成熟的 Bug/测试/安全流程，以及全部运行时和 embedded/build-time 依赖都处于 `main` 可接受范围。MIR 模板特别要求审查最终制品中链接或构建进去的所有代码；这对静态链接 Go 模块和内嵌前端依赖尤其严格。[Ubuntu MIR](https://documentation.ubuntu.com/project/MIR/main-inclusion-review/)、[MIR reporter template](https://documentation.ubuntu.com/project/MIR/mir-reporters-template/)

WuKongIM 当前并不需要 `main` 才能让用户一键安装，而且由单人上游直接满足 Ubuntu owning-team 和完整 dependency promotion 的成本很高，故应先以 `universe` 为目标。

### 已发布 Ubuntu 版本

包进入开发系列不会自动出现在旧 LTS。旧 LTS 需要单独走官方 backports，且必须在目标稳定版环境构建和测试。[Ubuntu Backports](https://documentation.ubuntu.com/project/how-ubuntu-is-made/processes/backports/)

## Fedora

### 准入条件与流程

Fedora 新包不是由厂商上传二进制后自动托管，而是由 Fedora 个人贡献者持续维护：

1. 创建 Fedora Account，按维护者加入流程找到 sponsor 并加入 `packagers` 组。[Joining the Package Maintainers](https://docs.fedoraproject.org/en-US/package-maintainers/Joining_the_Package_Maintainers/)
2. 编写 spec 和 SRPM，在 `mock`/scratch build 中验证，并运行 `rpmlint`。
3. 提交正式 Package Review；reviewer 按 MUST 清单检查许可证、来源、构建、依赖、文件所有权、架构、脚本和系统集成。任一 MUST 未满足都是 blocker。[New Package Process](https://docs.fedoraproject.org/en-US/package-maintainers/New_Package_Process_for_New_Contributors/)、[Package Review Guidelines](https://docs.fedoraproject.org/en-US/packaging-guidelines/ReviewGuidelines/)
4. 评审通过后请求 dist-git 仓库和发行版分支，导入 spec/source，在 Koji 构建，再通过 Bodhi 把更新送入测试和稳定仓库。[Fedora Packaging Guidelines](https://docs.fedoraproject.org/en-US/packaging-guidelines/)

主要硬门槛包括：

- License 必须属于 Fedora Allowed Licenses，spec 使用正确的 SPDX 表达式，并覆盖主项目、Go vendor、前端及其他随附内容。[Fedora Allowed Licenses](https://docs.fedoraproject.org/en-US/legal/allowed-licenses/)、[Fedora Licensing Guidelines](https://docs.fedoraproject.org/en-US/packaging-guidelines/LicensingGuidelines/)
- Koji/mock 构建不能访问网络，所有构建输入必须预先成为可验证 Source 或 BuildRequires。[Build-time network access](https://docs.fedoraproject.org/en-US/packaging-guidelines/#_build_time_network_access)
- 不能把上游预编译程序或库当成官方 RPM 内容；程序必须从 source package 所含源码重建。[No inclusion of pre-built binaries or libraries](https://docs.fedoraproject.org/en-US/packaging-guidelines/what-can-be-packaged/#_no_inclusion_of_pre_built_binaries_or_libraries)
- 至少应在 primary architecture 上构建。排除其他架构需要 `ExcludeArch` 和可追踪的技术原因，而不是因为上游暂时没有发布相应二进制。[Package Review Guidelines](https://docs.fedoraproject.org/en-US/packaging-guidelines/ReviewGuidelines/)
- systemd 服务、系统用户、目录、权限、配置和 scriptlets 必须使用 Fedora 规定的宏和语义。[Fedora systemd scriptlets](https://docs.fedoraproject.org/en-US/packaging-guidelines/Scriptlets/#_systemd)、[Users and Groups](https://docs.fedoraproject.org/en-US/packaging-guidelines/UsersAndGroups/)

### Go vendoring 的现行规则

Fedora 当前 Go 指南要求新的 Go 包使用 vendored module dependencies。推荐通过 `go2rpm --profile vendor` 和 `go_vendor_archive` 生成可复现 vendor archive；所有 vendor 模块都要产生 `bundled(golang(...))` provides。主项目和每个 vendored 模块都必须包含可识别的许可证文件，`License` 字段要表达累计许可证，且必须运行 `go_vendor_license report` 并处理错误。[Fedora Golang Packaging Guidelines](https://docs.fedoraproject.org/en-US/packaging-guidelines/Golang/)、[Golang packages vendored by default](https://fedoraproject.org/wiki/Changes/GolangPackagesVendoredByDefault)、[go-vendor-tools](https://fedora.gitlab.io/sigs/go/go-vendor-tools/)

这使 Fedora 路线不必像 Debian 一样先把约 92 个 Go 模块分别做成 RPM，但也把这些模块的许可证核验和漏洞跟踪责任集中到 WuKongIM 包维护者身上。任何缺少明确许可证文件的 vendor 模块都可能阻塞评审。

### 上架后的责任

维护者要持续响应 Bug、安全问题和构建失败，维护受支持 Fedora 分支，并通过 Bodhi 按稳定发行版更新政策发布。失联包会进入 nonresponsive/orphan/retire 流程；收录不是永久无条件托管。[Package Maintainer Responsibilities](https://docs.fedoraproject.org/en-US/fesco/Package_maintainer_responsibilities/)、[Fedora Updates Policy](https://docs.fedoraproject.org/en-US/fesco/Updates_Policy/)、[Nonresponsive Maintainers Policy](https://docs.fedoraproject.org/en-US/fesco/Policy_for_nonresponsive_package_maintainers/)

## EPEL

### 前置条件与申请

EPEL 通常要求包先进入 Fedora；EPEL-only package 是需要充分说明的少数例外。维护者必须具备 Fedora `packagers` 权限。[EPEL Package Request](https://docs.fedoraproject.org/en-US/epel/epel-package-request/)

- 若自己拥有 Fedora 包，按标准流程用 `fedpkg request-branch` 请求对应 EPEL 分支。
- 请求以该 source package 的正式跟踪项为准；不同 EPEL major 需要分别请求，EPEL 10 还需明确 minor 目标。
- 若 Fedora 包属于其他维护者，应先请求其参与。当前流程是等待一周后提醒，再等两周仍无响应，才可走 stalled EPEL request/releng 权限流程。
- 分支获批后仍需在目标 EPEL buildroot 中构建、测试并持续维护。

### EPEL 特有限制

- EPEL 是 RHEL Target Base 的补充，包不能替换、覆盖或扰动 RHEL 基础包；所有构建和运行依赖必须来自 Target Base 或 EPEL，不能依赖未默认启用的 module stream。[EPEL Policy](https://docs.fedoraproject.org/en-US/epel/epel-policy/)、[EPEL Packaging](https://docs.fedoraproject.org/en-US/epel/epel-packaging/)
- 如果同名包后来进入 RHEL，EPEL 包通常需要退休或按政策过渡。
- EPEL 面向多年稳定使用，不鼓励破坏 ABI、配置或用户体验的不兼容大升级。安全修复应尽量最小化/回补；无法避免的重大升级必须走不兼容升级流程并提前公告。[EPEL Updates Policy](https://docs.fedoraproject.org/en-US/epel/epel-policy-updates/)、[EPEL Incompatible Upgrades Policy](https://docs.fedoraproject.org/en-US/epel/epel-policy-incompatible-upgrades/)
- EPEL 10 按 minor 维护分支/仓库，旧 minor 随新的 RHEL minor 推进而结束生命周期，维护计划不能只写笼统的“支持 EPEL 10”。[EPEL Branches](https://docs.fedoraproject.org/en-US/epel/branches/)

EPEL 官方文档目前有一处需要在实际送审时再次确认：专门的 Updates Policy 写 testing 至少一周或达到 `+3 karma`，总 policy 的部分说明仍出现两周。执行时应以 Bodhi 当前行为和 EPEL SIG 的确认结果为准，不应把任何一处静态天数写进自动发布承诺。

对 WuKongIM，还必须先验证每个目标 EPEL buildroot 是否提供所需 Go 工具链、RPM 宏和非 Go 构建工具；不能假设当前上游的 Go 1.25 toolchain 在所有 EPEL/RHEL 基线都存在。缺失依赖若不能合规引入 EPEL，将直接阻塞该目标分支。

## 典型阻塞点（按当前项目风险排序）

1. **许可证与 preferred source 不完整。** 根许可证缺失、vendor 模块许可证不全，或内嵌前端只有编译产物，都会直接触发法律/源码审查。
2. **把现有 nFPM 二进制包误当 source package。** 官方仓库不会以 GitHub Release 二进制作为可信构建结果，必须重新设计 Debian source package 和 Fedora SRPM。
3. **无网络构建无法闭合。** Debian buildd、Fedora Koji/mock 都不能在构建时运行在线模块下载；依赖和前端工具链必须由 Archive/BuildRequires 或受政策允许且可核验的 source/vendor 输入提供。
4. **缺少长期发行版维护者。** Sponsor/reviewer 只帮助准入，不接管上游的 Bug、安全、依赖和分支维护。
5. **架构和目标版本覆盖不足。** 只产 amd64 不一定绝对禁止，但会增加 Fedora 审查阻力，也限制 Debian/Ubuntu 用户覆盖。
6. **更新策略与上游节奏冲突。** Fedora stable、EPEL 和 Debian stable/backports 都不适合无约束地自动追随每个上游版本；需要安全修复回补、兼容性判断和逐分支测试。
7. **网络服务的安全表面积。** 对外监听、systemd 权限、默认配置、数据目录迁移、密钥/凭据处理和安全响应记录都会受到 sponsor/reviewer 的重点检查；Ubuntu `main` 的安全审查尤其严格。[Ubuntu Security Updates](https://documentation.ubuntu.com/security/security-updates/)

## 建议的执行清单

### 阶段 0：先让上游具备送审条件

- 恢复并发布根 `LICENSE`，建立主项目、所有 Go 模块、前端 npm 依赖、字体/图片的版权与 SPDX 清单。
- 保存对应前端构建产物的完整 preferred source，并实现从干净源码、无网络环境重建二进制和 Web UI。
- 发布稳定的 `v3.0.0` source tarball，提供不可变下载地址、校验值和签名；不要以 prerelease 作为首个官方仓库目标，除非有明确需要。
- 验证 amd64 与 arm64/aarch64；记录其他 primary architectures 的真实失败原因。
- 固化 systemd 用户、目录、配置、升级、卸载和数据保留语义；为发行版安全联系人和漏洞处理时限建立公开流程。

### 阶段 1：Fedora

- 用当前 Fedora Go vendoring 流程生成 spec、vendor archive、累计 SPDX 与 vendor license report。
- 在 Rawhide 和目标稳定 Fedora 的 mock 中无网络构建，运行 `rpmlint`、安装/升级/卸载和服务测试。
- 先用 COPR 打磨 spec 是可行的工程手段，但 COPR 不等于 Fedora 官方仓库准入。
- 找 sponsor，提交 Package Review；通过后请求 dist-git 分支，在 Koji/Bodhi 完成首次正式发布。

### 阶段 2：EPEL

- Fedora 收录后，确认包名/文件/依赖不与 RHEL Target Base 冲突。
- 分别验证目标 EPEL 9、EPEL 10 minor 的工具链和依赖，按需要请求分支。
- 为企业用户制定兼容更新、CVE 回补和不兼容升级沟通方案。

### 阶段 3：Debian

- 先与 Debian Go Team 沟通 92 个外部模块和前端依赖的处理方案，再提交 ITP。
- 新建真正的 `debian/` source packaging；在 unstable sbuild 环境无网络构建，完成 lintian、piuparts/autopkgtest 和服务生命周期测试。
- 在 mentors 发布并提交 RFS，响应 sponsor 评审；通过 NEW 后维护 unstable/testing 迁移。

### 阶段 4：Ubuntu

- 优先在 Debian Import Freeze 前让 Debian 包自动同步到 Ubuntu `universe`。
- 对已经发布的 Ubuntu LTS，按需求分别申请 backport。
- 只有出现明确 Canonical/Ubuntu owning team、系统级依赖需求或默认安装场景时，再评估 MIR 进入 `main`。

## 最终判断

WuKongIM 在产品形态上适合进入这些官方仓库：它是可作为 systemd 服务运行的 Go 网络服务，现有打包已提供用户、目录和服务安全化的良好基础。但当前还不具备直接送审条件。第一优先级不是填写申请表，而是完成许可证恢复、第三方代码审计、内嵌前端 preferred source、无网络源码重建和长期 maintainer 承诺。

若目标是最快实现无需 `curl` 的官方安装体验，推荐并行推进 Fedora 与 Debian，但将 Debian → Ubuntu `universe` 作为 Ubuntu 主路线、Fedora → EPEL 作为 RHEL 生态主路线；暂不投入 Ubuntu `main`。在这些基础完成前，`packages.githubim.com` 自建 APT/RPM 仓库仍应继续作为可用安装渠道，但应明确它是项目官方仓库，不是 Debian/Ubuntu/Fedora/EPEL 发行版官方仓库。
