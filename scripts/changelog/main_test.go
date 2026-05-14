package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNoFragmentsExitsNonZero verifies that when changelog.d contains only
// .gitkeep (no real fragment files), run() returns a non-nil error.
func TestNoFragmentsExitsNonZero(t *testing.T) {
	dir := t.TempDir()

	// Create changelog.d with only .gitkeep
	fragmentsDir := filepath.Join(dir, "changelog.d")
	if err := os.MkdirAll(fragmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fragmentsDir, ".gitkeep"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a minimal CHANGELOG.md
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte(minimalChangelog), 0o644); err != nil {
		t.Fatal(err)
	}

	err := run(changelog, fragmentsDir, "0.2.0", "", time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected non-nil error when no fragments exist, got nil")
	}
}

// TestBasicAssembly verifies that a single fragment with ### Added and one
// bullet point produces a new versioned section in CHANGELOG.md.
func TestBasicAssembly(t *testing.T) {
	dir := t.TempDir()

	fragmentsDir := filepath.Join(dir, "changelog.d")
	if err := os.MkdirAll(fragmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write .gitkeep (should be preserved)
	if err := os.WriteFile(filepath.Join(fragmentsDir, ".gitkeep"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write one fragment
	fragment := `### Added

- My shiny new feature.
`
	if err := os.WriteFile(filepath.Join(fragmentsDir, "add-feature.md"), []byte(fragment), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create CHANGELOG.md with a prior versioned release and comparison links
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte(minimalChangelog), 0o644); err != nil {
		t.Fatal(err)
	}

	err := run(changelog, fragmentsDir, "0.2.0", "", time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	contents, err := os.ReadFile(changelog)
	if err != nil {
		t.Fatal(err)
	}
	got := string(contents)

	// New versioned section header must be present
	if !strings.Contains(got, "## [0.2.0] — 2026-04-19") {
		t.Errorf("expected '## [0.2.0] — 2026-04-19' in output, got:\n%s", got)
	}

	// The bullet must appear under Added
	if !strings.Contains(got, "- My shiny new feature.") {
		t.Errorf("expected fragment bullet in output, got:\n%s", got)
	}

	// Fragment file must be deleted
	if _, err := os.Stat(filepath.Join(fragmentsDir, "add-feature.md")); !os.IsNotExist(err) {
		t.Errorf("expected fragment file to be deleted after assembly")
	}

	// .gitkeep must remain
	if _, err := os.Stat(filepath.Join(fragmentsDir, ".gitkeep")); err != nil {
		t.Errorf("expected .gitkeep to remain: %v", err)
	}

	// New comparison links must be present
	if !strings.Contains(got, "[0.2.0]: https://github.com/devenjarvis/refrain/compare/v0.1.0...v0.2.0") {
		t.Errorf("expected new versioned comparison link, got:\n%s", got)
	}
	if !strings.Contains(got, "[Unreleased]: https://github.com/devenjarvis/refrain/compare/v0.2.0...HEAD") {
		t.Errorf("expected updated Unreleased link, got:\n%s", got)
	}
}

// minimalChangelog is a CHANGELOG.md that matches the post-Task-3 state:
// header block, one prior versioned section, and comparison links at the bottom.
const minimalChangelog = `# Changelog

All notable changes to Refrain will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Every PR should update the ` + "`[Unreleased]`" + ` section with a short entry describing the change.

## [0.1.0] — 2026-04-18

Initial public release.

[Unreleased]: https://github.com/devenjarvis/refrain/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/devenjarvis/refrain/releases/tag/v0.1.0
`
