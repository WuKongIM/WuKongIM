package channelid

import (
	"errors"
	"testing"
)

func TestDecodePersonChannelRejectsAmbiguousOrIncompleteIDs(t *testing.T) {
	left, right, err := DecodePersonChannel("alice@bob")
	if err != nil || left != "alice" || right != "bob" {
		t.Fatalf("DecodePersonChannel() = (%q, %q, %v), want (alice, bob, nil)", left, right, err)
	}

	for _, channelID := range []string{"", "alice", "@bob", "alice@", "alice@bob@carol"} {
		if _, _, err := DecodePersonChannel(channelID); !errors.Is(err, ErrInvalidPersonChannel) {
			t.Errorf("DecodePersonChannel(%q) error = %v, want ErrInvalidPersonChannel", channelID, err)
		}
	}
}

func TestNormalizePersonChannelRequiresSenderMembership(t *testing.T) {
	canonical := EncodePersonChannel("alice", "bob")
	tests := []struct {
		name      string
		senderUID string
		channelID string
		want      string
		wantErr   bool
	}{
		{name: "direct recipient", senderUID: "alice", channelID: "bob", want: canonical},
		{name: "canonical pair", senderUID: "alice", channelID: canonical, want: canonical},
		{name: "empty sender", channelID: "bob", wantErr: true},
		{name: "empty channel", senderUID: "alice", wantErr: true},
		{name: "malformed pair", senderUID: "alice", channelID: "alice@", wantErr: true},
		{name: "sender is not participant", senderUID: "carol", channelID: canonical, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePersonChannel(tt.senderUID, tt.channelID)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidPersonChannel) {
					t.Fatalf("NormalizePersonChannel() error = %v, want ErrInvalidPersonChannel", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("NormalizePersonChannel() = (%q, %v), want (%q, nil)", got, err, tt.want)
			}
		})
	}
}
