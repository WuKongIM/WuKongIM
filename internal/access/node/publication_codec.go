package node

import "github.com/WuKongIM/WuKongIM/pkg/protocol/publication"

// readPublicationBytes bounds and validates before taking ownership.
func readPublicationBytes(body []byte, offset int) ([]byte, int, error) {
	size, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if size > publication.MaxEncodedBytes || size > uint64(len(body)-next) {
		return nil, offset, publication.ErrTooLarge
	}
	if size == 0 {
		return nil, next, nil
	}
	end := next + int(size)
	value := body[next:end]
	if _, err := publication.Decode(value); err != nil {
		return nil, offset, err
	}
	return append([]byte(nil), value...), end, nil
}
