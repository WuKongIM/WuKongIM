# WuKongIM 进入 Linux 发行版官方仓库的条件

日期：2026-09-03

## 结论

可以申请，但现有 `packages.githubim.com` 中由 GoReleaser/nFPM 生成的二进制
DEB/RPM 不能直接提交给 Debian、Ubuntu、Fedora 或 EPEL。官方仓库通常要求由
发行版构建系统使用源码包重建，并由发行版内的个人维护者长期负责审查、漏洞、
构建失败和版本更新。

建议并行走两条上游路径：

1. DEB：Debian `main` -> Ubuntu `universe` 自动同步。
2. RPM：Fedora -> EPEL。

当前自建仓库应继续保留，用于快速发布最新版；官方仓库的版本通常更保守、进入
周期也更长。

## 共同准入条件

- 软件及随源码分发的依赖必须具有发行版认可的自由软件许可证，且版权与许可证
  信息完整、可审计。
- 必须提交发行版原生源码包，由官方构建服务在受控环境中完成构建；构建过程不能
  临时联网下载依赖，也不能把上游预编译二进制当作最终构建输入。
- 构建依赖和运行依赖必须在目标发行版中存在，或按目标发行版允许的 Go vendoring
  规则完整携带并审计。
- 包名、文件布局、systemd 服务、用户创建、配置文件、升级/卸载脚本、日志和数据
  目录必须符合对应发行版的 Packaging Policy。
- 包必须能够在目标架构上构建和运行；排除架构需要合理技术依据，Fedora 还要求
  为 `ExcludeArch` 提供对应 Bugzilla 记录。
- 需要真实个人维护者持续处理安全问题、Bug、构建失败和发行版生命周期内的更新。

## 各发行版流程

### Debian

1. 在 WNPP 中提交 ITP（Intent To Package），说明软件用途、上游地址和许可证。
2. 准备 Debian 原生源码包，包括 `debian/control`、`rules`、`changelog`、
   `copyright`、测试和必要补丁；包应面向当前 Debian unstable 构建。
3. 满足 Debian Policy 和 DFSG；构建只能依赖 `build-essential`、Essential 包及
   明确声明的 `Build-Depends`，`debian/rules` 的构建目标通常不得访问网络。
4. 非 Debian Developer 需要在 mentors.debian.net/Salsa 发布源码包，并找到
   Debian Developer Sponsor 审查和代为上传。
5. 首次上传进入 NEW queue，由 Archive Admin 审查包名、许可证、版权和归档组件。

Apache-2.0 本身可进入 Debian `main`，但所有随包分发的文件和第三方代码都要逐项
满足 DFSG 和版权记录要求。

### Ubuntu

最推荐的路径不是重复打包，而是先进入 Debian。Debian unstable 中没有 Ubuntu
差异的包，通常会在 Debian Import Freeze 前自动同步到 Ubuntu，服务器包一般进入
`universe`；这已经支持用户直接 `sudo apt install wukongim`，不必进入 Ubuntu
`main`。

若绕过 Debian 直接申请 Ubuntu 新包，需要 Launchpad `needs-packaging` 请求、完整
源码包、具有上传权限的 Sponsor，以及 Archive Admin 的 NEW 审查。冻结后还需额外
的 Feature Freeze/同步例外。新包进入开发中的 Ubuntu 版本，不会自动出现在已经
发布的 LTS；现有 LTS 需要另走 backports 等流程。稳定发行版直接新增源码包是少见
例外，并需要更严格审批。

### Fedora

1. 申请 Fedora Account，并完成首次贡献者流程；首个包需要获得 Sponsor，加入
   `packagers` 组后才能自行维护。
2. 编写 Fedora 原生 `wukongim.spec`，生成 SRPM，使用 `rpmlint`、Mock/Koji 等验证。
3. 在 Bugzilla 提交 Package Review；审查者会检查命名、许可证 SPDX、上游源码
   一致性、依赖、架构、文件所有权、脚本和实际运行情况。
4. 审查通过后建立 dist-git 分支，由 Koji 从源码构建，再通过 Bodhi 推送更新。

Fedora 43 起，Go 应用可以按 `go-vendor-tools` 路线携带 vendored 依赖，但 vendored
代码仍需完整的许可证清单和持续漏洞维护；这不是跳过源码审查。

### EPEL

EPEL 遵循 Fedora Packaging Guidelines，现实路径是先让软件进入 Fedora，再由
Fedora packager 申请目标 EPEL 分支。包不得替换或干扰 RHEL BaseOS/AppStream 中
的包，全部构建和运行依赖必须能从目标 RHEL 构建环境或 EPEL 获得。EPEL 更新政策
更强调兼容和稳定，维护者需要覆盖相应企业版生命周期；破坏 ABI、配置或用户行为
的重大升级受严格限制。

## WuKongIM 当前差距

### 必须先解决

1. **根目录缺少实际许可证文件。** README 声明 Apache-2.0，nFPM 元数据也写明
   Apache-2.0，但当前 Git 树中没有 `LICENSE`/`COPYING` 文件。正式提交前应加入
   Apache-2.0 全文，并完成所有第三方源代码、前端资产及生成物的版权/许可证清单。
2. **缺少发行版原生源码包。** 现有 `.goreleaser.packages.yaml` 只为 Linux amd64
   构建二进制，再用 nFPM 封装为 DEB/RPM。需要新增 Debian `debian/` 源码包装和
   Fedora `.spec`，让官方构建系统从源代码产生二进制包。
3. **需要可离线重建的依赖闭包。** 项目使用较新的 Go toolchain 和较大的 Go module
   依赖图，当前没有 `vendor/`。必须分别验证 Debian 与 Fedora 的依赖策略和目标
   buildroot 能否满足构建，且构建时不访问公网。
4. **内嵌 Manager Web 产物需要处理。** `internal/access/manager/webui/dist` 是提交到
   仓库并由 `go:embed` 嵌入的生成产物。发行版审查可能要求证明其可由对应前端源码
   重建，并审计 JavaScript 依赖和许可证；不能只把未知来源的压缩产物视为源码。
5. **架构覆盖不足。** 当前发布包只构建 amd64。应至少验证 arm64，并为 Fedora 的
   其他主要架构选择支持或提供可审核的排除理由。

### 强烈建议

- 以稳定的 `v3.0.0` 作为首次申报版本，比 beta 版本更利于 Debian、Fedora 和尤其
  EPEL 的长期维护承诺。
- 补齐 man page、示例配置、源代码构建测试、安装/升级/卸载测试和可重复构建材料。
- 由 `tangtaoit` 作为明确的上游联系人和发行版维护者；Debian 路线寻找 DD Sponsor，
  Fedora 路线寻找首次包 Sponsor。
- 保留 `packages.githubim.com` 作为 upstream 快速发布渠道；官方仓库作为发行版节奏
  较慢但无需添加第三方源的渠道。

## 推荐实施顺序

1. 在上游先补许可证和第三方版权清单，形成完全离线、可重复的源码构建。
2. 补 arm64 等目标架构测试，并完成 Debian/Fedora 原生打包草案。
3. 发布稳定版 `v3.0.0` 后，同时启动 Debian ITP 和 Fedora Package Review。
4. Debian 通过后跟踪 Ubuntu 自动同步；Fedora 通过后申请 EPEL 9/10 等目标分支。
5. 如需让已发布的 Debian stable/Ubuntu LTS 立即可装，再单独评估 backports；不要把
   它与进入下一开发版官方仓库混为一件事。

## 一手资料

- [Debian Developer's Reference: Adding new packages](https://www.debian.org/doc/manuals/developers-reference/pkgs.html)
- [Debian Developer's Reference: Sponsoring packages](https://www.debian.org/doc/manuals/developers-reference/beyond-pkging.html)
- [Debian Policy: Source packages](https://www.debian.org/doc/debian-policy/ch-source.html)
- [Debian Policy: The Debian archive](https://www.debian.org/doc/debian-policy/ch-archive)
- [Debian Go Packaging](https://go-team.pages.debian.net/packaging.html)
- [Ubuntu: Create a new package](https://ubuntu.com/project/docs/contributors/new-package/create-a-new-package/)
- [Ubuntu: Package sponsorship](https://ubuntu.com/project/docs/how-ubuntu-is-made/processes/sponsorship/)
- [Ubuntu: Request a package sync](https://documentation.ubuntu.com/project/contributors/uploading/request-a-sync/)
- [Ubuntu: Archive Admin NEW review](https://documentation.ubuntu.com/project/maintainers/AA/aa-new-review/)
- [Fedora: New package process for new contributors](https://docs.fedoraproject.org/en-US/package-maintainers/New_Package_Process_for_New_Contributors/)
- [Fedora: Package Review Guidelines](https://docs.fedoraproject.org/en-US/packaging-guidelines/ReviewGuidelines/)
- [Fedora: Allowed licenses](https://docs.fedoraproject.org/en-US/legal/allowed-licenses/)
- [Fedora: Go packages vendored by default](https://fedoraproject.org/wiki/Changes/GolangPackagesVendoredByDefault)
- [EPEL: Package request](https://docs.fedoraproject.org/en-US/epel/epel-package-request/)
- [EPEL: Policy](https://docs.fedoraproject.org/en-US/epel/epel-policy/)
- [EPEL: Updates policy](https://docs.fedoraproject.org/en-US/epel/epel-policy-updates/)
