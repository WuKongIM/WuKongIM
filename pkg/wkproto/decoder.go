package wkproto

import "fmt"

// var (
// 	decOne = sync.Pool{
// 		New: func() interface{} {
// 			return make([]byte, 1)
// 		},
// 	}
// 	decTwo = sync.Pool{
// 		New: func() interface{} {
// 			return make([]byte, 2)
// 		},
// 	}
// 	decFour = sync.Pool{
// 		New: func() interface{} {
// 			return make([]byte, 4)
// 		},
// 	}
// 	decEight = sync.Pool{
// 		New: func() interface{} {
// 			return make([]byte, 8)
// 		},
// 	}
// )

// var decoderPool = sync.Pool{
// 	New: func() any {
// 		return &Decoder{}
// 	},
// }

// Decoder 解码
type Decoder struct {
	p      []byte
	offset int
}

// NewDecoder NewDecoder
func NewDecoder(p []byte) *Decoder {
	return &Decoder{
		p: p,
	}
}

// Len 长度
func (d *Decoder) Len() int {
	return len(d.p) - d.offset
}

// Int Int
// func (d *Decoder) Int() (int, error) {
// 	b := decOne.Get().([]byte)
// 	defer func() {
// 		decOne.Put(b)
// 	}()
// 	if n, err := d.r.Read(b); err != nil {
// 		return 0, err
// 	} else if n != 1 {
// 		return 0, fmt.Errorf("Decoder couldn't read expect bytes %d of 1", n)
// 	}
// 	return int(b[0]), nil
// }

// Uint8 Uint8
func (d *Decoder) Uint8() (uint8, error) {
	if d.offset+1 > len(d.p) {
		return 0, fmt.Errorf("Decoder couldn't read expect bytes %d of %d", d.offset+1, len(d.p))
	}
	b := d.p[d.offset]
	d.offset += 1
	return b, nil
}

// Int16 Int16
func (d *Decoder) Int16() (int16, error) {
	if d.offset+2 > len(d.p) {
		return 0, fmt.Errorf("Decoder couldn't read expect bytes %d of %d", d.offset+2, len(d.p))
	}
	b := d.p[d.offset : d.offset+2]
	d.offset += 2
	return (int16(b[0]) << 8) | int16(b[1]), nil
}

// Uint16 Uint16
func (d *Decoder) Uint16() (uint16, error) {
	if i, err := d.Int16(); err != nil {
		return 0, err
	} else {
		return uint16(i), nil
	}
}

// Bytes Bytes
func (d *Decoder) Bytes(num int) ([]byte, error) {
	if d.offset+num > len(d.p) {
		return nil, fmt.Errorf("Decoder couldn't read expect bytes %d of %d", d.offset+num, len(d.p))
	}
	b := d.p[d.offset : d.offset+num]
	d.offset += num
	return b, nil

}

// Int64 Int64
func (d *Decoder) Int64() (int64, error) {
	if d.offset+8 > len(d.p) {
		return 0, fmt.Errorf("Decoder couldn't read expect bytes %d of %d", d.offset+8, len(d.p))
	}
	b := d.p[d.offset : d.offset+8]
	d.offset += 8
	return (int64(b[0]) << 56) | (int64(b[1]) << 48) | (int64(b[2]) << 40) | int64(b[3])<<32 | int64(b[4])<<24 | int64(b[5])<<16 | int64(b[6])<<8 | int64(b[7]), nil
}

// Uint64 Uint64
func (d *Decoder) Uint64() (uint64, error) {
	if d.offset+8 > len(d.p) {
		return 0, fmt.Errorf("Decoder couldn't read expect bytes %d of %d", d.offset+8, len(d.p))
	}
	b := d.p[d.offset : d.offset+8]
	d.offset += 8
	return (uint64(b[0]) << 56) | (uint64(b[1]) << 48) | (uint64(b[2]) << 40) | uint64(b[3])<<32 | uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7]), nil
}

// Int32 Int32
func (d *Decoder) Int32() (int32, error) {
	if d.offset+4 > len(d.p) {
		return 0, fmt.Errorf("Decoder couldn't read expect bytes %d of %d", d.offset+4, len(d.p))
	}
	b := d.p[d.offset : d.offset+4]
	d.offset += 4
	return (int32(b[0]) << 24) | (int32(b[1]) << 16) | (int32(b[2]) << 8) | int32(b[3]), nil
}

// Uint32 Uint32
func (d *Decoder) Uint32() (uint32, error) {
	i, err := d.Int32()
	if err != nil {
		return 0, err
	}
	return uint32(i), nil
}

func (d *Decoder) String() (string, error) {
	if buf, err := d.Binary(); err != nil {
		return "", err
	} else {
		return string(buf), nil
	}
}

// StringAll StringAll
func (d *Decoder) StringAll() (string, error) {
	if buf, err := d.BinaryAll(); err != nil {
		return "", err
	} else {
		return string(buf), nil
	}
}

// Binary Binary
func (d *Decoder) Binary() ([]byte, error) {
	size, err := d.Int16()
	if err != nil {
		return nil, err
	}
	if d.offset+int(size) > len(d.p) {
		return nil, fmt.Errorf("Decoder couldn't read expect bytes %d of %d", d.offset+int(size), len(d.p))
	}
	b := d.p[d.offset : d.offset+int(size)]
	d.offset += int(size)
	return b, nil
}

// BinaryAll BinaryAll
func (d *Decoder) BinaryAll() ([]byte, error) {

	remains := d.Len()

	b := d.p[d.offset:]
	d.offset += remains
	return b, nil

}

// Variable Variable
func (d *Decoder) Variable() (uint64, error) {
	var (
		size uint64
		mul  uint64 = 1
	)
	for {
		i, err := d.Uint8()
		if err != nil {
			return 0, err
		}
		size += uint64(i&0x7F) * mul
		mul *= 0x80
		if i&0x80 == 0 {
			break
		}
	}
	return size, nil
}

// // Int16 Int16
// func Int16(reader io.Reader) (int16, error) {
// 	b := decTwo.Get().([]byte)
// 	defer func() {
// 		decTwo.Put(b)
// 	}()
// 	if n, err := reader.Read(b); err != nil {
// 		return 0, err
// 	} else if n != 2 {
// 		return 0, fmt.Errorf("Decoder couldn't read expect bytes %d of 2", n)
// 	}
// 	return (int16(b[0]) << 8) | int16(b[1]), nil
// }

// // Binary Binary
// func Binary(reader io.Reader) ([]byte, error) {
// 	size, err := Int16(reader)
// 	if err != nil {
// 		return nil, err
// 	}
// 	buf := make([]byte, uint16(size))
// 	if size == 0 {
// 		return buf, nil
// 	}
// 	if n, err := reader.Read(buf); err != nil {
// 		return nil, err
// 	} else if n != int(size) {
// 		return nil, fmt.Errorf("Decoder couldn't read expect bytes %d of %d", n, size)
// 	}
// 	return buf, nil
// }

// // Int32 Int32
// func Int32(reader io.Reader) (int32, error) {
// 	b := decFour.Get().([]byte)
// 	defer func() {
// 		decFour.Put(b)
// 	}()
// 	if n, err := reader.Read(b); err != nil {
// 		return 0, err
// 	} else if n != 4 {
// 		return 0, fmt.Errorf("Decoder couldn't read expect bytes %d of 4", n)
// 	}
// 	return (int32(b[0]) << 24) | (int32(b[1]) << 16) | (int32(b[2]) << 8) | int32(b[3]), nil
// }

// Uint32 Uint32
// func Uint32(reader io.Reader) (uint32, error) {
// 	if i, err := Int32(reader); err != nil {
// 		return 0, err
// 	} else {
// 		return uint32(i), nil
// 	}
// }
