package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/config"
)

func TestBilingualConfigurationReferenceCoversPublicSchema(t *testing.T) {
	fields := config.SchemaFields()
	if len(fields) == 0 {
		t.Fatal("SchemaFields() is empty")
	}

	for _, name := range []string{"reference.mdx", "reference.en.mdx"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "docs-site", "content", "docs", "server", "configuration", name)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			text := string(body)
			rowCount := 0
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(line, "| `") && strings.Contains(line, " | `WK_") {
					cells := strings.Split(line, "|")
					if len(cells) != 6 {
						continue
					}
					rowCount++
					description := strings.TrimSpace(cells[4])
					if description == "" || description == "—" {
						t.Errorf("%s schema row %q has no description", name, strings.TrimSpace(cells[1]))
					}
				}
			}
			if rowCount != len(fields) {
				t.Errorf("%s contains %d schema rows, want %d", name, rowCount, len(fields))
			}
			for _, field := range fields {
				rowPrefix := fmt.Sprintf("| `%s` | `%s` | `%s` |", field.TOMLPath, field.EnvKey, field.Kind)
				if count := strings.Count(text, rowPrefix); count != 1 {
					t.Errorf("%s contains schema row %q %d times, want 1", name, rowPrefix, count)
				}
			}
		})
	}
}
