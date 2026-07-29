package backup_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"sync"
	"testing"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestFullExporterWritesVerifiableSlotArtifact(t *testing.T) {
	store := newMemoryArchiveStore()
	source := &fakeFullSlotSource{streams: []runtimebackup.FullSlotStream{
		{
			Kind:    backupartifact.ChunkKindMetadata,
			Reader:  io.NopCloser(bytes.NewReader([]byte("metadata-snapshot"))),
			Records: 3,
		},
		{
			Kind:         backupartifact.ChunkKindMessages,
			Reader:       io.NopCloser(bytes.NewReader([]byte("message-snapshot-a"))),
			Records:      2,
			MaxMessageID: 91,
		},
		{
			Kind:         backupartifact.ChunkKindMessages,
			Reader:       io.NopCloser(bytes.NewReader([]byte("message-snapshot-b"))),
			Records:      4,
			MaxMessageID: 99,
		},
	}}
	exporter, err := runtimebackup.NewFullExporter(runtimebackup.FullExporterOptions{
		Store: store, Source: source, TempDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewFullExporter(): %v", err)
	}

	reference, err := exporter.ExportSlot(context.Background(), "backup-1", 7)
	if err != nil {
		t.Fatalf("ExportSlot(): %v", err)
	}
	if reference.Records != 9 || reference.MaxMessageID != 99 {
		t.Fatalf("reference = %#v", reference)
	}
	manifestBody := store.body("backups/backup-1/" + reference.ManifestKey)
	manifest, err := backupartifact.LoadSlotManifest(manifestBody)
	if err != nil {
		t.Fatalf("LoadSlotManifest(): %v", err)
	}
	if len(manifest.Chunks) != 3 ||
		manifest.Chunks[0].Stream != 0 ||
		manifest.Chunks[1].Stream != 1 ||
		manifest.Chunks[2].Stream != 2 {
		t.Fatalf("chunks = %#v", manifest.Chunks)
	}
	want := [][]byte{
		[]byte("metadata-snapshot"),
		[]byte("message-snapshot-a"),
		[]byte("message-snapshot-b"),
	}
	for index, chunk := range manifest.Chunks {
		var decoded bytes.Buffer
		if err := backupartifact.DecodeChunk(
			&decoded,
			bytes.NewReader(store.body("backups/backup-1/"+chunk.Key)),
			chunk.Descriptor,
		); err != nil {
			t.Fatalf("DecodeChunk(%d): %v", index, err)
		}
		if !bytes.Equal(decoded.Bytes(), want[index]) {
			t.Fatalf("decoded[%d] = %q", index, decoded.Bytes())
		}
	}
}

type fakeFullSlotSource struct {
	streams []runtimebackup.FullSlotStream
}

func (s *fakeFullSlotSource) OpenFullSlot(
	context.Context,
	uint16,
) (runtimebackup.FullSlotCapture, error) {
	streams := make([]runtimebackup.FullSlotStream, len(s.streams))
	for index, stream := range s.streams {
		body, err := io.ReadAll(stream.Reader)
		if err != nil {
			return nil, err
		}
		stream.Reader = io.NopCloser(bytes.NewReader(body))
		streams[index] = stream
	}
	return &fakeFullSlotCapture{streams: streams}, nil
}

type fakeFullSlotCapture struct {
	streams []runtimebackup.FullSlotStream
	index   int
}

func (c *fakeFullSlotCapture) Cut() backupartifact.SlotCut {
	return backupartifact.SlotCut{
		PhysicalSlotID: 1, LeaderTerm: 2, AppliedTerm: 2,
		ConfigurationVersion: 3,
		AppliedIndex:         4, CapturedAtUnixMillis: 1_800_000_000_000,
	}
}

func (c *fakeFullSlotCapture) Next(
	context.Context,
) (runtimebackup.FullSlotStream, error) {
	if c.index == len(c.streams) {
		return runtimebackup.FullSlotStream{}, io.EOF
	}
	stream := c.streams[c.index]
	c.index++
	return stream, nil
}

func (c *fakeFullSlotCapture) Close() error { return nil }

type memoryArchiveStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMemoryArchiveStore() *memoryArchiveStore {
	return &memoryArchiveStore{objects: map[string][]byte{}}
}

func (s *memoryArchiveStore) Put(
	_ context.Context,
	object backupartifact.PutObject,
) error {
	body, err := io.ReadAll(object.Body)
	if err != nil {
		return err
	}
	if uint64(len(body)) != object.ExpectedBytes {
		return errors.New("size mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if object.IfAbsent {
		if _, exists := s.objects[object.Key]; exists {
			return backupartifact.ErrObjectExists
		}
	}
	s.objects[object.Key] = append([]byte(nil), body...)
	return nil
}

func (s *memoryArchiveStore) Open(
	_ context.Context,
	key string,
) (io.ReadCloser, backupartifact.ArchiveObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, exists := s.objects[key]
	if !exists {
		return nil, backupartifact.ArchiveObject{}, backupartifact.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(body)), backupartifact.ArchiveObject{
		Key: key, Bytes: uint64(len(body)),
	}, nil
}

func (s *memoryArchiveStore) List(
	_ context.Context,
	prefix string,
) ([]backupartifact.ArchiveObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var objects []backupartifact.ArchiveObject
	for key, body := range s.objects {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			objects = append(objects, backupartifact.ArchiveObject{
				Key: key, Bytes: uint64(len(body)),
			})
		}
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	return objects, nil
}

func (s *memoryArchiveStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *memoryArchiveStore) DeletePrefix(
	_ context.Context,
	prefix string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.objects {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(s.objects, key)
		}
	}
	return nil
}

func (s *memoryArchiveStore) body(key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.objects[key]...)
}
