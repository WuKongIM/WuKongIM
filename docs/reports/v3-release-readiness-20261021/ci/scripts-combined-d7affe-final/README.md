# d7affe 第一轮 CI 终态补充

08:43Z 后读取的四workflow均终态。Candidate仅scripts失败（原六文件与唯一255失败见generation-1），完整default和迁移/消息E2E通过。三节点性能与correctness、SDK三端、native包构建及四发行版lifecycle的GitHub job均success。

本代只新增E2E原始小产物与所有终态job快照，不覆盖或重复generation-1失败。E2E包含五迁移拓扑通过、跨节点四case通过（2000QPS容量对比case按既有开关skip）、1/3节点自聊重启通过。源merge/tree核对、四命令exit0/cleanup完整、未截断。该产物排除binary，摘要由工作流提供并与自聊报告一致，不能称离线重算了二进制摘要。

其它三个workflow在本代只核GitHub状态/steps，未额外下载其全部产物。整体scripts门禁仍失败、255根因未定位，不能报全CI通过。本代封存后不再修改。
