package messageevents

import "testing"

func TestMessageCommittedCloneDeepCopiesDeliveryFields(t *testing.T) {
	original := MessageCommitted{
		MessageID:         1,
		MessageSeq:        2,
		ChannelID:         "g1",
		ChannelType:       2,
		FromUID:           "u1",
		SenderNodeID:      7,
		SenderSessionID:   42,
		ClientMsgNo:       "client-1",
		ServerTimestampMS: 1234,
		Payload:           []byte("hello"),
		RedDot:            true,
		MessageScopedUIDs: []string{"u2", "u3"},
	}

	cloned := original.Clone()
	original.Payload[0] = 'H'
	original.MessageScopedUIDs[0] = "mutated"

	if got := string(cloned.Payload); got != "hello" {
		t.Fatalf("cloned payload = %q, want hello", got)
	}
	if got := cloned.MessageScopedUIDs[0]; got != "u2" {
		t.Fatalf("cloned scoped uid = %q, want u2", got)
	}
	if cloned.SenderSessionID != 42 {
		t.Fatalf("SenderSessionID = %d, want 42", cloned.SenderSessionID)
	}
	if cloned.SenderNodeID != 7 {
		t.Fatalf("SenderNodeID = %d, want 7", cloned.SenderNodeID)
	}
	if cloned.ServerTimestampMS != 1234 {
		t.Fatalf("ServerTimestampMS = %d, want 1234", cloned.ServerTimestampMS)
	}
	if !cloned.RedDot {
		t.Fatalf("RedDot = false, want true")
	}
}
