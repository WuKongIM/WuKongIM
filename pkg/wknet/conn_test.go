package wknet

import (
	"fmt"
	"testing"
	"time"
)

func TestConn(t *testing.T) {
	conns := make([]*DefaultConn, 0)
	conns = append(conns, &DefaultConn{
		id:  1,
		uid: "1",
		fd:  1,
	}, &DefaultConn{
		id:  2,
		uid: "2",
		fd:  2,
	}, &DefaultConn{
		id:  3,
		uid: "3",
		fd:  3,
	}, &DefaultConn{
		id:  4,
		uid: "4",
		fd:  4,
	}, &DefaultConn{
		id:  5,
		uid: "5",
		fd:  5,
	},
	)

	for _, conn := range conns {
		go func(newConn *DefaultConn) {
			time.Sleep(time.Millisecond * 100)
			fmt.Println("conn->", newConn.id)
		}(conn)
	}
	time.Sleep(time.Second * 5)
}
