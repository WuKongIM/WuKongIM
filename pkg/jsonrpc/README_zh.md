# JSON-RPC 2.0 编解码包 (`pkg/jsonrpc`)

## 概述

本包提供了根据 JSON-RPC 2.0 规范进行消息编码和解码的功能。它旨在处理 WuKongIM 系统内部使用的特定请求、响应和通知类型，这些类型定义在 `types.go` 文件中。

## 主要函数

### `Encode(msg interface{}) ([]byte, error)`

接收一个代表 JSON-RPC 消息（请求、响应或通知，例如 `ConnectRequest`、`GenericResponse`）的 Go 结构体，并将其编组（marshal）为 JSON 字节切片。它依赖于 Go 标准库的 `json.Marshal` 行为以及消息结构体上定义的 `json:` 标签。

**示例：**

```go
import "github.com/WuKongIM/WuKongIM/pkg/jsonrpc"

// 假设 connectReq 是一个已填充数据的 jsonrpc.ConnectRequest 结构体
jsonData, err := jsonrpc.Encode(connectReq)
if err != nil {
    // 处理编码错误
}
// jsonData 现在包含了 JSON-RPC 请求的字节数据
```

### `Decode(decoder *json.Decoder) (interface{}, Probe, error)`

从提供的 `json.Decoder`（通常包装一个 `io.Reader`，如网络连接）中读取下一个 JSON 对象，并尝试将其解码为已知的 WuKongIM JSON-RPC 消息类型之一。

1.  **探测 (Probing):** 首先将消息解码到一个临时的 `Probe` 结构体中，通过检查 `id`、`method`、`result` 和 `error` 等字段的存在来确定消息类型（请求、响应或通知）。
2.  **类型验证:** 根据 JSON-RPC 2.0 规则验证消息的基本结构。
3.  **具体解码:** 根据确定的类型和 `method` 字段（对于请求/通知），将消息解码为对应的具体 Go 结构体（例如 `ConnectRequest`、`SendRequest`、`GenericResponse`、`RecvNotification`）。
4.  **返回值:**
    *   `interface{}`: 解码后的消息，其类型为具体的 Go 类型（例如 `ConnectRequest`），如果发生错误则为 `nil`。
    *   `Probe`: 中间的 Probe 结构体，包含原始 JSON 字段（`Params`、`Result`、`Error` 作为 `json.RawMessage`）。即使后续解码步骤出错，这个结构体也可能有用。
    *   `error`: 解码过程中遇到的任何错误（包括 JSON 语法错误、验证错误或未知方法）。如果输入流正常结束，则返回 `io.EOF`。

**示例：**

```go
import (
	"encoding/json"
	"net"
	"log"
	"github.com/WuKongIM/WuKongIM/pkg/jsonrpc"
	"io"
)

func handleConnection(conn net.Conn) {
	defer conn.Close()
	decoder := json.NewDecoder(conn)

	for {
		msg, probe, err := jsonrpc.Decode(decoder)
		if err != nil {
			if err == io.EOF {
				log.Println("连接已关闭。")
				break
			}
			log.Printf("解码错误: %v. Probe 数据: %+v\n", err, probe)
			// 处理错误 (例如，发送错误响应，关闭连接)
			break // 或根据错误处理策略决定是否继续
		}

		// 根据解码后的消息类型进行处理
		switch m := msg.(type) {
		case jsonrpc.ConnectRequest:
			log.Printf("收到 ConnectRequest: %+v\n", m)
			// 处理连接请求...
		case jsonrpc.SendRequest:
			log.Printf("收到 SendRequest: %+v\n", m)
			// 处理发送请求...
		case jsonrpc.GenericResponse:
             log.Printf("收到 GenericResponse: %+v\n", m)
             // 处理响应... 或许可以使用 probe.ID 来匹配请求
		case jsonrpc.RecvNotification:
			log.Printf("收到 RecvNotification: %+v\n", m)
            // 处理接收通知...
		default:
			log.Printf("收到未知消息类型: %T. Probe 数据: %+v\n", msg, probe)
		}
	}
}

```

## 消息类型

具体的消息结构（请求、响应、通知、参数、结果等）定义在 `types.go` 文件中。像 `BaseRequest`、`BaseResponse` 和 `BaseNotification` 这样的基础类型提供了通用的字段。

## 当前限制 / 注意事项

*   **`Decode` 中的 `Header`/`Setting` 处理:** 当前 `Decode` 的实现主要依赖 `json.Unmarshal` 根据结构体标签填充字段。对于像 `SendRequest` 这样具有顶层 `Header` 和 `Setting` 字段（在 `Params` 之外）的消息类型，`Decode` *确实*会尝试基于初始探测来填充这些字段。但是，对于那些期望 `Header`/`Setting` *存在于* `Params` 结构体内部的类型（如 `RecvNotification`），正确的解码依赖于 `RecvNotificationParams` 结构体具有相应的字段和正确的标签。请查阅 `types.go` 中的结构体定义以获取准确的行为信息。
*   **未知方法:** 对于方法名未在其内部 `switch` 语句中显式处理的请求或通知，`Decode` 将返回错误。 