package editor

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetect_UnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("only runs on unsupported platforms")
	}
	if got := Detect(); got != nil {
		t.Fatalf("Detect on %s: want nil, got %v", runtime.GOOS, got)
	}
}

func TestDetect_Darwin_FiltersAndOrders(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only runs on darwin")
	}

	root := t.TempDir()
	// Install two editors out of curated order.
	mkAppBundle(t, root, "Visual Studio Code.app")
	mkAppBundle(t, root, "Zed.app")

	withSearchRoots(t, []string{root})

	got := Detect()
	if len(got) != 2 {
		t.Fatalf("expected 2 editors, got %d: %+v", len(got), got)
	}
	// Curated order puts Zed before Visual Studio Code.
	if got[0].Name != "Zed" || got[1].Name != "Visual Studio Code" {
		t.Fatalf("wrong order: %+v", got)
	}
	if got[0].Command != `open -a "Zed"` {
		t.Fatalf("unexpected Zed command: %q", got[0].Command)
	}
	if got[1].Command != `open -a "Visual Studio Code"` {
		t.Fatalf("unexpected VS Code command: %q", got[1].Command)
	}
}

func TestDetect_Darwin_EmptyWhenNothingInstalled(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only runs on darwin")
	}
	root := t.TempDir()
	withSearchRoots(t, []string{root})
	if got := Detect(); len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestDetect_Darwin_FileNotDirIgnored(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only runs on darwin")
	}
	root := t.TempDir()
	// Write a regular file named Zed.app — should be ignored (not a bundle).
	if err := os.WriteFile(filepath.Join(root, "Zed.app"), []byte("not a bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	withSearchRoots(t, []string{root})
	if got := Detect(); len(got) != 0 {
		t.Fatalf("expected file-not-dir to be ignored, got %+v", got)
	}
}

func TestMatchCommand(t *testing.T) {
	tests := []struct {
		in       string
		wantName string
	}{
		{`open -a "Zed"`, "Zed"},
		{`open -a "Visual Studio Code"`, "Visual Studio Code"},
		{"zed -n", ""},
		{"", ""},
		{`open -a Zed`, ""}, // unquoted form is not a curated match
	}
	for _, tc := range tests {
		got := MatchCommand(tc.in)
		if tc.wantName == "" {
			if got != nil {
				t.Errorf("MatchCommand(%q) = %+v, want nil", tc.in, got)
			}
			continue
		}
		if got == nil || got.Name != tc.wantName {
			t.Errorf("MatchCommand(%q) = %+v, want name %q", tc.in, got, tc.wantName)
		}
	}
}

func TestDetect_Linux_FindsCLIs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only runs on linux")
	}
	available := map[string]bool{"code": true, "zed": true}
	withLookPath(t, func(name string) (string, error) {
		if available[name] {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	})

	got := detectLinux()
	if len(got) != 2 {
		t.Fatalf("expected 2 editors, got %d: %+v", len(got), got)
	}
	if got[0].Name != "Zed" || got[0].Command != "zed" {
		t.Fatalf("expected Zed first, got %+v", got[0])
	}
	if got[1].Name != "Visual Studio Code" || got[1].Command != "code" {
		t.Fatalf("expected VS Code second, got %+v", got[1])
	}
}

func TestDetect_Linux_IncludesEnvEditors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only runs on linux")
	}
	withLookPath(t, func(name string) (string, error) {
		if name == "nvim" {
			return "/usr/bin/nvim", nil
		}
		return "", exec.ErrNotFound
	})
	t.Setenv("VISUAL", "nvim")
	t.Setenv("EDITOR", "")

	got := detectLinux()
	if len(got) != 1 {
		t.Fatalf("expected 1 editor from $VISUAL, got %d: %+v", len(got), got)
	}
	if got[0].Command != "nvim" {
		t.Fatalf("expected nvim, got %+v", got[0])
	}
}

func TestDetect_Linux_NoDuplicateEnv(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only runs on linux")
	}
	withLookPath(t, func(name string) (string, error) {
		if name == "code" {
			return "/usr/bin/code", nil
		}
		return "", exec.ErrNotFound
	})
	t.Setenv("VISUAL", "code")

	got := detectLinux()
	if len(got) != 1 {
		t.Fatalf("expected 1 (no duplicate from $VISUAL), got %d: %+v", len(got), got)
	}
}

func TestMatchCommand_LinuxCLI(t *testing.T) {
	got := MatchCommand("code")
	if got == nil || got.Name != "Visual Studio Code" {
		t.Fatalf("MatchCommand(\"code\") = %+v, want VS Code", got)
	}
	got = MatchCommand("zed")
	if got == nil || got.Name != "Zed" {
		t.Fatalf("MatchCommand(\"zed\") = %+v, want Zed", got)
	}
}

// mkAppBundle creates a directory simulating a .app bundle under root.
func mkAppBundle(t *testing.T, root, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
		t.Fatal(err)
	}
}

// withSearchRoots swaps the package-level searchRoots for the duration of a test.
func withSearchRoots(t *testing.T, roots []string) {
	t.Helper()
	prev := searchRoots
	searchRoots = func() []string { return roots }
	t.Cleanup(func() { searchRoots = prev })
}

// withLookPath swaps the package-level lookPath for the duration of a test.
func withLookPath(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	prev := lookPath
	lookPath = fn
	t.Cleanup(func() { lookPath = prev })
}
