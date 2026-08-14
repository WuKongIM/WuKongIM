// Package flowdoc parses the closed metadata shared by FLOW navigation files
// and Agent context discovery.
package flowdoc

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var errFrontMatterMissing = errors.New("FLOW front matter is missing")

// Scope controls whether a FLOW applies only to its directory-local package
// or to that complete directory subtree.
type Scope string

const (
	ScopePackage Scope = "package"
	ScopeSubtree Scope = "subtree"
)

// Metadata is the closed FLOW front-matter contract. Legacy is true only when
// transition compatibility supplied the historical subtree behavior.
type Metadata struct {
	// Scope selects package-local or recursive subtree applicability.
	Scope Scope
	// Summary is the 1-160 byte printable-ASCII discovery description.
	Summary string
	// Legacy records temporary front-matter-free subtree compatibility.
	Legacy bool
}

// ParseMetadata reads the closed two-field FLOW front matter. When
// allowLegacy is true, a file without front matter retains the historical
// subtree scope until repository migration is complete.
func ParseMetadata(content []byte, allowLegacy bool) (Metadata, error) {
	lines, closing, err := splitFrontMatter(content)
	if err != nil {
		if allowLegacy && errors.Is(err, errFrontMatterMissing) {
			return Metadata{Scope: ScopeSubtree, Legacy: true}, nil
		}
		return Metadata{}, err
	}

	values := make(map[string]string, 2)
	for _, line := range lines[1:closing] {
		if line == "" {
			return Metadata{}, errors.New("FLOW front matter contains a blank line")
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || value == "" || value[0] != ' ' {
			return Metadata{}, errors.New("FLOW front matter field is malformed")
		}
		value = strings.TrimSpace(value)
		if key != "scope" && key != "summary" {
			return Metadata{}, errors.New("FLOW front matter contains an unknown field")
		}
		if _, duplicate := values[key]; duplicate {
			return Metadata{}, errors.New("FLOW front matter contains a duplicate field")
		}
		values[key] = value
	}
	if len(values) != 2 {
		return Metadata{}, errors.New("FLOW front matter must contain scope and summary")
	}

	metadata := Metadata{Scope: Scope(values["scope"]), Summary: values["summary"]}
	if metadata.Scope != ScopePackage && metadata.Scope != ScopeSubtree {
		return Metadata{}, errors.New("FLOW scope must be package or subtree")
	}
	if len(metadata.Summary) == 0 || len(metadata.Summary) > 160 ||
		metadata.Summary != strings.TrimSpace(metadata.Summary) ||
		!printableASCII(metadata.Summary) {
		return Metadata{}, errors.New("FLOW summary must be 1-160 printable ASCII bytes")
	}
	return metadata, nil
}

func splitFrontMatter(content []byte) ([]string, int, error) {
	if !utf8.Valid(content) {
		return nil, 0, errors.New("FLOW content is not valid UTF-8")
	}
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return nil, 0, errFrontMatterMissing
	}
	lines := strings.Split(normalized, "\n")
	for index := 1; index < len(lines); index++ {
		if lines[index] == "---" {
			return lines, index, nil
		}
	}
	return nil, 0, errors.New("FLOW front matter is not closed")
}

func printableASCII(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] > 0x7e {
			return false
		}
	}
	return true
}
