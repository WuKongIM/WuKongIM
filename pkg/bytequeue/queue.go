package bytequeue

import "github.com/valyala/bytebufferpool"

type ByteQueue struct {
	buffer     *bytebufferpool.ByteBuffer
	offsetSize uint64 // 偏移大小
	totalSize  uint64 // 总大小

}

func New() *ByteQueue {
	return &ByteQueue{
		buffer: bytebufferpool.Get(),
	}
}

// Write 写入字节
func (b *ByteQueue) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n, err := b.buffer.Write(p)
	if err != nil {
		return 0, err
	}
	b.totalSize += uint64(n)
	return n, nil
}

// 从指定位置开始读取n个字节
func (b *ByteQueue) Peek(startPosition uint64, n int) []byte {
	if b.totalSize == 0 {
		return nil
	}
	if startPosition >= b.totalSize {
		return nil
	}
	startIndex := int(startPosition - b.offsetSize)
	if startIndex < 0 {
		return nil
	}
	if startIndex+n > len(b.buffer.B) {
		n = len(b.buffer.B) - int(startIndex)
	}
	return b.buffer.B[startIndex : startIndex+n]
}

func (b *ByteQueue) Discard(endPosition uint64) {
	n := int(endPosition - b.offsetSize)
	_ = b.discard(n)
}

func (b *ByteQueue) discard(n int) int {
	if n == 0 {
		return 0
	}
	if n >= len(b.buffer.B) {
		b.offsetSize += uint64(len(b.buffer.B))
		b.buffer.B = b.buffer.B[:0]
		return len(b.buffer.B)
	}
	b.buffer.B = b.buffer.B[n:]
	b.offsetSize += uint64(n)
	return n
}

func (b *ByteQueue) Reset() {
	b.buffer.Reset()
	b.offsetSize = 0
	b.totalSize = 0
	bytebufferpool.Put(b.buffer)
}
