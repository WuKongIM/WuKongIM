package reviewagentverify

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

// FileType is the bounded review treatment of one repository blob.
type FileType string

const (
	FileTypeText   FileType = "text"
	FileTypeBinary FileType = "binary"
)

// RawFile is one complete file record supplied by a trusted exact-tree
// reader. PatchTruncated is fatal rather than advisory.
type RawFile struct {
	Path           string
	OldPath        string
	Status         contract.FileStatus
	Mode           string
	Type           FileType
	Generated      bool
	Patch          []byte
	Content        []byte
	PatchTruncated bool
	Additions      uint64
	Deletions      uint64
}

// InventoryLimits bound complete diff ingestion.
type InventoryLimits struct {
	MaxFiles      int
	MaxTotalBytes int64
	MaxLines      int64
}

// Inventory proves whether every declared changed file is represented.
type Inventory struct {
	Complete      bool
	DeclaredFiles int
	TotalBytes    int64
	TotalLines    int64
	Files         []contract.ChangedFile
}

// BuildInventory validates and canonicalizes the complete changed-file set.
func BuildInventory(
	declaredFiles int,
	rawFiles []RawFile,
	limits InventoryLimits,
) (Inventory, error) {
	if declaredFiles <= 0 || limits.MaxFiles <= 0 ||
		limits.MaxTotalBytes <= 0 || limits.MaxLines <= 0 {
		return Inventory{}, errors.New("invalid changed-file inventory limits")
	}
	if declaredFiles != len(rawFiles) {
		return Inventory{}, errors.New("changed-file inventory count mismatch")
	}
	if declaredFiles > limits.MaxFiles {
		return Inventory{}, errors.New("changed-file budget exceeded")
	}
	files := append([]RawFile(nil), rawFiles...)
	slices.SortFunc(files, func(left, right RawFile) int {
		return strings.Compare(left.Path, right.Path)
	})
	result := Inventory{
		Complete:      true,
		DeclaredFiles: declaredFiles,
		Files:         make([]contract.ChangedFile, 0, len(files)),
	}
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if err := validateRawFile(file); err != nil {
			return Inventory{}, err
		}
		key := strings.ToLower(file.Path)
		if _, exists := seen[key]; exists {
			return Inventory{}, errors.New("duplicate changed-file path")
		}
		seen[key] = struct{}{}
		// A complete text patch already contains both revisions, including the
		// head content. Charge that representation once. Binary patches are
		// empty, so charge the exact blob retained only for its digest.
		result.TotalBytes += int64(len(file.Patch))
		result.TotalLines += countLines(file.Patch)
		if file.Type == FileTypeBinary {
			result.TotalBytes += int64(len(file.Content))
		}
		if result.TotalBytes > limits.MaxTotalBytes {
			return Inventory{}, errors.New("changed-byte budget exceeded")
		}
		if result.TotalLines > limits.MaxLines {
			return Inventory{}, errors.New("changed-line budget exceeded")
		}
		result.Files = append(result.Files, contract.ChangedFile{
			Path:         file.Path,
			PreviousPath: file.OldPath,
			Status:       file.Status,
			Mode:         file.Mode,
			Type:         string(file.Type),
			Generated:    file.Generated,
			Patch:        string(file.Patch),
			PatchDigest:  bytesDigest(file.Patch),
			Content: func() string {
				if file.Type == FileTypeBinary {
					return ""
				}
				return string(file.Content)
			}(),
			ContentDigest: bytesDigest(file.Content),
			Additions:     file.Additions,
			Deletions:     file.Deletions,
		})
	}
	return result, nil
}

func validateRawFile(file RawFile) error {
	if !validPath(file.Path) ||
		(file.Mode != "100644" && file.Mode != "100755") ||
		file.PatchTruncated {
		return errors.New("invalid or truncated changed file")
	}
	switch file.Type {
	case FileTypeText:
		if len(file.Patch) == 0 || !utf8.Valid(file.Patch) ||
			!utf8.Valid(file.Content) {
			return errors.New("text changed file lacks complete UTF-8 content")
		}
	case FileTypeBinary:
	default:
		return errors.New("unsupported changed-file type")
	}
	switch file.Status {
	case contract.FileStatusAdded,
		contract.FileStatusModified,
		contract.FileStatusRemoved:
		if file.OldPath != "" {
			return errors.New("non-rename changed file has an old path")
		}
	case contract.FileStatusRenamed:
		if !validPath(file.OldPath) || file.OldPath == file.Path {
			return errors.New("rename lacks distinct old and new paths")
		}
	default:
		return errors.New("unsupported changed-file status")
	}
	return nil
}

func validPath(value string) bool {
	if value == "" || len(value) > 4096 || path.IsAbs(value) ||
		path.Clean(value) != value || strings.Contains(value, `\`) ||
		value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return false
	}
	return true
}

func bytesDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func countLines(value []byte) int64 {
	if len(value) == 0 {
		return 0
	}
	count := int64(1)
	for _, character := range value {
		if character == '\n' {
			count++
		}
	}
	return count
}
