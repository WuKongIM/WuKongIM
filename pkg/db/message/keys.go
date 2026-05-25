package message

import "github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"

func channelPartitionID(key ChannelKey) []byte {
	return keycodec.AppendString(nil, string(key))
}

func encodeMessageRowPrefix(channelKey ChannelKey) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionChannel, channelPartitionID(channelKey)).
		Row(TableIDMessage).
		Key()
}

func encodeMessageRowKey(channelKey ChannelKey, seq uint64, familyID uint16) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionChannel, channelPartitionID(channelKey)).
		Row(TableIDMessage).
		Uint64(seq).
		Family(familyID).
		Key()
}

func encodeMessageIndexPrefix(channelKey ChannelKey, indexID uint16) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionChannel, channelPartitionID(channelKey)).
		Index(TableIDMessage, indexID).
		Key()
}

func encodeMessageSystemPrefix(channelKey ChannelKey, systemID uint16) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionChannel, channelPartitionID(channelKey)).
		System(TableIDMessage, systemID).
		Key()
}

func encodeCatalogKey(channelKey ChannelKey) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMessage).
		Partition(keycodec.PartitionGlobal, nil).
		Catalog().
		String(string(channelKey)).
		Key()
}
