

useage

```go


 wknet.Serve("tcp://127.0.0.1:1999",handler,opts...)

```

event

```go
OnConnect(c *wknet.Conn)

OnClose(c *wknet.Conn)

OnData(c *wknet.Conn)

```