package render

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/securekomodo/codeshot/internal/diff"
	"github.com/securekomodo/codeshot/internal/highlight"
	"github.com/securekomodo/codeshot/internal/preset"
	"github.com/securekomodo/codeshot/internal/settings"
	"github.com/securekomodo/codeshot/internal/theme"
)

// Every preset with its sample, in every theme, must lay out, serialize and
// rasterize; the PNG must decode to the declared size.
func TestEveryPresetEveryTheme(t *testing.T) {
	if testing.Short() {
		t.Skip("rasterizing 200+ cards")
	}
	dir := t.TempDir()
	themes := theme.IDs()
	for i, p := range preset.All {
		sample, err := p.Sample()
		if err != nil {
			t.Fatal(err)
		}
		for j, th := range themes {
			// Rasterize each theme once and each preset once; SVG for the rest.
			s := settings.Defaults(p)
			s.Theme = th
			s.Backdrop = theme.BackdropIDs()[(i+j)%len(theme.BackdropIDs())]
			var c *Card
			if p.Render == preset.Split {
				oldSample, newSample, err := p.SamplePair()
				if err != nil {
					t.Fatal(err)
				}
				c, err = BuildDiff(s, oldSample, newSample)
			} else {
				c, err = Build(s, sample)
			}
			if err != nil {
				t.Fatalf("%s/%s: %v", p.Key, th, err)
			}
			doc := c.SVG(false)
			if !bytes.HasPrefix(doc, []byte("<svg")) || !bytes.HasSuffix(bytes.TrimSpace(doc), []byte("</svg>")) {
				t.Errorf("%s/%s: malformed svg", p.Key, th)
			}
			if i != j && j != 0 {
				continue
			}
			data, err := c.PNG(2)
			if err != nil {
				t.Fatalf("%s/%s: %v", p.Key, th, err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s/%s: %v", p.Key, th, err)
			}
			w, h := c.Size(2)
			if b := img.Bounds(); b.Dx() != w || b.Dy() != h {
				t.Errorf("%s/%s: png %v want %dx%d", p.Key, th, b, w, h)
			}
			if err := os.WriteFile(filepath.Join(dir, p.Key+"-"+th+".png"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestTransparentAndSolidCorners(t *testing.T) {
	p, _ := preset.Get("code")
	s := settings.Defaults(p)
	s.Backdrop = "none"
	c, err := Build(s, "x")
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.PNG(1)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := png.Decode(bytes.NewReader(data))
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Errorf("none backdrop: corner alpha %d want 0", a)
	}
	s.Backdrop = "ink"
	c, _ = Build(s, "x")
	data, _ = c.PNG(1)
	img, _ = png.Decode(bytes.NewReader(data))
	r, g, b, _ := img.At(0, 0).RGBA()
	if r>>8 != 0x0b || g>>8 != 0x0d || b>>8 != 0x10 {
		t.Errorf("ink backdrop: corner = %x %x %x", r>>8, g>>8, b>>8)
	}
	if !strings.Contains(string(c.SVG(true)), "@font-face") {
		t.Error("SVG(true) should embed fonts")
	}
}

func TestBuildErrors(t *testing.T) {
	p, _ := preset.Get("code")
	s := settings.Defaults(p)
	s.Font = "nope"
	if _, err := Build(s, "x"); err == nil {
		t.Error("unknown font should fail")
	}
	s = settings.Defaults(p)
	s.Theme = "nope"
	if _, err := Build(s, "x"); err == nil {
		t.Error("unknown theme should fail")
	}
	sp, _ := preset.Get("side-by-side")
	if _, err := Build(settings.Defaults(sp), "x"); err == nil {
		t.Error("the side-by-side preset should refuse a single input")
	}
}

// The side-by-side card: both files keep their line numbers and syntax
// highlighting, changes are marked on their side, and the alignment,
// encoding and size limits give clear errors.
func TestBuildDiff(t *testing.T) {
	p, _ := preset.Get("side-by-side")
	s := settings.Defaults(p)
	s.Language = "go"

	before := "package a\n\nfunc one() int {\n\treturn 1\n}\n"
	after := "package a\n\nfunc one() int {\n\treturn 2\n}\n\nfunc two() {}\n"
	c, err := BuildDiff(s, before, after)
	if err != nil {
		t.Fatal(err)
	}
	L := c.Layout
	// Both panes draw every row, and both gutters carry source line numbers.
	if len(L.Rows) != 2*7 {
		t.Errorf("rows = %d, want 2 per aligned row", len(L.Rows))
	}
	var gutter []string
	for _, g := range L.Gutter {
		gutter = append(gutter, g.Text)
	}
	for _, want := range []string{"1", "4", "6", "7"} {
		if !slices.Contains(gutter, want) {
			t.Errorf("gutter %v is missing line number %s", gutter, want)
		}
	}
	// The changed return line is tinted on both sides; the added function
	// only on the right. Plus the divider hairline.
	var delBands, addBands, other int
	for _, b := range L.Bands {
		switch b.Fill.Hex {
		case highlight.Red:
			delBands++
		case highlight.Teal:
			addBands++
		default:
			other++
		}
	}
	if delBands == 0 || addBands == 0 || other == 0 {
		t.Errorf("bands: %d deleted, %d added, %d other", delBands, addBands, other)
	}
	// Syntax highlighting survived: the keyword "func" is colored, not plain.
	var colored bool
	for _, r := range L.Rows {
		for _, sp := range r.Spans {
			if sp.Text == "func" && sp.Color != "" {
				colored = true
			}
		}
	}
	if !colored {
		t.Error("changed lines should keep their syntax colors")
	}
	if _, err := c.PNG(1); err != nil {
		t.Fatal(err)
	}

	// CRLF against LF is no change at all: only the divider band remains.
	c, err = BuildDiff(s, "a\r\nb\r\n", "a\nb\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Layout.Bands) != 1 {
		t.Errorf("newline-only difference should mark nothing, got %d bands", len(c.Layout.Bands))
	}

	// An empty side is pure insertions; two empty sides are an empty card.
	c, err = BuildDiff(s, "", "x\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Layout.Rows) != 2 || len(c.Layout.Gutter) != 1 {
		t.Errorf("empty old side: rows %d gutter %d", len(c.Layout.Rows), len(c.Layout.Gutter))
	}
	if _, err := BuildDiff(s, "", ""); err != nil {
		t.Errorf("two empty files should still render: %v", err)
	}

	// Invalid encoding and oversize inputs fail with a named side.
	if _, err := BuildDiff(s, "caf\xe9", "x"); err == nil || !strings.Contains(err.Error(), "old input") {
		t.Errorf("invalid encoding: %v", err)
	}
	if _, err := BuildDiff(s, "x", strings.Repeat("y", diff.MaxInputBytes+1)); err == nil || !strings.Contains(err.Error(), "new input") {
		t.Errorf("oversize: %v", err)
	}
}
