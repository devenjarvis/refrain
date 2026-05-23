// Package editor detects installed GUI editors on macOS and Linux and maps
// stored launch commands back to detected editors for UI pre-selection.
package editor

import (
	"fmt"
	"os"
	"os/exec"
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

// curatedCLIs lists editor CLI commands to probe on Linux, in display order.
var curatedCLIs = []struct {
	Name string
	Cmd  string
}{
	{"Zed", "zed"},
	{"Visual Studio Code", "code"},
	{"Cursor", "cursor"},
	{"IntelliJ IDEA", "idea"},
	{"GoLand", "goland"},
	{"PyCharm", "pycharm"},
	{"WebStorm", "webstorm"},
	{"RubyMine", "rubymine"},
	{"PhpStorm", "phpstorm"},
	{"CLion", "clion"},
	{"RustRover", "rustrover"},
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

// lookPath is overridden in tests.
var lookPath = exec.LookPath

// Detect probes known locations for editors and returns matches in curated
// order. On macOS it probes .app bundles; on Linux it checks PATH for CLI
// commands and $VISUAL/$EDITOR. On other platforms returns nil.
func Detect() []Editor {
	switch runtime.GOOS {
	case "darwin":
		return detectDarwin()
	case "linux":
		return detectLinux()
	default:
		return nil
	}
}

func detectDarwin() []Editor {
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

func detectLinux() []Editor {
	out := make([]Editor, 0, len(curatedCLIs))
	seen := make(map[string]bool)
	for _, cli := range curatedCLIs {
		if _, err := lookPath(cli.Cmd); err == nil {
			out = append(out, Editor{
				Name:    cli.Name,
				Command: cli.Cmd,
			})
			seen[cli.Cmd] = true
		}
	}
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if val := os.Getenv(env); val != "" && !seen[val] {
			if _, err := lookPath(val); err == nil {
				out = append(out, Editor{
					Name:    "$" + env + " (" + val + ")",
					Command: val,
				})
				seen[val] = true
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
	for _, cli := range curatedCLIs {
		if cmd == cli.Cmd {
			return &Editor{Name: cli.Name, Command: cli.Cmd}
		}
	}
	return nil
}
