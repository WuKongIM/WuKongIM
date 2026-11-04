package fsm

import metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"

// replayFormat1Policy distinguishes legacy Channel commands by the missing
// policy-version tag. New deletions carry that tag too, even with value zero.
func replayFormat1Policy(wb *metadb.WriteBatch, hashSlot uint16, decoded command, raw []byte) error {
	var ch metadb.Channel
	var conditional *metadb.ChannelConditionalMutationResult
	deleted := false
	switch c := decoded.(type) {
	case *applyDeltaCmd:
		original, err := decodeCommand(c.OriginalCmd)
		if err != nil {
			return err
		}
		return replayFormat1Policy(wb, hashSlot, original, c.OriginalCmd)
	case *upsertChannelCmd:
		ch = c.channel
	case *createChannelCmd:
		ch = c.channel
		conditional = c.result
	case *patchChannelBusinessFlagsCmd:
		ch = metadb.Channel{ChannelID: c.channelID, ChannelType: c.channelType, SendBan: c.flags.SendBan}
		conditional = c.result
	case *deleteChannelCmd:
		ch = metadb.Channel{ChannelID: c.channelID, ChannelType: c.channelType}
		deleted = true
	default:
		return nil
	}
	for off := headerSize; off < len(raw); {
		tag, _, n, err := readTLV(raw[off:])
		if err != nil {
			return err
		}
		off += n
		if tag == tagChannelSendBanVersion {
			return nil
		}
	}
	return wb.ReplayFormat1ChannelPolicy(hashSlot, ch, conditional, deleted)
}
