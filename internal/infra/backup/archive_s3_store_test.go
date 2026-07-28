package backup

import (
	"bytes"
	"context"
	"io"
	"sort"
	"testing"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestS3ArchiveStoreKeepsKeysInsideConfiguredPrefix(t *testing.T) {
	api := &memoryS3ArchiveAPI{objects: map[string][]byte{}}
	store := newS3ArchiveStore(api, "tenant/cluster-a")
	body := []byte("archive")

	if err := store.Put(context.Background(), backupartifact.PutObject{
		Key: "backups/backup-1/COMPLETE", Body: bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)), IfAbsent: true,
	}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if _, exists := api.objects["tenant/cluster-a/backups/backup-1/COMPLETE"]; !exists {
		t.Fatalf("objects = %#v", api.objects)
	}
	items, err := store.List(context.Background(), "backups")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(items) != 1 || items[0].Key != "backups/backup-1/COMPLETE" {
		t.Fatalf("items = %#v", items)
	}
}

func TestParseS3EndpointRejectsEmbeddedPathOrCredentials(t *testing.T) {
	for _, value := range []string{
		"https://user:secret@s3.example.com",
		"https://s3.example.com/path",
		"ftp://s3.example.com",
	} {
		if _, _, err := parseS3Endpoint(value); err == nil {
			t.Fatalf("parseS3Endpoint(%q) error = nil", value)
		}
	}
}

func TestS3ArchiveStoreDeletePrefixDoesNotMatchSiblingPrefix(t *testing.T) {
	api := &memoryS3ArchiveAPI{objects: map[string][]byte{
		"root/backups/abc/manifest.json":  []byte("abc"),
		"root/backups/abc2/manifest.json": []byte("abc2"),
	}}
	store := newS3ArchiveStore(api, "root")
	if err := store.DeletePrefix(context.Background(), "backups/abc"); err != nil {
		t.Fatalf("DeletePrefix(): %v", err)
	}
	if _, exists := api.objects["root/backups/abc/manifest.json"]; exists {
		t.Fatal("target subtree remains")
	}
	if _, exists := api.objects["root/backups/abc2/manifest.json"]; !exists {
		t.Fatal("sibling prefix was deleted")
	}
}

type memoryS3ArchiveAPI struct {
	objects map[string][]byte
}

func (m *memoryS3ArchiveAPI) put(
	_ context.Context,
	key string,
	body io.Reader,
	expected uint64,
	ifAbsent bool,
) error {
	if ifAbsent {
		if _, exists := m.objects[key]; exists {
			return backupartifact.ErrObjectExists
		}
	}
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if uint64(len(value)) != expected {
		return backupartifact.ErrInvalidObject
	}
	m.objects[key] = value
	return nil
}

func (m *memoryS3ArchiveAPI) open(
	_ context.Context,
	key string,
) (io.ReadCloser, s3ArchiveObject, error) {
	value, exists := m.objects[key]
	if !exists {
		return nil, s3ArchiveObject{}, backupartifact.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(value)), s3ArchiveObject{
		key: key, bytes: uint64(len(value)),
	}, nil
}

func (m *memoryS3ArchiveAPI) list(
	_ context.Context,
	prefix string,
) ([]s3ArchiveObject, error) {
	result := make([]s3ArchiveObject, 0)
	for key, value := range m.objects {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			result = append(result, s3ArchiveObject{
				key: key, bytes: uint64(len(value)),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].key < result[j].key })
	return result, nil
}

func (m *memoryS3ArchiveAPI) remove(_ context.Context, key string) error {
	delete(m.objects, key)
	return nil
}
