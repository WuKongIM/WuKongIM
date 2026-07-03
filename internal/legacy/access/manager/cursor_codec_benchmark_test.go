package manager

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
)

func BenchmarkManagerMessageCursorCodec(b *testing.B) {
	cursor := managementusecase.MessageListCursor{BeforeSeq: 123456789}
	binaryCursor, err := encodeMessageCursor(cursor)
	if err != nil {
		b.Fatalf("encodeMessageCursor() error = %v", err)
	}
	legacyCursor := mustEncodeLegacyMessageCursorForBenchmark(b, cursor)

	b.Run("binary_encode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := encodeMessageCursor(cursor); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("binary_decode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := decodeMessageCursor(binaryCursor); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("legacy_json_encode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = mustEncodeLegacyMessageCursorForBenchmark(b, cursor)
		}
	})
	b.Run("legacy_json_decode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := decodeMessageCursor(legacyCursor); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkManagerChannelRuntimeMetaCursorCodec(b *testing.B) {
	cursor := managementusecase.ChannelRuntimeMetaListCursor{SlotID: 123, ChannelID: "group-123456789", ChannelType: 2}
	binaryCursor, err := encodeChannelRuntimeMetaCursor(cursor)
	if err != nil {
		b.Fatalf("encodeChannelRuntimeMetaCursor() error = %v", err)
	}
	legacyCursor := mustEncodeLegacyChannelRuntimeMetaCursorForBenchmark(b, cursor)

	b.Run("binary_encode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := encodeChannelRuntimeMetaCursor(cursor); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("binary_decode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := decodeChannelRuntimeMetaCursor(binaryCursor); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("legacy_json_encode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = mustEncodeLegacyChannelRuntimeMetaCursorForBenchmark(b, cursor)
		}
	})
	b.Run("legacy_json_decode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := decodeChannelRuntimeMetaCursor(legacyCursor); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func mustEncodeLegacyMessageCursorForBenchmark(b *testing.B, cursor managementusecase.MessageListCursor) string {
	b.Helper()
	payload, err := json.Marshal(messageCursorPayload{Version: 1, BeforeSeq: cursor.BeforeSeq})
	if err != nil {
		b.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func mustEncodeLegacyChannelRuntimeMetaCursorForBenchmark(b *testing.B, cursor managementusecase.ChannelRuntimeMetaListCursor) string {
	b.Helper()
	payload, err := json.Marshal(channelRuntimeMetaCursorPayload{Version: 1, SlotID: cursor.SlotID, ChannelID: cursor.ChannelID, ChannelType: cursor.ChannelType})
	if err != nil {
		b.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}
