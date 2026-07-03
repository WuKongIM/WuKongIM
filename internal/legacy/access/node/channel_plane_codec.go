package node

import (
	"fmt"

	runtimechannelplane "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/channelplane"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

var (
	channelPlaneAppendBatchesRequestMagic  = [...]byte{'W', 'K', 'C', 'P', 1}
	channelPlaneAppendBatchesResponseMagic = [...]byte{'W', 'K', 'C', 'Q', 1}
)

func encodeChannelPlaneAppendBatchesRequest(req runtimechannelplane.AppendBatchesRequest) ([]byte, error) {
	dst := make([]byte, 0, len(channelPlaneAppendBatchesRequestMagic)+128)
	dst = append(dst, channelPlaneAppendBatchesRequestMagic[:]...)
	dst = appendUvarint(dst, uint64(len(req.Batches)))
	for _, batch := range req.Batches {
		dst = appendRouteEpoch(dst, batch.RouteEpoch)
		dst = appendChannelAppendBatchRequest(dst, batch.Request)
	}
	return dst, nil
}

func decodeChannelPlaneAppendBatchesRequest(body []byte) (runtimechannelplane.AppendBatchesRequest, error) {
	if !hasMagic(body, channelPlaneAppendBatchesRequestMagic[:]) {
		return runtimechannelplane.AppendBatchesRequest{}, fmt.Errorf("access/node: invalid channel plane append batches request codec")
	}
	offset := len(channelPlaneAppendBatchesRequestMagic)
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return runtimechannelplane.AppendBatchesRequest{}, err
	}
	offset = next
	countLen, err := readCollectionLen(count, len(body)-offset, "channel plane append batches")
	if err != nil {
		return runtimechannelplane.AppendBatchesRequest{}, err
	}
	req := runtimechannelplane.AppendBatchesRequest{Batches: make([]runtimechannelplane.AppendBatchEnvelope, countLen)}
	for i := range req.Batches {
		if req.Batches[i].RouteEpoch, offset, err = readRouteEpoch(body, offset); err != nil {
			return runtimechannelplane.AppendBatchesRequest{}, err
		}
		if req.Batches[i].Request, offset, err = readChannelAppendBatchRequest(body, offset); err != nil {
			return runtimechannelplane.AppendBatchesRequest{}, err
		}
	}
	if offset != len(body) {
		return runtimechannelplane.AppendBatchesRequest{}, fmt.Errorf("access/node: trailing channel plane append batches request bytes")
	}
	return req, nil
}

func encodeChannelPlaneAppendBatchesResponse(resp runtimechannelplane.AppendBatchesResponse) ([]byte, error) {
	dst := make([]byte, 0, len(channelPlaneAppendBatchesResponseMagic)+128)
	dst = append(dst, channelPlaneAppendBatchesResponseMagic[:]...)
	dst = appendUvarint(dst, uint64(len(resp.Results)))
	for _, result := range resp.Results {
		dst = appendString(dst, result.Status)
		dst = appendUvarint(dst, uint64(result.Leader))
		dst = appendChannelAppendBatchResult(dst, result.Result)
	}
	return dst, nil
}

func decodeChannelPlaneAppendBatchesResponse(body []byte) (runtimechannelplane.AppendBatchesResponse, error) {
	if !hasMagic(body, channelPlaneAppendBatchesResponseMagic[:]) {
		return runtimechannelplane.AppendBatchesResponse{}, fmt.Errorf("access/node: invalid channel plane append batches response codec")
	}
	offset := len(channelPlaneAppendBatchesResponseMagic)
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return runtimechannelplane.AppendBatchesResponse{}, err
	}
	offset = next
	countLen, err := readCollectionLen(count, len(body)-offset, "channel plane append batch results")
	if err != nil {
		return runtimechannelplane.AppendBatchesResponse{}, err
	}
	resp := runtimechannelplane.AppendBatchesResponse{Results: make([]runtimechannelplane.AppendBatchRemoteResult, countLen)}
	for i := range resp.Results {
		if resp.Results[i].Status, offset, err = readString(body, offset); err != nil {
			return runtimechannelplane.AppendBatchesResponse{}, err
		}
		var leader uint64
		if leader, offset, err = readUvarint(body, offset); err != nil {
			return runtimechannelplane.AppendBatchesResponse{}, err
		}
		resp.Results[i].Leader = channel.NodeID(leader)
		if resp.Results[i].Result, offset, err = readChannelAppendBatchResult(body, offset); err != nil {
			return runtimechannelplane.AppendBatchesResponse{}, err
		}
	}
	if offset != len(body) {
		return runtimechannelplane.AppendBatchesResponse{}, fmt.Errorf("access/node: trailing channel plane append batches response bytes")
	}
	return resp, nil
}

func appendRouteEpoch(dst []byte, epoch runtimechannelplane.RouteEpoch) []byte {
	dst = appendUvarint(dst, epoch.RouteGeneration)
	dst = appendUvarint(dst, epoch.ChannelEpoch)
	return appendUvarint(dst, epoch.LeaderEpoch)
}

func readRouteEpoch(body []byte, offset int) (runtimechannelplane.RouteEpoch, int, error) {
	var epoch runtimechannelplane.RouteEpoch
	var err error
	if epoch.RouteGeneration, offset, err = readUvarint(body, offset); err != nil {
		return runtimechannelplane.RouteEpoch{}, offset, err
	}
	if epoch.ChannelEpoch, offset, err = readUvarint(body, offset); err != nil {
		return runtimechannelplane.RouteEpoch{}, offset, err
	}
	if epoch.LeaderEpoch, offset, err = readUvarint(body, offset); err != nil {
		return runtimechannelplane.RouteEpoch{}, offset, err
	}
	return epoch, offset, nil
}
