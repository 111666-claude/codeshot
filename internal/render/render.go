// Package render runs the whole pipeline: prepare the text, highlight or
// colorize it, lay out the card, and serialize it as SVG or rasterize it
// to PNG.
package render

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/securekomodo/codeshot/internal/diff"
	"github.com/securekomodo/codeshot/internal/fonts"
	"github.com/securekomodo/codeshot/internal/highlight"
	"github.com/securekomodo/codeshot/internal/layout"
	"github.com/securekomodo/codeshot/internal/preset"
	"github.com/securekomodo/codeshot/internal/raster"
	"github.com/securekomodo/codeshot/internal/settings"
	"github.com/securekomodo/codeshot/internal/svg"
	"github.com/securekomodo/codeshot/internal/theme"
)

// Card is a laid-out image ready to be serialized.
type Card struct {
	Layout *layout.Layout
	faces  []*fonts.Face
}

// Build validates the settings and lays out src as a card.
func Build(s settings.Settings, src string) (*Card, error) {
	if s.Preset.Render == preset.Split {
		return nil, errors.New("the side-by-side preset compares two inputs; use BuildDiff")
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	th, _ := theme.Get(s.Theme)
	bd, _ := theme.GetBackdrop(s.Backdrop)
	in, faces, err := resolveFonts(s)
	if err != nil {
		return nil, err
	}
	in.Settings, in.Theme, in.Backdrop = s, th, bd

	lines := layout.PrepareText(src)
	if s.Preset.Render == preset.Prism {
		in.Lines, err = highlight.Highlight(lines, s.Language, th)
		if err != nil {
			return nil, fmt.Errorf("highlight: %w", err)
		}
	} else {
		opts := highlight.Options{Theme: th, Prompt: s.Prompt}
		if s.Chrome == settings.ChromeKali {
			opts.PromptColor, opts.PromptStyle = highlight.KaliBlue, highlight.PromptStyleKali
		}
		in.Lines = highlight.Colorize(s.Preset.Render, lines, opts)
	}

	if in.Image, err = loadWatermark(s); err != nil {
		return nil, err
	}
	L, err := layout.Compute(in)
	if err != nil {
		return nil, err
	}
	return &Card{Layout: L, faces: faces}, nil
}

// BuildDiff validates the settings and lays out old/new as a side-by-side
// comparison card: the two files lined up by diff.Align, each pane with its
// own line numbers and syntax highlighting. Both inputs are checked against
// the diff package's size and encoding limits first.
func BuildDiff(s settings.Settings, oldSrc, newSrc string) (*Card, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if err := diff.Check("old", oldSrc); err != nil {
		return nil, err
	}
	if err := diff.Check("new", newSrc); err != nil {
		return nil, err
	}
	th, _ := theme.Get(s.Theme)
	bd, _ := theme.GetBackdrop(s.Backdrop)
	in, faces, err := resolveFonts(s)
	if err != nil {
		return nil, err
	}
	in.Settings, in.Theme, in.Backdrop = s, th, bd

	oldLines := prepareSide(oldSrc)
	newLines := prepareSide(newSrc)
	hlOld, err := highlight.Highlight(oldLines, s.Language, th)
	if err != nil {
		return nil, fmt.Errorf("highlight: %w", err)
	}
	hlNew, err := highlight.Highlight(newLines, s.Language, th)
	if err != nil {
		return nil, fmt.Errorf("highlight: %w", err)
	}
	rows, err := diff.Align(oldLines, newLines)
	if err != nil {
		return nil, err
	}
	d := &layout.DiffInput{Rows: make([]layout.DiffRow, 0, len(rows))}
	for _, r := range rows {
		var dr layout.DiffRow
		if r.OldNo > 0 {
			dr.Left = layout.DiffSide{Line: hlOld[r.OldNo-1], No: r.OldNo, Changed: r.OldDel}
		}
		if r.NewNo > 0 {
			dr.Right = layout.DiffSide{Line: hlNew[r.NewNo-1], No: r.NewNo, Changed: r.NewIns}
		}
		d.Rows = append(d.Rows, dr)
	}
	in.Diff = d

	if in.Image, err = loadWatermark(s); err != nil {
		return nil, err
	}
	L, err := layout.Compute(in)
	if err != nil {
		return nil, err
	}
	return &Card{Layout: L, faces: faces}, nil
}

// prepareSide normalizes one side of a comparison. An empty file has no
// lines at all, so it diffs as pure insertions or deletions.
func prepareSide(src string) []string {
	if src == "" {
		return nil
	}
	return layout.PrepareText(src)
}

// resolveFonts loads the fonts a card needs and starts its layout input.
func resolveFonts(s settings.Settings) (layout.Input, []*fonts.Face, error) {
	var in layout.Input
	code, err := fonts.Load(s.Font)
	if err != nil {
		return in, nil, err
	}
	fallbacks, err := fonts.Fallbacks()
	if err != nil {
		return in, nil, err
	}
	code.SetFallbacks(fallbacks...)
	faces := append([]*fonts.Face{code}, fallbacks...)
	if bold, err := fonts.Bold(code.ID); err != nil {
		return in, nil, err
	} else if bold != nil {
		faces = append(faces, bold)
	}
	in.Code = code
	if s.ShowChrome || s.Label != "" {
		inter, err := fonts.Inter()
		if err != nil {
			return in, nil, err
		}
		in.Title = inter
		faces = append(faces, inter)
	}
	if s.ShowChrome && s.Badge() != settings.BadgeNone && code.ID != "jetbrains" {
		// The badge always uses the UI mono font, JetBrains Mono.
		mono, err := fonts.Load("jetbrains")
		if err != nil {
			return in, nil, err
		}
		in.Badge = mono
		faces = append(faces, mono)
	}
	return in, faces, nil
}

// loadWatermark reads the image named by the settings, or returns nil for
// the none and swirl watermarks.
func loadWatermark(s settings.Settings) (*layout.WatermarkImage, error) {
	if s.Watermark == "" || s.Watermark == settings.WatermarkNone || s.Watermark == settings.WatermarkSwirl {
		return nil, nil
	}
	return loadImage(s.Watermark, s.WatermarkOpacity)
}

// loadImage reads a PNG, JPEG or SVG file for use as a watermark.
func loadImage(path string, opacity float64) (*layout.WatermarkImage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("watermark: %w", err)
	}
	img := &layout.WatermarkImage{Data: data, Opacity: opacity}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".svg":
		img.Mime = "image/svg+xml"
		img.W, img.H = svgSize(data)
	default:
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("watermark %s: %w (PNG, JPEG or SVG)", path, err)
		}
		img.Mime, img.W, img.H = "image/"+format, cfg.Width, cfg.Height
	}
	return img, nil
}

var svgDims = regexp.MustCompile(`(?s)<svg[^>]*?\s(width|height)="([0-9.]+)[a-z]*"[^>]*?\s(width|height)="([0-9.]+)[a-z]*"`)

// svgSize reads an SVG's width and height attributes (1:1 if absent).
func svgSize(data []byte) (int, int) {
	if m := svgDims.FindSubmatch(data); m != nil {
		a, _ := strconv.ParseFloat(string(m[2]), 64)
		b, _ := strconv.ParseFloat(string(m[4]), 64)
		if string(m[1]) == "width" {
			return int(a), int(b)
		}
		return int(b), int(a)
	}
	return 1, 1
}

// SVG serializes the card at 1x. With embed set, the code and title fonts
// are inlined as @font-face data URIs (the fallback faces are not: browsers
// have their own symbol and emoji fonts, and they would add 2 MB).
func (c *Card) SVG(embed bool) []byte {
	o := svg.Options{}
	if embed {
		for _, f := range c.faces {
			if f.ID != "fallback" {
				o.EmbedFonts = append(o.EmbedFonts, f)
			}
		}
	}
	return svg.Render(c.Layout, o)
}

// PNG rasterizes the card at the given pixel ratio.
func (c *Card) PNG(scale int) ([]byte, error) {
	if scale < 1 {
		scale = 1
	}
	doc := svg.Render(c.Layout, svg.Options{Scale: float64(scale), FastShadow: true})
	data := make([][]byte, len(c.faces))
	for i, f := range c.faces {
		data[i] = f.Data
	}
	w := uint32(c.Layout.W * float64(scale))
	h := uint32(c.Layout.H * float64(scale))
	return raster.PNG(doc, data, w, h)
}

// Size returns the PNG dimensions at the given scale.
func (c *Card) Size(scale int) (w, h int) {
	return int(c.Layout.W) * scale, int(c.Layout.H) * scale
}
