package gnet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
)

const (
	proxyV1Signature      = "PROXY"
	proxyV2Signature      = "\r\n\r\n\x00\r\nQUIT\n"
	proxyV1MaxHeaderBytes = 107
	// Bound optional v2 metadata independently of the application's payload limit.
	proxyMaxHeaderBytes = 4096
	// The absolute preface deadline is not extended by partial reads.
	proxyHeaderTimeout = 5 * time.Second
)

var (
	errProxyHeaderInvalid  = errors.New("gateway/transport/gnet: invalid PROXY protocol header")
	errProxyHeaderTooLarge = errors.New("gateway/transport/gnet: PROXY protocol header too large")
	errProxyHeaderTimeout  = errors.New("gateway/transport/gnet: PROXY protocol header timeout")
	errProxyPeerUntrusted  = errors.New("gateway/transport/gnet: PROXY protocol from untrusted TCP peer")
)

type proxyHeader struct {
	consumed int            // zero means ordinary application bytes, not a PROXY header.
	source   netip.AddrPort // invalid for UNKNOWN, LOCAL, and unsupported address families.
}

// parseProxyHeader inspects only the beginning of a connection. A matching
// partial signature needs more bytes; after a full signature, errors never fall
// back to application traffic. It never retains the borrowed input buffer.
func parseProxyHeader(buf []byte) (header proxyHeader, complete bool, err error) {
	if len(buf) == 0 {
		return header, false, nil
	}
	if bytes.HasPrefix(buf, []byte(proxyV1Signature)) {
		return parseProxyV1(buf)
	}
	if bytes.HasPrefix(buf, []byte(proxyV2Signature)) {
		return parseProxyV2(buf)
	}
	if bytes.HasPrefix([]byte(proxyV1Signature), buf) || bytes.HasPrefix([]byte(proxyV2Signature), buf) {
		return header, false, nil
	}
	return header, true, nil
}

// parseProxyV1 validates the bounded ASCII line, including both endpoint families.
func parseProxyV1(buf []byte) (header proxyHeader, complete bool, err error) {
	end := bytes.Index(buf[:min(len(buf), proxyV1MaxHeaderBytes)], []byte("\r\n"))
	if end < 0 {
		if len(buf) >= proxyV1MaxHeaderBytes {
			return header, true, errProxyHeaderTooLarge
		}
		return header, false, nil
	}
	line := string(buf[:end])
	if line == "PROXY UNKNOWN" || strings.HasPrefix(line, "PROXY UNKNOWN ") {
		return proxyHeader{consumed: end + 2}, true, nil
	}
	parts := strings.Split(line, " ")
	if len(parts) != 6 || parts[0] != "PROXY" || (parts[1] != "TCP4" && parts[1] != "TCP6") {
		return header, true, errProxyHeaderInvalid
	}
	source, sourceErr := netip.ParseAddr(parts[2])
	dest, destErr := netip.ParseAddr(parts[3])
	ipv4 := parts[1] == "TCP4"
	if sourceErr != nil || destErr != nil || source.Zone() != "" || dest.Zone() != "" || source.Is4() != ipv4 || dest.Is4() != ipv4 {
		return header, true, errProxyHeaderInvalid
	}
	port, ok := parseProxyPort(parts[4])
	_, destOK := parseProxyPort(parts[5])
	if !ok || !destOK {
		return header, true, errProxyHeaderInvalid
	}
	return proxyHeader{consumed: end + 2, source: netip.AddrPortFrom(source, port)}, true, nil
}

func parseProxyPort(value string) (uint16, bool) {
	if value == "" || len(value) > 5 || (len(value) > 1 && value[0] == '0') {
		return 0, false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	port, err := strconv.ParseUint(value, 10, 16)
	return uint16(port), err == nil
}

// parseProxyV2 consumes exactly the declared header, never adjacent application bytes.
func parseProxyV2(buf []byte) (header proxyHeader, complete bool, err error) {
	if len(buf) < 16 {
		return header, false, nil
	}
	command := buf[12] & 0xf
	if buf[12]>>4 != 2 || command > 1 {
		return header, true, errProxyHeaderInvalid
	}
	size := 16 + int(binary.BigEndian.Uint16(buf[14:16]))
	if size > proxyMaxHeaderBytes {
		return header, true, errProxyHeaderTooLarge
	}
	if len(buf) < size {
		return header, false, nil
	}
	header.consumed = size
	// LOCAL must ignore even nonzero/unknown family and payload bytes.
	if command == 0 {
		return header, true, nil
	}
	family, protocol := buf[13]>>4, buf[13]&0xf
	if family > 3 || protocol > 2 {
		return header, true, errProxyHeaderInvalid
	}
	var addressBytes int
	switch buf[13] {
	case 0x11:
		addressBytes = 12
		if size < 16+addressBytes {
			return header, true, errProxyHeaderInvalid
		}
		header.source = netip.AddrPortFrom(netip.AddrFrom4([4]byte(buf[16:20])), binary.BigEndian.Uint16(buf[24:26]))
	case 0x21:
		addressBytes = 36
		if size < 16+addressBytes {
			return header, true, errProxyHeaderInvalid
		}
		header.source = netip.AddrPortFrom(netip.AddrFrom16([16]byte(buf[16:32])), binary.BigEndian.Uint16(buf[48:50]))
	default:
		// Valid unsupported families/transports have UNSPEC semantics: consume
		// the entire header and preserve the physical endpoints.
		return header, true, nil
	}
	// Unknown TLV values are opaque. Validate their framing without retaining
	// metadata or treating SSL TLVs as evidence of a local TLS connection.
	for rest := buf[16+addressBytes : size]; len(rest) > 0; {
		if len(rest) < 3 {
			return header, true, errProxyHeaderInvalid
		}
		n := 3 + int(binary.BigEndian.Uint16(rest[1:3]))
		if n > len(rest) {
			return header, true, errProxyHeaderInvalid
		}
		rest = rest[n:]
	}
	return header, true, nil
}

// proxyHandshake exists only until initial detection completes. Its buffer and
// timer pointer belong to the event loop; done arbitrates timeout versus success.
// AfterFunc creates no waiting goroutine per accepted connection.
type proxyHandshake struct {
	pending []byte
	trusted bool
	done    atomic.Bool
	timer   *time.Timer
}

// startProxyHandshake checks the physical peer once and starts the fixed preface
// deadline before any Session or HTTP Upgrade callbacks can run.
func (s *connState) startProxyHandshake(timeout time.Duration) {
	// An omitted or empty allowlist deliberately permits any physical peer.
	h := &proxyHandshake{trusted: len(s.runtime.trustedProxies) == 0}
	peer, err := netip.ParseAddrPort(s.peerAddr)
	if !h.trusted && err == nil {
		address := peer.Addr().Unmap().WithZone("")
		for _, prefix := range s.runtime.trustedProxies {
			if prefix.Contains(address) {
				h.trusted = true
				break
			}
		}
	}
	s.proxy = h
	h.timer = time.AfterFunc(timeout, func() {
		if h.done.CompareAndSwap(false, true) {
			s.rejectProxyHandshake(errProxyHeaderTimeout)
		}
	})
}

// cancelProxyHandshake fences the timer before releasing the event-loop state.
func (s *connState) cancelProxyHandshake() {
	if h := s.proxy; h != nil {
		h.done.Store(true)
		h.timer.Stop()
		s.proxy = nil
	}
}

// consumeProxyHandshake returns up to two borrowed application slices so a
// fragmented preface cannot force copying an arbitrarily large coalesced payload.
// The caller must consume/copy them before the next event-loop read.
func (s *connState) consumeProxyHandshake(data []byte) (prefix, rest []byte, ready bool) {
	h := s.proxy
	if h.done.Load() {
		return nil, nil, false
	}
	buf := data
	if len(h.pending) > 0 {
		n := min(len(data), proxyMaxHeaderBytes-len(h.pending))
		// Explicit capacity keeps Go slice growth inside the header budget.
		if len(h.pending)+n > cap(h.pending) {
			next := make([]byte, len(h.pending), min(proxyMaxHeaderBytes, max(len(h.pending)+n, 2*cap(h.pending))))
			copy(next, h.pending)
			h.pending = next
		}
		h.pending = append(h.pending, data[:n]...)
		buf, rest = h.pending, data[n:]
	}
	header, complete, err := parseProxyHeader(buf)
	// Once the signature is complete, reject untrusted assertions without
	// waiting for the rest of a potentially slow header.
	if !h.trusted && (bytes.HasPrefix(buf, []byte(proxyV1Signature)) || bytes.HasPrefix(buf, []byte(proxyV2Signature))) {
		err = errProxyPeerUntrusted
	}
	if err != nil {
		if h.done.CompareAndSwap(false, true) {
			h.timer.Stop()
			s.rejectProxyHandshake(err)
		}
		return nil, nil, false
	}
	if !complete {
		if len(h.pending) == 0 {
			h.pending = make([]byte, len(buf), max(64, len(buf)))
			copy(h.pending, buf)
		}
		return nil, nil, false
	}
	if !h.done.CompareAndSwap(false, true) {
		return nil, nil, false
	}
	h.timer.Stop()
	if header.source.IsValid() {
		s.remoteAddr = header.source.String()
	}
	s.proxy = nil
	if s.runtime.opts.Network == "tcp" {
		s.enqueueOpen()
	}
	return buf[header.consumed:], rest, true
}

// rejectProxyHandshake reports only a fixed cause and the physical peer, never
// untrusted header contents. No Session callbacks run for a rejected preface.
func (s *connState) rejectProxyHandshake(err error) {
	transport.LogConnectFailure(s.runtime.opts, s.id, s.localAddr, s.peerAddr, err)
	s.fail(err)
	_ = s.raw.Close()
}
