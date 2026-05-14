// Package editor detects installed GUI editors on macOS and maps stored
// launch commands back to detected editors for UI pre-selection.
package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Editor is a detected editor with a display name and the command refrain stores
// to launch it.
type Editor struct {
	Name    string
	Command string
}

// curatedApps lists the macOS .app bundles we probe, in display order.
var curatedApps = []struct {
	Name   string
	Bundle string
}{
	{"Zed", "Zed.app"},
	{"Visual Studio Code", "Visual Studio Code.app"},
	{"Cursor", "Cursor.app"},
	{"IntelliJ IDEA", "IntelliJ IDEA.app"},
	{"IntelliJ IDEA CE", "IntelliJ IDEA CE.app"},
	{"GoLand", "GoLand.app"},
	{"PyCharm", "PyCharm.app"},
	{"PyCharm CE", "PyCharm CE.app"},
	{"WebStorm", "WebStorm.app"},
	{"RubyMine", "RubyMine.app"},
	{"PhpStorm", "PhpStorm.app"},
	{"CLion", "CLion.app"},
	{"RustRover", "RustRover.app"},
}

// searchRoots returns the directories to probe for .app bundles on macOS.
// Overridden in tests via setSearchRoots.
var searchRoots = defaultSearchRoots

func defaultSearchRoots() []string {
	roots := []string{"/Applications"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	return roots
}

// Detect probes known locations for editor .app bundles on macOS and returns
// the matches in curated-list order. On non-darwin platforms returns nil.
func Detect() []Editor {
	if runtime.GOOS != "darwin" {
		return nil
	}
	roots := searchRoots()
	out := make([]Editor, 0, len(curatedApps))
	for _, app := range curatedApps {
		for _, root := range roots {
			if stat, err := os.Stat(filepath.Join(root, app.Bundle)); err == nil && stat.IsDir() {
				out = append(out, Editor{
					Name:    app.Name,
					Command: fmt.Sprintf(`open -a %q`, app.Name),
				})
				break
			}
		}
	}
	return out
}

// MatchCommand reverse-looks up a stored command string to a curated editor.
// Returns nil if no exact match. Used to pre-select the dropdown when loading
// an existing config.
func MatchCommand(cmd string) *Editor {
	for _, app := range curatedApps {
		expected := fmt.Sprintf(`open -a %q`, app.Name)
		if cmd == expected {
			return &Editor{Name: app.Name, Command: expected}
		}
	}
	return nil
}
