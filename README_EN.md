## WuKongIM (Everything so easy)

WuKongIM is a high-performance universal communication service that supports various scenarios such as instant messaging, message push, IoT communication, audio and video signaling, live broadcasting with bullet comments, customer service systems, AI communication, and instant communities.

[中文文档](./README_CN.md)

<p align="center">
<img align="left" width="160" src="./docs/logo.png">
<ul>
<!-- <li><strong>QQ group</strong>: <a href="#">496193831</a></li> -->
<li><strong>Website</strong>: http://www.githubim.com</li>
<li><strong>Source</strong>: https://github.com/WuKongIM/WuKongIM</li>
<li><strong>Protocol</strong>: <a href="./docs/protocol.md">WuKongIM Protocol</a></li>
<li><strong>Issues</strong>: https://github.com/WuKongIM/WuKongIM/issues</li>
<li><strong>Docs</strong>: http://www.githubim.com</li>
</ul>
</p>

[![Docs](https://img.shields.io/badge/docs-latest-green.svg)](http://githubim.com/docs)
[![](https://img.shields.io/apm/l/vim-mode)](./LICENSE)

Features
--------

- Simple deployment without any third-party dependencies, can be started with a single command.
- Self-developed message database for permanent message storage.
- Self-developed binary protocol with support for custom protocols.
- End-to-end encryption of message channels and content to prevent man-in-the-middle attacks and message tampering.
- Support for multiple device logins with real-time data synchronization.
- Easy to use with powerful performance. In single-machine testing on a Mac notebook, it achieves a throughput of over 160,000 messages per second (including storage). To achieve this performance and ease of use, the system implements message storage completely independently, without relying on any third-party components.
- Strong scalability using a channel-based design approach. Currently supports group channels and point-to-point channels, and can be extended to include custom channels for functionalities such as robot channels and customer service channels.
- Supports both TCP and WebSocket protocols.
- Channels support thousands of subscribers.


Getting Started
---------------

To get started with WuKongIM, follow these steps:

1. Clone the repository:

> git clone https://github.com/WuKongIM/WuKongIM.git

2. Run the service:

> cd WuKongIM

> go run main.go



## Client SDK

[Android SDK](https://github.com/WuKongIM/WuKongIMAndroidSDK.git)

[iOS SDK](https://github.com/WuKongIM/WuKongIMiOSSDK.git)

[JS SDK](https://github.com/WuKongIM/WuKongIMJSSDK.git)

[Flutter SDK](https://github.com/WuKongIM/WuKongIMFlutterSDK.git)

[Docs](http://www.githubim.com/docs)

## App Demo

[Android Demo](https://github.com/WuKongIM/WuKongIMAndroidDemo.git)

[iOS Demo](https://github.com/WuKongIM/WuKongIMiOSDemo.git)

[Web Demo](https://github.com/WuKongIM/WuKongIMJSDemo.git)


<!-- ## Quick start -->

<!-- <img  src="./docs/quick.gif"  alt="Quick start"/> -->


<!-- [Get WuKongIM executable file](./INSTALL.md) -->
<!-- 
#### Run the server (Note: Because it rewrites the Go network library, this library is temporarily not supported by Windows. Windows recommends using Docker to run.)

```
$ go run cmd/app/main.go -e mode=test 
```

After the server is running, visit http://127.0.0.1:1516/api to view the api document

#### Client SDK

Android SDK: [Android SDK (built-in simple demo)](https://github.com/WuKongIM/WuKongIMAndroidSDK.git)

iOS SDK: Open source from Star to 500 (please help us some Star, thanks 😄)

JS SDK: Star to 1000 open source (please help us some Star, thanks 😄)

note： Please check [document](http://www.githubim.com/docs) for the use of SDK

## Quick play

***Log in to test1, test2 and test2 to send a message "hello" to test1***

```
// Log in to test1
$ go run cmd/play/main.go -user=test1 
```

```
// Log in to test2
$ go run cmd/play/main.go -user=test2 
```

test2 sends the message hello to test1

```
$ >send hello to test1
```


### Performance Testing

One-click pressure test

```
./bench.sh
```

My test results are as follows:

Achieve a throughput of 63420 messages per second, which is close to the pressure test data of redis!

```
goos: darwin
goarch: amd64
cpu: Intel(R) Core(TM) i7-7700HQ CPU @ 2.80GHz

SEND: 2021/06/29 15:05:49 duration: 10.605478656s - 12.096mb/s - 63420.051ops/s - 15.768us/op
``` -->


<!-- 
***分布式***

节点初始化

```
// 开启proxy服务 指定初始化的节点nodes
# WuKongIM proxy -c ./configs/proxy.toml  -e replica=1
```


```
// 初始化的节点启动
# WuKongIM -c ./configs/config.toml -proxy=xx.xx.xx.xx:16666 -e nodeID=1001 -e nodeAddr=127.0.0.1:6666
(或者 WuKongIM -c ./configs/config.toml -peers=1@http://127.0.0.1:6000,2@http://127.0.0.1:6001,3@http://127.0.0.1:6002 -e nodeID=1)
```

```
// 初始化的节点启动
# WuKongIM  -e proxy=xx.xx.xx.xx:16666 -e nodeID=1002 -e nodeAddr=127.0.0.1:6667
```

增加节点

```
# WuKongIM  -proxy=xx.xx.xx.xx:16666 -e nodeID=1003 -join
```

移除节点

```
# WuKongIM -e nodeID=1003 -remove
``` -->



#### Run via Docker Compose

```
$ docker-compose up 
```

## Architecture

***完整架构***

<img src="./docs/architecture/architecture.png" alt="Architecture"/>

***认证逻辑***

<img src="./docs/architecture/auth.png" alt="Architecture"/>

***消息处理逻辑***

<img src="./docs/architecture/processmsg.png" alt="Architecture"/>
