##  WuKongIM（ 一切很简单）

本项目是一款简单易用，性能强劲，设计理念简洁的即时通讯服务，完全自定义协议的聊天(Chat)系统
<p align="center">
<img align="left" width="160" src="./docs/logo.png">
<ul>
<li><strong>QQ群</strong>: <a href="#">750224611</a></li>
<li><strong>官网</strong>: http://www.githubim.com</li>
<li><strong>源码</strong>: https://github.com/WuKongIM/WuKongIM</li>
<li><strong>通讯协议</strong>: <a href="./docs/protocol.md">WuKongIM协议</a></li>
<li><strong>提问</strong>: https://github.com/WuKongIM/WuKongIM/issues</li>
<li><strong>文档</strong>: http://www.githubim.com/docs</li>
</ul>
</p>

[![Docs](https://img.shields.io/badge/docs-latest-green.svg)](http://githubim.com/docs)
[![](https://img.shields.io/apm/l/vim-mode)](./LICENSE)

## 特点

* go语言开发，高性能与易维护兼得。
* 底层100%开源。
* 二进制协议(支持自定义)，包大小极小，最小心跳包只有1byte，耗电小，流量小，传输速度快。
* 消息通道和消息内容全程加密，防中间人攻击和串改消息内容。
* 简单易用，性能强劲，MAC笔记本单机测试6w多/秒的消息吞吐量，为了达到这性能和简单易用，完全自主实现消息存储，无如何第三方组件依赖，一条命令即可启动服务
* 采用tcp协议+ack机制保证消息稳定可靠不丢。
* 扩展性强 采用频道设计理念，目前支持群组频道，点对点频道，后续可以根据自己业务自定义频道可实现机器人频道，客服频道等等功能。
* 多端同步，web，pc，app消息实时同步。
* 同时无差别支持tcp，websocket。
* 万人群支持。
* 消息分区永久存储，卸载设备消息不丢。
* 支持读模式的离线拉取

<!-- ## 快速入门 -->

<!-- <img  src="./docs/quick.gif"  alt="快速入门"/> -->


<!-- [获取limaoim执行文件](./INSTALL.md) -->

<!-- #### 运行服务端 （注意：因为重写了Go的网络库，此库Windows暂时不支持，Windows建议使用Docker运行。） -->
<!-- 
```
$ go run cmd/app/main.go -e mode=test (mode=test是测试模式 方便快速试玩 生产不要加此参数)
``` -->

<!-- 服务器运行后，访问 http://127.0.0.1:1516/api 查看api文档 -->

## 客户端SDK

[Android SDK](https://github.com/WuKongIM/WuKongIMAndroidSDK.git)

[iOS SDK](https://github.com/WuKongIM/WuKongIMiOSSDK.git)

[JS SDK](https://github.com/WuKongIM/WuKongIMJSSDK.git)

SDK的使用请查看[文档](http://www.githubim.com/docs)

## 客户端Demo

[Android Demo](https://github.com/WuKongIM/WuKongIMAndroidDemo.git)

[iOS Demo](https://github.com/WuKongIM/WuKongIMiOSDemo.git)

[Web Demo](https://github.com/WuKongIM/WuKongIMJSDemo.git)

<!-- 
## 快速试玩

***登录test1,test2 test2向test1发送一条消息“hello”***

```
// 登录test1
$ go run cmd/play/main.go -user=test1 
```

```
// 登录test2
$ go run cmd/play/main.go -user=test2 
```

test2发送消息hello给test1

```
$ > send hello to test1
```


### 性能测试

一键压测

```
./bench.sh
```

本人测试结果如下：

达到每秒63420条消息的吞吐量，接近redis的压测数据！

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
# limaoim proxy -c ./configs/proxy.toml  -e replica=1
```


```
// 初始化的节点启动
# limaoim -c ./configs/config.toml -proxy=xx.xx.xx.xx:16666 -e nodeID=1001 -e nodeAddr=127.0.0.1:6666
(或者 limaoim -c ./configs/config.toml -peers=1@http://127.0.0.1:6000,2@http://127.0.0.1:6001,3@http://127.0.0.1:6002 -e nodeID=1)
```

```
// 初始化的节点启动
# limaoim  -e proxy=xx.xx.xx.xx:16666 -e nodeID=1002 -e nodeAddr=127.0.0.1:6667
```

增加节点

```
# limaoim  -proxy=xx.xx.xx.xx:16666 -e nodeID=1003 -join
```

移除节点

```
# limaoim -e nodeID=1003 -remove
``` -->



## 通过Docker Compose运行

```
$ docker-compose up 
```
