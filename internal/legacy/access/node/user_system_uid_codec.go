package node

import "fmt"

var (
	systemUIDCacheRequestMagic  = [...]byte{'W', 'K', 'U', 'S', 1}
	systemUIDCacheResponseMagic = [...]byte{'W', 'K', 'U', 'T', 1}
)

func encodeSystemUIDCacheRequest(req systemUIDCacheRequest) ([]byte, error) {
	dst := make([]byte, 0, len(systemUIDCacheRequestMagic)+16+len(req.UIDs)*16)
	dst = append(dst, systemUIDCacheRequestMagic[:]...)
	dst = appendString(dst, req.Op)
	dst = appendStrings(dst, req.UIDs)
	return dst, nil
}

func decodeSystemUIDCacheRequest(body []byte) (systemUIDCacheRequest, error) {
	if !hasMagic(body, systemUIDCacheRequestMagic[:]) {
		return systemUIDCacheRequest{}, fmt.Errorf("access/node: invalid system uid cache request codec")
	}
	offset := len(systemUIDCacheRequestMagic)
	op, next, err := readString(body, offset)
	if err != nil {
		return systemUIDCacheRequest{}, err
	}
	uids, next, err := readStrings(body, next)
	if err != nil {
		return systemUIDCacheRequest{}, err
	}
	if next != len(body) {
		return systemUIDCacheRequest{}, fmt.Errorf("access/node: trailing system uid cache request bytes")
	}
	return systemUIDCacheRequest{Op: op, UIDs: uids}, nil
}

func encodeSystemUIDCacheResponse(resp systemUIDCacheResponse) ([]byte, error) {
	dst := make([]byte, 0, len(systemUIDCacheResponseMagic)+16)
	dst = append(dst, systemUIDCacheResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	return dst, nil
}

func decodeSystemUIDCacheResponse(body []byte) (systemUIDCacheResponse, error) {
	if !hasMagic(body, systemUIDCacheResponseMagic[:]) {
		return systemUIDCacheResponse{}, fmt.Errorf("access/node: invalid system uid cache response codec")
	}
	offset := len(systemUIDCacheResponseMagic)
	status, next, err := readString(body, offset)
	if err != nil {
		return systemUIDCacheResponse{}, err
	}
	if next != len(body) {
		return systemUIDCacheResponse{}, fmt.Errorf("access/node: trailing system uid cache response bytes")
	}
	return systemUIDCacheResponse{Status: status}, nil
}
