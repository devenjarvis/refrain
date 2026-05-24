//go:build e2e

package e2e

import (
	"path/filepath"
	"testing"
)

func TestDashboardRendersOnStartup(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	s.AssertScreenContains("FOCUS")

	s.AssertScreenContains("navigate")
	s.AssertScreenContains("new session")
	s.AssertScreenContains("quit")

	s.AssertScreenContains(filepath.Base(s.repoDir))
}

func TestNavigationJK(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	s.Press("n")
	s.WaitForText("back", 10000)

	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	s.Press("n")
	s.WaitForText("back", 10000)

	s.Press("Escape")
	s.WaitForText("navigate", 10000)

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
