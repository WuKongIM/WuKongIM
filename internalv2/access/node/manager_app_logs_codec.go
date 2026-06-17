package node

import (
	"encoding/json"
	"fmt"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

var (
	managerAppLogRequestMagic  = [...]byte{'W', 'K', 'V', 'G', 1}
	managerAppLogResponseMagic = [...]byte{'W', 'K', 'V', 'g', 1}
)

const (
	managerAppLogOpSources = "sources"
	managerAppLogOpEntries = "entries"

	managerAppLogOpSourcesID byte = iota + 1
	managerAppLogOpEntriesID

	maxManagerAppLogRPCCollectionLen = 2048
)

type managerAppLogRPCRequest struct {
	Op      string
	NodeID  uint64
	Source  string
	Limit   int
	Cursor  string
	Keyword string
	Levels  []string
}

type managerAppLogRPCResponse struct {
	Status  string
	Sources managementusecase.ApplicationLogSourcesResponse
	Entries managementusecase.ApplicationLogEntriesResponse
}

func encodeManagerAppLogRequest(req managerAppLogRPCRequest) ([]byte, error) {
	opID, err := managerAppLogOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, 128)
	dst = append(dst, managerAppLogRequestMagic[:]...)
	dst = append(dst, opID)
	dst = appendUvarint(dst, req.NodeID)
	dst = appendString(dst, req.Source)
	dst = appendVarint(dst, int64(req.Limit))
	dst = appendString(dst, req.Cursor)
	dst = appendString(dst, req.Keyword)
	dst = appendStringSlice(dst, req.Levels)
	return dst, nil
}

func decodeManagerAppLogRequest(body []byte) (managerAppLogRPCRequest, error) {
	if !hasMagic(body, managerAppLogRequestMagic[:]) {
		return managerAppLogRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager app log request codec")
	}
	offset := len(managerAppLogRequestMagic)
	opID, offset, err := readByte(body, offset, "manager app log op")
	if err != nil {
		return managerAppLogRPCRequest{}, err
	}
	op, err := managerAppLogOpFromID(opID)
	if err != nil {
		return managerAppLogRPCRequest{}, err
	}
	var req managerAppLogRPCRequest
	req.Op = op
	if req.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return managerAppLogRPCRequest{}, err
	}
	if req.Source, offset, err = readString(body, offset); err != nil {
		return managerAppLogRPCRequest{}, err
	}
	limit, offset, err := readVarint(body, offset)
	if err != nil {
		return managerAppLogRPCRequest{}, err
	}
	req.Limit = int(limit)
	if req.Cursor, offset, err = readString(body, offset); err != nil {
		return managerAppLogRPCRequest{}, err
	}
	if req.Keyword, offset, err = readString(body, offset); err != nil {
		return managerAppLogRPCRequest{}, err
	}
	if req.Levels, offset, err = readStringSlice(body, offset, "manager app log levels"); err != nil {
		return managerAppLogRPCRequest{}, err
	}
	if len(req.Levels) > maxManagerAppLogRPCCollectionLen {
		return managerAppLogRPCRequest{}, fmt.Errorf("internalv2/access/node: too many manager app log levels: %d", len(req.Levels))
	}
	if offset != len(body) {
		return managerAppLogRPCRequest{}, fmt.Errorf("internalv2/access/node: trailing manager app log request bytes")
	}
	return req, nil
}

func encodeManagerAppLogResponse(resp managerAppLogRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, 256)
	dst = append(dst, managerAppLogResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendApplicationLogSourcesPage(dst, resp.Sources)
	dst = appendApplicationLogEntriesPage(dst, resp.Entries)
	return dst, nil
}

func decodeManagerAppLogResponse(body []byte) (managerAppLogRPCResponse, error) {
	if !hasMagic(body, managerAppLogResponseMagic[:]) {
		return managerAppLogRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager app log response codec")
	}
	offset := len(managerAppLogResponseMagic)
	var resp managerAppLogRPCResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return managerAppLogRPCResponse{}, err
	}
	if resp.Sources, offset, err = readApplicationLogSourcesPage(body, offset); err != nil {
		return managerAppLogRPCResponse{}, err
	}
	if resp.Entries, offset, err = readApplicationLogEntriesPage(body, offset); err != nil {
		return managerAppLogRPCResponse{}, err
	}
	if offset != len(body) {
		return managerAppLogRPCResponse{}, fmt.Errorf("internalv2/access/node: trailing manager app log response bytes")
	}
	return resp, nil
}

func appendApplicationLogSourcesPage(dst []byte, page managementusecase.ApplicationLogSourcesResponse) []byte {
	dst = appendUvarint(dst, page.NodeID)
	dst = appendUvarint(dst, uint64(len(page.Sources)))
	for _, source := range page.Sources {
		dst = appendString(dst, source.Name)
		dst = appendString(dst, source.File)
		dst = appendBoolByte(dst, source.Available)
		dst = appendVarint(dst, source.SizeBytes)
		dst = appendVarint(dst, source.ModifiedAt.UnixNano())
	}
	return dst
}

func readApplicationLogSourcesPage(body []byte, offset int) (managementusecase.ApplicationLogSourcesResponse, int, error) {
	var page managementusecase.ApplicationLogSourcesResponse
	var err error
	if page.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	n, offset, err := readUvarint(body, offset)
	if err != nil {
		return page, offset, err
	}
	if n > maxManagerAppLogRPCCollectionLen {
		return page, offset, fmt.Errorf("internalv2/access/node: too many manager app log sources: %d", n)
	}
	page.Sources = make([]managementusecase.ApplicationLogSource, 0, n)
	for i := uint64(0); i < n; i++ {
		var source managementusecase.ApplicationLogSource
		var modifiedAt int64
		if source.Name, offset, err = readString(body, offset); err != nil {
			return page, offset, err
		}
		if source.File, offset, err = readString(body, offset); err != nil {
			return page, offset, err
		}
		if source.Available, offset, err = readBoolByte(body, offset, "manager app log source available"); err != nil {
			return page, offset, err
		}
		if source.SizeBytes, offset, err = readVarint(body, offset); err != nil {
			return page, offset, err
		}
		if modifiedAt, offset, err = readVarint(body, offset); err != nil {
			return page, offset, err
		}
		source.ModifiedAt = time.Unix(0, modifiedAt).UTC()
		page.Sources = append(page.Sources, source)
	}
	return page, offset, nil
}

func appendApplicationLogEntriesPage(dst []byte, page managementusecase.ApplicationLogEntriesResponse) []byte {
	dst = appendUvarint(dst, page.NodeID)
	dst = appendString(dst, page.Source)
	dst = appendString(dst, page.Cursor)
	dst = appendBoolByte(dst, page.Rotated)
	dst = appendUvarint(dst, uint64(len(page.Items)))
	for _, item := range page.Items {
		dst = appendApplicationLogEntry(dst, item)
	}
	return dst
}

func readApplicationLogEntriesPage(body []byte, offset int) (managementusecase.ApplicationLogEntriesResponse, int, error) {
	var page managementusecase.ApplicationLogEntriesResponse
	var err error
	if page.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.Source, offset, err = readString(body, offset); err != nil {
		return page, offset, err
	}
	if page.Cursor, offset, err = readString(body, offset); err != nil {
		return page, offset, err
	}
	if page.Rotated, offset, err = readBoolByte(body, offset, "manager app log entries rotated"); err != nil {
		return page, offset, err
	}
	n, offset, err := readUvarint(body, offset)
	if err != nil {
		return page, offset, err
	}
	if n > maxManagerAppLogRPCCollectionLen {
		return page, offset, fmt.Errorf("internalv2/access/node: too many manager app log entries: %d", n)
	}
	page.Items = make([]managementusecase.ApplicationLogEntry, 0, n)
	for i := uint64(0); i < n; i++ {
		var item managementusecase.ApplicationLogEntry
		if item, offset, err = readApplicationLogEntry(body, offset); err != nil {
			return page, offset, err
		}
		page.Items = append(page.Items, item)
	}
	return page, offset, nil
}

func appendApplicationLogEntry(dst []byte, item managementusecase.ApplicationLogEntry) []byte {
	dst = appendUvarint(dst, item.Seq)
	dst = appendUvarint(dst, item.Offset)
	dst = appendVarint(dst, item.Time.UnixNano())
	dst = appendString(dst, item.Level)
	dst = appendString(dst, item.Module)
	dst = appendString(dst, item.Caller)
	dst = appendString(dst, item.Message)
	fields, _ := json.Marshal(item.Fields)
	dst = appendBytes(dst, fields)
	dst = appendString(dst, item.Raw)
	return appendBoolByte(dst, item.Truncated)
}

func readApplicationLogEntry(body []byte, offset int) (managementusecase.ApplicationLogEntry, int, error) {
	var item managementusecase.ApplicationLogEntry
	var timestamp int64
	var fields []byte
	var err error
	if item.Seq, offset, err = readUvarint(body, offset); err != nil {
		return item, offset, err
	}
	if item.Offset, offset, err = readUvarint(body, offset); err != nil {
		return item, offset, err
	}
	if timestamp, offset, err = readVarint(body, offset); err != nil {
		return item, offset, err
	}
	item.Time = time.Unix(0, timestamp).UTC()
	if item.Level, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.Module, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.Caller, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.Message, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if fields, offset, err = readBytes(body, offset); err != nil {
		return item, offset, err
	}
	if len(fields) > 0 {
		if err := json.Unmarshal(fields, &item.Fields); err != nil {
			return item, offset, err
		}
	}
	if item.Raw, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.Truncated, offset, err = readBoolByte(body, offset, "manager app log entry truncated"); err != nil {
		return item, offset, err
	}
	return item, offset, nil
}

func managerAppLogOpID(op string) (byte, error) {
	switch op {
	case managerAppLogOpSources:
		return managerAppLogOpSourcesID, nil
	case managerAppLogOpEntries:
		return managerAppLogOpEntriesID, nil
	default:
		return 0, fmt.Errorf("internalv2/access/node: unknown manager app log op %q", op)
	}
}

func managerAppLogOpFromID(opID byte) (string, error) {
	switch opID {
	case managerAppLogOpSourcesID:
		return managerAppLogOpSources, nil
	case managerAppLogOpEntriesID:
		return managerAppLogOpEntries, nil
	default:
		return "", fmt.Errorf("internalv2/access/node: unknown manager app log op id %d", opID)
	}
}
