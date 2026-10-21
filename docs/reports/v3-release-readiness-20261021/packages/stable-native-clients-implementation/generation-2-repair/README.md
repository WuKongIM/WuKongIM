# Stable native clients v2 修复

v1 原始实现、两个 P2 与首轮审查全部保留。本代只改 driver/tests，父任务另将 workflow guard 从47改49。完整冻结位于 ../final-freeze-v2，差量3文件；34个受保护文件保持基线字节。

- 创建前登记UUID/name；按本UUID label发现不确定创建，核对ID/name/label后删除，最后必须查询零库存。阶段一停止reader产生的错误不可被最终成功查询洗白。actual exercise替身探针保留原TERM失败、删除本run资源并保留外部sentinel。
- DNF缓存挂载使用installroot前缀；前后文件摘要、inode和atime证明同代重复轮实际读取原metadata。前测hash后将atime归epoch；后测先stat再hash避免观测自己制造读取信号。无有效文件/无重复读取/noatime均拒绝。
- 缓存观测与清除仅由本run登记的root helper完成：同固定image、network none、只挂cache读写/control只读、DAC_OVERRIDE/FOWNER，无host sudo、CHOWN或Docker socket。先停止reader，再清cache，再最终查零；不依赖host访问root缓存。fixture exporter改为宿主非rootUID:GID，避免公共多级目录无法删除。
- 保留wrongSHA真实安装负例，增加A3.0.0真实安装与expected3.0.1不符、真安装后只在throwaway容器移除wkcli两负例；原acceptance程序不替换。

01/03/05为先红记录，02/04/06/07为随后绿；最终16+34+12=62项轻量相关测试通过。08是实际exercise路径的命令替身复验，无Docker/GPG/编译。49个native案例仍待首次Linux CI，未宣称root缓存或真实安装已实际通过。受控TERM在finally清理中忽略再次TERM；SIGKILL/宿主丢失无法保证finally。

DNF依据：[官方命令说明](https://dnf.readthedocs.io/en/latest/command_ref.html#options)、[DNF4.14.0 CLI](https://raw.githubusercontent.com/rpm-software-management/dnf/4.14.0/dnf/cli/cli.py) 的_read_conf_file/prepend_installroot。
