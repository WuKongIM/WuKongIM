package node

import "fmt"

var (
	connectionsRequestMagic  = [...]byte{'W', 'K', 'C', 'L', 1}
	connectionsResponseMagic = [...]byte{'W', 'K', 'C', 'M', 1}
	connectionRequestMagic   = [...]byte{'W', 'K', 'C', 'D', 1}
	connectionResponseMagic  = [...]byte{'W', 'K', 'C', 'E', 1}
)

func encodeConnectionsRequestBinary(req connectionsRequest) ([]byte, error) {
	dst := make([]byte, 0, len(connectionsRequestMagic)+10)
	dst = append(dst, connectionsRequestMagic[:]...)
	dst = appendUvarint(dst, req.NodeID)
	return dst, nil
}

func decodeConnectionsRequest(body []byte) (connectionsRequest, error) {
	if !hasMagic(body, connectionsRequestMagic[:]) {
		return connectionsRequest{}, fmt.Errorf("access/node: invalid connections request codec")
	}
	offset := len(connectionsRequestMagic)
	nodeID, next, err := readUvarint(body, offset)
	if err != nil {
		return connectionsRequest{}, err
	}
	if next != len(body) {
		return connectionsRequest{}, fmt.Errorf("access/node: trailing connections request bytes")
	}
	return connectionsRequest{NodeID: nodeID}, nil
}

func encodeConnectionRequestBinary(req connectionRequest) ([]byte, error) {
	dst := make([]byte, 0, len(connectionRequestMagic)+20)
	dst = append(dst, connectionRequestMagic[:]...)
	dst = appendUvarint(dst, req.NodeID)
	dst = appendUvarint(dst, req.SessionID)
	return dst, nil
}

func decodeConnectionRequest(body []byte) (connectionRequest, error) {
	if !hasMagic(body, connectionRequestMagic[:]) {
		return connectionRequest{}, fmt.Errorf("access/node: invalid connection request codec")
	}
	offset := len(connectionRequestMagic)
	nodeID, next, err := readUvarint(body, offset)
	if err != nil {
		return connectionRequest{}, err
	}
	sessionID, next, err := readUvarint(body, next)
	if err != nil {
		return connectionRequest{}, err
	}
	if next != len(body) {
		return connectionRequest{}, fmt.Errorf("access/node: trailing connection request bytes")
	}
	return connectionRequest{NodeID: nodeID, SessionID: sessionID}, nil
}

func encodeConnectionsResponse(resp connectionsResponse) ([]byte, error) {
	dst := make([]byte, 0, len(connectionsResponseMagic)+32+len(resp.Items)*96)
	dst = append(dst, connectionsResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendUvarint(dst, uint64(len(resp.Items)))
	for _, item := range resp.Items {
		dst = appendConnection(dst, item)
	}
	return dst, nil
}

func decodeConnectionsResponse(body []byte) (connectionsResponse, error) {
	if !hasMagic(body, connectionsResponseMagic[:]) {
		return connectionsResponse{}, fmt.Errorf("access/node: invalid connections response codec")
	}
	offset := len(connectionsResponseMagic)
	status, next, err := readString(body, offset)
	if err != nil {
		return connectionsResponse{}, err
	}
	count, next, err := readUvarint(body, next)
	if err != nil {
		return connectionsResponse{}, err
	}
	offset = next
	itemsLen, err := readCollectionLen(count, len(body)-offset, "connections")
	if err != nil {
		return connectionsResponse{}, err
	}
	items := make([]Connection, itemsLen)
	for i := range items {
		if items[i], offset, err = readConnection(body, offset); err != nil {
			return connectionsResponse{}, err
		}
	}
	if offset != len(body) {
		return connectionsResponse{}, fmt.Errorf("access/node: trailing connections response bytes")
	}
	return connectionsResponse{Status: status, Items: items}, nil
}

func encodeConnectionResponse(resp connectionResponse) ([]byte, error) {
	dst := make([]byte, 0, len(connectionResponseMagic)+128)
	dst = append(dst, connectionResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendConnection(dst, resp.Item)
	return dst, nil
}

func decodeConnectionResponse(body []byte) (connectionResponse, error) {
	if !hasMagic(body, connectionResponseMagic[:]) {
		return connectionResponse{}, fmt.Errorf("access/node: invalid connection response codec")
	}
	offset := len(connectionResponseMagic)
	status, next, err := readString(body, offset)
	if err != nil {
		return connectionResponse{}, err
	}
	item, next, err := readConnection(body, next)
	if err != nil {
		return connectionResponse{}, err
	}
	if next != len(body) {
		return connectionResponse{}, fmt.Errorf("access/node: trailing connection response bytes")
	}
	return connectionResponse{Status: status, Item: item}, nil
}

func appendConnection(dst []byte, item Connection) []byte {
	dst = appendUvarint(dst, item.NodeID)
	dst = appendUvarint(dst, item.SessionID)
	dst = appendString(dst, item.UID)
	dst = appendString(dst, item.DeviceID)
	dst = appendString(dst, item.DeviceFlag)
	dst = appendString(dst, item.DeviceLevel)
	dst = appendUvarint(dst, item.SlotID)
	dst = appendString(dst, item.State)
	dst = appendString(dst, item.Listener)
	dst = appendUvarint(dst, connectionUnixNano(item.ConnectedAt))
	dst = appendString(dst, item.RemoteAddr)
	dst = appendString(dst, item.LocalAddr)
	return dst
}

func readConnection(body []byte, offset int) (Connection, int, error) {
	var item Connection
	var err error
	if item.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return Connection{}, offset, err
	}
	if item.SessionID, offset, err = readUvarint(body, offset); err != nil {
		return Connection{}, offset, err
	}
	if item.UID, offset, err = readString(body, offset); err != nil {
		return Connection{}, offset, err
	}
	if item.DeviceID, offset, err = readString(body, offset); err != nil {
		return Connection{}, offset, err
	}
	if item.DeviceFlag, offset, err = readString(body, offset); err != nil {
		return Connection{}, offset, err
	}
	if item.DeviceLevel, offset, err = readString(body, offset); err != nil {
		return Connection{}, offset, err
	}
	if item.SlotID, offset, err = readUvarint(body, offset); err != nil {
		return Connection{}, offset, err
	}
	if item.State, offset, err = readString(body, offset); err != nil {
		return Connection{}, offset, err
	}
	if item.Listener, offset, err = readString(body, offset); err != nil {
		return Connection{}, offset, err
	}
	var connectedAt uint64
	if connectedAt, offset, err = readUvarint(body, offset); err != nil {
		return Connection{}, offset, err
	}
	item.ConnectedAt = connectionTimeFromUnixNano(connectedAt)
	if item.RemoteAddr, offset, err = readString(body, offset); err != nil {
		return Connection{}, offset, err
	}
	if item.LocalAddr, offset, err = readString(body, offset); err != nil {
		return Connection{}, offset, err
	}
	return item, offset, nil
}
