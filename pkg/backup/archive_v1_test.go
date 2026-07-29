package backup_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestArchiveManifestV1RoundTripRequiresEveryHashSlot(t *testing.T) {
	manifest := validArchiveManifest()

	body, err := backup.MarshalArchiveManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalArchiveManifest(): %v", err)
	}
	decoded, err := backup.LoadArchiveManifest(body)
	if err != nil {
		t.Fatalf("LoadArchiveManifest(): %v", err)
	}
	if decoded.ID != manifest.ID || len(decoded.Slots) != backup.DefaultHashSlotCount {
		t.Fatalf("decoded manifest = %#v", decoded)
	}

	incomplete := manifest
	incomplete.Slots = incomplete.Slots[:len(incomplete.Slots)-1]
	if _, err := backup.MarshalArchiveManifest(incomplete); err == nil {
		t.Fatal("MarshalArchiveManifest() accepted a partial archive")
	}
}

func TestArchiveManifestV1RejectsUnknownFields(t *testing.T) {
	body, err := backup.MarshalArchiveManifest(validArchiveManifest())
	if err != nil {
		t.Fatalf("MarshalArchiveManifest(): %v", err)
	}
	body = bytes.Replace(body, []byte(`"id":`), []byte(`"unexpected":true,"id":`), 1)

	if _, err := backup.LoadArchiveManifest(body); err == nil {
		t.Fatal("LoadArchiveManifest() accepted an unknown field")
	}
}

func TestCompleteMarkerBindsExactManifest(t *testing.T) {
	body, err := backup.MarshalArchiveManifest(validArchiveManifest())
	if err != nil {
		t.Fatalf("MarshalArchiveManifest(): %v", err)
	}
	marker, err := backup.NewCompleteMarker(body)
	if err != nil {
		t.Fatalf("NewCompleteMarker(): %v", err)
	}
	markerBody, err := backup.MarshalCompleteMarker(marker)
	if err != nil {
		t.Fatalf("MarshalCompleteMarker(): %v", err)
	}
	if _, err := backup.LoadCompleteMarker(markerBody, body); err != nil {
		t.Fatalf("LoadCompleteMarker(): %v", err)
	}

	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-2] ^= 1
	if _, err := backup.LoadCompleteMarker(markerBody, tampered); err == nil {
		t.Fatal("LoadCompleteMarker() accepted a different manifest")
	}
}

func TestSlotManifestV1RoundTripRequiresOrderedChunks(t *testing.T) {
	digest := strings.Repeat("a", 64)
	manifest := backup.SlotManifest{
		Format:   backup.SlotManifestFormat,
		Version:  backup.SlotManifestVersion,
		HashSlot: 7,
		Cut: backup.SlotCut{
			PhysicalSlotID:       3,
			LeaderTerm:           11,
			AppliedTerm:          10,
			ConfigurationVersion: 9,
			AppliedIndex:         42,
			CapturedAtUnixMillis: 1785267601000,
		},
		Chunks: []backup.ChunkReference{
			{
				Kind:     backup.ChunkKindMetadata,
				Sequence: 1,
				Stream:   0,
				Part:     1,
				Final:    true,
				Key:      "slots/007/meta-000001.zst",
				Descriptor: backup.ChunkDescriptor{
					StoredSHA256:  digest,
					LogicalSHA256: digest,
					LogicalBytes:  12,
					StoredBytes:   8,
					Compression:   backup.CompressionZstd,
				},
				Records: 2,
			},
			{
				Kind:     backup.ChunkKindMessages,
				Sequence: 1,
				Stream:   1,
				Part:     1,
				Final:    true,
				Key:      "slots/007/messages-000001.zst",
				Descriptor: backup.ChunkDescriptor{
					StoredSHA256:  digest,
					LogicalSHA256: digest,
					LogicalBytes:  24,
					StoredBytes:   16,
					Compression:   backup.CompressionZstd,
				},
				Records:      3,
				MaxMessageID: 99,
			},
		},
		LogicalBytes: 36,
		StoredBytes:  24,
		Records:      5,
		MaxMessageID: 99,
	}

	body, err := backup.MarshalSlotManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalSlotManifest(): %v", err)
	}
	decoded, err := backup.LoadSlotManifest(body)
	if err != nil {
		t.Fatalf("LoadSlotManifest(): %v", err)
	}
	if decoded.HashSlot != 7 || len(decoded.Chunks) != 2 {
		t.Fatalf("decoded manifest = %#v", decoded)
	}

	outOfOrder := manifest
	outOfOrder.Chunks = append([]backup.ChunkReference(nil), manifest.Chunks...)
	outOfOrder.Chunks[1].Sequence = 2
	if _, err := backup.MarshalSlotManifest(outOfOrder); err == nil {
		t.Fatal("MarshalSlotManifest() accepted a chunk sequence gap")
	}
}

func TestMessageChunkManifestRoundTripKeepsRPCReferenceBounded(t *testing.T) {
	digest := strings.Repeat("b", 64)
	chunks := []backup.ChunkReference{
		{
			Kind: backup.ChunkKindMessages, Sequence: 9, Stream: 4,
			Part: 1, Final: false,
			Key: "slots/007/attempts/00000001/messages-000009.zst",
			Descriptor: backup.ChunkDescriptor{
				StoredSHA256: digest, LogicalSHA256: digest,
				LogicalBytes: 64, StoredBytes: 32,
				Compression: backup.CompressionZstd,
			},
			Records: 3, MaxMessageID: 100,
		},
		{
			Kind: backup.ChunkKindMessages, Sequence: 10, Stream: 4,
			Part: 2, Final: true,
			Key: "slots/007/attempts/00000001/messages-000010.zst",
			Descriptor: backup.ChunkDescriptor{
				StoredSHA256: digest, LogicalSHA256: digest,
				LogicalBytes: 12, StoredBytes: 8,
				Compression: backup.CompressionZstd,
			},
		},
	}
	manifest, err := backup.NewMessageChunkManifest(7, chunks)
	if err != nil {
		t.Fatalf("NewMessageChunkManifest(): %v", err)
	}
	body, err := backup.MarshalMessageChunkManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalMessageChunkManifest(): %v", err)
	}
	loaded, err := backup.LoadMessageChunkManifest(body)
	if err != nil {
		t.Fatalf("LoadMessageChunkManifest(): %v", err)
	}
	if len(loaded.Chunks) != 2 || loaded.LogicalBytes != 76 ||
		loaded.StoredBytes != 40 || loaded.Records != 3 ||
		loaded.MaxMessageID != 100 {
		t.Fatalf("loaded manifest = %#v", loaded)
	}
}

func TestChunkV1StreamsCompressedContentAndRejectsCorruption(t *testing.T) {
	plaintext := []byte(strings.Repeat("wukongim-full-backup\n", 4096))
	var encoded bytes.Buffer
	descriptor, err := backup.EncodeChunk(&encoded, bytes.NewReader(plaintext))
	if err != nil {
		t.Fatalf("EncodeChunk(): %v", err)
	}
	if descriptor.LogicalBytes != uint64(len(plaintext)) {
		t.Fatalf("LogicalBytes = %d, want %d", descriptor.LogicalBytes, len(plaintext))
	}
	if descriptor.StoredBytes >= descriptor.LogicalBytes {
		t.Fatalf("StoredBytes = %d, want smaller than %d", descriptor.StoredBytes, descriptor.LogicalBytes)
	}

	var decoded bytes.Buffer
	if err := backup.DecodeChunk(&decoded, bytes.NewReader(encoded.Bytes()), descriptor); err != nil {
		t.Fatalf("DecodeChunk(): %v", err)
	}
	if !bytes.Equal(decoded.Bytes(), plaintext) {
		t.Fatal("decoded chunk does not match plaintext")
	}

	corrupt := append([]byte(nil), encoded.Bytes()...)
	corrupt[len(corrupt)/2] ^= 0xff
	if err := backup.DecodeChunk(&bytes.Buffer{}, bytes.NewReader(corrupt), descriptor); err == nil {
		t.Fatal("DecodeChunk() accepted corrupt content")
	}
}

func validArchiveManifest() backup.ArchiveManifest {
	slots := make([]backup.SlotReference, backup.DefaultHashSlotCount)
	for slot := range slots {
		sum := sha256.Sum256([]byte{byte(slot), byte(slot >> 8)})
		slots[slot] = backup.SlotReference{
			HashSlot:       uint16(slot),
			ManifestKey:    "slots/" + leftPadSlot(slot) + "/manifest.json",
			ManifestSHA256: hex.EncodeToString(sum[:]),
			LogicalBytes:   uint64(slot + 1),
			StoredBytes:    uint64(slot + 1),
			Records:        uint64(slot + 1),
		}
	}
	return backup.ArchiveManifest{
		Format:                backup.ArchiveFormat,
		Version:               backup.ArchiveVersion,
		ID:                    "bk_20260729_010000_01",
		Trigger:               backup.TriggerScheduled,
		SourceClusterID:       "cluster-1",
		SourceApplication:     "wukongim-test",
		HashSlotCount:         backup.DefaultHashSlotCount,
		StartedAtUnixMillis:   1785267600000,
		CompletedAtUnixMillis: 1785267660000,
		CutStartedUnixMillis:  1785267601000,
		CutEndedUnixMillis:    1785267602000,
		Compression:           backup.CompressionZstd,
		Checksum:              backup.ChecksumSHA256,
		Slots:                 slots,
	}
}

func leftPadSlot(slot int) string {
	if slot < 10 {
		return "00" + string(rune('0'+slot))
	}
	if slot < 100 {
		return "0" + string(rune('0'+slot/10)) + string(rune('0'+slot%10))
	}
	return string(rune('0'+slot/100)) +
		string(rune('0'+(slot/10)%10)) +
		string(rune('0'+slot%10))
}
