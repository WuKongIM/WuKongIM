package codec

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEncoderWriteStringUsesStringWriter(t *testing.T) {
	writer := &recordingStringWriter{}
	enc := NewEncoderBuffer(writer)

	enc.WriteString("hello")

	assert.Equal(t, 1, writer.stringWrites)
	assert.Equal(t, []byte{0, 5, 'h', 'e', 'l', 'l', 'o'}, writer.Bytes())
}

type recordingStringWriter struct {
	data         []byte
	stringWrites int
}

func (w *recordingStringWriter) Write(p []byte) (int, error) {
	w.data = append(w.data, p...)
	return len(p), nil
}

func (w *recordingStringWriter) WriteByte(b byte) error {
	w.data = append(w.data, b)
	return nil
}

func (w *recordingStringWriter) WriteString(s string) (int, error) {
	w.stringWrites++
	w.data = append(w.data, s...)
	return len(s), nil
}

func (w *recordingStringWriter) WriteTo(dst io.Writer) (int64, error) {
	n, err := dst.Write(w.data)
	return int64(n), err
}

func (w *recordingStringWriter) Bytes() []byte {
	return w.data
}

func (w *recordingStringWriter) Len() int {
	return len(w.data)
}
