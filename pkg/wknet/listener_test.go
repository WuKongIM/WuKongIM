package wknet

import (
	"net"
	"syscall"
	"testing"
)

func TestParseSockaddr(t *testing.T) {
	t.Run("IPv4", func(t *testing.T) {
		addr := &syscall.SockaddrInet4{
			Addr: [4]byte{127, 0, 0, 1},
			Port: 8080,
		}
		expected := &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 8080,
		}

		result, err := parseSockaddr(addr)
		if err != nil {
			t.Errorf("Unexpected error: %s", err)
		}
		if !result.(*net.TCPAddr).IP.Equal(expected.IP) || result.(*net.TCPAddr).Port != expected.Port {
			t.Errorf("Expected %v, got %v", expected, result)
		}
	})

	t.Run("IPv6", func(t *testing.T) {
		// 省略 IPv6 测试实现，类似于 IPv4
	})

	t.Run("UnsupportedType", func(t *testing.T) {
		addr := &syscall.SockaddrLinklayer{} // 不支持的类型
		_, err := parseSockaddr(addr)
		if err == nil {
			t.Errorf("Expected error, got nil")
		}
	})
}
