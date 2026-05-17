package main

import (
	"fmt"
	"net/http"

	"github.com/WuKongIM/go-pdk/pdk"
)

const (
	pluginNo      = "wk.echo"
	pluginVersion = "0.0.1"
)

func main() {
	if err := pdk.RunServer(New, pluginNo, pdk.WithVersion(pluginVersion), pdk.WithPriority(1)); err != nil {
		panic(err)
	}
}

type EchoPlugin struct{}

func New() interface{} { return &EchoPlugin{} }

func (p *EchoPlugin) Route(r *pdk.Route) {
	r.GET("/echo", p.echo)
}

func (p *EchoPlugin) echo(c *pdk.HttpContext) {
	c.Response.Headers["X-Plugin-No"] = pluginNo
	c.JSON(http.StatusAccepted, map[string]any{
		"echo":   c.GetQuery("q"),
		"method": c.Request.Method,
		"path":   c.Request.Path,
	})
}

func (p *EchoPlugin) Setup() {
	fmt.Println("echo plugin setup")
}
