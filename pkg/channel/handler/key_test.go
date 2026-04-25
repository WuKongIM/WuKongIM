package handler

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestKeyFromChannelID(t *testing.T) {
	id := channel.ChannelID{ID: "a/b:c", Type: 3}

	got := KeyFromChannelID(id)
	want := channel.ChannelKey("channel/3/YS9iOmM")
	if got != want {
		t.Fatalf("KeyFromChannelID() = %q, want %q", got, want)
	}
}

func TestParseChannelKey(t *testing.T) {
	key := channel.ChannelKey("channel/2/ZzFAeTI")

	got, err := ParseChannelKey(key)
	if err != nil {
		t.Fatalf("ParseChannelKey() error = %v", err)
	}
	want := channel.ChannelID{ID: "g1@y2", Type: 2}
	if got != want {
		t.Fatalf("ParseChannelKey() = %+v, want %+v", got, want)
	}
}

func TestParseChannelKeyRejectsInvalidFormat(t *testing.T) {
	tests := []channel.ChannelKey{
		"group-21",
		"channel/2",
		"channel/x/abc",
		"channel/2/!!!",
	}

	for _, key := range tests {
		t.Run(string(key), func(t *testing.T) {
			if _, err := ParseChannelKey(key); err != channel.ErrInvalidMeta {
				t.Fatalf("ParseChannelKey(%q) error = %v, want %v", key, err, channel.ErrInvalidMeta)
			}
		})
	}
}
