package model

import "testing"

func TestVisibleIndexForContentLine_strideMatchesBubbles(t *testing.T) {
	const itemH, gap = 2, 1
	if itemH+gap != 3 {
		t.Fatalf("expected vertical stride 3 lines per item slot, got %d", itemH+gap)
	}

	cases := []struct {
		line   int
		start  int
		end    int
		want   int
		wantOK bool
	}{
		{line: -1, start: 0, end: 3, want: -1, wantOK: false},
		{line: 0, start: 0, end: 3, want: 0, wantOK: true},
		{line: 1, start: 0, end: 3, want: 0, wantOK: true},
		{line: 2, start: 0, end: 3, want: 1, wantOK: true}, // blank row between item 0 and 1
		{line: 3, start: 0, end: 3, want: 1, wantOK: true},
		{line: 4, start: 0, end: 3, want: 1, wantOK: true},
		{line: 5, start: 0, end: 3, want: 2, wantOK: true},
		{line: 6, start: 0, end: 3, want: 2, wantOK: true},
		{line: 99, start: 0, end: 3, want: 2, wantOK: true}, // past last row → last item
		{line: 0, start: 2, end: 4, want: 2, wantOK: true},
	}
	for _, tc := range cases {
		got, ok := visibleIndexForContentLine(tc.line, tc.start, tc.end, itemH, gap)
		if ok != tc.wantOK || got != tc.want {
			t.Fatalf("line=%d [%d,%d): got (%d,%v) want (%d,%v)",
				tc.line, tc.start, tc.end, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestVisibleIndexForContentLine_gapTwoDriftsVsBubbles(t *testing.T) {
	// Four items: with correct gap=1, first line of item 3 is at offset 9.
	// Old wrong gap=2 advances too fast and maps that row to item 2 instead.
	gotWrong, _ := visibleIndexForContentLine(9, 0, 4, 2, 2)
	if gotWrong != 2 {
		t.Fatalf("wrong gap=2: expected index 2 at line 9 (stale), got %d", gotWrong)
	}
	gotRight, _ := visibleIndexForContentLine(9, 0, 4, 2, 1)
	if gotRight != 3 {
		t.Fatalf("gap=1: line 9 should be item 3, got %d", gotRight)
	}
}
