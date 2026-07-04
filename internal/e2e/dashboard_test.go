//go:build e2e

package e2e

import (
	"path/filepath"
	"testing"
)

func TestSessionListRendersOnStartup(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	// Empty-state hint block.
	s.AssertScreenContains("new session")
	s.AssertScreenContains("add repo")

	// Status bar.
	s.AssertScreenContains("navigate")
	s.AssertScreenContains("quit")

	// Repo header.
	s.AssertScreenContains(filepath.Base(s.repoDir))
}

func TestNavigationJK(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	createBlankSession(t, s)
	s.Press("Escape")
	s.WaitForText(listAnchor, 10000)

	createBlankSession(t, s)
	s.Press("Escape")
	s.WaitForText(listAnchor, 10000)

	initial := s.Screenshot()

	s.Press("k")
	s.WaitStable(500)
	afterK := s.Screenshot()

	if initial == afterK {
		t.Errorf("expected screen to change after pressing k, but it did not\nScreen:\n%s", afterK)
	}

	s.Press("j")
	s.WaitStable(500)
	afterJ := s.Screenshot()

	if afterK == afterJ {
		t.Errorf("expected screen to change after pressing j, but it did not\nScreen:\n%s", afterJ)
	}
}
