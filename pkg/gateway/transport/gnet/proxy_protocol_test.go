package gnet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func proxyV2TestHeader(command, family byte, payload []byte) []byte {
	out := append([]byte(proxyV2Signature), 0x20|command, family, 0, 0)
	binary.BigEndian.PutUint16(out[14:16], uint16(len(payload)))
	return append(out, payload...)
}

func proxyV2TestAddress(ipv6 bool) []byte {
	if ipv6 {
		source := netip.MustParseAddr("2001:db8::1").As16()
		dest := netip.MustParseAddr("2001:db8::2").As16()
		out := append(source[:], dest[:]...)
		return append(out, 0x30, 0x39, 0x13, 0xec)
	}
	return []byte{203, 0, 113, 1, 10, 0, 0, 2, 0x30, 0x39, 0x13, 0xec}
}

func TestProxyProtocolHeadersAndEveryPartialPrefix(t *testing.T) {
	tests := []struct {
		name   string
		wire   []byte
		source string
	}{
		{"v1 ipv4", []byte("PROXY TCP4 203.0.113.1 10.0.0.2 12345 5100\r\n"), "203.0.113.1:12345"},
		{"v1 ipv6", []byte("PROXY TCP6 2001:db8::1 2001:db8::2 12345 5100\r\n"), "[2001:db8::1]:12345"},
		{"v1 mapped ipv6", []byte("PROXY TCP6 ::ffff:203.0.113.1 ::ffff:10.0.0.2 12345 0\r\n"), "[::ffff:203.0.113.1]:12345"},
		{"v1 zero port", []byte("PROXY TCP4 203.0.113.1 10.0.0.2 0 0\r\n"), "203.0.113.1:0"},
		{"v1 unknown", []byte("PROXY UNKNOWN\r\n"), ""},
		{"v1 unknown opaque", []byte("PROXY UNKNOWN arbitrary ignored data\r\n"), ""},
		{"v1 maximum", []byte("PROXY UNKNOWN " + strings.Repeat("x", 91) + "\r\n"), ""},
		{"v2 ipv4", proxyV2TestHeader(1, 0x11, proxyV2TestAddress(false)), "203.0.113.1:12345"},
		{"v2 ipv6", proxyV2TestHeader(1, 0x21, proxyV2TestAddress(true)), "[2001:db8::1]:12345"},
		{"v2 unknown tlv", proxyV2TestHeader(1, 0x11, append(proxyV2TestAddress(false), 0xea, 0, 3, 1, 2, 3)), "203.0.113.1:12345"},
		{"v2 local ignores family and bytes", proxyV2TestHeader(0, 0xff, []byte{1, 2, 3, 4}), ""},
		{"v2 unspec", proxyV2TestHeader(1, 0, []byte{1, 2, 3}), ""},
		{"v2 unsupported udp", proxyV2TestHeader(1, 0x12, proxyV2TestAddress(false)), ""},
		{"v2 unsupported unix", proxyV2TestHeader(1, 0x31, make([]byte, 216)), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for n := 0; n < len(tt.wire); n++ {
				if _, complete, err := parseProxyHeader(tt.wire[:n]); complete || err != nil {
					t.Fatalf("prefix %d: complete=%v err=%v", n, complete, err)
				}
			}
			wire := append(append([]byte(nil), tt.wire...), []byte("business payload")...)
			header, complete, err := parseProxyHeader(wire)
			if err != nil || !complete || header.consumed != len(tt.wire) {
				t.Fatalf("header=%+v complete=%v err=%v", header, complete, err)
			}
			source := ""
			if header.source.IsValid() {
				source = header.source.String()
			}
			if source != tt.source {
				t.Fatalf("source=%q want %q", source, tt.source)
			}
			if string(wire[header.consumed:]) != "business payload" {
				t.Fatal("lost coalesced payload")
			}
		})
	}
}

func TestProxyProtocolDirectTrafficIsUnchanged(t *testing.T) {
	for _, data := range [][]byte{{0x10, 0}, []byte("GET / HTTP/1.1\r\n"), []byte("POST /"), []byte("PROXimate"), []byte("\r\nother"), bytes.Repeat([]byte{'x'}, 1<<20)} {
		header, complete, err := parseProxyHeader(data)
		if err != nil || !complete || header.consumed != 0 || header.source.IsValid() {
			t.Fatalf("direct prefix %x: %+v %v %v", data[:min(16, len(data))], header, complete, err)
		}
	}
}

func TestProxyProtocolRejectsMalformedHeaders(t *testing.T) {
	badVersion := proxyV2TestHeader(1, 0, nil)
	badVersion[12] = 0x31
	tests := [][]byte{
		[]byte("PROXYX TCP4 1.1.1.1 2.2.2.2 1 2\r\n"),
		[]byte("PROXY  TCP4 1.1.1.1 2.2.2.2 1 2\r\n"),
		[]byte("PROXY TCP4 1.1.1.1 ::1 1 2\r\n"),
		[]byte("PROXY TCP6 ::1%eth0 ::1 1 2\r\n"),
		[]byte("PROXY TCP4 256.1.1.1 2.2.2.2 1 2\r\n"),
		[]byte("PROXY TCP4 01.1.1.1 2.2.2.2 1 2\r\n"),
		[]byte("PROXY TCP4 1.1.1.1 2.2.2.2 -1 2\r\n"),
		[]byte("PROXY TCP4 1.1.1.1 2.2.2.2 +1 2\r\n"),
		[]byte("PROXY TCP4 1.1.1.1 2.2.2.2 01 2\r\n"),
		[]byte("PROXY TCP4 1.1.1.1 2.2.2.2 1 65536\r\n"),
		[]byte("PROXY UDP4 1.1.1.1 2.2.2.2 1 2\r\n"),
		badVersion, proxyV2TestHeader(2, 0, nil), proxyV2TestHeader(1, 0xf1, nil), proxyV2TestHeader(1, 0x13, nil),
		proxyV2TestHeader(1, 0x11, make([]byte, 11)), proxyV2TestHeader(1, 0x21, make([]byte, 35)),
		proxyV2TestHeader(1, 0x11, append(proxyV2TestAddress(false), 0xea, 0)),
		proxyV2TestHeader(1, 0x11, append(proxyV2TestAddress(false), 0xea, 0, 2, 1)),
	}
	for i, wire := range tests {
		if _, _, err := parseProxyHeader(wire); !errors.Is(err, errProxyHeaderInvalid) {
			t.Errorf("case %d: err=%v", i, err)
		}
	}
	for _, wire := range [][]byte{[]byte("PROXY " + strings.Repeat("x", 101)), proxyV2TestHeader(1, 0, make([]byte, proxyMaxHeaderBytes))} {
		if _, _, err := parseProxyHeader(wire); !errors.Is(err, errProxyHeaderTooLarge) {
			t.Fatalf("size error=%v", err)
		}
	}
}

func FuzzProxyProtocolHeader(f *testing.F) {
	f.Add([]byte("PROXY TCP4 203.0.113.1 10.0.0.2 12345 5100\r\n"))
	f.Add(proxyV2TestHeader(1, 0x21, proxyV2TestAddress(true)))
	f.Add([]byte("GET / HTTP/1.1\r\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		header, complete, err := parseProxyHeader(data)
		if err == nil && complete && (header.consumed < 0 || header.consumed > len(data) || header.consumed > proxyMaxHeaderBytes) {
			t.Fatalf("invalid consumed=%d", header.consumed)
		}
		if !complete && err == nil && len(data) >= proxyMaxHeaderBytes {
			t.Fatal("unbounded incomplete header")
		}
	})
}
