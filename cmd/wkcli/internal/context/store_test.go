package contextcmd

import (
	"reflect"
	"testing"
)

func TestStoreSavesListsLoadsAndRemovesContext(t *testing.T) {
	store := NewStore(t.TempDir())
	ctx := Context{
		Name:        "dev",
		Description: "local cluster",
		Servers:     []string{"http://127.0.0.1:5001", "http://127.0.0.1:5002"},
	}

	if err := store.Save(ctx); err != nil {
		t.Fatalf("save context: %v", err)
	}
	if err := store.Select("dev"); err != nil {
		t.Fatalf("select context: %v", err)
	}

	loaded, err := store.Load("dev")
	if err != nil {
		t.Fatalf("load context: %v", err)
	}
	if !reflect.DeepEqual(loaded, ctx) {
		t.Fatalf("loaded context = %#v, want %#v", loaded, ctx)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatalf("current context: %v", err)
	}
	if current != "dev" {
		t.Fatalf("current = %q, want dev", current)
	}
	contexts, err := store.List()
	if err != nil {
		t.Fatalf("list contexts: %v", err)
	}
	if len(contexts) != 1 || contexts[0].Name != "dev" {
		t.Fatalf("contexts = %#v, want dev only", contexts)
	}

	removedCurrent, err := store.Remove("dev")
	if err != nil {
		t.Fatalf("remove context: %v", err)
	}
	if !removedCurrent {
		t.Fatalf("expected removing selected context to report current removal")
	}
	current, err = store.Current()
	if err != nil {
		t.Fatalf("current after remove: %v", err)
	}
	if current != "" {
		t.Fatalf("current after remove = %q, want empty", current)
	}
}

func TestParseServerValuesSplitsAndTrimsRepeatedFlags(t *testing.T) {
	servers := parseServerValues([]string{
		"http://127.0.0.1:5001, http://127.0.0.1:5002",
		"http://127.0.0.1:5003",
	})

	want := []string{
		"http://127.0.0.1:5001",
		"http://127.0.0.1:5002",
		"http://127.0.0.1:5003",
	}
	if !reflect.DeepEqual(servers, want) {
		t.Fatalf("servers = %#v, want %#v", servers, want)
	}
}

func TestValidateContextRejectsUnsafeNameAndMissingServer(t *testing.T) {
	for _, ctx := range []Context{
		{Name: "../dev", Servers: []string{"http://127.0.0.1:5001"}},
		{Name: "dev", Servers: nil},
		{Name: "dev", Servers: []string{"127.0.0.1:5001"}},
	} {
		if err := validateContext(ctx); err == nil {
			t.Fatalf("expected validation error for %#v", ctx)
		}
	}
}
