package layout

import (
	"strings"
	"testing"

	"github.com/securekomodo/codeshot/internal/highlight"
	"github.com/securekomodo/codeshot/internal/preset"
)

// diffFixture builds an Input for the side-by-side preset from aligned rows.
func diffFixture(t *testing.T, rows ...DiffRow) Input {
	t.Helper()
	in, s := fixture(t, "side-by-side")
	if s.Preset.Render != preset.Split {
		t.Fatal("the side-by-side preset should have the split render mode")
	}
	in.Diff = &DiffInput{Rows: rows}
	return in
}

func side(text string, no int, changed bool) DiffSide {
	if no == 0 {
		return DiffSide{}
	}
	return DiffSide{Line: highlight.Line{{Text: text}}, No: no, Changed: changed}
}

func TestComputeDiff(t *testing.T) {
	in := diffFixture(t,
		DiffRow{Left: side("same", 1, false), Right: side("same", 1, false)},
		DiffRow{Left: side("old", 2, true), Right: side("new", 2, true)},
		DiffRow{Left: side("gone", 3, true)},
		DiffRow{Right: side("added", 3, true)},
	)
	L, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	// Two panes per aligned row, the right pane to the right of the left.
	if len(L.Rows) != 8 {
		t.Fatalf("rows = %d, want 8", len(L.Rows))
	}
	for i := 0; i < len(L.Rows); i += 2 {
		if L.Rows[i].Y != L.Rows[i+1].Y || L.Rows[i].X >= L.Rows[i+1].X {
			t.Errorf("row pair %d not aligned: %+v / %+v", i/2, L.Rows[i], L.Rows[i+1])
		}
	}
	if L.Rows[0].X != L.Card.X+CodePadX+in.Code.Advance('0', 15)+GutterPad {
		t.Errorf("left pane x = %v (gutter should be one digit wide)", L.Rows[0].X)
	}
	// Both gutters carry their own source line numbers; the empty side of
	// rows 3 and 4 shows no number.
	var nums []string
	for _, g := range L.Gutter {
		nums = append(nums, g.Text)
	}
	want := []string{"1", "1", "2", "2", "3", "3"}
	if strings.Join(nums, ",") != strings.Join(want, ",") {
		t.Errorf("gutter = %v, want %v", nums, want)
	}
	// Bands: the changed pair is tinted on both sides, the unpaired
	// deletion on the left, the insertion on the right, plus the divider.
	var del, add, gap, divider int
	for _, b := range L.Bands {
		switch b.Fill.Hex {
		case highlight.Red:
			del++
		case highlight.Teal:
			add++
		case "#ffffff", "#000000":
			if b.Box.W == 1 {
				divider++
			} else {
				gap++
			}
		}
	}
	if del != 2 || add != 2 || gap != 2 || divider != 1 {
		t.Errorf("bands: %d del %d add %d gap %d divider, want 2/2/2/1", del, add, gap, divider)
	}
	// A band spans its pane, from the card edge to the divider.
	for _, b := range L.Bands {
		if b.Fill.Hex == highlight.Red && b.Box.X != L.Card.X {
			t.Errorf("left band should start at the card edge: %+v", b.Box)
		}
	}
	// Row height and card height match the single-pane layout.
	if L.LineHeight != 24 || L.Card.H != BarHeight+CodePadTop+4*24+CodePadBottom {
		t.Errorf("lh %v card %v", L.LineHeight, L.Card.H)
	}
}

// A long line wraps inside its pane; the other side gets blank continuation
// rows so the two stay aligned, and only the first row shows a number.
func TestComputeDiffWrapsLongLines(t *testing.T) {
	long := strings.Repeat("word ", 30)
	in := diffFixture(t,
		DiffRow{Left: side("short", 1, false), Right: side("short", 1, false)},
		DiffRow{Left: side(long, 2, true), Right: side("tiny", 2, true)},
		DiffRow{Left: side("after", 3, false), Right: side("after", 3, false)},
	)
	s := in.Settings
	s.Wrap = 20 // per pane
	in.Settings = s
	L, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	// The long line takes several rows; the right side pads along, and the
	// line after the long one lines up on both sides.
	if len(L.Rows) < 2*5 || len(L.Rows)%2 != 0 {
		t.Fatalf("rows = %d", len(L.Rows))
	}
	var leftNums, rightNums []string
	for i, g := range L.Gutter {
		if i%2 == 0 {
			leftNums = append(leftNums, g.Text)
		} else {
			rightNums = append(rightNums, g.Text)
		}
	}
	if strings.Join(leftNums, ",") != "1,2,3" || strings.Join(rightNums, ",") != "1,2,3" {
		t.Errorf("gutter numbers: left %v right %v (continuations should be blank)", leftNums, rightNums)
	}
	// The last pair of rows is the trailing "after" line, aligned.
	last := L.Rows[len(L.Rows)-2:]
	if last[0].Spans.Text() != "after" || last[1].Spans.Text() != "after" || last[0].Y != last[1].Y {
		t.Errorf("trailing row misaligned: %+v", last)
	}
	// Every wrapped row of the changed line keeps its band.
	var del int
	for _, b := range L.Bands {
		if b.Fill.Hex == highlight.Red {
			del++
		}
	}
	if del < 3 {
		t.Errorf("the wrapped deleted line should be tinted on every row, got %d", del)
	}
	// The card stays within the wrap's width.
	cell := in.Code.Advance('0', 15)
	if w := 2*CodePadX + 2*(cell+GutterPad) + PaneGap + 2*20*cell; L.Card.W > w+1 {
		t.Errorf("card %v wider than the wrap allows (~%v)", L.Card.W, w)
	}
}

// Duplicate lines on both sides align without inventing changes.
func TestComputeDiffDuplicates(t *testing.T) {
	in := diffFixture(t,
		DiffRow{Left: side("x", 1, false), Right: side("x", 1, false)},
		DiffRow{Left: side("x", 2, false), Right: side("x", 2, false)},
	)
	L, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range L.Bands {
		if b.Fill.Hex == highlight.Red || b.Fill.Hex == highlight.Teal {
			t.Errorf("identical sides should have no change bands: %+v", b)
		}
	}
}

// No rows at all (two empty files) still lays out a card.
func TestComputeDiffEmpty(t *testing.T) {
	in := diffFixture(t)
	L, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(L.Rows) != 0 || L.Card.H != BarHeight+CodePadTop+CodePadBottom {
		t.Errorf("empty diff: rows %d card %v", len(L.Rows), L.Card.H)
	}
}
