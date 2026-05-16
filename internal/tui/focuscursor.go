package tui

// FocusedCursor tracks the pipeline cursor: which fullscreen-focus section
// (Planning / Building / Reviewing / Shipping) the cursor is on, plus a
// per-section index. Replaces the four separate index fields and panic-guarded
// section-pointer helpers that previously sprawled across the App struct.
//
// The cursor is intentionally a pure value type with no closures: callers pass
// per-section row counts to navigation methods. This keeps the type cheap to
// copy (App's Update returns the model by value) and trivial to test in
// isolation without instantiating a dashboard.
type FocusedCursor struct {
	indices [4]int // indexed by focusSection
	section focusSection
}

// NewFocusedCursor returns a cursor positioned at the start of Planning.
func NewFocusedCursor() FocusedCursor {
	return FocusedCursor{section: focusSectionPlanning}
}

// Section returns the section the cursor is currently on.
func (c FocusedCursor) Section() focusSection { return c.section }

// Index returns the cursor index within the given section.
func (c FocusedCursor) Index(s focusSection) int { return c.indices[s] }

// SetSection moves the cursor to a section without changing per-section indices.
func (c *FocusedCursor) SetSection(s focusSection) { c.section = s }

// SetIndex sets the index for the given section, leaving the active section
// unchanged. Bounds are not enforced here; callers should clamp first if the
// underlying list may have shrunk.
func (c *FocusedCursor) SetIndex(s focusSection, idx int) { c.indices[s] = idx }

// JumpTo moves the cursor to (section, index) in one call. Used by click
// handlers and post-create landing logic.
func (c *FocusedCursor) JumpTo(s focusSection, idx int) {
	c.section = s
	c.indices[s] = idx
}

// MoveUp moves the cursor up one row. When at the top of the current section,
// it transitions to the last row of the nearest non-empty earlier section.
func (c *FocusedCursor) MoveUp(counts [4]int) {
	if c.indices[c.section] > 0 {
		c.indices[c.section]--
		return
	}
	order := focusSectionsInOrder()
	cur := -1
	for i, s := range order {
		if s == c.section {
			cur = i
			break
		}
	}
	for i := cur - 1; i >= 0; i-- {
		s := order[i]
		if counts[s] > 0 {
			c.section = s
			c.indices[s] = counts[s] - 1
			return
		}
	}
}

// MoveDown moves the cursor down one row. When at the bottom of the current
// section, it transitions to the first row of the nearest non-empty later
// section.
func (c *FocusedCursor) MoveDown(counts [4]int) {
	if c.indices[c.section] < counts[c.section]-1 {
		c.indices[c.section]++
		return
	}
	order := focusSectionsInOrder()
	cur := -1
	for i, s := range order {
		if s == c.section {
			cur = i
			break
		}
	}
	for i := cur + 1; i < len(order); i++ {
		s := order[i]
		if counts[s] > 0 {
			c.section = s
			c.indices[s] = 0
			return
		}
	}
}

// Clamp keeps per-section indices and the cursor section in valid ranges as
// the underlying lists change (sessions transition phases, etc.). When the
// cursor's current section becomes empty, the cursor falls through to the
// next non-empty section in render order so it stays on a visible row.
func (c *FocusedCursor) Clamp(counts [4]int) {
	for i := range c.indices {
		n := counts[i]
		if n <= 0 {
			c.indices[i] = 0
			continue
		}
		if c.indices[i] >= n {
			c.indices[i] = n - 1
		}
		if c.indices[i] < 0 {
			c.indices[i] = 0
		}
	}
	if counts[c.section] > 0 {
		return
	}
	for _, s := range focusSectionsInOrder() {
		if counts[s] > 0 {
			c.section = s
			return
		}
	}
	c.section = focusSectionPlanning
}
