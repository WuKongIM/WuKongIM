package client

import "github.com/WuKongIM/WuKongIM/pkg/wknet"

type Client struct {
	eng *wknet.Engine
}

func New(addr string) *Client {
	return &Client{
		eng: wknet.NewEngine(wknet.WithAddr(addr)),
	}
}
