package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestJSONHelpersUseNeutralNames(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	dir := filepath.Dir(current)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}

	var offenders []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		text := string(body)
		for _, token := range []string{"bind" + "Leg" + "acy", "write" + "Leg" + "acy"} {
			if strings.Contains(text, token) {
				offenders = append(offenders, name+":"+token)
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("JSON helper names must use neutral prefixes: %s", strings.Join(offenders, ", "))
	}
}
