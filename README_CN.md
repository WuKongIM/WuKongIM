##  悟空IM（让信息传递更简单）

9年积累，沉淀出来的高性能通用通讯服务，支持即时通讯，站内/系统消息，消息中台，物联网通讯，音视频信令，直播弹幕，客服系统，AI通讯，即时社区等场景。

`本项目需要在go1.20.0或以上环境编译`

#### 2.0.0-beta版本发布正式发布！（beta版本不建议上生产，生产请使用1.2.x的版本）


[English](./README.md)

<p align="center">
<img align="left" height="110" src="./docs/logo.png">
<ul>
<!-- <li><strong>QQ群</strong>: <a href="#">750224611</a></li> -->
<!-- <li><strong>微信</strong>: <a href="#">wukongimgo（备注进群）</a></li> -->
<li><strong>官网</strong>: https://githubim.com</li>
<li><strong>通讯协议</strong>: <a href="https://githubim.com/guide/proto.html">WuKongIM协议</a></li>
<li><strong>提问</strong>: https://github.com/WuKongIM/WuKongIM/issues</li>
<li><strong>文档</strong>: https://githubim.com</li>
</ul>
</p>

[![](https://img.shields.io/github/license/WuKongIM/WuKongIM?color=yellow&style=flat-square)](./LICENSE)
[![](https://img.shields.io/badge/go-%3E%3D1.20-30dff3?style=flat-square&logo=go)](https://github.com/WuKongIM/WuKongIM)
[![](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/report/github.com/WuKongIM/WuKongIM)
<a href="https://join.slack.com/t/wukongim/shared_invite/zt-22o7we8on-2iKNUmgigB9ERdF9XUivmw"><img src="https://img.shields.io/badge/Slack-99%2B-blueviolet?logo=slack&amp;logoColor=white"></a>

架构图
--------

![架构图](./docs/architecture/cluster.png)


节点故障转移演示
--------

![节点故障转移演示](./docs/architecture/cluster-failover.webp)

演示
--------

**聊天Demo**

web聊天场景演示： http://imdemo.githubim.com

后端监控演示： http://monitor.githubim.com/web

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
- [x] 支持Datasource，无缝对接自己的业务系统数据源
- [x] 支持Websocket连接
- [x] 支持TLS 1.3
- [x] 支持Prometheus监控
- [x] 监控系统开发
- [x] 支持Windows系统(仅开发用)
- [x] 支持流式消息，类似chatgpt的结果输出流
- [x] 支持分布式
    - [x] 去中心化设计，任意一个节点宕机，集群自动修复
    - [x] 集群节点之间数据互备，任意一个节点损害，不影响数据完整性
    - [x] 支持集群快速自动扩容
    - [ ] 支持长连接CDN，解决跨国跨地区长连接不稳定问题



快速运行
---------------


### Docker部署（单机）

```shell

docker run -d -p 15001:5001 -p 15100:5100 -p 15172:5172 -p 15200:5200 -p 15210:5210 -p 15300:5300  --name wukongim -v ./wukongim:/root/wukongim  wukongim/wukongim:v2.0.0-beta-20240428

```

### Docker部署（分布式）
    
```yaml

git clone https://github.com/WuKongIM/WuKongIM.git

cd ./WuKongIM/docker/cluster

sudo docker compose up -d

```


### 访问

查询系统信息: http://127.0.0.1:15001/varz

查看监控信息： http://127.0.0.1:15300/web

客户端演示地址：http://127.0.0.1:15172/chatdemo (分布式地址为：http://127.0.0.1:15172/login)

端口解释:

```
15001: api端口
15100: tcp长连接端口
15172: demo端口
15200: websocket长连接端口
15300: 监控系统端口
```




配套SDK源码和Demo
---------------


| 项目名 | Github地址 | Gitee地址 | 开源协议 | 说明 |
| ---- | ---------- | --------- | ---- |  ---- |
|   WuKongIM   |   [Github](https://github.com/WuKongIM/WuKongIM)         |     [Gitee](https://gitee.com/WuKongDev/WuKongIM)  | Apache2.0   |    悟空IM通讯端，负责长连接维护，消息投递等等   |
|   WuKongIMAndroidSDK   |   [Github](https://github.com/WuKongIM/WuKongIMAndroidSDK)         |     [Gitee](https://gitee.com/WuKongDev/WuKongIMAndroidSDK) | Apache2.0       |    悟空IM的Android SDK  |
|   WuKongIMiOSSDK   |   [Github](https://github.com/WuKongIM/WuKongIMiOSSDK)         |     [Gitee](https://gitee.com/WuKongDev/WuKongIMiOSSDK)  | Apache2.0     |    悟空IM的iOS SDK  |
|   WuKongIMUniappSDK   |   [Github](https://github.com/WuKongIM/WuKongIMUniappSDK)         |     [Gitee](https://gitee.com/WuKongDev/WuKongIMUniappSDK)  | Apache2.0     |    悟空IM的 Uniapp SDK  |
|   WuKongIMJSSDK   |   [Github](https://github.com/WuKongIM/WuKongIMJSSDK)         |     [Gitee](https://gitee.com/WuKongDev/WuKongIMJSSDK)   | Apache2.0    |    悟空IM的 JS SDK  |
|   WuKongIMFlutterSDK   |    [Github](https://github.com/WuKongIM/WuKongIMFlutterSDK)        |    [Gitee](https://gitee.com/WuKongDev/WuKongIMFlutterSDK)   | Apache2.0  |    悟空IM的 Flutter SDK |
|   WuKongIMReactNativeDemo   |   [Github](https://github.com/wengqianshan/WuKongIMReactNative)         |     无  | Apache2.0    |    悟空IM的 React Native Demo(由贡献者 [wengqianshan](https://github.com/wengqianshan) 提供)  |


| 项目名 | Github地址 | Example | 文档 | 说明 |
| ---- | ---------- | --------- | ---- |  ---- |
|   WuKongIM   |   [Github](https://github.com/WuKongIM/WuKongIM)         |     无 |  [文档](https://githubim.com/sdk/android.html)  &nbsp;&nbsp;&nbsp;&nbsp;       |    悟空IM通讯端，负责长连接维护，消息投递等等 |
|   WuKongIMAndroidSDK   |   [Github](https://github.com/WuKongIM/WuKongIMAndroidSDK)         |     [Example](https://github.com/WuKongIM/WuKongIMAndroidSDK/tree/master/app) | [文档](https://githubim.com/sdk/android.html)    |    悟空IM的Android SDK  |
|   WuKongIMiOSSDK   |   [Github](https://github.com/WuKongIM/WuKongIMiOSSDK)         |     [Example](https://github.com/WuKongIM/WuKongIMiOSSDK/tree/main/Example)  | [文档](https://githubim.com/sdk/ios.html)     |    悟空IM的iOS SDK  |
|   WuKongIMUniappSDK   |   [Github](https://github.com/WuKongIM/WuKongIMUniappSDK)         |     [Example](https://github.com/WuKongIM/WuKongIMUniappSDK/tree/main/examples)  | [文档](https://githubim.com/sdk/uniapp.html)      |    悟空IM的 Uniapp SDK  |
|   WuKongIMJSSDK   |   [Github](https://github.com/WuKongIM/WuKongIMJSSDK)         |     [Example](https://github.com/WuKongIM/WuKongIMJSSDK/tree/main/examples)   | [文档](https://githubim.com/sdk/javascript.html)     |    悟空IM的 JS SDK  |
|   WuKongIMFlutterSDK   |    [Github](https://github.com/WuKongIM/WuKongIMFlutterSDK)        |    [Example](https://github.com/WuKongIM/WuKongIMFlutterSDK/tree/master/example)   |[文档](https://githubim.com/sdk/flutter.html)    |    悟空IM的 Flutter SDK |
|   WuKongIMReactNativeDemo   |   [Github](https://github.com/wengqianshan/WuKongIMReactNative)         |     无  |  无  |    悟空IM的 React Native Demo(由贡献者 [wengqianshan](https://github.com/wengqianshan) 提供)  |



图解
---------------

总体架构图

![总体架构图](./docs/architecture/architecture2.png)

业务系统对接

![image](./docs/业务系统对接图.png)


Webhook对接图

![image](./docs/webhook.png)


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

案例展示
---------------

**项目名**

TangSengDaoDao

**开源地址**

https://github.com/TangSengDaoDao/TangSengDaoDaoServer

**截图**

||||
|:---:|:---:|:--:|
|![](./docs/case/tsdaodao/screenshot/conversationlist.webp)|![](./docs/case/tsdaodao/screenshot/messages.webp)|![](./docs/case/tsdaodao/screenshot/robot.webp)|


|||          |
|:---:|:---:|:-------------------:|

![](./docs/case/tsdaodao/screenshot/pc11.png)




Wechat
---------------

如果有需要，加我拉你进群，微信号：wukongimgo

![image](./wechat.jpg)



License
---------------

WuKongIM is licensed under the [Apache License 2.0](./LICENSE).
