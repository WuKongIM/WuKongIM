##  悟空IM（让信息传递更简单）

8年积累，沉淀出来的高性能通用通讯服务，支持即时通讯，站内/系统消息，物联网通讯，音视频信令，直播弹幕，客服系统，AI通讯，即时社区等场景。

（注意：此项目是一个通用的底层即时通讯服务，上层需要对接自己的具体业务系统（通过webhook和datasource机制非常轻松与自己业务系统对接），此项目核心点主要维护大量客户端的长连接，并根据第三方业务系统配置的投递消息规则进行消息投递。）

`本项目需要在go1.20.0或以上环境编译`

web聊天场景演示： http://imdemo.githubim.com

后端监控演示： http://monitor.githubim.com/web

[English](./README.md)

<p align="center">
<img align="left" height="110" src="./docs/logo.png">
<ul>
<!-- <li><strong>QQ群</strong>: <a href="#">750224611</a></li> -->
<!-- <li><strong>微信</strong>: <a href="#">wukongimgo（备注进群）</a></li> -->
<li><strong>官网</strong>: https://githubim.com</li>
<li><strong>通讯协议</strong>: <a href="https://githubim.com/guide/proto.html">WuKongIM协议</a></li>
<li><strong>提问</strong>: https://github.com/WuKongIM/WuKongIM/issues</li>
<li><strong>文档</strong>: http://www.githubim.com</li>
</ul>
</p>

[![](https://img.shields.io/github/license/WuKongIM/WuKongIM?color=yellow&style=flat-square)](./LICENSE)
[![](https://img.shields.io/badge/go-%3E%3D1.20-30dff3?style=flat-square&logo=go)](https://github.com/WuKongIM/WuKongIM)
[![](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/report/github.com/WuKongIM/WuKongIM)

演示
--------

**实战项目**

唐僧叨叨(通讯层基于WuKongIM)：https://github.com/TangSengDaoDao/TangSengDaoDaoServer

**聊天Demo**

![image](./docs/demo.gif)

**客服Demo**

待补充

**ChatGPT Demo**

待补充

**直播弹幕 Demo**

待补充

**站内消息Demo**

待补充

**音视频信令Demo**

待补充


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
- [x] 支持Windows系统(仅开发用)
- [x] 支持流式消息，类似chatgpt的结果输出流
- [ ] 支持分布式

图解
---------------

![image](./docs/业务系统对接图.png)


![image](./docs/webhook.png)

快速部署
---------------

```shell

# 安装
curl -sSL https://gitee.com/WuKongDev/WuKongIMCli/raw/main/install.sh | sudo bash 

# 运行
wk run 

```

`WK_EXTERNAL_IP：为服务器外网IP，用于客户端连接，如果仅测试，客户端和服务器都在一个局域网内，这里可以填部署服务器的局域网IP`

查询系统信息: http://127.0.0.1:5001/varz

查看监控信息： http://127.0.0.1:5300/web

客户端演示地址：http://127.0.0.1:5172/chatdemo

其他部署方式详见文档：http://githubim.com/guide/quickstart

端口解释:

```
5001: api端口
5100: tcp长连接端口
5172: demo端口
5200: websocket长连接端口
5300: 监控系统端口
```


配套SDK源码和Demo
---------------


[iOS Demo 和 SDK 源码](https://github.com/WuKongIM/WuKongIMiOSSDK)

[Android Demo 和 SDK 源码](https://github.com/WuKongIM/WuKongIMAndroidSDK)

[Web Demo 和 SDK 源码](https://github.com/WuKongIM/WuKongIMJSSDK)

[Uniapp Demo 和 SDK 源码](https://github.com/WuKongIM/WuKongIMUniappSDK)

[React Native Demo 源码](https://github.com/wengqianshan/WuKongIMReactNative)

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

Star
---------------

我们团队一直致力于即时通讯的研发，需要您的鼓励，如果您觉得本项目对您有帮助，欢迎点个star，您的支持是我们最大的动力。

Wechat
---------------

如果有需要，加我拉你进群，微信号：wukongimgo

![image](./wechat.jpg)



License
---------------

WuKongIM is licensed under the [Apache License 2.0](./LICENSE).
