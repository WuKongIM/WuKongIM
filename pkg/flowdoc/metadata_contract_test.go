package flowdoc

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMetadataAcceptsTheClosedPortableContract(t *testing.T) {
	for _, content := range []string{
		"---\nscope: package\nsummary: Package-local navigation.\n---\n",
		"---\r\nscope: subtree\r\nsummary: Recursive navigation.\r\n---\r\n",
	} {
		metadata, err := ParseMetadata([]byte(content))
		if err != nil {
			t.Fatalf("ParseMetadata(): %v", err)
		}
		if metadata.Summary == "" || (metadata.Scope != ScopePackage && metadata.Scope != ScopeSubtree) {
			t.Fatalf("metadata = %+v", metadata)
		}
	}
	if !printableASCII("A printable summary ~ 123") || printableASCII("contains\ttab") || printableASCII("包含中文") {
		t.Fatal("printableASCII() did not enforce portable discovery text")
	}
}

func TestParseMetadataRejectsAmbiguousOrUnboundedFrontMatter(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{name: "invalid utf8", content: []byte{0xff}, want: "UTF-8"},
		{name: "missing front matter", content: []byte("# pkg Flow\n"), want: errFrontMatterMissing.Error()},
		{name: "unclosed", content: []byte("---\nscope: package\nsummary: Valid\n"), want: "not closed"},
		{name: "blank field line", content: []byte("---\nscope: package\n\nsummary: Valid\n---\n"), want: "blank line"},
		{name: "no colon", content: []byte("---\nscope package\nsummary: Valid\n---\n"), want: "malformed"},
		{name: "empty key", content: []byte("---\n: package\nsummary: Valid\n---\n"), want: "malformed"},
		{name: "empty value", content: []byte("---\nscope:\nsummary: Valid\n---\n"), want: "malformed"},
		{name: "missing space", content: []byte("---\nscope:package\nsummary: Valid\n---\n"), want: "malformed"},
		{name: "unknown field", content: []byte("---\nscope: package\nsummary: Valid\nowner: team\n---\n"), want: "unknown field"},
		{name: "duplicate field", content: []byte("---\nscope: package\nscope: subtree\nsummary: Valid\n---\n"), want: "duplicate field"},
		{name: "missing summary", content: []byte("---\nscope: package\n---\n"), want: "scope and summary"},
		{name: "unknown scope", content: []byte("---\nscope: repository\nsummary: Valid\n---\n"), want: "package or subtree"},
		{name: "empty summary", content: []byte("---\nscope: package\nsummary:  \n---\n"), want: "printable ASCII"},
		{name: "long summary", content: []byte("---\nscope: package\nsummary: " + strings.Repeat("x", 161) + "\n---\n"), want: "printable ASCII"},
		{name: "non ascii summary", content: []byte("---\nscope: package\nsummary: 导航\n---\n"), want: "printable ASCII"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseMetadata(test.content)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseMetadata() error = %v, want %q", err, test.want)
			}
			if test.name == "missing front matter" && !errors.Is(err, errFrontMatterMissing) {
				t.Fatalf("missing error = %v, want sentinel", err)
			}
		})
	}
}
