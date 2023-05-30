## WuKongIM (Make information transfer easier)

WuKongIM is a high-performance universal communication service that supports various scenarios such as instant messaging, message push, IoT communication, audio and video signaling, live broadcasting with bullet comments, customer service systems, AI communication, and instant communities.

[中文文档](./README_CN.md)

<p align="center">
<img align="left" height="110" src="./docs/logo.png">
<ul>
<!-- <li><strong>QQ group</strong>: <a href="#">496193831</a></li> -->
<li><strong>Website</strong>: http://www.githubim.com</li>
<li><strong>Protocol</strong>: <a href="https://githubim.com/guide/proto">WuKongIM Protocol</a></li>
<li><strong>Issues</strong>: https://github.com/WuKongIM/WuKongIM/issues</li>
<li><strong>Docs</strong>: http://www.githubim.com</li>
</ul>
</p>

[![](https://img.shields.io/github/license/WuKongIM/WuKongIM?color=yellow&style=flat-square)](./LICENSE)
[![](https://img.shields.io/badge/go-%3E%3D1.17-30dff3?style=flat-square&logo=go)](https://github.com/WuKongIM/WuKongIM)
[![](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/report/github.com/WuKongIM/WuKongIM)

Features
--------

- 📚 Fully self-developed: WuKongIM uses a self-developed message database, binary protocol, and network library, and supports custom protocols.
- 🚀 High performance: WuKongIM can handle millions of online users and has a throughput of 160,000 messages per second (including database operations).
- 🔔 Zero dependencies: WuKongIM has no third-party dependencies and is easy to deploy.
- 🔐 Security: WuKongIM encrypts both message channels and message content to prevent man-in-the-middle attacks and message tampering.
- 🧱 Highly extensible: WuKongIM uses a channel-based design and currently supports group and point-to-point channels. It can be extended to support custom channels for use cases such as chatbots and customer service.


Quick Start
---------------

```
docker run -p 5000:5000 -p 5100:5100 -p 5200:5200 -p 5300:5300 -e WK_EXTERNAL_IP=xxx.xxx.xxx.xx --name wukongim -v ./wukongim:/root/wukongim wukongim/wukongim:latest
```

`WK_EXTERNAL_IP is the environment variable used to set the external IP address of the server for client connections. If this variable is not set, the internal IP address will be used by default.`

View server information: http://127.0.0.1:5000/varz

Demo: http://imdemo.githubim.com

For more deployment options, see the [documentation](http://githubim.com/guide/quickstart).


SDK source code and demos
---------------

iOS demo and SDK source code

Android demo and SDK source code

Web demo and SDK source code

Flutter demo and SDK source code (to be improved)

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

License
---------------

WuKongIM is licensed under the [Apache License 2.0](./LICENSE).