package tui

import "testing"

func TestClampedMove(t *testing.T) {
	tests := []struct {
		name          string
		cur, delta, n int
		want          int
	}{
		{"down within range", 1, 1, 5, 2},
		{"up within range", 3, -1, 5, 2},
		{"up clamps at zero", 0, -1, 5, 0},
		{"down clamps at last", 4, 1, 5, 4},
		{"empty list yields zero", 0, 1, 0, 0},
		{"empty list up yields zero", 0, -1, 0, 0},
		{"out-of-range cursor pulled to last", 9, 1, 5, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampedMove(tc.cur, tc.delta, tc.n); got != tc.want {
				t.Errorf("clampedMove(%d, %d, %d) = %d, want %d", tc.cur, tc.delta, tc.n, got, tc.want)
			}
		})
	}
}
