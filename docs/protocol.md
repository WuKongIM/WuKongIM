
## WuKongIM协议 


## 控制报文结构
| 参数名           |  类型  | 说明       |
| :-----          | :---  | :---       |
| Fixed header    | 2 byte  | 固定报头 |
| Variable header |  bytes  | 可变报头 |
| Payload         | bytes | 消息体 |


## 固定报头

每个 WuKongIM 控制报文都包含一个固定报头

<table>
  <tr>
    <th>Bit</th>
    <th>7</th>
    <th>6</th>
    <th>5</th>
    <th>4</th>
    <th>3</th>
    <th>2</th>
    <th>1</th>
    <th>0</th>
  </tr>
  <tr>
    <td>byte 1</td>
    <td colspan="4">WuKongIM控制报文的类型</td>
    <td colspan="4">用于指定控制报文类型的标志位</td>
  </tr>
  <tr>
   <td>byte 2...</td>
   <td colspan="8">剩余长度</td>
  </tr>
</table>

#### WuKongIM控制报文的类型

<table>
  <tr>
    <th>名字</th>
    <th>值</th>
    <th>描述</th>
  </tr>
  <tr>
    <td>Reserved</td>
    <td >0</td>
    <td>保留位</td>
  </tr>
  <tr>
    <td>CONNECT</td>
    <td >1</td>
    <td>客户端请求连接到服务器(c2s)</td>
  </tr>
  <tr>
    <td>CONNACK</td>
    <td >2</td>
    <td>服务端收到连接请求后确认的报文(s2c)</td>
  </tr>
  <tr>
    <td>SEND</td>
    <td >3</td>
    <td>发送消息(c2s)</td>
  </tr>
  <tr>
    <td>SENDACK</td>
    <td >4</td>
    <td>收到消息确认的报文(s2c)</td>
  </tr>
  <tr>
    <td>RECVEIVED</td>
    <td >5</td>
    <td>收取消息(s2c)</td>
  </tr>
  <tr>
    <td>REVACK</td>
    <td >6</td>
    <td>收取消息确认(c2s)</td>
  </tr>
  <tr>
    <td>PING</td>
    <td >7</td>
    <td>ping请求</td>
  </tr>
  <tr>
    <td>PONG</td>
    <td >8</td>
    <td>对ping请求的相应</td>
  </tr>
  <tr>
    <td>DISCONNECT</td>
    <td >9</td>
    <td>请求断开连接</td>
  </tr>
 
</table>

#### 用于指定控制报文类型的标志位

<table>
  <tr>
    <th>bit</th>
    <th>3</th>
    <th>2</th>
    <th>1</th>
    <th>0</th>
  </tr>
  <tr>
    <td>byte</td>
    <td>DUP</td>
    <td>SyncOnce</td>
    <td>RedDot</td>
    <td>NoPersist</td>
  </tr>
  
</table>

备注:
```
 DUP: 是否是重复的消息(客户端重发消息的时候需要将DUP标记为1)
 SyncOnce: 只同步一次 在多端设备的情况下 如果有一个设备拉取过此消息，其他设备将不会再拉取到此消息（比如加好友消息）
 RedDot: 客户端收到消息是否显示红点
 NoPersist: 是否不存储此消息
```



#### 剩余长度

在当前消息中剩余的byte(字节)数，包含可变头部和负荷(内容)。

单个字节最大值：01111111，16进制：0x7F，10进制为127。

WuKongIM协议规定，第八位（最高位）若为1，则表示还有后续字节存在。

WuKongIM协议最多允许4个字节表示剩余长度。最大长度为：0xFF,0xFF,0xFF,0x7F，二进制表示为:11111111,11111111,11111111,01111111，十进制：268435455 byte=261120KB=256MB=0.25GB 四个字节之间值的范围：



Digits  |	From |	To
---|---|---
1 |	0 (0x00) |	127 (0x7F)
2 |	128 (0x80, 0x01) |	16 383 (0xFF, 0x7F)
3 |	16 384 (0x80, 0x80, 0x01) |	2 097 151 (0xFF, 0xFF, 0x7F)
4 |	2 097 152 (0x80, 0x80, 0x80, 0x01)	| 268 435 455 (0xFF, 0xFF, 0xFF, 0x7F)
其实换个方式理解：第1字节的基数是1，而第2字节的基数：128，以此类推，第三字节的基数是：128*128=2的14次方，第四字节是：128*128*128=2的21次方；

例如，需要表达321=2*128+65.（2字节）：10100001 0000 0011.

（和我们理解的低位运算放置顺序不一样，第一个字节是低位，后续字节是高位，但字节内部本身是低位右边，高位左边）。

#### 字符串UTF-8编码
有关字符串，WuKongIM采用的是修改版的UTF-8编码，一般形式为如下：

<table>
  <tr>
    <th>bit</th>
    <th>7</th>
    <th>6</th>
    <th>5</th>
    <th>4</th>
    <th>3</th>
    <th>2</th>
    <th>1</th>
    <th>0</th>
  </tr>
  <tr>
    <td>byte 1</td>
    <td colspan="8">String Length MSB</td>
  </tr>
  <tr>
    <td>byte 2</td>
    <td colspan="8">String Length MSB</td>
  </tr>
  <tr>
    <td>bytes 3...</td>
    <td colspan="8">Encoded Character Data</td>
  </tr>
</table>

 

## 可变报头

某些控制报文包含一个可变报头部分。它在固定报头和有效载荷之间。可变报头的内容根据报文类型的不同而不同。可变报头的报文标识符(Packet Identifier)字段存在于在多个类型的报文里。

---
## CONNECT 连接报文

<table>
  <tr>
    <th>参数名</th>
    <th>类型</th>
    <th>说明</th>
  </tr>
  <tr>
    <td>Frame Type</td>
    <td >0.5 byte</td>
    <td>报文类型(1)</td>
  </tr>
  <tr>
    <td>Flag</td>
    <td >0.5 byte</td>
    <td> 标示位</td>
  </tr>
  <tr>
    <td>Remaining Length</td>
    <td >... byte</td>
    <td>报文剩余长度</td>
  </tr>
  <tr>
    <td>Protocol Version</td>
    <td>int8</td>
    <td>协议版本号</td>
  </tr>
   <tr>
    <td>UID</td>
    <td>string</td>
    <td>用户ID</td>
  </tr>
  <tr>
    <td>Token</td>
    <td>string</td>
    <td>用户的token</td>
  </tr>
   <tr>
    <td>Client Key</td>
    <td>string</td>
    <td>客户端KEY (客户端KEY (base64编码的DH公钥))</td>
  </tr>
   <tr>
    <td>Device Flag</td>
    <td>int8</td>
    <td>设备标示(同标示同账号互踢)</td>
  </tr>
  <tr>
    <td>Device ID</td>
    <td>string</td>
    <td>设备唯一ID</td>
  </tr>
   <tr>
    <td>Client Timestamp</td>
    <td>int64</td>
    <td>客户端当前时间戳(13位时间戳,到毫秒)</td>
  </tr>
  
</table>

## CONNACK 连接确认
CONNACK 报文由服务端所发送，作为对来自客户端的 CONNECT 报文的响应，如果客户端在合理的时间内没有收到服务端的 CONNACK 报文，客户端应该关闭网络连接。合理的时间取决于应用的类型和通信基础设施。

<table>
  <tr>
    <th>参数名</th>
    <th>类型</th>
    <th>说明</th>
  </tr>
  <tr>
    <td>Frame Type</td>
    <td >0.5 byte</td>
    <td>报文类型(2)</td>
  </tr>
  <tr>
    <td>Flag</td>
    <td >0.5 byte</td>
    <td> 标示位</td>
  </tr>
  <tr>
    <td>Remaining Length</td>
    <td >... byte</td>
    <td>报文剩余长度</td>
  </tr>
   <tr>
    <td>Server Key</td>
    <td>string</td>
    <td>服务端base64的DH公钥</td>
  </tr>
   <tr>
    <td>Salt</td>
    <td>string</td>
    <td>安全码
    </td>
  </tr>
  <tr>
    <td>Time Diff</td>
    <td>int64</td>
    <td>客户端时间与服务器的差值，单位毫秒。
    </td>
  </tr>
   <tr>
    <td>Reason Code</td>
    <td>uint8</td>
    <td>连接原因码（见附件）</td>
  </tr>
  
</table>

## SEND 发送消息

<table>
  <tr>
    <th>参数名</th>
    <th>类型</th>
    <th>说明</th>
  </tr>
  <tr>
    <td>Frame Type</td>
    <td >0.5 byte</td>
    <td>报文类型(3)</td>
  </tr>
  <tr>
    <td>Flag</td>
    <td >0.5 byte</td>
    <td> 标示位</td>
  </tr>
  <tr>
    <td>Remaining Length</td>
    <td >... byte</td>
    <td>报文剩余长度</td>
  </tr>
  <tr>
    <td>Setting</td>
    <td>1 byte</td>
    <td>消息设置（见下 版本4有效）</td>
  </tr>
  <tr>
    <td>Msg Key</td>
    <td>string</td>
    <td>用于验证此消息是否合法（仿中间人篡改）</td>
  </tr>
  <tr>
    <td>Client Seq</td>
    <td>uint32</td>
    <td>客户端消息序列号(由客户端生成，每个客户端唯一)</td>
  </tr>
  <tr>
    <td>Client Msg No</td>
    <td>string</td>
    <td>客户端唯一标示，用于客户端消息去重（version==2）</td>
  </tr>
   <tr>
    <td>Channel Id</td>
    <td>string</td>
    <td>频道ID（如果是个人频道ChannelId为个人的UID）</td>
  </tr>
   <tr>
    <td>Channel Type</td>
    <td>int8</td>
    <td>频道类型（1.个人 2.群组）</td>
  </tr>
  <tr>
    <td>Payload</td>
    <td>... byte</td>
    <td>消息内容</td>
  </tr>
  
</table>

## SENDACK 发送消息确认

<table>
  <tr>
    <th>参数名</th>
    <th>类型</th>
    <th>说明</th>
  </tr>
  <tr>
    <td>Frame Type</td>
    <td >0.5 byte</td>
    <td>报文类型(4)</td>
  </tr>
  <tr>
    <td>Flag</td>
    <td >0.5 byte</td>
    <td> 标示位</td>
  </tr>
  <tr>
    <td>Remaining Length</td>
    <td >... byte</td>
    <td>报文剩余长度</td>
  </tr>
     <tr>
    <td>Client Seq</td>
    <td>uint32</td>
    <td>客户端消息序列号</td>
  </tr>
  <tr>
    <td>Message ID</td>
    <td>uint64</td>
    <td>服务端的消息ID(全局唯一)</td>
  </tr>
  <tr>
    <td>Message Seq</td>
    <td>uint32</td>
    <td>消息序号（有序递增，用户唯一）</td>
  </tr>
  <tr>
    <td>Reason Code</td>
    <td>uint8</td>
    <td>发送原因代码 1表示成功</td>
  </tr>
  
</table>

## RECV 收消息

<table>
  <tr>
    <th>参数名</th>
    <th>类型</th>
    <th>说明</th>
  </tr>
  <tr>
    <td>Frame Type</td>
    <td >0.5 byte</td>
    <td>报文类型(5)</td>
  </tr>
  <tr>
    <td>Flag</td>
    <td >0.5 byte</td>
    <td> 标示位</td>
  </tr>
  <tr>
    <td>Remaining Length</td>
    <td >... byte</td>
    <td>报文剩余长度</td>
  </tr>
   <tr>
    <td>Setting</td>
    <td>1 byte</td>
    <td>消息设置（见下 版本4有效）</td>
  </tr>
   <tr>
    <td>Msg Key</td>
    <td>string</td>
    <td>用于验证此消息是否合法（仿中间人篡改）</td>
  </tr>
   <tr>
    <td>Message ID</td>
    <td>uint64</td>
    <td>服务端的消息ID(全局唯一)</td>
  </tr>
  <tr>
    <td>Message Seq</td>
    <td>uint32</td>
    <td>服务端的消息序列号(有序递增，用户唯一)</td>
  </tr>
 <tr>
    <td>Client Msg No</td>
    <td>string</td>
    <td>客户端唯一标示，用于客户端消息去重（version==2）</td>
  </tr>
  <tr>
    <td>Message Timestamp</td>
    <td>int32</td>
    <td>服务器消息时间戳(10位，到秒)</td>
  </tr>
  <tr>
    <td>Channel ID</td>
    <td>string</td>
    <td>频道ID</td>
  </tr>
  <tr>
    <td>Channel Type</td>
    <td>int8</td>
    <td>频道类型</td>
  </tr>
  <tr>
    <td>From UID</td>
    <td>string</td>
    <td>发送者UID</td>
  </tr>
  <tr>
    <td>Payload</td>
    <td>... byte</td>
    <td>消息内容</td>
  </tr>
  
</table>

## RECVACK 收消息确认

<table>
  <tr>
    <th>参数名</th>
    <th>类型</th>
    <th>说明</th>
  </tr>
  <tr>
    <td>Frame Type</td>
    <td >0.5 byte</td>
    <td>报文类型(6)</td>
  </tr>
  <tr>
    <td>Flag</td>
    <td >0.5 byte</td>
    <td> 标示位</td>
  </tr>
  <tr>
    <td>Remaining Length</td>
    <td >... byte</td>
    <td>报文剩余长度</td>
  </tr>
   <tr>
    <td>Message ID</td>
    <td>uint64</td>
    <td>服务端的消息ID(全局唯一)</td>
  </tr>
  <tr>
    <td>Message Seq</td>
    <td>uint32</td>
    <td>序列号</td>
  </tr>
  
</table>

## PING 

<table>
  <tr>
    <th>参数名</th>
    <th>类型</th>
    <th>说明</th>
  </tr>
  <tr>
    <td>Frame Type</td>
    <td >0.5 byte</td>
    <td>报文类型(7)</td>
  </tr>
  <tr>
    <td>Flag</td>
    <td >0.5 byte</td>
    <td> 标示位</td>
  </tr>
</table>

## PONG 

<table>
  <tr>
    <th>参数名</th>
    <th>类型</th>
    <th>说明</th>
  </tr>
  <tr>
    <td>Frame Type</td>
    <td >0.5 byte</td>
    <td>报文类型(8)</td>
  </tr>
  <tr>
    <td>Flag</td>
    <td >0.5 byte</td>
    <td> 标示位</td>
  </tr>
</table>

## DISCONNECT 

<table>
  <tr>
    <th>参数名</th>
    <th>类型</th>
    <th>说明</th>
  </tr>
  <tr>
    <td>Frame Type</td>
    <td >0.5 byte</td>
    <td>报文类型(9)</td>
  </tr>
  <tr>
    <td>Flag</td>
    <td >0.5 byte</td>
    <td> 标示位</td>
  </tr>
  <tr>
    <td>Remaining Length</td>
    <td >... byte</td>
    <td>报文剩余长度</td>
  </tr>
  <tr>
    <td>ReasonCode</td>
    <td >uint8</td>
    <td>原因代码</td>
  </tr>
  <tr>
    <td>Reason</td>
    <td >string</td>
    <td>原因</td>
  </tr>
</table>


## 消息设置


<table>
  <tr>
    <th>bit</th>
    <th>7</th>
    <th>6</th>
    <th>5</th>
    <th>4</th>
    <th>3</th>
    <th>2</th>
    <th>1</th>
    <th>0</th>
  </tr>
  <tr>
    <td>byte</td>
    <td>Receipt</td>
    <td>Reserved</td>
    <td>Reserved</td>
    <td>Reserved</td>
    <td>Reserved</td>
    <td>Reserved</td>
    <td>Reserved</td>
    <td>Reserved</td>
  </tr>
  
</table>

消息设置目前大小为1byte 8个bit

Receipt： 消息已读回执，此标记表示，此消息需要已读回执

Reserved：保留位，暂未用到


## Payload 推荐结构

* 文本

```
{
    "type": 1,
    "content": "这是一条文本消息" 
}
```

* 文本(带@)
```
{
    "type": 1,
    "content": "这是一条文本消息",
    "mention":{
        "all": 0, // 是否@所有人  0. @用户 1. @所有
        "uids":["1223","2323"] // 如果all=1 此字段为空
    }
}
```


* 文本(带回复)

```
{
    "type": 1,
    "content": "回复了某某" ,
    "reply": {
        "root_mid": "xxx",  // 根消息的message_id
        "message_id": "xxxx", // 被回复的消息ID
        "message_seq": xxx, // 被回复的消息seq
        "from_uid": "xxxx", // 被回复消息的发送者
        "from_name": "xxx", // 被回复消息的发送者名称
        "payload": {}     //   被回复消息的payload
    }
}
```

* 图片

```
 {
    "type": 2,
     "url": "http://xxxxx.com/xxx", // 图片下载地址
     "width": 200, // 图片宽度
     "height": 320 // 图片高度
 }
```

* GIF

```
 {
    "type": 3,
     "url": "http://xxxxx.com/xxx", // gif下载地址
     "width": 72, // gif宽度
     "height": 72  // gif高度
 }
```

* 语音

```
 {
    "type": 4,
     "url": "http://xxxxx.com/xxx", // 语音下载地址
     "timeTrad": 10 // 语音秒长
}
```

* 文件

```
 {
    "type": 8,
     "url": "http://xxxxx.com/xxx", // 文件下载地址
     "name":"xxxx.docx", // 文件名称
     "size": 238734 // 大小 单位byte
}
```

* 命令消息

```
 {
    "type": 99,
     "cmd": "groupUpdate", // 命令指令标示
     "param": {} // 命令对应的数据
}
```



## 系统消息
系统消息的type必须大于1000

* 创建群聊  (NoPersist:0,RedDot:0,SyncOnce:1)

张三邀请李四、王五加入群聊
```
{
    "type": 1001,
    "creator": "xxx",  // 创建者uid
    "creator_name": "张三", // 创建者名称
    "content": "{0}邀请{1}、{2}加入群聊",
    "extra": [{"uid":"xxx","name":"张三"},{"uid":"xx01","name":"李四"},{"uid":"xx02","name":"王五"}]
}
```

* 添加群成员  (NoPersist:0,RedDot:0,SyncOnce:1)

张三邀请李四、王五加入群聊
```
{
    "type": 1002,
    "content": "{0}邀请{1}、{2}加入群聊",
    "extra": [{"uid":"xxx","name":"张三"},{"uid":"xx01","name":"李四"},{"uid":"xx02","name":"王五"}]
}

```
* 移除群成员  (NoPersist:0,RedDot:0,SyncOnce:1)

张三将李四移除群聊

```
{
    "type": 1003,
    "content": "{0}将{1}移除群聊",
    "extra": [{"uid":"xxx","name":"张三"},{"uid":"xx01","name":"李四"}]
}
```

* 群成员被踢  (NoPersist:0,RedDot:1,SyncOnce:0)
```
{
    "type": 1010,
    "content": "你被{0}移除群聊",
    "extra": [{"uid":"xxx","name":"张三"}]
}
```
张三将李四移除群聊

```
{
    "type": 1003,
    "content": "{0}将{1}移除群聊",
    "extra": [{"uid":"xxx","name":"张三"},{"uid":"xx01","name":"李四"}]
}
```

* 更新群名称 (NoPersist:0,RedDot:0,SyncOnce:1)

张三修改群名称为"测试群"

```
{
    "type": 1005,
    "content": "{0}修改群名为\"测试群\"",
    "extra": [{"uid":"xxx","name":"张三"}]
}
```

* 更新群公告 (NoPersist:0,RedDot:0,SyncOnce:1)

张三修改群公告为"这是一个群公告"

```
{
    "type": 1005,
    "content": "{0}修改群公告为\"这是一个群公告\"",
    "extra": [{"uid":"xxx","name":"张三"}]
}
```

* 撤回消息 (NoPersist:0,RedDot:0,SyncOnce:1)

张三撤回了一条消息

```
{
    "type": 1006,
    "message_id": "234343435",  // 需要撤回的消息ID
    "content": "{0}撤回了一条消息",
    "extra": [{"uid":"xxx","name":"张三"}]
}
```



## 命令类消息

* 命令类消息 (SyncOnce:1) 


```

{
    "type": 99,
    "cmd": "cmd",  // 命令标示
    "param": {} // 命令参数
}

```

* 群成员信息有更新（收到此消息客户端应该增量同步群成员信息）

```
{
    "type": 99,
    "cmd": "memberUpdate",
    "param": {
        "group_no": "xxxx"
    }
}
```

* 红点消除（收到此命令客户端应将对应的会话信息的红点消除）

```
{
    "type": 99,
    "cmd": "unreadClear",
    "param": {
        "channel_id": "xxxx",
        "channel_type": 2
    }
}
```