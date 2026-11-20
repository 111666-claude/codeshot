package layout

import (
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/securekomodo/codeshot/internal/highlight"
	"github.com/securekomodo/codeshot/internal/settings"
	"github.com/securekomodo/codeshot/internal/theme"
)

// PaneGap is the space between the two panes of the side-by-side preset,
// in CSS px; the divider hairline sits in its middle.
const PaneGap = 24

// MaxVisualRows caps the rows a side-by-side card may have after wrapping,
// so a huge input cannot turn into an unbounded image (and raster job).
const MaxVisualRows = 2000

// DiffSide is one side of an aligned diff row: the highlighted line, its
// 1-based source line number (0 when the side has no line here), and whether
// the line changed (deleted on the left, inserted on the right).
type DiffSide struct {
	Line    highlight.Line
	No      int
	Changed bool
}

// DiffRow is one aligned pair of lines.
type DiffRow struct {
	Left, Right DiffSide
}

// DiffInput is the aligned pair of files the side-by-side preset draws.
type DiffInput struct {
	Rows []DiffRow
}

// computeDiff lays out the side-by-side preset: two panes, each with its
// own line-number gutter and syntax highlighting, aligned row by row, with
// deleted lines tinted red on the left and inserted ones teal on the right.
func computeDiff(in Input) (*Layout, error) {
	s := in.Settings
	d := in.Diff
	size := float64(s.FontSize)
	lh := math.Round(size * 1.6)
	light := in.Theme.Light()

	plain, err := theme.ParseColor(in.Theme.Plain.Color)
	if err != nil {
		if light {
			plain = theme.Color{Hex: "#1a1a1a", Alpha: 1}
		} else {
			plain = theme.Color{Hex: "#e6e8eb", Alpha: 1}
		}
	}
	window, err := theme.ParseColor(in.Theme.Window)
	if err != nil {
		return nil, fmt.Errorf("theme %s: %w", in.Theme.ID, err)
	}
	titleFont, badgeFont := in.Title, in.Badge
	if titleFont == nil {
		titleFont = in.Code
	}
	if badgeFont == nil {
		badgeFont = in.Code
	}

	cell := in.Code.Advance('0', size)
	maxNo := 1
	for _, r := range d.Rows {
		maxNo = max(maxNo, r.Left.No, r.Right.No)
	}
	digits := len(strconv.Itoa(maxNo))
	gutterW := 0.0
	if s.ShowLineNumbers {
		gutterW = float64(digits)*cell + GutterPad
	}

	var badge *Badge
	if s.ShowChrome {
		badge = buildBadge(s, badgeFont, light)
	}
	badgeW := 0.0
	if badge != nil {
		badgeW = badge.Box.W
	}

	// The fixed part of a row's width: padding, both gutters and the gap
	// between the panes. What remains is split evenly between the panes.
	frameW := 2*CodePadX + 2*gutterW + PaneGap
	colsAt := func(width float64) int {
		return max(1, int((width-frameW)/cell/2))
	}
	cols := 0
	capW := float64(MaxCardWidth)
	switch {
	case s.Wrap > 0:
		cols = s.Wrap
	case s.Width > 0:
		cols = colsAt(float64(s.Width))
	case s.MaxWidth > 0:
		cols = colsAt(float64(s.MaxWidth))
		capW = float64(s.MaxWidth)
	}

	// Wrap each side of every row on its own, then pad the shorter side so
	// the two stay aligned: a wrapped line's continuation rows sit across
	// from the other side's rows (or from blanks).
	type vrow struct {
		left, right highlight.Line
		leftNo      int
		rightNo     int
		leftBand    theme.Color
		rightBand   theme.Color
	}
	add := theme.Color{Hex: highlight.Teal, Alpha: 0.13}
	del := theme.Color{Hex: highlight.Red, Alpha: 0.13}
	gap := theme.Color{Hex: "#ffffff", Alpha: 0.04}
	if light {
		add.Alpha, del.Alpha = 0.18, 0.18
		gap = theme.Color{Hex: "#000000", Alpha: 0.05}
	}
	wrapSide := func(l highlight.Line) []highlight.Line {
		if cols <= 0 || utf8.RuneCountInString(l.Text()) <= cols {
			return []highlight.Line{l}
		}
		return wrapLine(l, cols)
	}
	var vrows []vrow
	for _, r := range d.Rows {
		ls, rs := wrapSide(r.Left.Line), wrapSide(r.Right.Line)
		for i := 0; i < max(len(ls), len(rs)); i++ {
			var v vrow
			if i < len(ls) {
				v.left = ls[i]
				if i == 0 {
					v.leftNo = r.Left.No
				}
			}
			if i < len(rs) {
				v.right = rs[i]
				if i == 0 {
					v.rightNo = r.Right.No
				}
			}
			switch {
			case r.Left.Changed:
				v.leftBand = del
			case r.Right.Changed && r.Left.No == 0:
				v.leftBand = gap // the empty side of an insertion
			}
			switch {
			case r.Right.Changed:
				v.rightBand = add
			case r.Left.Changed && r.Right.No == 0:
				v.rightBand = gap // the empty side of a deletion
			}
			vrows = append(vrows, v)
		}
	}
	if len(vrows) > MaxVisualRows {
		return nil, fmt.Errorf("the wrapped comparison has %d rows, over the %d limit (raise --max-width or compare a smaller range)", len(vrows), MaxVisualRows)
	}

	maxPane := 0.0
	for _, v := range vrows {
		for _, side := range []highlight.Line{v.left, v.right} {
			w := 0.0
			for _, sp := range side {
				w += in.Code.Width(sp.Text, size)
			}
			maxPane = math.Max(maxPane, w)
		}
	}

	// The title is centered, so it needs the same clearance on both sides:
	// enough for the dots on the left and the badge on the right.
	inset := math.Max(TitleInset, BarPadX+badgeW+BadgeGap)
	kali := s.ShowChrome && s.Chrome == settings.ChromeKali
	if kali {
		inset = 3*KaliButtonGap + KaliButtonInset // the controls sit on the right, the title is centered
	}
	windows := s.ShowChrome && s.Chrome == settings.ChromeWindows
	cardW := math.Ceil(frameW + 2*maxPane)
	if s.Width > 0 {
		cardW = float64(s.Width)
	} else {
		minW := float64(MinCardWidth)
		if s.ShowChrome && !windows {
			titleW := math.Min(titleFont.Width(s.DisplayTitle(), TitleSize), MaxTitleRoom)
			minW = math.Ceil(2*inset + titleW)
		}
		if windows {
			// icon, title and the three control cells
			titleW := math.Min(titleFont.Width(s.DisplayTitle(), WinTitleSize), MaxTitleRoom)
			minW = math.Ceil(WinIconInset + WinIconSize + WinTitleGap + titleW + WinTitleGap + 3*WinButtonW + badgeW)
		}
		if kali {
			menuW := float64(KaliMenuPadX)
			for _, item := range KaliMenu {
				menuW += titleFont.Width(item, KaliMenuSize) + KaliMenuGap
			}
			minW = math.Max(minW, math.Ceil(menuW+BarPadX+badgeW))
		}
		cardW = math.Min(math.Max(cardW, minW), capW)
	}
	// Slack from a wider minimum (or a fixed --width) is shared by the panes.
	paneW := (cardW - frameW) / 2

	barH := 0.0
	if s.ShowChrome {
		switch s.Chrome {
		case settings.ChromeWindows:
			barH = WinTitleHeight
		case settings.ChromeKali:
			barH = KaliTitleHeight + KaliMenuHeight
		default:
			barH = BarHeight
			if badge != nil {
				barH = BarWithBadge
			}
		}
	}
	// Whole pixels keep the PNG size exact at every scale (the badge bar is 50.5px).
	cardH := math.Ceil(barH + CodePadTop + float64(len(vrows))*lh + CodePadBottom)
	pad := float64(s.Padding)
	// A caption takes the place of the top padding: the space above the
	// window is whatever is larger, the padding or the pill with some air.
	top := pad
	if s.Label != "" {
		top = math.Max(pad, labelBand(float64(s.LabelSize)))
	}

	L := &Layout{
		W: cardW + 2*pad, H: cardH + pad + top,
		Backdrop: in.Backdrop, ShowBackground: s.ShowBackground && !in.Backdrop.Transparent,
		Card:   Rect{pad, top, cardW, cardH},
		Radius: float64(s.Radius), CardRadius: float64(s.CardRadius),
		Window: window, Shadow: s.Shadow, Light: light,
		Font: in.Code, FontSize: size, LineHeight: lh, Plain: plain,
	}

	if s.ShowChrome && s.Chrome == settings.ChromeWindows {
		L.Chrome = buildWindowsChrome(L.Card, s.DisplayTitle(), titleFont, badge)
	} else if kali {
		L.Chrome = buildKaliChrome(L.Card, s.DisplayTitle(), titleFont, badge)
	} else if s.ShowChrome {
		L.Chrome = buildChrome(L.Card, barH, s.DisplayTitle(), titleFont, badge, inset, light)
	}
	if s.Label != "" {
		L.Label = buildLabel(L.Card, s.Label, titleFont, float64(s.LabelSize), top)
	}

	asc, desc := in.Code.Metrics(size)
	codeTop := L.Card.Y + barH + CodePadTop
	xL := L.Card.X + CodePadX + gutterW
	xR := xL + paneW + PaneGap + gutterW
	dividerX := xL + paneW + PaneGap/2
	gutterHex, gutterOp := highlight.Gutter(light)
	for i, v := range vrows {
		y := codeTop + float64(i)*lh + (lh-(asc+desc))/2 + asc
		rowTop := codeTop + float64(i)*lh
		if v.leftBand.Alpha > 0 {
			L.Bands = append(L.Bands, Band{Rect{L.Card.X, rowTop, dividerX - L.Card.X, lh}, v.leftBand})
		}
		if v.rightBand.Alpha > 0 {
			L.Bands = append(L.Bands, Band{Rect{dividerX, rowTop, L.Card.X + cardW - dividerX, lh}, v.rightBand})
		}
		L.Rows = append(L.Rows,
			Row{X: xL, Y: y, Spans: v.left},
			Row{X: xR, Y: y, Spans: v.right})
		if s.ShowLineNumbers {
			if v.leftNo > 0 {
				L.Gutter = append(L.Gutter, Text{
					X: xL - GutterPad, Y: y, Anchor: "end",
					Size: size, Font: in.Code, Text: strconv.Itoa(v.leftNo),
					Color: theme.Color{Hex: gutterHex, Alpha: gutterOp},
				})
			}
			if v.rightNo > 0 {
				L.Gutter = append(L.Gutter, Text{
					X: xR - GutterPad, Y: y, Anchor: "end",
					Size: size, Font: in.Code, Text: strconv.Itoa(v.rightNo),
					Color: theme.Color{Hex: gutterHex, Alpha: gutterOp},
				})
			}
		}
	}
	// The divider hairline runs the height of the code area.
	divider := theme.Color{Hex: "#ffffff", Alpha: 0.07}
	if light {
		divider = theme.Color{Hex: "#000000", Alpha: 0.08}
	}
	L.Bands = append(L.Bands, Band{Rect{dividerX, L.Card.Y + barH, 1, cardH - barH}, divider})

	L.Swirl = s.Watermark == settings.WatermarkSwirl
	if in.Image != nil && in.Image.W > 0 && in.Image.H > 0 {
		// Bottom-right of the window body, as on the single-pane cards.
		bodyY := L.Card.Y + barH
		h := math.Min((L.Card.H-barH)*0.7, L.Card.W*0.7)
		w := h * float64(in.Image.W) / float64(in.Image.H)
		L.Watermark = &Watermark{
			Box:  Rect{L.Card.X + L.Card.W - w*0.85, bodyY + (L.Card.H - barH) - h*0.95, w, h},
			Data: in.Image.Data, Mime: in.Image.Mime, Opacity: in.Image.Opacity,
		}
	}
	return L, nil
}
