package transport

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
)

type slabPool struct {
	sizes []int
	pools []sync.Pool
}

func newSlabPool(sizes []int) *slabPool {
	sp := &slabPool{
		sizes: append([]int(nil), sizes...),
		pools: make([]sync.Pool, len(sizes)),
	}
	for i, size := range sp.sizes {
		size := size
		sp.pools[i] = sync.Pool{
			New: func() any {
				return make([]byte, size)
			},
		}
	}
	return sp
}

func (sp *slabPool) get(n int) ([]byte, func()) {
	for i, size := range sp.sizes {
		if n <= size {
			buf := sp.pools[i].Get().([]byte)
			return buf[:size], func() {
				sp.pools[i].Put(buf[:size])
			}
		}
	}
	buf := make([]byte, n)
	return buf, func() {}
}

var frameSlabPool = newSlabPool([]int{512, 4096, 65536})

func encodeHeader(msgType uint8, bodyLen int) [HeaderSize]byte {
	var hdr [HeaderSize]byte
	hdr[0] = msgType
	binary.BigEndian.PutUint32(hdr[1:], uint32(bodyLen))
	return hdr
}

func readFrame(r io.Reader) (msgType uint8, body []byte, release func(), err error) {
	var hdr [HeaderSize]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, nil, err
	}
	msgType = hdr[0]
	if msgType == 0 {
		return 0, nil, nil, ErrInvalidMsgType
	}
	bodyLen := binary.BigEndian.Uint32(hdr[1:5])
	if bodyLen > MaxFrameSize {
		return 0, nil, nil, fmt.Errorf("%w: %d bytes", ErrMsgTooLarge, bodyLen)
	}
	if bodyLen == 0 {
		return msgType, nil, func() {}, nil
	}

	buf, rel := frameSlabPool.get(int(bodyLen))
	body = buf[:bodyLen]
	if _, err = io.ReadFull(r, body); err != nil {
		rel()
		return 0, nil, nil, err
	}
	return msgType, body, rel, nil
}

func writeFrame(bufs *net.Buffers, msgType uint8, body []byte) {
	hdr := encodeHeader(msgType, len(body))
	*bufs = append(*bufs, hdr[:], body)
}

// WriteMessage preserves the legacy API while using the new frame encoder.
func WriteMessage(w io.Writer, msgType uint8, body []byte) error {
	var bufs net.Buffers
	writeFrame(&bufs, msgType, body)
	_, err := bufs.WriteTo(w)
	return err
}

// ReadMessage preserves the legacy API while using the new frame reader.
func ReadMessage(r io.Reader) (msgType uint8, body []byte, err error) {
	msgType, body, release, err := readFrame(r)
	if err != nil {
		return 0, nil, err
	}
	if release != nil {
		defer release()
	}
	if len(body) == 0 {
		return msgType, nil, nil
	}
	out := append([]byte(nil), body...)
	return msgType, out, nil
}
