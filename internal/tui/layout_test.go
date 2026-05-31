package tui

import (
	"strings"
	"testing"
)

func TestInnerWidth(t *testing.T) {
	cases := []struct {
		name string
		w    int
		want int
	}{
		{"typical", 80, 78},
		{"narrow", 10, 8},
		{"two", 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := innerWidth(tc.w); got != tc.want {
				t.Errorf("innerWidth(%d) = %d, want %d", tc.w, got, tc.want)
			}
		})
	}
}

func TestModalContentWidth(t *testing.T) {
	cases := []struct {
		name string
		w    int
		want int
	}{
		{"typical", 80, 76},
		{"chrome-exact", 4, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modalContentWidth(tc.w); got != tc.want {
				t.Errorf("modalContentWidth(%d) = %d, want %d", tc.w, got, tc.want)
			}
		})
	}
}

func TestResolveSidebarWidth(t *testing.T) {
	cases := []struct {
		name       string
		configured int
		want       int
	}{
		{"configured", 42, 42},
		{"zero falls back to default", 0, defaultSidebarWidth},
		{"negative falls back to default", -5, defaultSidebarWidth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveSidebarWidth(tc.configured); got != tc.want {
				t.Errorf("resolveSidebarWidth(%d) = %d, want %d", tc.configured, got, tc.want)
			}
		})
	}
}

func TestPreviewTermWidth(t *testing.T) {
	// total - sidebar - separator(1) - 2*border(2) = total - sidebar - 3.
	if got := previewTermWidth(100, 30); got != 67 {
		t.Errorf("previewTermWidth(100, 30) = %d, want 67", got)
	}
}

func TestFillHeight(t *testing.T) {
	lineCount := func(s string) int {
		if s == "" {
			return 1
		}
		return strings.Count(s, "\n") + 1
	}
	cases := []struct {
		name    string
		content string
		width   int
		height  int
		want    int
	}{
		{"short content padded to height", "line1\nline2\nline3", 80, 10, 10},
		{"exact fit unchanged", "line1\nline2\nline3", 80, 3, 3},
		{"zero height returns empty/one row", "", 80, 0, 1},
		{"zero width does not panic", "line1\nline2", 0, 5, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fillHeight(tc.content, tc.width, tc.height)
			if n := lineCount(got); n != tc.want {
				t.Errorf("fillHeight(%q, %d, %d): got %d lines, want %d\ngot=%q", tc.content, tc.width, tc.height, n, tc.want, got)
			}
		})
	}
}

func TestSplitColumns(t *testing.T) {
	cases := []struct {
		name      string
		total     int
		strategy  columnStrategy
		gap       int
		wantLeft  int
		wantRight int
	}{
		{
			// repo picker: half width, min 30, cap so right keeps 20.
			name:     "repopicker wide",
			total:    120,
			strategy: columnStrategy{num: 1, den: 2, min: 30, reserve: 20},
			gap:      separatorWidth,
			wantLeft: 60, wantRight: 59,
		},
		{
			name:     "repopicker min floor",
			total:    50,
			strategy: columnStrategy{num: 1, den: 2, min: 30, reserve: 20},
			gap:      separatorWidth,
			wantLeft: 30, wantRight: 19,
		},
		{
			name:     "repopicker reserve cap",
			total:    44,
			strategy: columnStrategy{num: 1, den: 2, min: 30, reserve: 20},
			gap:      separatorWidth,
			// half=22 → but min 30 floors it to 30, then reserve caps to 44-20=24.
			wantLeft: 24, wantRight: 19,
		},
		{
			// file browser: third width, min 20, no cap.
			name:     "filebrowser",
			total:    90,
			strategy: columnStrategy{num: 1, den: 3, min: 20},
			gap:      separatorWidth,
			wantLeft: 30, wantRight: 59,
		},
		{
			// branch picker: third width, min 25.
			name:     "branchpicker min",
			total:    60,
			strategy: columnStrategy{num: 1, den: 3, min: 25},
			gap:      separatorWidth,
			wantLeft: 25, wantRight: 34,
		},
		{
			// shipping feedback: 4/10, min 30, gap 5.
			name:     "shipping",
			total:    100,
			strategy: columnStrategy{num: 4, den: 10, min: 30},
			gap:      5,
			wantLeft: 40, wantRight: 55,
		},
		{
			name:     "right clamps to zero",
			total:    30,
			strategy: columnStrategy{num: 1, den: 1, min: 0},
			gap:      separatorWidth,
			wantLeft: 30, wantRight: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left, right := splitColumns(tc.total, tc.strategy, tc.gap)
			if left != tc.wantLeft || right != tc.wantRight {
				t.Errorf("splitColumns(%d, %+v, %d) = (%d, %d), want (%d, %d)",
					tc.total, tc.strategy, tc.gap, left, right, tc.wantLeft, tc.wantRight)
			}
		})
	}
}
