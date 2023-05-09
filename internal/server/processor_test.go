package server

import (
	"fmt"
	"testing"
)

func TestSameFrames(t *testing.T) {

	tests := make([]int, 0, 100)
	tests = append(tests, 1, 2, 3, 4)

	fmt.Println(fmt.Sprintf("tests-->%p", tests))
	dd := tests[1:]
	fmt.Println(fmt.Sprintf("tests-->%p", dd))

	dd[0] = 100
	// dd = nil

	// fmt.Println(fmt.Sprintf("tests-->%p", dd))

	fmt.Println("tests111--->", tests)

	var kk = func(z []int) {
		fmt.Println(fmt.Sprintf("zz-->%p", z))
	}
	kk(dd)
	// p := &Processor{}

	// p.sameFrames([]lmproto.Frame{
	// 	&lmproto.SendPacket{},
	// 	&lmproto.SendPacket{},
	// 	&lmproto.SendPacket{},
	// 	&lmproto.SendPacket{},
	// 	&lmproto.SendPacket{},
	// 	&lmproto.PingPacket{},
	// 	&lmproto.SendPacket{},
	// 	&lmproto.SendPacket{},
	// }, func(fs []lmproto.Frame) {
	// 	fmt.Println("fs--->", fs)
	// })
}
