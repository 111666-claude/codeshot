package diff

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// checkRoundTrip verifies the invariants every alignment must keep: the old
// sides of the rows rebuild the old file, the new sides rebuild the new
// file, and the line numbers run 1..N on each side.
func checkRoundTrip(t *testing.T, rows []Row, oldLines, newLines []string) {
	t.Helper()
	var oldBack, newBack []string
	oldNo, newNo := 1, 1
	for i, r := range rows {
		if r.OldNo == 0 && r.NewNo == 0 {
			t.Fatalf("row %d has neither side: %+v", i, r)
		}
		if r.OldNo > 0 {
			if r.OldNo != oldNo {
				t.Fatalf("row %d: old number %d, want %d", i, r.OldNo, oldNo)
			}
			oldNo++
			oldBack = append(oldBack, r.Old)
		}
		if r.NewNo > 0 {
			if r.NewNo != newNo {
				t.Fatalf("row %d: new number %d, want %d", i, r.NewNo, newNo)
			}
			newNo++
			newBack = append(newBack, r.New)
		}
		if r.OldDel && r.OldNo == 0 || r.NewIns && r.NewNo == 0 {
			t.Fatalf("row %d: change mark without a line: %+v", i, r)
		}
	}
	if strings.Join(oldBack, "\n") != strings.Join(oldLines, "\n") {
		t.Fatalf("old side rebuilt %q, want %q", oldBack, oldLines)
	}
	if strings.Join(newBack, "\n") != strings.Join(newLines, "\n") {
		t.Fatalf("new side rebuilt %q, want %q", newBack, newLines)
	}
}

func align(t *testing.T, oldLines, newLines []string) []Row {
	t.Helper()
	rows, err := Align(oldLines, newLines)
	if err != nil {
		t.Fatal(err)
	}
	checkRoundTrip(t, rows, oldLines, newLines)
	return rows
}

// A function moved to another spot comes out as a deletion where it stood
// and an insertion where it landed, with the untouched lines equal.
func TestAlignMovedFunction(t *testing.T) {
	old := []string{"package a", "func one() {}", "func two() {}", "func three() {}", "var done bool"}
	new := []string{"package a", "func two() {}", "func three() {}", "func one() {}", "var done bool"}
	rows := align(t, old, new)
	var dels, inss, equals int
	for _, r := range rows {
		switch {
		case r.OldDel:
			dels++
		case r.NewIns:
			inss++
		default:
			equals++
		}
	}
	if dels != 1 || inss != 1 || equals != 4 {
		t.Errorf("moving one line: %d del %d ins %d equal, want 1/1/4", dels, inss, equals)
	}
}

// Three consecutive lines replaced by four: the first three pairs sit
// across from each other, the fourth new line gets an empty old side.
func TestAlignConsecutiveChanges(t *testing.T) {
	old := []string{"head", "a", "b", "c", "tail"}
	new := []string{"head", "w", "x", "y", "z", "tail"}
	rows := align(t, old, new)
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6", len(rows))
	}
	for i, want := range [][2]string{{"a", "w"}, {"b", "x"}, {"c", "y"}, {"", "z"}} {
		r := rows[i+1]
		if r.Old != want[0] || r.New != want[1] {
			t.Errorf("row %d = %q/%q, want %q/%q", i+1, r.Old, r.New, want[0], want[1])
		}
	}
	if r := rows[4]; r.OldNo != 0 || r.NewNo != 5 || !r.NewIns {
		t.Errorf("the extra inserted line: %+v", r)
	}
	if rows[0].OldNo != 1 || rows[0].NewNo != 1 || rows[5].OldNo != 5 || rows[5].NewNo != 6 {
		t.Errorf("context numbering: first %+v last %+v", rows[0], rows[5])
	}
}

// Repeated lines must not confuse the search: dropping one of three
// identical lines is a single deletion.
func TestAlignDuplicateLines(t *testing.T) {
	rows := align(t, []string{"x", "x", "x"}, []string{"x", "x"})
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	var dels int
	for _, r := range rows {
		if r.OldDel {
			dels++
		}
	}
	if dels != 1 {
		t.Errorf("deletions = %d, want 1", dels)
	}
}

// An empty side diffs as pure insertions or deletions; two empty sides
// align to nothing at all.
func TestAlignEmptySides(t *testing.T) {
	rows := align(t, nil, []string{"a", "b"})
	if len(rows) != 2 || !rows[0].NewIns || rows[0].OldNo != 0 || rows[1].NewNo != 2 {
		t.Errorf("all-insert: %+v", rows)
	}
	rows = align(t, []string{"a"}, nil)
	if len(rows) != 1 || !rows[0].OldDel || rows[0].NewNo != 0 {
		t.Errorf("all-delete: %+v", rows)
	}
	if rows := align(t, nil, nil); len(rows) != 0 {
		t.Errorf("empty vs empty: %+v", rows)
	}
}

// A file rewritten beyond the edit-distance budget falls back to one
// delete+insert block instead of burning CPU on the search.
func TestAlignRewriteFallsBack(t *testing.T) {
	var old, new []string
	for i := 0; i < 600; i++ {
		old = append(old, fmt.Sprintf("old-%d", i))
		new = append(new, fmt.Sprintf("new-%d", i))
	}
	rows := align(t, old, new)
	if len(rows) != 600 {
		t.Fatalf("rows = %d, want 600 paired rows", len(rows))
	}
	for i, r := range rows[:3] {
		if !r.OldDel || !r.NewIns || r.OldNo != i+1 || r.NewNo != i+1 {
			t.Errorf("row %d: %+v", i, r)
		}
	}
}

func TestAlignLimits(t *testing.T) {
	many := make([]string, MaxLines+1)
	if _, err := Align(many, nil); err == nil || !strings.Contains(err.Error(), "lines") {
		t.Errorf("old over the line limit: %v", err)
	}
	if _, err := Align(nil, many); err == nil || !strings.Contains(err.Error(), "lines") {
		t.Errorf("new over the line limit: %v", err)
	}
	rows := make([]string, MaxRows+1)
	if _, err := Align(rows, rows[:1]); err == nil || !strings.Contains(err.Error(), "rows") {
		t.Errorf("over the row limit: %v", err)
	}
}

// Random shuffles of a small alphabet (lots of duplicate lines) must always
// round-trip, and never take more edits than the trivial script.
func TestAlignRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 0))
	for trial := 0; trial < 200; trial++ {
		var old, new []string
		for i := 0; i < 1+rng.IntN(60); i++ {
			old = append(old, string(rune('a'+rng.IntN(6))))
		}
		for i := 0; i < 1+rng.IntN(60); i++ {
			new = append(new, string(rune('a'+rng.IntN(6))))
		}
		rows := align(t, old, new)
		var edits int
		for _, r := range rows {
			if r.OldDel {
				edits++
			}
			if r.NewIns {
				edits++
			}
		}
		if edits > len(old)+len(new) {
			t.Fatalf("trial %d: %d edits for %d+%d lines", trial, edits, len(old), len(new))
		}
	}
}

func TestCheck(t *testing.T) {
	if err := Check("old", strings.Repeat("x", MaxInputBytes+1)); err == nil || !strings.Contains(err.Error(), "old input") {
		t.Errorf("oversize: %v", err)
	}
	if err := Check("new", "caf\xe9"); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("invalid encoding: %v", err)
	}
	if err := Check("old", "plain ascii\n"); err != nil {
		t.Errorf("valid input: %v", err)
	}
}
