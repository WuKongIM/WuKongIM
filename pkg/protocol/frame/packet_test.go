package frame

import "testing"

func TestSendPacketUniqueKeyAndRecvReset(t *testing.T) {
	send := &SendPacket{
		ChannelID:   "channel-1",
		ChannelType: 2,
		ClientMsgNo: "msg-123",
		ClientSeq:   42,
	}

	if got, want := send.UniqueKey(), "channel-1-2-msg-123-42"; got != want {
		t.Fatalf("SendPacket.UniqueKey() = %q, want %q", got, want)
	}

	recv := &RecvPacket{
		Framer: Framer{
			FrameType:        RECV,
			RemainingLength:  12,
			NoPersist:        true,
			RedDot:           true,
			SyncOnce:         true,
			DUP:              true,
			HasServerVersion: true,
			End:              true,
			FrameSize:        99,
		},
		Setting:     SettingReceiptEnabled,
		MsgKey:      "msg-key",
		Expire:      10,
		MessageID:   11,
		MessageSeq:  12,
		ClientMsgNo: "client-msg",
		StreamNo:    "stream-no",
		StreamId:    13,
		StreamFlag:  StreamFlagEnd,
		Timestamp:   14,
		ChannelID:   "channel-2",
		ChannelType: 3,
		Topic:       "topic",
		FromUID:     "from",
		Payload:     []byte("payload"),
		ClientSeq:   15,
	}

	recv.Reset()

	if recv.Framer.GetFrameType() != UNKNOWN {
		t.Fatalf("RecvPacket.Reset() FrameType = %v, want %v", recv.Framer.GetFrameType(), UNKNOWN)
	}
	if recv.Framer.GetRemainingLength() != 0 || recv.Framer.GetNoPersist() || recv.Framer.GetRedDot() || recv.Framer.GetsyncOnce() || recv.Framer.GetDUP() || recv.Framer.GetHasServerVersion() || recv.Framer.GetEnd() || recv.Framer.GetFrameSize() != 0 {
		t.Fatal("RecvPacket.Reset() did not clear framer fields")
	}
	if recv.Setting != 0 || recv.MsgKey != "" || recv.Expire != 0 || recv.MessageID != 0 || recv.MessageSeq != 0 || recv.ClientMsgNo != "" || recv.StreamNo != "" || recv.StreamId != 0 || recv.StreamFlag != 0 || recv.Timestamp != 0 || recv.ChannelID != "" || recv.ChannelType != 0 || recv.Topic != "" || recv.FromUID != "" || recv.Payload != nil || recv.ClientSeq != 0 {
		t.Fatal("RecvPacket.Reset() did not clear packet fields")
	}
}
