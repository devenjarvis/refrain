package tui

import "testing"

// TestFocusedCursor_MoveDown_WithinSection verifies that MoveDown advances the
// index within the current section as long as there are more rows.
func TestFocusedCursor_MoveDown_WithinSection(t *testing.T) {
	c := NewFocusedCursor()
	counts := [4]int{focusSectionPlanning: 3, focusSectionBuilding: 2}

	c.MoveDown(counts)
	if got := c.Index(focusSectionPlanning); got != 1 {
		t.Fatalf("first MoveDown: index=%d, want 1", got)
	}
	c.MoveDown(counts)
	if got := c.Index(focusSectionPlanning); got != 2 {
		t.Fatalf("second MoveDown: index=%d, want 2", got)
	}
}

// TestFocusedCursor_MoveDown_CrossesSections verifies that hitting the bottom
// of the current section transitions to the next non-empty section.
func TestFocusedCursor_MoveDown_CrossesSections(t *testing.T) {
	c := NewFocusedCursor()
	c.SetIndex(focusSectionPlanning, 0)
	counts := [4]int{focusSectionPlanning: 1, focusSectionBuilding: 2}

	c.MoveDown(counts)
	if c.Section() != focusSectionBuilding || c.Index(focusSectionBuilding) != 0 {
		t.Fatalf("expected hop to building[0], got section=%v idx=%d",
			c.Section(), c.Index(focusSectionBuilding))
	}
}

// TestFocusedCursor_MoveDown_SkipsEmpty verifies that empty sections are
// skipped during downward navigation.
func TestFocusedCursor_MoveDown_SkipsEmpty(t *testing.T) {
	c := NewFocusedCursor()
	counts := [4]int{focusSectionPlanning: 1, focusSectionBuilding: 0, focusSectionReview: 1}

	c.MoveDown(counts)
	if c.Section() != focusSectionReview {
		t.Fatalf("expected skip empty building, landed on %v", c.Section())
	}
}

// TestFocusedCursor_MoveUp_CrossesSections verifies upward section hopping
// lands on the last row of the previous non-empty section.
func TestFocusedCursor_MoveUp_CrossesSections(t *testing.T) {
	c := NewFocusedCursor()
	c.SetSection(focusSectionReview)
	c.SetIndex(focusSectionReview, 0)
	counts := [4]int{focusSectionPlanning: 3, focusSectionBuilding: 0, focusSectionReview: 2}

	c.MoveUp(counts)
	if c.Section() != focusSectionPlanning || c.Index(focusSectionPlanning) != 2 {
		t.Fatalf("expected planning[2] (last row of skipped-empty hop), got section=%v idx=%d",
			c.Section(), c.Index(focusSectionPlanning))
	}
}

// TestFocusedCursor_Clamp_HopsToNonEmpty verifies that when the current
// section becomes empty, Clamp moves the cursor to the next non-empty section.
func TestFocusedCursor_Clamp_HopsToNonEmpty(t *testing.T) {
	c := NewFocusedCursor()
	c.SetSection(focusSectionBuilding)
	c.SetIndex(focusSectionBuilding, 1)
	counts := [4]int{focusSectionPlanning: 0, focusSectionBuilding: 0, focusSectionReview: 1}

	c.Clamp(counts)
	if c.Section() != focusSectionReview {
		t.Fatalf("expected hop to review, got %v", c.Section())
	}
}

// TestFocusedCursor_Clamp_KeepsIndicesBounded verifies that Clamp clamps
// out-of-range indices to the last valid row of each section.
func TestFocusedCursor_Clamp_KeepsIndicesBounded(t *testing.T) {
	c := NewFocusedCursor()
	c.SetIndex(focusSectionPlanning, 99)
	c.SetIndex(focusSectionBuilding, -3)
	counts := [4]int{focusSectionPlanning: 2, focusSectionBuilding: 1}

	c.Clamp(counts)
	if got := c.Index(focusSectionPlanning); got != 1 {
		t.Fatalf("planning idx not clamped: got %d, want 1", got)
	}
	if got := c.Index(focusSectionBuilding); got != 0 {
		t.Fatalf("building idx not clamped: got %d, want 0", got)
	}
}

// TestFocusedCursor_JumpTo_SetsBoth verifies JumpTo updates both section and
// per-section index in one call.
func TestFocusedCursor_JumpTo_SetsBoth(t *testing.T) {
	c := NewFocusedCursor()
	c.JumpTo(focusSectionShipping, 3)
	if c.Section() != focusSectionShipping || c.Index(focusSectionShipping) != 3 {
		t.Fatalf("expected (shipping, 3), got (%v, %d)", c.Section(), c.Index(focusSectionShipping))
	}
}
