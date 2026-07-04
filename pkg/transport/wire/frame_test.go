package wire

import (
	"bytes"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

func TestHeaderRoundTrip(t *testing.T) {
	header := Header{
		Kind:      core.FrameKindRPCRequest,
		Priority:  core.PriorityRPC,
		ServiceID: 42,
		RequestID: 99,
		BodyLen:   1234,
	}

	encoded := EncodeHeader(header)
	decoded, err := DecodeHeader(encoded[:], 4096)
	if err != nil {
		t.Fatalf("DecodeHeader() error = %v", err)
	}

	if decoded != header {
		t.Fatalf("decoded header = %+v, want %+v", decoded, header)
	}
	if reserved := encoded[20:24]; !bytes.Equal(reserved, []byte{0, 0, 0, 0}) {
		t.Fatalf("reserved bytes = %v, want zero", reserved)
	}
}

func TestDecodeHeaderRejectsInvalidMagic(t *testing.T) {
	encoded := EncodeHeader(Header{
		Kind:     core.FrameKindData,
		Priority: core.PriorityRaft,
	})
	encoded[0] = 0
	encoded[1] = 0

	_, err := DecodeHeader(encoded[:], 1024)
	if !errors.Is(err, core.ErrInvalidFrame) {
		t.Fatalf("DecodeHeader() error = %v, want ErrInvalidFrame", err)
	}
}

func TestDecodeHeaderRejectsShortHeader(t *testing.T) {
	_, err := DecodeHeader(make([]byte, HeaderSize-1), 1024)
	if !errors.Is(err, core.ErrInvalidFrame) {
		t.Fatalf("DecodeHeader() error = %v, want ErrInvalidFrame", err)
	}
}

func TestDecodeHeaderRejectsInvalidVersionFlagsReservedAndKind(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*[HeaderSize]byte)
	}{
		{
			name: "version",
			mutate: func(encoded *[HeaderSize]byte) {
				encoded[2] = 2
			},
		},
		{
			name: "flags",
			mutate: func(encoded *[HeaderSize]byte) {
				encoded[3] = 1
			},
		},
		{
			name: "reserved",
			mutate: func(encoded *[HeaderSize]byte) {
				encoded[23] = 1
			},
		},
		{
			name: "kind",
			mutate: func(encoded *[HeaderSize]byte) {
				encoded[4] = 99
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodeHeader(Header{
				Kind:     core.FrameKindData,
				Priority: core.PriorityRaft,
			})
			tt.mutate(&encoded)

			_, err := DecodeHeader(encoded[:], 1024)
			if !errors.Is(err, core.ErrInvalidFrame) {
				t.Fatalf("DecodeHeader() error = %v, want ErrInvalidFrame", err)
			}
		})
	}
}

func TestDecodeHeaderRejectsOversizedBody(t *testing.T) {
	encoded := EncodeHeader(Header{
		Kind:     core.FrameKindData,
		Priority: core.PriorityRaft,
		BodyLen:  1025,
	})

	_, err := DecodeHeader(encoded[:], 1024)
	if !errors.Is(err, core.ErrMsgTooLarge) {
		t.Fatalf("DecodeHeader() error = %v, want ErrMsgTooLarge", err)
	}
}

func TestDecodeHeaderRejectsInvalidPriority(t *testing.T) {
	encoded := EncodeHeader(Header{
		Kind:     core.FrameKindData,
		Priority: core.Priority(99),
	})

	_, err := DecodeHeader(encoded[:], 1024)
	if !errors.Is(err, core.ErrInvalidPriority) {
		t.Fatalf("DecodeHeader() error = %v, want ErrInvalidPriority", err)
	}
}
