package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	goast "go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"go.yaml.in/yaml/v3"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	return runWithExecutor(args, stdout, stderr, executeFocusedCommand)
}

type focusedCommandExecutor func(
	context.Context,
	string,
	[]string,
	io.Writer,
	io.Writer,
) error

func runWithExecutor(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	executor focusedCommandExecutor,
) int {
	flags := flag.NewFlagSet("skillcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	runFocused := flags.Bool("run-focused", false, "run explicitly registered focused tests")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "skillcheck: unexpected arguments: %v\n", flags.Args())
		return 2
	}

	entries, err := os.ReadDir(filepath.Join(*root, ".agents", "skills"))
	if err != nil {
		fmt.Fprintf(stderr, ".agents/skills: %v\n", err)
		return 1
	}
	skills := 0
	var diagnostics []string
	skillNames := make(map[string]string)
	focusedTestRequired := make(map[string]struct{})
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skills++
		skillRelative := filepath.Join(".agents", "skills", entry.Name(), "SKILL.md")
		if _, err := os.Stat(filepath.Join(*root, skillRelative)); err != nil {
			if os.IsNotExist(err) {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s: required file is missing",
					filepath.ToSlash(skillRelative),
				))
			} else {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s: %v", filepath.ToSlash(skillRelative), err,
				))
			}
			continue
		}
		metadata, err := readSkillMetadata(filepath.Join(*root, skillRelative))
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: %v", filepath.ToSlash(skillRelative), err,
			))
			continue
		}
		if earlier, exists := skillNames[metadata.Name]; exists {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: frontmatter name %q duplicates %s",
				filepath.ToSlash(skillRelative), metadata.Name, earlier,
			))
		} else {
			skillNames[metadata.Name] = filepath.ToSlash(skillRelative)
		}
		if len(metadata.Name) > 64 || !skillNamePattern.MatchString(metadata.Name) {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: frontmatter name must be 1-64 lowercase letters, digits, or single hyphen-separated words",
				filepath.ToSlash(skillRelative),
			))
		}
		if metadata.Name != entry.Name() {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: frontmatter name %q must match directory %q",
				filepath.ToSlash(skillRelative), metadata.Name, entry.Name(),
			))
		}
		if strings.TrimSpace(metadata.Description) == "" {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: frontmatter description must be non-empty",
				filepath.ToSlash(skillRelative),
			))
		}
		openAIRelative := filepath.Join(
			".agents", "skills", entry.Name(), "agents", "openai.yaml",
		)
		if _, err := os.Stat(filepath.Join(*root, openAIRelative)); err != nil {
			if os.IsNotExist(err) {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s: required file is missing",
					filepath.ToSlash(openAIRelative),
				))
			} else {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s: %v", filepath.ToSlash(openAIRelative), err,
				))
			}
			continue
		}
		openAI, err := readOpenAIInterface(filepath.Join(*root, openAIRelative))
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: %v", filepath.ToSlash(openAIRelative), err,
			))
			continue
		}
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "display_name", value: openAI.Interface.DisplayName},
			{name: "short_description", value: openAI.Interface.ShortDescription},
			{name: "default_prompt", value: openAI.Interface.DefaultPrompt},
		} {
			if strings.TrimSpace(field.value) == "" {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s: interface.%s must be non-empty",
					filepath.ToSlash(openAIRelative), field.name,
				))
			}
		}
		diagnostics = append(
			diagnostics,
			validateMarkdownReferences(
				*root,
				filepath.Join(".agents", "skills", entry.Name()),
			)...,
		)
		diagnostics = append(
			diagnostics,
			validateStructuredFixtures(
				*root,
				filepath.Join(".agents", "skills", entry.Name()),
			)...,
		)
		diagnostics = append(
			diagnostics,
			validateSkillFileModes(
				*root,
				filepath.Join(".agents", "skills", entry.Name()),
			)...,
		)
		if skillNeedsFocusedTest(
			filepath.Join(*root, ".agents", "skills", entry.Name()),
		) {
			focusedTestRequired[metadata.Name] = struct{}{}
		}
	}
	registry, registryDiagnostics := readFocusedTestRegistry(
		*root,
		skillNames,
		focusedTestRequired,
	)
	diagnostics = append(diagnostics, registryDiagnostics...)
	diagnostics = append(diagnostics, validateReviewPolicyChecks(*root)...)
	if len(diagnostics) != 0 {
		sort.Strings(diagnostics)
		for _, diagnostic := range diagnostics {
			fmt.Fprintln(stderr, diagnostic)
		}
		return 1
	}
	if !*runFocused {
		fmt.Fprintf(
			stdout,
			"skillcheck: %d %s valid\n",
			skills,
			pluralize(skills, "skill", "skills"),
		)
		return 0
	}
	tests := append([]focusedTest(nil), registry.Tests...)
	sort.Slice(tests, func(left int, right int) bool {
		return tests[left].ID < tests[right].ID
	})
	patterns := make([]string, 0, len(tests))
	totalTimeoutSeconds := 0
	for _, test := range tests {
		fmt.Fprintf(stdout, "skillcheck: running focused test %s (%s)\n", test.ID, test.Skill)
		patterns = append(patterns, test.Arguments[4])
		totalTimeoutSeconds += test.TimeoutSeconds
	}
	if len(tests) > 0 {
		arguments := append([]string(nil), tests[0].Arguments...)
		if len(patterns) > 1 {
			for index, pattern := range patterns {
				patterns[index] = "(" + pattern + ")"
			}
			arguments[4] = strings.Join(patterns, "|")
		}
		ctx, cancel := context.WithTimeout(
			context.Background(), time.Duration(totalTimeoutSeconds)*time.Second,
		)
		err := executor(ctx, *root, arguments, stdout, stderr)
		cancel()
		if err != nil {
			if len(tests) == 1 {
				fmt.Fprintf(stderr, ".agents/skill-tests.json: focused test %q failed: %v\n", tests[0].ID, err)
			} else {
				fmt.Fprintf(stderr, ".agents/skill-tests.json: focused test batch failed: %v\n", err)
			}
			return 1
		}
	}
	fmt.Fprintf(
		stdout,
		"skillcheck: %d %s valid; %d focused %s passed\n",
		skills,
		pluralize(skills, "skill", "skills"),
		len(tests),
		pluralize(len(tests), "test", "tests"),
	)
	return 0
}

func pluralize(count int, singular string, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func executeFocusedCommand(
	ctx context.Context,
	root string,
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
	command.Dir = root
	command.Stdout = stdout
	command.Stderr = stderr
	command.Env = environmentWith(os.Environ(), "GOWORK", "off")
	return command.Run()
}

func environmentWith(environment []string, name string, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}

type skillMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func readSkillMetadata(path string) (skillMetadata, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return skillMetadata{}, err
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) < 3 || strings.TrimSuffix(lines[0], "\r") != "---" {
		return skillMetadata{}, fmt.Errorf("must start with YAML frontmatter")
	}
	closing := -1
	for index := 1; index < len(lines); index++ {
		if strings.TrimSuffix(lines[index], "\r") == "---" {
			closing = index
			break
		}
	}
	if closing < 0 {
		return skillMetadata{}, fmt.Errorf("YAML frontmatter is not closed")
	}
	var metadata skillMetadata
	if err := yaml.NewDecoder(bytes.NewBufferString(
		strings.Join(lines[1:closing], "\n"),
	)).Decode(&metadata); err != nil {
		return skillMetadata{}, fmt.Errorf("decode YAML frontmatter: %w", err)
	}
	return metadata, nil
}

type openAIInterfaceDocument struct {
	Interface struct {
		DisplayName      string `yaml:"display_name"`
		ShortDescription string `yaml:"short_description"`
		DefaultPrompt    string `yaml:"default_prompt"`
	} `yaml:"interface"`
}

func readOpenAIInterface(path string) (openAIInterfaceDocument, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return openAIInterfaceDocument{}, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var document openAIInterfaceDocument
	if err := decoder.Decode(&document); err != nil {
		return openAIInterfaceDocument{}, fmt.Errorf("decode YAML: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return openAIInterfaceDocument{}, fmt.Errorf("must contain one YAML document")
		}
		return openAIInterfaceDocument{}, fmt.Errorf("decode trailing YAML: %w", err)
	}
	return document, nil
}

var (
	skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

func validateMarkdownReferences(repoRoot string, skillRelative string) []string {
	skillRoot := filepath.Join(repoRoot, skillRelative)
	var diagnostics []string
	_ = filepath.WalkDir(skillRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			relative, _ := filepath.Rel(repoRoot, path)
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: %v", filepath.ToSlash(relative), walkErr,
			))
			return nil
		}
		if entry.IsDir() || strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			relative, _ := filepath.Rel(repoRoot, path)
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: %v", filepath.ToSlash(relative), err,
			))
			return nil
		}
		document := goldmark.DefaultParser().Parse(text.NewReader(raw))
		_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			var destination []byte
			switch typed := node.(type) {
			case *ast.Link:
				destination = typed.Destination
			case *ast.Image:
				destination = typed.Destination
			default:
				return ast.WalkContinue, nil
			}
			target := string(destination)
			parsed, err := url.Parse(target)
			if err != nil || parsed.Scheme != "" || strings.HasPrefix(target, "#") {
				return ast.WalkContinue, nil
			}
			if parsed.Path == "" {
				return ast.WalkContinue, nil
			}
			relativeMarkdown, _ := filepath.Rel(repoRoot, path)
			resolved := filepath.Clean(filepath.Join(
				filepath.Dir(path), filepath.FromSlash(parsed.Path),
			))
			relativeToSkill, err := filepath.Rel(skillRoot, resolved)
			if filepath.IsAbs(parsed.Path) || err != nil || relativeToSkill == ".." ||
				strings.HasPrefix(relativeToSkill, ".."+string(filepath.Separator)) {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s: local reference %q escapes the skill directory",
					filepath.ToSlash(relativeMarkdown), target,
				))
				return ast.WalkContinue, nil
			}
			if _, err := os.Stat(resolved); err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s: local reference %q does not exist",
					filepath.ToSlash(relativeMarkdown), target,
				))
				return ast.WalkContinue, nil
			}
			canonicalSkillRoot, rootErr := filepath.EvalSymlinks(skillRoot)
			canonicalResolved, resolvedErr := filepath.EvalSymlinks(resolved)
			if rootErr == nil && resolvedErr == nil {
				canonicalRelative, relErr := filepath.Rel(canonicalSkillRoot, canonicalResolved)
				if relErr != nil || canonicalRelative == ".." ||
					strings.HasPrefix(canonicalRelative, ".."+string(filepath.Separator)) {
					diagnostics = append(diagnostics, fmt.Sprintf(
						"%s: local reference %q escapes the skill directory through a symlink",
						filepath.ToSlash(relativeMarkdown), target,
					))
				}
			}
			return ast.WalkContinue, nil
		})
		return nil
	})
	return diagnostics
}

func validateStructuredFixtures(repoRoot string, skillRelative string) []string {
	skillRoot := filepath.Join(repoRoot, skillRelative)
	var diagnostics []string
	_ = filepath.WalkDir(skillRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		extension := strings.ToLower(filepath.Ext(path))
		if walkErr != nil || entry.IsDir() ||
			(extension != ".json" && extension != ".yaml" && extension != ".yml") {
			return nil
		}
		relativeToSkill, err := filepath.Rel(skillRoot, path)
		if err != nil || !strings.Contains(
			string(filepath.Separator)+relativeToSkill,
			string(filepath.Separator)+"fixtures"+string(filepath.Separator),
		) {
			return nil
		}
		relative, _ := filepath.Rel(repoRoot, path)
		raw, err := os.ReadFile(path)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: %v", filepath.ToSlash(relative), err,
			))
			return nil
		}
		if extension == ".json" {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			var value any
			if err := decoder.Decode(&value); err != nil {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s: invalid JSON: %v", filepath.ToSlash(relative), err,
				))
				return nil
			}
			if err := decoder.Decode(&value); err != io.EOF {
				if err == nil {
					diagnostics = append(diagnostics, fmt.Sprintf(
						"%s: invalid JSON: multiple values",
						filepath.ToSlash(relative),
					))
				} else {
					diagnostics = append(diagnostics, fmt.Sprintf(
						"%s: invalid JSON: %v", filepath.ToSlash(relative), err,
					))
				}
			}
			return nil
		}
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		var value any
		if err := decoder.Decode(&value); err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: invalid YAML: %v", filepath.ToSlash(relative), err,
			))
			return nil
		}
		if err := decoder.Decode(&value); err != io.EOF {
			if err == nil {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s: invalid YAML: multiple documents",
					filepath.ToSlash(relative),
				))
			} else {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s: invalid YAML: %v", filepath.ToSlash(relative), err,
				))
			}
		}
		return nil
	})
	return diagnostics
}

func validateSkillFileModes(repoRoot string, skillRelative string) []string {
	skillRoot := filepath.Join(repoRoot, skillRelative)
	var diagnostics []string
	_ = filepath.WalkDir(skillRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		relativeToSkill, err := filepath.Rel(skillRoot, path)
		if err != nil {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		hasShebang, err := fileHasShebang(path)
		if err != nil {
			relative, _ := filepath.Rel(repoRoot, path)
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: %v", filepath.ToSlash(relative), err,
			))
			return nil
		}
		executable := info.Mode().Perm()&0o111 != 0
		if executable && !hasShebang {
			relative, _ := filepath.Rel(repoRoot, path)
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: files without a shebang must not be executable",
				filepath.ToSlash(relative),
			))
			return nil
		}
		parts := strings.Split(filepath.ToSlash(relativeToSkill), "/")
		inScripts := len(parts) >= 2 && parts[0] == "scripts"
		if executable && !inScripts {
			relative, _ := filepath.Rel(repoRoot, path)
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: files outside scripts/ must not be executable",
				filepath.ToSlash(relative),
			))
			return nil
		}
		if inScripts && hasShebang && !executable {
			relative, _ := filepath.Rel(repoRoot, path)
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: files under scripts/ must be executable",
				filepath.ToSlash(relative),
			))
		}
		return nil
	})
	return diagnostics
}

func fileHasShebang(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	var prefix [2]byte
	read, err := io.ReadFull(file, prefix[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false, err
	}
	return read == len(prefix) && string(prefix[:]) == "#!", nil
}

func skillNeedsFocusedTest(skillRoot string) bool {
	for _, directory := range []string{"references", "fixtures", "scripts"} {
		info, err := os.Stat(filepath.Join(skillRoot, directory))
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

type focusedTestRegistry struct {
	// SchemaVersion selects the strict registry contract understood by skillcheck.
	SchemaVersion int `json:"schema_version"`
	// Tests is the complete allowlist of focused commands skillcheck may execute.
	Tests []focusedTest `json:"tests"`
}

type focusedTest struct {
	// ID is the stable diagnostic identity of this focused contract.
	ID string `json:"id"`
	// Skill names the existing skill whose behavior the command verifies.
	Skill string `json:"skill"`
	// Arguments is an argv-only command constrained by parseFocusedTestCommand.
	Arguments []string `json:"arguments"`
	// TimeoutSeconds contributes to the registry's shared focused-batch deadline.
	TimeoutSeconds int `json:"timeout_seconds"`
}

var focusedTestIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const maxFocusedTestSeconds = 40

func readFocusedTestRegistry(
	repoRoot string,
	skillNames map[string]string,
	focusedTestRequired map[string]struct{},
) (focusedTestRegistry, []string) {
	const registryRelative = ".agents/skill-tests.json"
	path := filepath.Join(repoRoot, filepath.FromSlash(registryRelative))
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return focusedTestRegistry{}, []string{
				registryRelative + ": required file is missing",
			}
		}
		return focusedTestRegistry{}, []string{
			fmt.Sprintf("%s: %v", registryRelative, err),
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var registry focusedTestRegistry
	if err := decoder.Decode(&registry); err != nil {
		return focusedTestRegistry{}, []string{
			fmt.Sprintf("%s: invalid registry JSON: %v", registryRelative, err),
		}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return focusedTestRegistry{}, []string{
			registryRelative + ": invalid registry JSON: trailing value",
		}
	}

	var diagnostics []string
	if registry.SchemaVersion != 1 {
		diagnostics = append(diagnostics,
			registryRelative+": schema_version must equal 1",
		)
	}
	seenIDs := make(map[string]struct{})
	registeredSkills := make(map[string]struct{})
	totalTimeoutSeconds := 0
	for _, test := range registry.Tests {
		totalTimeoutSeconds += test.TimeoutSeconds
		if len(test.ID) > 64 || !focusedTestIDPattern.MatchString(test.ID) {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: test id %q must be 1-64 lowercase letters, digits, or single hyphen-separated words",
				registryRelative, test.ID,
			))
		}
		if _, exists := seenIDs[test.ID]; exists {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: test id %q is duplicated", registryRelative, test.ID,
			))
		}
		seenIDs[test.ID] = struct{}{}
		if _, exists := skillNames[test.Skill]; !exists {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: test %q references unknown skill %q",
				registryRelative, test.ID, test.Skill,
			))
		}
		registeredSkills[test.Skill] = struct{}{}
		if test.TimeoutSeconds < 1 || test.TimeoutSeconds > maxFocusedTestSeconds {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: test %q timeout_seconds must be between 1 and %d",
				registryRelative, test.ID, maxFocusedTestSeconds,
			))
		}
		pattern, allowed := parseFocusedTestCommand(test.Arguments)
		if !allowed {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: test %q must be an explicit go test ./scripts/skillcontracts -run '^Test...' -count=1 command",
				registryRelative, test.ID,
			))
			continue
		}
		matched, err := matchesFocusedContractTest(repoRoot, pattern)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: test %q cannot inspect default skillcontracts tests: %v",
				registryRelative, test.ID, err,
			))
		} else if !matched {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: test %q -run pattern %q matches no default skillcontracts test",
				registryRelative, test.ID, pattern.String(),
			))
		}
	}
	if totalTimeoutSeconds > maxFocusedTestSeconds {
		diagnostics = append(diagnostics, fmt.Sprintf(
			"%s: total timeout_seconds must not exceed %d",
			registryRelative, maxFocusedTestSeconds,
		))
	}
	for skill := range focusedTestRequired {
		if _, exists := registeredSkills[skill]; !exists {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: skill %q has references/, fixtures/, or scripts/ and must register a focused test",
				registryRelative, skill,
			))
		}
	}
	return registry, diagnostics
}

func parseFocusedTestCommand(arguments []string) (*regexp.Regexp, bool) {
	if len(arguments) != 6 || arguments[0] != "go" || arguments[1] != "test" ||
		arguments[2] != "./scripts/skillcontracts" || arguments[3] != "-run" ||
		arguments[5] != "-count=1" || !strings.HasPrefix(arguments[4], "^Test") {
		return nil, false
	}
	pattern, err := regexp.Compile(arguments[4])
	return pattern, err == nil
}

func matchesFocusedContractTest(repoRoot string, pattern *regexp.Regexp) (bool, error) {
	contractsRoot := filepath.Join(repoRoot, "scripts", "skillcontracts")
	matched := false
	err := filepath.WalkDir(contractsRoot, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != contractsRoot && (entry.Name() == "testdata" ||
				entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".") ||
				strings.HasPrefix(entry.Name(), "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		included, err := build.Default.MatchFile(filepath.Dir(path), entry.Name())
		if err != nil {
			return err
		}
		if !included {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*goast.FuncDecl)
			if !ok || !isDiscoverableGoTest(parsed, function) {
				continue
			}
			if pattern.MatchString(function.Name.Name) {
				matched = true
				return fs.SkipAll
			}
		}
		return nil
	})
	return matched, err
}

func isDiscoverableGoTest(file *goast.File, function *goast.FuncDecl) bool {
	if function.Recv != nil || !isGoTestName(function.Name.Name) ||
		function.Type.TypeParams != nil || function.Type.Params == nil ||
		len(function.Type.Params.List) != 1 ||
		function.Type.Params.NumFields() != 1 ||
		function.Type.Results != nil && function.Type.Results.NumFields() != 0 {
		return false
	}
	parameter, ok := function.Type.Params.List[0].Type.(*goast.StarExpr)
	if !ok {
		return false
	}
	aliases, dotImported := testingImportAliases(file)
	switch typed := parameter.X.(type) {
	case *goast.SelectorExpr:
		qualifier, ok := typed.X.(*goast.Ident)
		if !ok {
			return false
		}
		_, imported := aliases[qualifier.Name]
		return imported && typed.Sel.Name == "T"
	case *goast.Ident:
		return dotImported && typed.Name == "T"
	default:
		return false
	}
}

func isGoTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	if len(name) == len("Test") {
		return true
	}
	for _, first := range name[len("Test"):] {
		return !unicode.IsLower(first)
	}
	return false
}

func testingImportAliases(file *goast.File) (map[string]struct{}, bool) {
	aliases := make(map[string]struct{})
	dotImported := false
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != "testing" {
			continue
		}
		if imported.Name == nil {
			aliases["testing"] = struct{}{}
			continue
		}
		switch imported.Name.Name {
		case ".":
			dotImported = true
		case "_":
		default:
			aliases[imported.Name.Name] = struct{}{}
		}
	}
	return aliases, dotImported
}

type reviewPolicySubset struct {
	// TrustedChecks is the policy-owned executable catalog. This validator reads
	// it but never reconstructs or overrides its commands.
	TrustedChecks map[string]reviewPolicyCheck `json:"trusted_checks"`
	// PathRules is the policy-owned mapping from changed paths to named checks.
	PathRules []reviewPolicyPathRule `json:"path_rules"`
}

type reviewPolicyCheck struct {
	// Arguments is the policy-owned argv executed without a shell.
	Arguments []string `json:"arguments"`
	// WorkingDir must keep repository-relative contract paths meaningful.
	WorkingDir string `json:"working_dir"`
	// TimeoutSeconds bounds this named check inside the combined fast-gate budget.
	TimeoutSeconds int `json:"timeout_seconds"`
	// MaxOutputBytes bounds evidence retained by the Review Agent.
	MaxOutputBytes int `json:"max_output_bytes"`
}

type reviewPolicyPathRule struct {
	// Name is diagnostic identity only; selection semantics come from policy.
	Name string `json:"name"`
	// Paths and Prefixes select changed repository artifacts.
	Paths    []string `json:"paths"`
	Prefixes []string `json:"prefixes"`
	// Checks references entries in TrustedChecks.
	Checks []string `json:"checks"`
}

type namedReviewPolicyCheck struct {
	name  string
	check reviewPolicyCheck
}

func validateReviewPolicyChecks(repoRoot string) []string {
	const policyRelative = ".github/review-agent/policy.json"
	policyDirectory := filepath.Join(repoRoot, ".github", "review-agent")
	if _, err := os.Stat(policyDirectory); os.IsNotExist(err) {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(policyRelative)))
	if err != nil {
		if os.IsNotExist(err) {
			return []string{policyRelative + ": required file is missing"}
		}
		return []string{fmt.Sprintf("%s: %v", policyRelative, err)}
	}
	var policy reviewPolicySubset
	if err := json.Unmarshal(raw, &policy); err != nil {
		return []string{fmt.Sprintf("%s: invalid JSON: %v", policyRelative, err)}
	}
	var diagnostics []string
	var skillRule *reviewPolicyPathRule
	for _, rule := range policy.PathRules {
		if slices.Contains(rule.Prefixes, ".agents/skills/") &&
			slices.Contains(rule.Paths, ".agents/skill-tests.json") {
			copied := rule
			skillRule = &copied
			break
		}
	}
	if skillRule == nil {
		return []string{
			policyRelative + ": must route .agents/skills/ and .agents/skill-tests.json through paired static and --run-focused checks",
		}
	}

	selected := make([]namedReviewPolicyCheck, 0, len(skillRule.Checks))
	for _, name := range skillRule.Checks {
		check, exists := policy.TrustedChecks[name]
		if !exists {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: trusted check %q is missing", policyRelative, name,
			))
			continue
		}
		selected = append(selected, namedReviewPolicyCheck{name: name, check: check})
	}
	if len(diagnostics) != 0 {
		return diagnostics
	}

	static, focused, paired := findSkillCheckPair(selected)
	if !paired {
		return []string{
			policyRelative + ": must route .agents/skills/ and .agents/skill-tests.json through paired static and --run-focused checks",
		}
	}
	// This validates the agreed public entrypoint as policy schema. The Review
	// Agent still obtains the argv it executes exclusively from the policy.
	staticArguments := []string{"go", "run", "./scripts/skillcheck"}
	focusedArguments := append(
		append([]string(nil), staticArguments...),
		"--run-focused",
	)
	if !slices.Equal(static.check.Arguments, staticArguments) ||
		!slices.Equal(focused.check.Arguments, focusedArguments) {
		return []string{
			policyRelative + ": skill checks must invoke go run ./scripts/skillcheck with only an optional --run-focused argument",
		}
	}
	for _, selectedCheck := range []namedReviewPolicyCheck{static, focused} {
		if selectedCheck.check.WorkingDir != "." {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: trusted check %q must run from repository root",
				policyRelative, selectedCheck.name,
			))
		}
		if selectedCheck.check.TimeoutSeconds < 1 {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: trusted check %q timeout_seconds must be positive",
				policyRelative, selectedCheck.name,
			))
		}
		if selectedCheck.check.MaxOutputBytes < 1 {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: trusted check %q max_output_bytes must be positive",
				policyRelative, selectedCheck.name,
			))
		}
	}
	combinedTimeout := static.check.TimeoutSeconds + focused.check.TimeoutSeconds
	if combinedTimeout > 60 {
		diagnostics = append(diagnostics, fmt.Sprintf(
			"%s: paired skill checks %q and %q declare %d seconds, exceeding the 60-second gate budget",
			policyRelative,
			static.name,
			focused.name,
			combinedTimeout,
		))
	}
	return diagnostics
}

func findSkillCheckPair(
	checks []namedReviewPolicyCheck,
) (namedReviewPolicyCheck, namedReviewPolicyCheck, bool) {
	for _, focused := range checks {
		staticArguments, removed := withoutArgument(
			focused.check.Arguments,
			"--run-focused",
		)
		if !removed {
			continue
		}
		for _, static := range checks {
			if slices.Equal(static.check.Arguments, staticArguments) {
				return static, focused, true
			}
		}
	}
	return namedReviewPolicyCheck{}, namedReviewPolicyCheck{}, false
}

func withoutArgument(arguments []string, removed string) ([]string, bool) {
	for index, argument := range arguments {
		if argument == removed {
			result := make([]string, 0, len(arguments)-1)
			result = append(result, arguments[:index]...)
			result = append(result, arguments[index+1:]...)
			return result, true
		}
	}
	return nil, false
}
