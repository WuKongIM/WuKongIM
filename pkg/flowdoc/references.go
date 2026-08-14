package flowdoc

import (
	"errors"
	"net/url"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// LocalReferences returns repository-local Markdown link destinations while
// ignoring external URLs and same-document anchors.
func LocalReferences(content []byte) ([]string, error) {
	document := goldmark.DefaultParser().Parse(text.NewReader(content))
	var result []string
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var destination []byte
		switch current := node.(type) {
		case *ast.Link:
			destination = current.Destination
		case *ast.Image:
			destination = current.Destination
		default:
			return ast.WalkContinue, nil
		}
		reference := strings.TrimSpace(string(destination))
		if reference == "" || strings.HasPrefix(reference, "#") {
			return ast.WalkContinue, nil
		}
		parsed, err := url.Parse(reference)
		if err != nil {
			return ast.WalkStop, errors.New("FLOW contains an invalid Markdown reference")
		}
		if parsed.Scheme != "" || parsed.Host != "" || parsed.Path == "" {
			return ast.WalkContinue, nil
		}
		path, err := url.PathUnescape(parsed.Path)
		if err != nil || path == "" {
			return ast.WalkStop, errors.New("FLOW contains an invalid local reference")
		}
		result = append(result, path)
		return ast.WalkContinue, nil
	})
	return result, err
}

// ReadFirstReferences returns local links from the required Read First
// section. ValidateBody must succeed before callers use this helper.
func ReadFirstReferences(content []byte) ([]string, error) {
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	start := -1
	end := len(lines)
	for index, line := range lines {
		if line == "## Read First" {
			start = index + 1
			continue
		}
		if start >= 0 && strings.HasPrefix(line, "## ") {
			end = index
			break
		}
	}
	if start < 0 {
		return nil, errors.New("Read First section is missing")
	}
	return LocalReferences([]byte(strings.Join(lines[start:end], "\n")))
}
