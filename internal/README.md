
## 简述

程序入口包

`server`: 服务启动入口

api包

`api`: 提供http接口

核心包

`user`： 生产或消费消息的载体

`channel`： 处理消息，比如，权限，存储，转发等

`pusher`： 推送消息（分发消息），负责将消息推送给设备

`eventbus`： 事件总线，将上述所有包的事件统一成接口。

基础包

`options`： 全局配置，管理整个应用的配置文件

`service`： 公用服务，对分布式，数据库，tag等等的统一封装

`ingress`: 集群内部的服务提供和调用，类似节点内的rpc包

`webhook`: webhook服务

`track`: 消息轨迹追踪

pkg包

pkg包说明请看 [pkg包说明](pkg/README.md)

## 源码阅读建议

入口

看源码最先看的应该是程序入口，这里是`server`包，可以了解到整个程序的启动流程

api

`api`包，了解整个程序提供的http接口

长连接

`pkg/wknet`包，负责tcp/websocket的连接

存储

`pkg/wkdb`包，负责消息，最近会话，频道等等的存储

分布式

`pkg/cluster`包，负责多节点的集群工作（这个包下有许多子包，还是很复杂的，后续再细说）

