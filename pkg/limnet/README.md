

useage

```go


 limnet.Serve("tcp://127.0.0.1:1999",handler,opts...)

```

event

```go
OnConnect(c *limnet.Conn)

OnClose(c *limnet.Conn)

OnData(c *limnet.Conn)

```