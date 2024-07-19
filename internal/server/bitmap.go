package server

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"

	"github.com/pkg/errors"
)

var ErrOutOfRange = errors.New("Value out of range.")

// BitMap is a struct containing a slice of bytes,
// being used as a bitmap.
type BitMap struct {
	size uint64
	vals []byte
}

// New returns a BitMap. It requires a size. A bitmap with a size of
// eight or less will be one byte in size, and so on.
func NewBitMap(s uint64) BitMap {
	l := s/8 + 1
	return BitMap{size: s, vals: make([]byte, l, l)}
}

// Size returns the size of a bitmap. This is the number
// of bits.
func (b BitMap) Size() uint64 {
	return b.size
}

// checkRange returns an error if the position
// passed is not allowed.
func (b BitMap) checkRange(i uint64) error {
	if i > b.Size() {
		return ErrOutOfRange
	}
	if i < 1 {
		return ErrOutOfRange
	}
	return nil
}

// For internal use; drives Set and Unset.
func (b BitMap) toggle(i uint64) {
	// Position of the byte in b.vals.
	p := i >> 3
	// Position of the bit in the byte.
	remainder := i - (p * 8)
	// Toggle the bit.
	if remainder == 1 {
		b.vals[p] = b.vals[p] ^ 1
	} else {
		b.vals[p] = b.vals[p] ^ (1 << uint(remainder-1))
	}
}

// Set sets a position in
// the bitmap to 1.
func (b BitMap) Set(i uint64) error {
	if x := b.checkRange(i); x != nil {
		return x
	}
	// Don't unset.
	val, err := b.IsSet(i)
	if err != nil {
		return err
	}
	if val {
		return nil
	}
	b.toggle(i)
	return nil
}

// Unset sets a position in
// the bitmap to 0.
func (b BitMap) Unset(i uint64) error {
	// Don't set.
	val, err := b.IsSet(i)
	if err != nil {
		return err
	}
	if val {
		b.toggle(i)
	}
	return nil
}

// Values returns a slice of ints
// represented by the values in the bitmap.
func (b BitMap) Values() ([]uint64, error) {
	list := make([]uint64, 0, b.Size())
	for i := uint64(1); i <= b.Size(); i++ {
		val, err := b.IsSet(i)
		if err != nil {
			return nil, err
		}
		if val {
			list = append(list, i)
		}
	}
	return list, nil
}

// IsSet returns a boolean indicating whether
// the bit is set for the position in question.
func (b BitMap) IsSet(i uint64) (bool, error) {
	if x := b.checkRange(i); x != nil {
		return false, x
	}
	p := i >> 3
	remainder := i - (p * 8)
	if remainder == 1 {
		return b.vals[p] > b.vals[p]^1, nil
	}
	return b.vals[p] > b.vals[p]^(1<<uint(remainder-1)), nil
}

func (b BitMap) ToString() (string, error) {

	dummy := struct {
		Size uint64
		Vals []byte
	}{
		Size: b.size,
		Vals: b.vals,
	}

	j, err := json.Marshal(dummy)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err = gz.Write(j)
	if err != nil {
		return "", err
	}
	err = gz.Close()
	if err != nil {
		return "", err
	}

	s := base64.StdEncoding.EncodeToString(buf.Bytes())
	return s, nil

}

func FromString(input string) (BitMap, error) {

	var dummy struct {
		Size uint64
		Vals []byte
	}

	raw, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		fmt.Println("error base64 decoding:", err)
		return BitMap{}, err
	}

	buf := bytes.NewBuffer(raw)
	gz, err := gzip.NewReader(buf)

	if err != nil {
		log.Printf("failed to create gzip reader: %s", err)
		return BitMap{}, err
	}

	inner, err := ioutil.ReadAll(gz)
	if err != nil {
		log.Printf("failed to read gzip reader: %s", err)
		return BitMap{}, err
	}

	err = json.Unmarshal(inner, &dummy)
	if err != nil {
		return BitMap{}, err
	}

	return BitMap{size: dummy.Size, vals: dummy.Vals}, nil

}
