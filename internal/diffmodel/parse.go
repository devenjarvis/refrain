// Package diffmodel wraps bluekeyes/go-gitdiff with a refrain-shaped data model
// tailored to the TUI diff view. It is renderer-agnostic (no styling imports)
// and produces immutable structures safe to share across goroutines.
package diffmodel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// LineKind classifies a hunk line.
type LineKind int

const (
	// LineContext is an unchanged context line.
	LineContext LineKind = iota
	// LineAdd is an added line (prefix '+').
	LineAdd
	// LineDelete is a deleted line (prefix '-').
	LineDelete
)

// Status describes the file-level change.
type Status int

const (
	// StatusModified means the file was modified in place.
	StatusModified Status = iota
	// StatusAdded means the file is new.
	StatusAdded
	// StatusDeleted means the file was removed.
	StatusDeleted
	// StatusRenamed means the file was renamed (OldPath is populated).
	StatusRenamed
	// StatusCopied means the file was copied from another (OldPath is populated).
	StatusCopied
)

// String returns the one-letter git status code.
func (s Status) String() string {
	switch s {
	case StatusAdded:
		return "A"
	case StatusDeleted:
		return "D"
	case StatusRenamed:
		return "R"
	case StatusCopied:
		return "C"
	default:
		return "M"
	}
}

// Line is one line within a hunk, with both side line numbers filled in so the
// renderer does not need to do any arithmetic.
type Line struct {
	Kind LineKind
	// Text is the line content without the leading +/- /space marker or
	// trailing newline.
	Text string
	// OldNum is the 1-based line number on the old side, or 0 if N/A.
	OldNum int
	// NewNum is the 1-based line number on the new side, or 0 if N/A.
	NewNum int
}

// Hunk is a contiguous block of changes within a file.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	// Header is the human-readable "@@ -a,b +c,d @@ section" line with a
	// leading-space separator before section, matching what git emits.
	Header string
	Lines  []Line
}

// File is a single file's diff.
type File struct {
	// Path is the current path (after rename, or the only path for non-renames).
	Path string
	// OldPath is populated only for renames/copies.
	OldPath string
	Status  Status
	// IsBinary is true for binary files; Hunks will be empty in that case.
	IsBinary bool
	Hunks    []Hunk
	// Insertions and Deletions are aggregate counts across all hunks.
	Insertions, Deletions int
}

// Model is the parsed diff, safe to share across goroutines (immutable after
// construction).
type Model struct {
	Files []File
}

// FileNode is a node in the collapsible file tree produced by Model.Tree.
// Folders have Children; leaves have a non-nil File pointing into the parent
// Model's Files slice.
type FileNode struct {
	// Name is the single path component (leaf: basename; folder: segment).
	// The synthetic root has Name "".
	Name string
	// Path is the full slash-joined path from the tree root.
	// For folders this is the prefix shared by all descendants.
	// For leaves this equals File.Path.
	Path string
	// IsLeaf is true for files, false for folders (and for the synthetic root).
	IsLeaf bool
	// File is non-nil exactly when IsLeaf is true.
	File *File
	// Children is populated for folders. Sorted: folders first, then files,
	// each group alphabetically.
	Children []*FileNode
}

// Parse parses a unified diff string and returns a Model. Empty input yields
// an empty Model with no error.
func Parse(raw string) (*Model, error) {
	if strings.TrimSpace(raw) == "" {
		return &Model{}, nil
	}

	parsed, _, err := gitdiff.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("diffmodel: parse: %w", err)
	}

	files := make([]File, 0, len(parsed))
	for _, pf := range parsed {
		files = append(files, convertFile(pf))
	}

	return &Model{Files: files}, nil
}

func convertFile(pf *gitdiff.File) File {
	status := StatusModified
	switch {
	case pf.IsNew:
		status = StatusAdded
	case pf.IsDelete:
		status = StatusDeleted
	case pf.IsRename:
		status = StatusRenamed
	case pf.IsCopy:
		status = StatusCopied
	}

	path := pf.NewName
	oldPath := ""
	if pf.IsDelete {
		// Deletes have empty NewName; fall back to OldName so the tree has
		// something to index on.
		path = pf.OldName
	}
	if pf.IsRename || pf.IsCopy {
		oldPath = pf.OldName
	}

	f := File{
		Path:     path,
		OldPath:  oldPath,
		Status:   status,
		IsBinary: pf.IsBinary,
	}

	if pf.IsBinary {
		return f
	}

	f.Hunks = make([]Hunk, 0, len(pf.TextFragments))
	for _, tf := range pf.TextFragments {
		h := convertFragment(tf)
		f.Insertions += int(tf.LinesAdded)
		f.Deletions += int(tf.LinesDeleted)
		f.Hunks = append(f.Hunks, h)
	}
	return f
}

func convertFragment(tf *gitdiff.TextFragment) Hunk {
	lines := make([]Line, 0, len(tf.Lines))
	oldNum := int(tf.OldPosition)
	newNum := int(tf.NewPosition)

	for _, l := range tf.Lines {
		text := stripTrailingNewline(l.Line)
		switch l.Op {
		case gitdiff.OpContext:
			lines = append(lines, Line{
				Kind:   LineContext,
				Text:   text,
				OldNum: oldNum,
				NewNum: newNum,
			})
			oldNum++
			newNum++
		case gitdiff.OpDelete:
			lines = append(lines, Line{
				Kind:   LineDelete,
				Text:   text,
				OldNum: oldNum,
			})
			oldNum++
		case gitdiff.OpAdd:
			lines = append(lines, Line{
				Kind:   LineAdd,
				Text:   text,
				NewNum: newNum,
			})
			newNum++
		}
	}

	return Hunk{
		OldStart: int(tf.OldPosition),
		OldCount: int(tf.OldLines),
		NewStart: int(tf.NewPosition),
		NewCount: int(tf.NewLines),
		Header:   buildHunkHeader(tf),
		Lines:    lines,
	}
}

// stripTrailingNewline removes a single trailing '\n', leaving '\r' intact if
// present (caller may want to preserve Windows line endings for display).
func stripTrailingNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}

func buildHunkHeader(tf *gitdiff.TextFragment) string {
	var b strings.Builder
	b.WriteString("@@ -")
	b.WriteString(fmt.Sprintf("%d", tf.OldPosition))
	if tf.OldLines != 1 {
		b.WriteString(fmt.Sprintf(",%d", tf.OldLines))
	}
	b.WriteString(" +")
	b.WriteString(fmt.Sprintf("%d", tf.NewPosition))
	if tf.NewLines != 1 {
		b.WriteString(fmt.Sprintf(",%d", tf.NewLines))
	}
	b.WriteString(" @@")
	if tf.Comment != "" {
		b.WriteString(" ")
		b.WriteString(tf.Comment)
	}
	return b.String()
}

// Tree returns a collapsible file tree for the Model. The returned root has
// Name == "" and is always a folder. A Model with no files returns a root
// with no Children.
func (m *Model) Tree() *FileNode {
	root := &FileNode{Name: "", Path: "", IsLeaf: false}
	if m == nil || len(m.Files) == 0 {
		return root
	}

	for i := range m.Files {
		f := &m.Files[i]
		insertFile(root, f)
	}
	sortNode(root)
	return root
}

func insertFile(root *FileNode, f *File) {
	parts := strings.Split(f.Path, "/")
	node := root
	for i, p := range parts {
		if i == len(parts)-1 {
			leaf := &FileNode{
				Name:   p,
				Path:   f.Path,
				IsLeaf: true,
				File:   f,
			}
			node.Children = append(node.Children, leaf)
			return
		}
		// Find or create folder.
		var folder *FileNode
		for _, c := range node.Children {
			if !c.IsLeaf && c.Name == p {
				folder = c
				break
			}
		}
		if folder == nil {
			var path string
			if node.Path == "" {
				path = p
			} else {
				path = node.Path + "/" + p
			}
			folder = &FileNode{Name: p, Path: path, IsLeaf: false}
			node.Children = append(node.Children, folder)
		}
		node = folder
	}
}

func sortNode(n *FileNode) {
	if n.IsLeaf {
		return
	}
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsLeaf != b.IsLeaf {
			return !a.IsLeaf // folders first
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		sortNode(c)
	}
}
