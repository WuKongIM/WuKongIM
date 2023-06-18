##  悟空IM（让信息传递更简单）

高性能通用通讯服务，支持即时通讯，消息推送，物联网通讯，音视频信令，直播弹幕，客服系统，AI通讯，即时社区等场景。

（注意：此项目是一个通用的底层即时通讯服务，上层需要对接自己的具体业务系统（通过webhook和datasource机制非常轻松与自己业务系统对接），此项目核心点主要维护大量客户端的长连接，并根据第三方业务系统配置的投递消息规则进行消息投递。）

`本项目需要在go1.20.0或以上环境编译`

[English](./README.md)

<p align="center">
<img align="left" height="110" src="./docs/logo.png">
<ul>
<!-- <li><strong>QQ群</strong>: <a href="#">750224611</a></li> -->
<!-- <li><strong>微信</strong>: <a href="#">wukongimgo（备注进群）</a></li> -->
<li><strong>官网</strong>: https://githubim.com</li>
<li><strong>通讯协议</strong>: <a href="https://githubim.com/guide/proto">WuKongIM协议</a></li>
<li><strong>提问</strong>: https://github.com/WuKongIM/WuKongIM/issues</li>
<li><strong>文档</strong>: http://www.githubim.com</li>
</ul>
</p>

[![](https://img.shields.io/github/license/WuKongIM/WuKongIM?color=yellow&style=flat-square)](./LICENSE)
[![](https://img.shields.io/badge/go-%3E%3D1.20-30dff3?style=flat-square&logo=go)](https://github.com/WuKongIM/WuKongIM)
[![](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/report/github.com/WuKongIM/WuKongIM)


<!-- 愿景
--------

深知开发一个即时通讯系统的复杂性，我们希望通过开源的方式，让更多的开发者可以快速的搭建自己的即时通讯系统，让信息传递更简单。 -->

特点
--------

- 📚 完全自研：自研消息数据库，消息分区永久存储，自研二进制协议(支持自定义)，重写Go底层网络库，无缝支持TCP和websocket。
- 🚀 性能强劲：单机支持百万用户同时在线，单机16w/秒消息（包括DB操作）吞吐量,一个频道支持万人同时订阅。
- 🔔 零依赖：没有依赖任何第三方组件，部署简单，一条命令即可启动
- 🔐 安全：消息通道和消息内容全程加密，防中间人攻击和窜改消息内容。
- 🧱 扩展性强：采用频道设计理念，目前支持群组频道，点对点频道，后续可以根据自己业务自定义频道可实现机器人频道，客服频道等等。


功能特性
---------------


- [x] 支持自定义消息
- [x] 支持订阅/发布者模式
- [x] 支持个人/群聊/客服/社区资讯频道
- [x] 支持频道黑明单
- [x] 支持频道白名单
- [x] 支持消息永久漫游，换设备登录，消息不丢失
- [x] 支持在线状态，支持同账号多设备同时在线
- [x] 支持多设备消息实时同步
- [x] 支持用户最近会话列表服务端维护
- [x] 支持指令消息
- [x] 支持离线指令接口
- [x] 支持Webhook，轻松对接自己的业务系统
- [x] 支持Datasoure，无缝对接自己的业务系统数据源
- [x] 支持Websocket连接
- [x] 支持TLS 1.3
- [x] 支持Prometheus监控
- [x] 监控系统开发
- [ ] 支持Windows系统部署


快速部署
---------------

```

docker run -d -p 5001:5001 -p 5100:5100 -p 5200:5200 -p 5300:5300 -e WK_EXTERNAL_IP=xxx.xxx.xxx.xx  --name wukongim -v ~/wukongim:/root/wukongim  wukongim/wukongim:latest

```

`WK_EXTERNAL_IP：为服务器外网IP，用于客户端连接，如果仅测试，客户端和服务器都在一个局域网内，这里可以填部署服务器的局域网IP`

查看监控信息： http://127.0.0.1:5300/web


客户端演示地址：http://imdemo.githubim.com

其他部署方式详见文档：http://githubim.com/guide/quickstart


配套SDK源码和Demo
---------------


[iOS Demo 和 SDK 源码](https://github.com/WuKongIM/WuKongIMiOSSDK)

[Android Demo 和 SDK 源码](https://github.com/WuKongIM/WuKongIMAndroidSDK)

[Web Demo 和 SDK 源码](https://github.com/WuKongIM/WuKongIMJSSDK)

[Flutter Demo 和 SDK 源码(待完善)](https://github.com/WuKongIM/WuKongIMFlutterSDK)


适用场景
---------------

#### 即时通讯

* 群频道支持
* 个人频道支持
* 消息永久存储
* 离线消息推送支持
* 最近会话维护

#### 消息推送/站内消息

* 群频道支持
* 个人频道支持
* 离线消息推送支持

#### 物联网通讯

* mqtt协议支持（待开发）
* 支持发布与订阅

#### 音视频信令服务器

* 支持临时指令消息投递

#### 直播弹幕

* 临时消息投递

* 临时订阅者支持

#### 客服系统

* 客服频道支持

* 消息支持投递给第三方服务器

* 第三方服务器可决定分配指定的订阅者成组投递

#### 实时AI反馈

* 支持客户端发的消息推送给第三方服务器，第三方服务器反馈给AI后返回的结果再推送给客户端

#### 即时社区

* 社区频道支持
* 支持topic模式的消息投递

监控截图
---------------

![image](./docs/screen1.png)
![image](./docs/screen2.png)
![image](./docs/screen3.png)
![image](./docs/screen4.png)
![image](./docs/screen5.png)


License
---------------

WuKongIM is licensed under the [Apache License 2.0](./LICENSE).
