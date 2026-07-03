package node

import (
	"encoding/binary"
	"fmt"
	"time"

	deliverytagruntime "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/deliverytag"
)

var (
	deliveryTagRequestMagic  = [...]byte{'W', 'K', 'D', 'T', 1}
	deliveryTagResponseMagic = [...]byte{'W', 'K', 'D', 'G', 1}
)

// DeliveryTagRequest carries a leader-only delivery tag lookup or update request.
type DeliveryTagRequest struct {
	// Op selects the RPC behavior, for example get or update.
	Op string `json:"op"`
	// ChannelID identifies the delivery channel.
	ChannelID string `json:"channel_id"`
	// ChannelType identifies the delivery channel type.
	ChannelType uint8 `json:"channel_type"`
	// TagKey is the public tag incarnation fence.
	TagKey string `json:"tag_key,omitempty"`
	// TagVersion is the public tag version fence.
	TagVersion uint64 `json:"tag_version,omitempty"`
	// TargetNodeID requests only the local partition for the given node.
	TargetNodeID uint64 `json:"target_node_id,omitempty"`
	// Tag carries the full tag body for update-style requests.
	Tag deliverytagruntime.DeliveryTag `json:"tag,omitempty"`
}

// DeliveryTagResponse returns the leader-authoritative tag state or retry status.
type DeliveryTagResponse struct {
	// Status is ok, not_leader, retryable, or a tag-specific stale status.
	Status string `json:"status"`
	// LeaderID identifies the current leader when the request hit a follower.
	LeaderID uint64 `json:"leader_id,omitempty"`
	// Result carries the detailed tag result when the status is retryable or stale.
	Result string `json:"result,omitempty"`
	// Tag is the authoritative tag payload returned by the leader.
	Tag deliverytagruntime.DeliveryTag `json:"tag,omitempty"`
}

type deliveryTagRequest = DeliveryTagRequest
type deliveryTagResponse = DeliveryTagResponse

func encodeDeliveryTagRequestBinary(req DeliveryTagRequest) ([]byte, error) {
	dst := make([]byte, 0, len(deliveryTagRequestMagic)+len(req.ChannelID)+len(req.TagKey)+len(req.Op)+128)
	dst = append(dst, deliveryTagRequestMagic[:]...)
	dst = appendString(dst, req.Op)
	dst = appendString(dst, req.ChannelID)
	dst = append(dst, req.ChannelType)
	dst = appendString(dst, req.TagKey)
	dst = appendUvarint(dst, req.TagVersion)
	dst = appendUvarint(dst, req.TargetNodeID)
	dst = appendDeliveryTag(dst, req.Tag)
	return dst, nil
}

func decodeDeliveryTagRequest(body []byte) (deliveryTagRequest, error) {
	if !isDeliveryTagRequestBinary(body) {
		return deliveryTagRequest{}, fmt.Errorf("access/node: invalid delivery tag request codec")
	}
	req, err := decodeDeliveryTagRequestBinary(body[len(deliveryTagRequestMagic):])
	if err != nil {
		return deliveryTagRequest{}, err
	}
	return req, nil
}

func encodeDeliveryTagResponseBinary(resp DeliveryTagResponse) ([]byte, error) {
	dst := make([]byte, 0, len(deliveryTagResponseMagic)+len(resp.Status)+len(resp.Result)+128)
	dst = append(dst, deliveryTagResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendUvarint(dst, resp.LeaderID)
	dst = appendString(dst, resp.Result)
	dst = appendDeliveryTag(dst, resp.Tag)
	return dst, nil
}

func decodeDeliveryTagResponseBinary(body []byte) (deliveryTagResponse, error) {
	if !isDeliveryTagResponseBinary(body) {
		return deliveryTagResponse{}, fmt.Errorf("access/node: invalid delivery tag response codec")
	}
	resp, err := decodeDeliveryTagResponseBinaryBody(body[len(deliveryTagResponseMagic):])
	if err != nil {
		return deliveryTagResponse{}, err
	}
	return resp, nil
}

func isDeliveryTagRequestBinary(body []byte) bool {
	return hasMagic(body, deliveryTagRequestMagic[:])
}

func isDeliveryTagResponseBinary(body []byte) bool {
	return hasMagic(body, deliveryTagResponseMagic[:])
}

func decodeDeliveryTagRequestBinary(body []byte) (deliveryTagRequest, error) {
	var req deliveryTagRequest
	var err error
	offset := 0
	if req.Op, offset, err = readString(body, offset); err != nil {
		return deliveryTagRequest{}, err
	}
	if req.ChannelID, offset, err = readString(body, offset); err != nil {
		return deliveryTagRequest{}, err
	}
	if offset >= len(body) {
		return deliveryTagRequest{}, fmt.Errorf("access/node: short delivery tag channel type")
	}
	req.ChannelType = body[offset]
	offset++
	if req.TagKey, offset, err = readString(body, offset); err != nil {
		return deliveryTagRequest{}, err
	}
	if req.TagVersion, offset, err = readUvarint(body, offset); err != nil {
		return deliveryTagRequest{}, err
	}
	if req.TargetNodeID, offset, err = readUvarint(body, offset); err != nil {
		return deliveryTagRequest{}, err
	}
	if req.Tag, offset, err = readDeliveryTag(body, offset); err != nil {
		return deliveryTagRequest{}, err
	}
	if offset != len(body) {
		return deliveryTagRequest{}, fmt.Errorf("access/node: trailing delivery tag request bytes")
	}
	return req, nil
}

func decodeDeliveryTagResponseBinaryBody(body []byte) (deliveryTagResponse, error) {
	var resp deliveryTagResponse
	var err error
	offset := 0
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return deliveryTagResponse{}, err
	}
	if resp.LeaderID, offset, err = readUvarint(body, offset); err != nil {
		return deliveryTagResponse{}, err
	}
	if resp.Result, offset, err = readString(body, offset); err != nil {
		return deliveryTagResponse{}, err
	}
	if resp.Tag, offset, err = readDeliveryTag(body, offset); err != nil {
		return deliveryTagResponse{}, err
	}
	if offset != len(body) {
		return deliveryTagResponse{}, fmt.Errorf("access/node: trailing delivery tag response bytes")
	}
	return resp, nil
}

func appendDeliveryTag(dst []byte, tag deliverytagruntime.DeliveryTag) []byte {
	dst = appendString(dst, tag.Key)
	dst = appendString(dst, tag.ChannelKey)
	dst = appendUvarint(dst, tag.TagVersion)
	dst = appendUvarint(dst, tag.SubscriberMutationVersion)
	dst = appendString(dst, tag.SourceChannelKey)
	dst = appendUvarint(dst, tag.SourceSubscriberMutationVersion)
	dst = appendPartitionTopologyVersion(dst, tag.Topology)
	dst = appendNodePartitions(dst, tag.Partitions)
	dst = appendVarint(dst, unixNanoOrZero(tag.CreatedAt))
	dst = appendVarint(dst, unixNanoOrZero(tag.LastAccess))
	return dst
}

func readDeliveryTag(body []byte, offset int) (deliverytagruntime.DeliveryTag, int, error) {
	var tag deliverytagruntime.DeliveryTag
	var err error
	if tag.Key, offset, err = readString(body, offset); err != nil {
		return deliverytagruntime.DeliveryTag{}, offset, err
	}
	if tag.ChannelKey, offset, err = readString(body, offset); err != nil {
		return deliverytagruntime.DeliveryTag{}, offset, err
	}
	if tag.TagVersion, offset, err = readUvarint(body, offset); err != nil {
		return deliverytagruntime.DeliveryTag{}, offset, err
	}
	if tag.SubscriberMutationVersion, offset, err = readUvarint(body, offset); err != nil {
		return deliverytagruntime.DeliveryTag{}, offset, err
	}
	if tag.SourceChannelKey, offset, err = readString(body, offset); err != nil {
		return deliverytagruntime.DeliveryTag{}, offset, err
	}
	if tag.SourceSubscriberMutationVersion, offset, err = readUvarint(body, offset); err != nil {
		return deliverytagruntime.DeliveryTag{}, offset, err
	}
	if tag.Topology, offset, err = readPartitionTopologyVersion(body, offset); err != nil {
		return deliverytagruntime.DeliveryTag{}, offset, err
	}
	if tag.Partitions, offset, err = readNodePartitions(body, offset); err != nil {
		return deliverytagruntime.DeliveryTag{}, offset, err
	}
	var created int64
	if created, offset, err = readVarint(body, offset); err != nil {
		return deliverytagruntime.DeliveryTag{}, offset, err
	}
	if created != 0 {
		tag.CreatedAt = time.Unix(0, created)
	}
	var accessed int64
	if accessed, offset, err = readVarint(body, offset); err != nil {
		return deliverytagruntime.DeliveryTag{}, offset, err
	}
	if accessed != 0 {
		tag.LastAccess = time.Unix(0, accessed)
	}
	return tag, offset, nil
}

func appendPartitionTopologyVersion(dst []byte, version deliverytagruntime.PartitionTopologyVersion) []byte {
	dst = appendUvarint(dst, version.HashSlotTableVersion)
	dst = appendUvarint(dst, uint64(len(version.SlotAuthorityRefs)))
	for _, ref := range version.SlotAuthorityRefs {
		dst = appendUvarint(dst, uint64(ref.SlotID))
		dst = appendUvarint(dst, ref.LeaderNodeID)
		dst = appendUvarint(dst, ref.ConfigEpoch)
		dst = appendUvarint(dst, ref.BalanceVersion)
	}
	return dst
}

func readPartitionTopologyVersion(body []byte, offset int) (deliverytagruntime.PartitionTopologyVersion, int, error) {
	var version deliverytagruntime.PartitionTopologyVersion
	var err error
	if version.HashSlotTableVersion, offset, err = readUvarint(body, offset); err != nil {
		return deliverytagruntime.PartitionTopologyVersion{}, offset, err
	}
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return deliverytagruntime.PartitionTopologyVersion{}, offset, err
	}
	offset = next
	refsLen, err := readCollectionLen(count, len(body)-offset, "delivery tag topology refs")
	if err != nil {
		return deliverytagruntime.PartitionTopologyVersion{}, offset, err
	}
	version.SlotAuthorityRefs = make([]deliverytagruntime.SlotAuthorityRef, refsLen)
	for i := range version.SlotAuthorityRefs {
		var slotID uint64
		if slotID, offset, err = readUvarint(body, offset); err != nil {
			return deliverytagruntime.PartitionTopologyVersion{}, offset, err
		}
		version.SlotAuthorityRefs[i].SlotID = uint32(slotID)
		if version.SlotAuthorityRefs[i].LeaderNodeID, offset, err = readUvarint(body, offset); err != nil {
			return deliverytagruntime.PartitionTopologyVersion{}, offset, err
		}
		if version.SlotAuthorityRefs[i].ConfigEpoch, offset, err = readUvarint(body, offset); err != nil {
			return deliverytagruntime.PartitionTopologyVersion{}, offset, err
		}
		if version.SlotAuthorityRefs[i].BalanceVersion, offset, err = readUvarint(body, offset); err != nil {
			return deliverytagruntime.PartitionTopologyVersion{}, offset, err
		}
	}
	return version, offset, nil
}

func appendNodePartitions(dst []byte, partitions []deliverytagruntime.NodePartition) []byte {
	dst = appendUvarint(dst, uint64(len(partitions)))
	for _, partition := range partitions {
		dst = appendUvarint(dst, partition.NodeID)
		dst = appendUvarint(dst, uint64(len(partition.UIDs)))
		for _, uid := range partition.UIDs {
			dst = appendString(dst, uid)
		}
	}
	return dst
}

func readNodePartitions(body []byte, offset int) ([]deliverytagruntime.NodePartition, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	partitionsLen, err := readCollectionLen(count, len(body)-offset, "delivery tag partitions")
	if err != nil {
		return nil, offset, err
	}
	partitions := make([]deliverytagruntime.NodePartition, partitionsLen)
	for i := range partitions {
		if partitions[i].NodeID, offset, err = readUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		uidCount, next, err := readUvarint(body, offset)
		if err != nil {
			return nil, offset, err
		}
		offset = next
		uidLen, err := readCollectionLen(uidCount, len(body)-offset, "delivery tag partition uids")
		if err != nil {
			return nil, offset, err
		}
		partitions[i].UIDs = make([]string, uidLen)
		for j := range partitions[i].UIDs {
			if partitions[i].UIDs[j], offset, err = readString(body, offset); err != nil {
				return nil, offset, err
			}
		}
	}
	return partitions, offset, nil
}

func appendVarint(dst []byte, v int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutVarint(buf[:], v)
	return append(dst, buf[:n]...)
}

func readVarint(body []byte, offset int) (int64, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("access/node: short varint")
	}
	v, n := binary.Varint(body[offset:])
	if n <= 0 {
		return 0, offset, fmt.Errorf("access/node: invalid varint")
	}
	return v, offset + n, nil
}

func unixNanoOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}
