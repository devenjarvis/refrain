// changelog assembles fragment files from changelog.d/ into CHANGELOG.md.
//
// Usage:
//
//	go run ./scripts/changelog --version 0.2.0
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// sectionOrder defines Keep a Changelog canonical ordering.
var sectionOrder = []string{
	"Added",
	"Changed",
	"Deprecated",
	"Removed",
	"Fixed",
	"Security",
}

func main() {
	version := flag.String("version", "", "version to release (e.g. 0.2.0)")
	notesFile := flag.String("notes-file", "", "optional path to write the release notes section")
	flag.Parse()

	if *version == "" {
		fmt.Fprintln(os.Stderr, "error: --version is required")
		os.Exit(1)
	}

	// Locate repo root relative to this script's invocation directory.
	// When run via `go run ./scripts/changelog`, the working directory is
	// the module root. Accept the cwd as the root.
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	changelog := filepath.Join(cwd, "CHANGELOG.md")
	fragmentsDir := filepath.Join(cwd, "changelog.d")

	if err := run(changelog, fragmentsDir, *version, *notesFile, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// run is the testable core of the tool.
func run(changelogPath, fragmentsDir, version, notesFile string, now time.Time) error {
	// 1. Collect fragment files (skip .gitkeep and hidden files).
	entries, err := os.ReadDir(fragmentsDir)
	if err != nil {
		return fmt.Errorf("reading changelog.d: %w", err)
	}

	fragmentFiles := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == ".gitkeep" || strings.HasPrefix(name, ".") {
			continue
		}
		fragmentFiles = append(fragmentFiles, filepath.Join(fragmentsDir, name))
	}

	if len(fragmentFiles) == 0 {
		return errors.New("no fragment files found in changelog.d (nothing to release)")
	}

	// 2. Parse fragments into section buckets.
	buckets := make(map[string][]string) // section name → bullet lines

	sectionRe := regexp.MustCompile(`^###\s+(.+)$`)

	for _, path := range fragmentFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		var currentSection string
		scanner := bufio.NewScanner(strings.NewReader(string(content)))
		for scanner.Scan() {
			line := scanner.Text()
			if m := sectionRe.FindStringSubmatch(line); m != nil {
				currentSection = strings.TrimSpace(m[1])
				continue
			}
			if currentSection == "" {
				continue
			}
			// Collect non-empty lines (bullets, continuation lines)
			if strings.TrimSpace(line) != "" {
				buckets[currentSection] = append(buckets[currentSection], line)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("scanning %s: %w", path, err)
		}
	}

	// 3. Build new version section.
	dateStr := now.Format("2006-01-02")
	var sb strings.Builder
	fmt.Fprintf(&sb, "## [%s] — %s\n", version, dateStr)

	for _, sec := range sectionOrder {
		bullets, ok := buckets[sec]
		if !ok || len(bullets) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "\n### %s\n\n", sec)
		for _, b := range bullets {
			fmt.Fprintln(&sb, b)
		}
	}

	newSection := sb.String()

	// 4. Read current CHANGELOG.md.
	changelogBytes, err := os.ReadFile(changelogPath)
	if err != nil {
		return fmt.Errorf("reading CHANGELOG.md: %w", err)
	}
	changelogContent := string(changelogBytes)

	// 5. Insert new section before the first `## [` line (which may be a
	//    prior release or an [Unreleased] section that was already removed).
	insertRe := regexp.MustCompile(`(?m)^## \[`)
	loc := insertRe.FindStringIndex(changelogContent)
	var updated string
	if loc != nil {
		updated = changelogContent[:loc[0]] + newSection + "\n" + changelogContent[loc[0]:]
	} else {
		// No existing versioned section — just append.
		updated = changelogContent + "\n" + newSection
	}

	// 6. Update comparison links at the bottom.
	//    We need to:
	//      a) Replace existing [Unreleased]: ...HEAD  with the new Unreleased link.
	//      b) Insert a new versioned comparison link after (or instead of) the old one.
	//
	//    Strategy: rebuild the links block at the bottom.

	// Find the previous latest version. First try the existing [Unreleased] link;
	// if that was already removed (normal post-Task-3 state), fall back to the
	// first versioned ## [X.Y.Z] section header in the file.
	prevVersionRe := regexp.MustCompile(`\[Unreleased\]: https://github\.com/([^/]+/[^/]+)/compare/v([^.]+\.[^.]+\.[^.]+)\.\.\.HEAD`)
	prevMatch := prevVersionRe.FindStringSubmatch(updated)

	repoSlug := "devenjarvis/refrain" // fallback
	prevVersion := ""
	if prevMatch != nil {
		repoSlug = prevMatch[1]
		prevVersion = prevMatch[2]
	} else {
		// No [Unreleased] link — extract prev version from the original changelog
		// content before the new section was inserted (so we don't accidentally
		// match the version we're currently releasing).
		sectionVerRe := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`)
		if m := sectionVerRe.FindStringSubmatch(changelogContent); m != nil {
			prevVersion = m[1]
		}
	}

	newUnreleasedLink := fmt.Sprintf("[Unreleased]: https://github.com/%s/compare/v%s...HEAD", repoSlug, version)

	// Replace old [Unreleased] link, or insert new ones before the existing links block.
	oldUnreleasedRe := regexp.MustCompile(`(?m)^\[Unreleased\]: .*$`)
	if oldUnreleasedRe.MatchString(updated) {
		updated = oldUnreleasedRe.ReplaceAllString(updated, newUnreleasedLink)
	} else {
		// No existing [Unreleased] link — insert new links before the first [ref]:
		// line so the conventional order ([Unreleased], newest→oldest) is preserved.
		firstRefRe := regexp.MustCompile(`(?m)^\[`)
		if loc := firstRefRe.FindStringIndex(updated); loc != nil {
			updated = updated[:loc[0]] + newUnreleasedLink + "\n" + updated[loc[0]:]
		} else {
			updated = strings.TrimRight(updated, "\n") + "\n" + newUnreleasedLink + "\n"
		}
	}

	// Insert new versioned link. If we found a previous version, insert the
	// new link right after the [Unreleased] line.
	if prevVersion != "" {
		newVersionLink := fmt.Sprintf("[%s]: https://github.com/%s/compare/v%s...v%s", version, repoSlug, prevVersion, version)
		// Insert after the Unreleased link line.
		updated = strings.Replace(
			updated,
			newUnreleasedLink,
			newUnreleasedLink+"\n"+newVersionLink,
			1,
		)
	}

	// 7. Optionally write just the new version section to a release-notes file.
	if notesFile != "" {
		if err := os.WriteFile(notesFile, []byte(newSection), 0o644); err != nil {
			return fmt.Errorf("writing notes file: %w", err)
		}
	}

	// 8. Write updated CHANGELOG.md.
	if err := os.WriteFile(changelogPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("writing CHANGELOG.md: %w", err)
	}

	// 9. Delete processed fragment files (leave .gitkeep).
	for _, path := range fragmentFiles {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing fragment %s: %w", path, err)
		}
	}

	fmt.Printf("Released %s (%s) from %d fragment(s)\n", version, dateStr, len(fragmentFiles))
	return nil
}
