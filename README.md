## WuKongIM (Make information transfer easier)

8 years of accumulation, precipitated a high-performance universal communication service,message center, supporting instant messaging, message push, IoT communication, audio and video signaling, live broadcast barrage, customer service system, AI communication, instant community and other scenarios.


(Note: This project is a general underlying instant messaging service. The upper layer needs to dock with its own specific business system (which can be easily docked with its own business system through the webhook and datasource mechanism). The core of this project mainly maintains a large number of long connections of clients and delivers messages according to the message rules of third-party business systems.)

`This project needs to be compiled in a go1.20.0 or higher version.`


[中文文档](./README_CN.md)

<p align="center">
<img align="left" height="110" src="./docs/logo.png">
<ul>
<!-- <li><strong>QQ group</strong>: <a href="#">496193831</a></li> -->
<li><strong>Website</strong>: https://githubim.com</li>
<li><strong>Protocol</strong>: <a href="https://githubim.com/guide/proto.html">WuKongIM Protocol</a></li>
<li><strong>Issues</strong>: https://github.com/WuKongIM/WuKongIM/issues</li>
<li><strong>Docs</strong>: https://githubim.com</li>
</ul>
</p>

[![](https://img.shields.io/github/license/WuKongIM/WuKongIM?color=yellow&style=flat-square)](./LICENSE)
[![](https://img.shields.io/badge/go-%3E%3D1.20-30dff3?style=flat-square&logo=go)](https://github.com/WuKongIM/WuKongIM)
[![](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/report/github.com/WuKongIM/WuKongIM)
<a href="https://join.slack.com/t/wukongim/shared_invite/zt-22o7we8on-2iKNUmgigB9ERdF9XUivmw"><img src="https://img.shields.io/badge/Slack-99%2B-blueviolet?logo=slack&amp;logoColor=white"></a>

Demo
--------

**Practical Project**

TangSengDaoDao (communication layer based on WuKongIM): https://github.com/TangSengDaoDao/TangSengDaoDaoServer

**Chat Demo**

![image](./docs/demo.gif)


Web chat scene demo: http://imdemo.githubim.com

Backend monitoring demo: http://monitor.githubim.com/web


Features
--------

- 📚 Fully self-developed: WuKongIM uses a self-developed message database, binary protocol, and network library, and supports custom protocols.
- 🚀 High performance: WuKongIM can handle millions of online users and has a throughput of 160,000 messages per second (including database operations).
- 🔔 Zero dependencies: WuKongIM has no third-party dependencies and is easy to deploy.
- 🔐 Security: WuKongIM encrypts both message channels and message content to prevent man-in-the-middle attacks and message tampering.
- 🧱 Highly extensible: WuKongIM uses a channel-based design and currently supports group and point-to-point channels. It can be extended to support custom channels for use cases such as chatbots and customer service.

TODO
---------------

- [x] Supports custom messages
- [x] Supports subscription/publisher mode
- [x] Supports personal/group chat/customer service/community news channels
- [x] Supports channel blacklists
- [x] Supports channel whitelists
- [x] Supports permanent message storage, device switching, and message retention
- [x] Supports online status and multiple devices logged in simultaneously with the same account
- [x] Supports real-time synchronization of messages across multiple devices
- [x] Supports server-side maintenance of user's recent conversation list
- [x] Supports command messages
- [x] Supports offline command interface
- [x] Supports Webhook, easy integration with your own business system
- [x] Supports Datasource, seamless integration with your own business system data source
- [x] Supports WebSocket connections
- [x] Supports TLS 1.3
- [x] Development of monitoring system
- [x] Support for Windows system (For development use only)
- [x] Supports streaming messages, similar to the output stream of chatgpt results.
- [ ] Supports distributed systems



Quick Start
---------------

```shell

git clone https://github.com/WuKongIM/WuKongIM.git

cd WuKongIM

go run main.go --config config/wk.yaml

```

View System information: http://127.0.0.1:5001/varz

View Monitor information: http://127.0.0.1:5300/web

Demo: http://127.0.0.1:5172/chatdemo

For more deployment options, see the [documentation](http://githubim.com/guide/quickstart).

Port explanation:

```
5001: API port
5100: TCP long connection port
5172: Demo port
5200: WebSocket long connection port
5300: Monitoring system port
```

SDK source code and demos
---------------

| Project Name | Github Address | Gitee Address | Documentation | Description |
| ---- | ---------- | --------- | ---- |  ---- |
|   WuKongIM   |   [Github](https://github.com/WuKongIM/WuKongIM)         |     [Gitee](https://gitee.com/WuKongDev/WuKongIM) | &nbsp;&nbsp;&nbsp;&nbsp; [Documentation](https://githubim.com/sdk/android.html)  &nbsp;&nbsp;&nbsp;&nbsp;       |    WuKongIM communication end, responsible for long connection maintenance, message delivery, etc. |
|   WuKongIMAndroidSDK   |   [Github](https://github.com/WuKongIM/WuKongIMAndroidSDK)         |     [Gitee](https://gitee.com/WuKongDev/WuKongIMAndroidSDK) | [Documentation](https://githubim.com/sdk/android.html)    |    WuKongIM's Android SDK  |
|   WuKongIMiOSSDK   |   [Github](https://github.com/WuKongIM/WuKongIMiOSSDK)         |     [Gitee](https://gitee.com/WuKongDev/WuKongIMiOSSDK)  | [Documentation](https://githubim.com/sdk/ios.html)     |    WuKongIM's iOS SDK  |
|   WuKongIMUniappSDK   |   [Github](https://github.com/WuKongIM/WuKongIMUniappSDK)         |     [Gitee](https://gitee.com/WuKongDev/WuKongIMUniappSDK)  | [Documentation](https://githubim.com/sdk/uniapp.html)      |    WuKongIM's Uniapp SDK  |
|   WuKongIMJSSDK   |   [Github](https://github.com/WuKongIM/WuKongIMJSSDK)         |     [Gitee](https://gitee.com/WuKongDev/WuKongIMJSSDK)   | [Documentation](https://githubim.com/sdk/javascript.html)     |    WuKongIM's JS SDK  |
|   WuKongIMFlutterSDK   |    [Github](https://github.com/WuKongIM/WuKongIMFlutterSDK)        |    [Gitee](https://gitee.com/WuKongDev/WuKongIMFlutterSDK)   |[Documentation](https://githubim.com/sdk/flutter.html)    |    WuKongIM's Flutter SDK |
|   WuKongIMReactNativeDemo   |   [Github](https://github.com/wengqianshan/WuKongIMReactNative)         |     None  |  None  |    WuKongIM's React Native Demo (provided by contributor [wengqianshan](https://github.com/wengqianshan))  |

Illustration
---------------

Architecture

![image](./docs/architecture/architecture2.png)



Business System Integration

![image](./docs/业务系统对接图.png)

Webhook

![image](./docs/webhook.png)


Applicable Scenarios
---------------

#### Instant Messaging

* Supports group channels
* Supports personal channels
* Supports permanent message storage
* Supports offline message push
* Supports recent conversation maintenance

#### Message Push/Site Message

* Supports group channels
* Supports personal channels
* Supports offline message push

#### IoT Communication

* Supports MQTT protocol (to be developed)
* Supports publish and subscribe

#### Audio and Video Signaling Server

* Supports temporary command message delivery

#### Live Broadcast Bullet Screens

* Supports temporary message delivery
* Supports temporary subscriber support

#### Customer Service System

* Supports customer service channels
* Messages can be delivered to third-party servers
* Third-party servers can decide to allocate designated subscribers to deliver messages in groups

#### Real-time AI Feedback

* Supports pushing messages sent by clients to third-party servers, and the results returned by AI after being fed back by third-party servers are pushed back to clients

#### Instant Community

* Supports community channels
* Supports message delivery in topic mode


Monitor
---------------

![image](./docs/screen1.png)
![image](./docs/screen2.png)
![image](./docs/screen3.png)
![image](./docs/screen4.png)
![image](./docs/screen5.png)

Star
------------

Our team has been committed to the research and development of instant messaging. We need your encouragement. If you find this project helpful, please give it a star. Your support is our greatest motivation.

Wechat
---------------

If necessary, add me and I will invite you to the group. My WeChat ID is wukongimgo.

![image](./wechat.jpg)


License
---------------

WuKongIM is licensed under the [Apache License 2.0](./LICENSE).