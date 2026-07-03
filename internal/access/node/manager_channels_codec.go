package node

import (
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

var (
	managerChannelRequestMagic  = [...]byte{'W', 'K', 'V', 'H', 1}
	managerChannelResponseMagic = [...]byte{'W', 'K', 'V', 'h', 1}
)

const maxManagerChannelRPCCollectionLen = 4096

type managerChannelRPCRequest struct {
	NodeID     uint64
	Limit      int
	TypeFilter int64
	Keyword    string
	Cursor     managementusecase.ChannelListCursor
}

type managerChannelRPCResponse struct {
	Status string
	Page   managementusecase.ListBusinessChannelsResponse
}

func encodeManagerChannelRequest(req managerChannelRPCRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, managerChannelRequestMagic[:]...)
	dst = appendUvarint(dst, req.NodeID)
	dst = appendVarint(dst, int64(req.Limit))
	dst = appendVarint(dst, req.TypeFilter)
	dst = appendString(dst, req.Keyword)
	return appendManagerChannelCursor(dst, req.Cursor), nil
}

func decodeManagerChannelRequest(body []byte) (managerChannelRPCRequest, error) {
	if !hasMagic(body, managerChannelRequestMagic[:]) {
		return managerChannelRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager channel request codec")
	}
	offset := len(managerChannelRequestMagic)
	nodeID, offset, err := readUvarint(body, offset)
	if err != nil {
		return managerChannelRPCRequest{}, err
	}
	limit, offset, err := readVarint(body, offset)
	if err != nil {
		return managerChannelRPCRequest{}, err
	}
	typeFilter, offset, err := readVarint(body, offset)
	if err != nil {
		return managerChannelRPCRequest{}, err
	}
	keyword, offset, err := readString(body, offset)
	if err != nil {
		return managerChannelRPCRequest{}, err
	}
	cursor, offset, err := readManagerChannelCursor(body, offset)
	if err != nil {
		return managerChannelRPCRequest{}, err
	}
	if offset != len(body) {
		return managerChannelRPCRequest{}, fmt.Errorf("internalv2/access/node: trailing manager channel request bytes")
	}
	return managerChannelRPCRequest{
		NodeID:     nodeID,
		Limit:      int(limit),
		TypeFilter: typeFilter,
		Keyword:    keyword,
		Cursor:     cursor,
	}, nil
}

func encodeManagerChannelResponse(resp managerChannelRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, 256)
	dst = append(dst, managerChannelResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	return appendManagerBusinessChannelPage(dst, resp.Page), nil
}

func decodeManagerChannelResponse(body []byte) (managerChannelRPCResponse, error) {
	if !hasMagic(body, managerChannelResponseMagic[:]) {
		return managerChannelRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager channel response codec")
	}
	offset := len(managerChannelResponseMagic)
	status, offset, err := readString(body, offset)
	if err != nil {
		return managerChannelRPCResponse{}, err
	}
	page, offset, err := readManagerBusinessChannelPage(body, offset)
	if err != nil {
		return managerChannelRPCResponse{}, err
	}
	if offset != len(body) {
		return managerChannelRPCResponse{}, fmt.Errorf("internalv2/access/node: trailing manager channel response bytes")
	}
	return managerChannelRPCResponse{Status: status, Page: page}, nil
}

func appendManagerBusinessChannelPage(dst []byte, page managementusecase.ListBusinessChannelsResponse) []byte {
	dst = appendManagerBusinessChannels(dst, page.Items)
	dst = appendBoolByte(dst, page.HasMore)
	return appendManagerChannelCursor(dst, page.NextCursor)
}

func readManagerBusinessChannelPage(body []byte, offset int) (managementusecase.ListBusinessChannelsResponse, int, error) {
	items, offset, err := readManagerBusinessChannels(body, offset)
	if err != nil {
		return managementusecase.ListBusinessChannelsResponse{}, offset, err
	}
	hasMore, offset, err := readBoolByte(body, offset, "manager channel has_more")
	if err != nil {
		return managementusecase.ListBusinessChannelsResponse{}, offset, err
	}
	cursor, offset, err := readManagerChannelCursor(body, offset)
	if err != nil {
		return managementusecase.ListBusinessChannelsResponse{}, offset, err
	}
	return managementusecase.ListBusinessChannelsResponse{Items: items, HasMore: hasMore, NextCursor: cursor}, offset, nil
}

func appendManagerBusinessChannels(dst []byte, items []managementusecase.BusinessChannelListItem) []byte {
	dst = appendUvarint(dst, uint64(len(items)))
	for _, item := range items {
		dst = appendString(dst, item.ChannelID)
		dst = appendVarint(dst, item.ChannelType)
		dst = appendUvarint(dst, uint64(item.SlotID))
		dst = appendUvarint(dst, uint64(item.HashSlot))
		dst = appendBoolByte(dst, item.Ban)
		dst = appendBoolByte(dst, item.Disband)
		dst = appendBoolByte(dst, item.SendBan)
		dst = appendUvarint(dst, item.SubscriberMutationVersion)
	}
	return dst
}

func readManagerBusinessChannels(body []byte, offset int) ([]managementusecase.BusinessChannelListItem, int, error) {
	n, offset, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if n > maxManagerChannelRPCCollectionLen {
		return nil, offset, fmt.Errorf("internalv2/access/node: too many manager channels: %d", n)
	}
	items := make([]managementusecase.BusinessChannelListItem, 0, n)
	for i := uint64(0); i < n; i++ {
		var item managementusecase.BusinessChannelListItem
		var unsigned uint64
		if item.ChannelID, offset, err = readString(body, offset); err != nil {
			return nil, offset, err
		}
		if item.ChannelType, offset, err = readVarint(body, offset); err != nil {
			return nil, offset, err
		}
		if unsigned, offset, err = readUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		item.SlotID = uint32(unsigned)
		if unsigned, offset, err = readUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		item.HashSlot = uint16(unsigned)
		if item.Ban, offset, err = readBoolByte(body, offset, "manager channel ban"); err != nil {
			return nil, offset, err
		}
		if item.Disband, offset, err = readBoolByte(body, offset, "manager channel disband"); err != nil {
			return nil, offset, err
		}
		if item.SendBan, offset, err = readBoolByte(body, offset, "manager channel send_ban"); err != nil {
			return nil, offset, err
		}
		if item.SubscriberMutationVersion, offset, err = readUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		items = append(items, item)
	}
	return items, offset, nil
}

func appendManagerChannelCursor(dst []byte, cursor managementusecase.ChannelListCursor) []byte {
	dst = appendUvarint(dst, uint64(cursor.SlotID))
	dst = appendString(dst, cursor.ChannelID)
	dst = appendVarint(dst, cursor.ChannelType)
	dst = appendVarint(dst, cursor.TypeFilter)
	return appendUvarint(dst, uint64(cursor.KeywordHash))
}

func readManagerChannelCursor(body []byte, offset int) (managementusecase.ChannelListCursor, int, error) {
	var cursor managementusecase.ChannelListCursor
	var unsigned uint64
	var err error
	if unsigned, offset, err = readUvarint(body, offset); err != nil {
		return cursor, offset, err
	}
	cursor.SlotID = uint32(unsigned)
	if cursor.ChannelID, offset, err = readString(body, offset); err != nil {
		return cursor, offset, err
	}
	if cursor.ChannelType, offset, err = readVarint(body, offset); err != nil {
		return cursor, offset, err
	}
	if cursor.TypeFilter, offset, err = readVarint(body, offset); err != nil {
		return cursor, offset, err
	}
	if unsigned, offset, err = readUvarint(body, offset); err != nil {
		return cursor, offset, err
	}
	cursor.KeywordHash = uint32(unsigned)
	return cursor, offset, nil
}

func appendBoolByte(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readBoolByte(body []byte, offset int, label string) (bool, int, error) {
	value, next, err := readByte(body, offset, label)
	if err != nil {
		return false, offset, err
	}
	switch value {
	case 0:
		return false, next, nil
	case 1:
		return true, next, nil
	default:
		return false, offset, fmt.Errorf("internalv2/access/node: invalid %s bool byte %d", label, value)
	}
}
