package server

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
)

var (
	SIGV1 = []byte{'\x50', '\x52', '\x4F', '\x58', '\x59'} // PROXY
	SIGV2 = []byte{'\x0D', '\x0A', '\x0D', '\x0A', '\x00', '\x0D', '\x0A', '\x51', '\x55', '\x49', '\x54', '\x0A'}
)

var (
	ErrNoProxyProtocol                  = errors.New("proxyproto: proxy protocol signature not present")
	ErrCantReadVersion1Header           = errors.New("proxyproto: can't read version 1 header")
	ErrVersion1HeaderTooLong            = errors.New("proxyproto: version 1 header must be 107 bytes or less")
	ErrLineMustEndWithCrlf              = errors.New("proxyproto: version 1 header is invalid, must end with \\r\\n")
	ErrCantReadAddressFamilyAndProtocol = errors.New("proxyproto: can't read address family or protocol")
	ErrInvalidAddress                   = errors.New("proxyproto: invalid address")
	ErrInvalidPortNumber                = errors.New("proxyproto: invalid port number")
	ErrCantReadLength                   = errors.New("proxyproto: can't read length")
	ErrInvalidLength                    = errors.New("proxyproto: invalid length")
)

const (
	crlf      = "\r\n"
	separator = " "

	lengthUnspec = uint16(0)
	lengthV4     = uint16(12)
	lengthV6     = uint16(36)
	lengthUnix   = uint16(216)
)

// AddressFamilyAndProtocol represents address family and transport protocol.
type AddressFamilyAndProtocol byte

const (
	UNSPEC       AddressFamilyAndProtocol = '\x00'
	TCPv4        AddressFamilyAndProtocol = '\x11'
	UDPv4        AddressFamilyAndProtocol = '\x12'
	TCPv6        AddressFamilyAndProtocol = '\x21'
	UDPv6        AddressFamilyAndProtocol = '\x22'
	UnixStream   AddressFamilyAndProtocol = '\x31'
	UnixDatagram AddressFamilyAndProtocol = '\x32'
)

// IsIPv4 returns true if the address family is IPv4 (AF_INET4), false otherwise.
func (ap AddressFamilyAndProtocol) IsIPv4() bool {
	return ap&0xF0 == 0x10
}

// IsIPv6 returns true if the address family is IPv6 (AF_INET6), false otherwise.
func (ap AddressFamilyAndProtocol) IsIPv6() bool {
	return ap&0xF0 == 0x20
}

// IsUnix returns true if the address family is UNIX (AF_UNIX), false otherwise.
func (ap AddressFamilyAndProtocol) IsUnix() bool {
	return ap&0xF0 == 0x30
}

// IsStream returns true if the transport protocol is TCP or STREAM (SOCK_STREAM), false otherwise.
func (ap AddressFamilyAndProtocol) IsStream() bool {
	return ap&0x0F == 0x01
}

// IsDatagram returns true if the transport protocol is UDP or DGRAM (SOCK_DGRAM), false otherwise.
func (ap AddressFamilyAndProtocol) IsDatagram() bool {
	return ap&0x0F == 0x02
}

// IsUnspec returns true if the transport protocol or address family is unspecified, false otherwise.
func (ap AddressFamilyAndProtocol) IsUnspec() bool {
	return (ap&0xF0 == 0x00) || (ap&0x0F == 0x00)
}

// func (ap AddressFamilyAndProtocol) toByte() byte {
// 	if ap.IsIPv4() && ap.IsStream() {
// 		return byte(TCPv4)
// 	} else if ap.IsIPv4() && ap.IsDatagram() {
// 		return byte(UDPv4)
// 	} else if ap.IsIPv6() && ap.IsStream() {
// 		return byte(TCPv6)
// 	} else if ap.IsIPv6() && ap.IsDatagram() {
// 		return byte(UDPv6)
// 	} else if ap.IsUnix() && ap.IsStream() {
// 		return byte(UnixStream)
// 	} else if ap.IsUnix() && ap.IsDatagram() {
// 		return byte(UnixDatagram)
// 	}

// 	return byte(UNSPEC)
// }

// 是否是代理协议
func isProxyProto(buff []byte) bool {

	b1 := buff[:1]
	if bytes.Equal(b1[:1], SIGV1[:1]) || bytes.Equal(b1[:1], SIGV2[:1]) {
		return true
	}
	return false
}

// 解析代理协议 如果是代理协议则解析出真实的地址（如果通过反向代理并开启了代理协议，需要从代理协议里获取到连接的真实ip）
func parseProxyProto(buff []byte) (remoteAddr net.Addr, size int, err error) {

	dataLen := len(buff)

	if dataLen <= 5 {
		return nil, 0, ErrNoProxyProtocol
	}

	b1 := buff[:1]

	if bytes.Equal(b1[:1], SIGV1[:1]) || bytes.Equal(b1[:1], SIGV2[:1]) {
		signature := buff[:5]
		if bytes.Equal(signature[:5], SIGV1) {
			return parseProxyProtoV1(buff)
		}
		if dataLen <= 12 {
			return nil, 0, ErrNoProxyProtocol
		}
		signature = buff[:12]
		if bytes.Equal(signature[:12], SIGV2) {
			fmt.Println("proxyproto: proxy protocol v2")
			return parseProxyProtoV2(buff)
		}
	}

	return nil, 0, ErrNoProxyProtocol
}

func parseProxyProtoV1(data []byte) (remoteAddr net.Addr, size int, err error) {
	//The header cannot be more than 107 bytes long. Per spec:
	//
	//   (...)
	//   - worst case (optional fields set to 0xff) :
	//     "PROXY UNKNOWN ffff:f...f:ffff ffff:f...f:ffff 65535 65535\r\n"
	//     => 5 + 1 + 7 + 1 + 39 + 1 + 39 + 1 + 5 + 1 + 5 + 2 = 107 chars
	//
	//   So a 108-byte buffer is always enough to store all the line and a
	//   trailing zero for string processing.
	//
	// It must also be CRLF terminated, as above. The header does not otherwise
	// contain a CR or LF byte.

	buffArray := bytes.Split(data, []byte{'\n'})
	buf := buffArray[0]

	if len(buf) >= 107 {
		// No delimiter in first 107 bytes
		return nil, 0, ErrVersion1HeaderTooLong
	}

	// Check for CR before LF.
	if len(buf) < 1 || buf[len(buf)-1] != '\r' {
		return nil, 0, ErrLineMustEndWithCrlf
	}

	// Check full signature.
	tokens := strings.Split(string(buf[:len(buf)-1]), separator)

	// Expect at least 2 tokens: "PROXY" and the transport protocol.
	if len(tokens) < 2 {
		return nil, 0, ErrCantReadAddressFamilyAndProtocol
	}

	// Read address family and protocol
	var transportProtocol AddressFamilyAndProtocol
	switch tokens[1] {
	case "TCP4":
		transportProtocol = TCPv4
	case "TCP6":
		transportProtocol = TCPv6
	case "UNKNOWN":
		transportProtocol = UNSPEC // doesn't exist in v1 but fits UNKNOWN
	default:
		return nil, 0, ErrCantReadAddressFamilyAndProtocol
	}

	// Expect 6 tokens only when UNKNOWN is not present.
	if transportProtocol != UNSPEC && len(tokens) < 6 {
		return nil, 0, ErrCantReadAddressFamilyAndProtocol
	}

	// Otherwise, continue to read addresses and ports
	sourceIP, err := parseV1IPAddress(transportProtocol, tokens[2])
	if err != nil {
		return nil, 0, err
	}

	sourcePort, err := parseV1PortNumber(tokens[4])
	if err != nil {
		return nil, 0, err
	}

	return &net.TCPAddr{
		IP:   sourceIP,
		Port: sourcePort,
	}, len(buf) + 1, nil
}

func parseV1IPAddress(protocol AddressFamilyAndProtocol, addrStr string) (net.IP, error) {
	addr, err := netip.ParseAddr(addrStr)
	if err != nil {
		return nil, ErrInvalidAddress
	}

	switch protocol {
	case TCPv4:
		if addr.Is4() {
			return net.IP(addr.AsSlice()), nil
		}
	case TCPv6:
		if addr.Is6() || addr.Is4In6() {
			return net.IP(addr.AsSlice()), nil
		}
	}

	return nil, ErrInvalidAddress
}

func parseV1PortNumber(portStr string) (int, error) {
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return 0, ErrInvalidPortNumber
	}
	return port, nil
}

func parseProxyProtoV2(buff []byte) (remoteAddr net.Addr, size int, err error) {

	decoder := wkproto.NewDecoder(buff)

	// Skip first 12 bytes (signature)
	_, _ = decoder.Bytes(12)

	// Read the 13th byte, protocol version and command
	_, _ = decoder.Bytes(1)

	// Read the 14th byte, address family and protocol
	addrFamilyProto, err := decoder.Bytes(1)
	if err != nil {
		return nil, 0, ErrCantReadAddressFamilyAndProtocol
	}

	addressFamilyAndProtocol := AddressFamilyAndProtocol(addrFamilyProto[0])

	// Make sure there are bytes available as specified in length
	length, err := decoder.Uint16()
	if err != nil {
		return nil, 0, ErrCantReadLength
	}

	if !validateProxyProtoLength(addressFamilyAndProtocol, length) {
		return nil, 0, ErrInvalidLength
	}

	// Return early if the length is zero, which means that
	// there's no address information and TLVs present for UNSPEC.
	if length == 0 {
		return nil, 0, ErrInvalidLength
	}

	payload, err := decoder.Bytes(int(length))
	if err != nil {
		return nil, 0, err
	}

	payloadReader := io.LimitReader(bytes.NewReader(payload), int64(length)).(*io.LimitedReader)

	size = 12 + 1 + 1 + 2 + int(length)

	if addressFamilyAndProtocol != UNSPEC {
		if addressFamilyAndProtocol.IsIPv4() {
			var addr _addr4
			if err := binary.Read(payloadReader, binary.BigEndian, &addr); err != nil {
				return nil, 0, ErrInvalidAddress
			}
			return &net.TCPAddr{
				IP:   net.IP(addr.Src[:]),
				Port: int(addr.SrcPort),
			}, size, nil
		} else if addressFamilyAndProtocol.IsIPv6() {
			var addr _addr6
			if err := binary.Read(payloadReader, binary.BigEndian, &addr); err != nil {
				return nil, 0, ErrInvalidAddress
			}
			return &net.TCPAddr{
				IP:   net.IP(addr.Src[:]),
				Port: int(addr.SrcPort),
			}, size, nil
		} else if addressFamilyAndProtocol.IsUnix() {
			var addr _addrUnix
			if err := binary.Read(payloadReader, binary.BigEndian, &addr); err != nil {
				return nil, 0, ErrInvalidAddress
			}
			network := "unix"
			if addressFamilyAndProtocol.IsDatagram() {
				network = "unixgram"
			}
			return &net.UnixAddr{
				Net:  network,
				Name: parseUnixName(addr.Src[:]),
			}, size, nil
		}
	}

	return nil, size, nil
}

func validateProxyProtoLength(transportProtocol AddressFamilyAndProtocol, length uint16) bool {
	if transportProtocol.IsIPv4() {
		return length >= lengthV4
	} else if transportProtocol.IsIPv6() {
		return length >= lengthV6
	} else if transportProtocol.IsUnix() {
		return length >= lengthUnix
	} else if transportProtocol.IsUnspec() {
		return length >= lengthUnspec
	}
	return false
}

func parseUnixName(b []byte) string {
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return string(b)
	}
	return string(b[:i])
}

type _ports struct {
	SrcPort uint16
	DstPort uint16
}

type _addr4 struct {
	Src     [4]byte
	Dst     [4]byte
	SrcPort uint16
	DstPort uint16
}

type _addr6 struct {
	Src [16]byte
	Dst [16]byte
	_ports
}

type _addrUnix struct {
	Src [108]byte
	Dst [108]byte
}
