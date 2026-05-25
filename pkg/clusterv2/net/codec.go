package clusternet

import "fmt"

// PutHeader appends a clusterv2 version/kind header to buf.
func PutHeader(buf []byte, version uint8, kind uint8) []byte {
	return append(buf, version, kind)
}

// CheckHeader validates a clusterv2 version/kind header and returns the payload.
func CheckHeader(data []byte, wantVersion uint8, wantKind uint8) ([]byte, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: short frame", ErrInvalidFrame)
	}
	if data[0] != wantVersion {
		return nil, fmt.Errorf("%w: version %d", ErrInvalidFrame, data[0])
	}
	if data[1] != wantKind {
		return nil, fmt.Errorf("%w: kind %d", ErrInvalidFrame, data[1])
	}
	return data[2:], nil
}
