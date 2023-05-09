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

	// p.sameFrames([]wkproto.Frame{
	// 	&wkproto.SendPacket{},
	// 	&wkproto.SendPacket{},
	// 	&wkproto.SendPacket{},
	// 	&wkproto.SendPacket{},
	// 	&wkproto.SendPacket{},
	// 	&wkproto.PingPacket{},
	// 	&wkproto.SendPacket{},
	// 	&wkproto.SendPacket{},
	// }, func(fs []wkproto.Frame) {
	// 	fmt.Println("fs--->", fs)
	// })
}
