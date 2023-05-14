## WuKongIM (Everything so easy)

This project is a simple and easy to use, powerful performance, simple design concept instant messaging service, fully custom protocol chat (Chat) system

[中文文档](./README_CN.md)

<p align="center">
<img align="left" width="160" src="./docs/logo.png">
<ul>
<!-- <li><strong>QQ group</strong>: <a href="#">496193831</a></li> -->
<li><strong>Website</strong>: http://www.githubim.com</li>
<li><strong>Source</strong>: https://github.com/WuKongIM/WuKongIM</li>
<li><strong>Protocol</strong>: <a href="./docs/protocol.md">WuKongIM Protocol</a></li>
<li><strong>Issues</strong>: https://github.com/WuKongIM/WuKongIM/issues</li>
<li><strong>Docs</strong>: http://www.githubim.com/docs</li>
</ul>
</p>

[![Docs](https://img.shields.io/badge/docs-latest-green.svg)](http://githubim.com/docs)
[![](https://img.shields.io/apm/l/vim-mode)](./LICENSE)

## Feature

* Go language development, high performance and easy maintenance have both
* The bottom layer is 100% open source
* Binary protocol (supports customization), the packet size is extremely small, the minimum heartbeat packet is only 1 byte, the power consumption is small, the flow is small, and the transmission speed is fast
* The message channel and message content are fully encrypted to prevent man-in-the-middle attacks and modify of message content.
* Simple and easy to use, strong performance, MAC notebook stand-alone test more than 6w/sec message throughput, in order to achieve this performance and easy to use, completely independent message storage, no need to rely on third-party components, a single command can start the service
* The tcp  + ack mechanism is used to ensure that the message is stable and reliable without loss
* Strong scalability Adopting the channel design concept, currently supports group channels, point-to-point channels, and you can customize channels according to your own business in the future. It can realize functions such as robot channels, customer service channels, etc
* Multi-terminal synchronization, real-time synchronization of web, pc, app messages
* TCP and Websocket are supported indiscriminately
* Ten thousand people support
* The message partition is permanently stored, and the message will not be lost when the device is uninstalled
* Support offline pull in read mode


## Client SDK

[Android SDK](https://github.com/WuKongIM/WuKongIMAndroidSDK.git)

[iOS SDK](https://github.com/WuKongIM/WuKongIMiOSSDK.git)

[JS SDK](https://github.com/WuKongIM/WuKongIMJSSDK.git)

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
