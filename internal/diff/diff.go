// Package diff lines up the lines of two files for the side-by-side
// preset: a Myers diff in linear space, with deletions and insertions
// paired row by row so a rewritten line sits across from its replacement.
package diff

import (
	"fmt"
	"unicode/utf8"
)

// Resource limits for the comparison. They keep a pasted monster file from
// turning the O((N+M)D) search into a hang, and bound the card (and the
// PNG rasterized from it) to a sane size.
const (
	// MaxInputBytes is the largest accepted input, per side.
	MaxInputBytes = 8 << 20 // 8 MiB
	// MaxLines is the most lines accepted, per side.
	MaxLines = 20000
	// MaxRows is the most aligned rows a comparison may produce. Taller
	// images are useless in release notes and cost real memory to raster.
	MaxRows = 1000
	// MaxEdit is the largest half-edit-distance the Myers search follows.
	// Past it the differing middle of the files becomes one delete+insert
	// block, which is what such a wholesale rewrite looks like anyway. It
	// stays well under MaxRows so the fallback itself fits the row limit.
	MaxEdit = 512
)

// Row is one aligned pair of source lines. A side that took no part in the
// row has No == 0 and an empty text.
type Row struct {
	Old, New     string
	OldNo, NewNo int  // 1-based line numbers; 0 when the side is absent
	OldDel       bool // the old line was deleted here
	NewIns       bool // the new line was inserted here
}

// Check validates one side of the comparison against the input limits,
// naming the side ("old" or "new") in the error.
func Check(side, src string) error {
	if len(src) > MaxInputBytes {
		return fmt.Errorf("%s input is %.1f MB, over the %d MB limit", side, float64(len(src))/(1<<20), MaxInputBytes>>20)
	}
	if !utf8.ValidString(src) {
		return fmt.Errorf("%s input is not valid UTF-8 (convert it first, e.g. with iconv -f ... -t UTF-8)", side)
	}
	return nil
}

// Align lines up old and new. Equal lines become shared rows; within a
// changed hunk deletions and insertions are paired index by index, so the
// k-th deleted line sits across from the k-th inserted one and any leftover
// gets an empty opposite side.
func Align(oldLines, newLines []string) ([]Row, error) {
	if len(oldLines) > MaxLines {
		return nil, fmt.Errorf("old input has %d lines, over the %d limit", len(oldLines), MaxLines)
	}
	if len(newLines) > MaxLines {
		return nil, fmt.Errorf("new input has %d lines, over the %d limit", len(newLines), MaxLines)
	}
	ops := make([]op, 0, len(oldLines)+len(newLines))
	editOps(oldLines, newLines, &ops)

	rows := make([]Row, 0, len(ops))
	oldNo, newNo := 1, 1
	for i := 0; i < len(ops); {
		if ops[i].kind == opEqual {
			rows = append(rows, Row{Old: ops[i].text, New: ops[i].text, OldNo: oldNo, NewNo: newNo})
			oldNo++
			newNo++
			i++
			continue
		}
		var dels, inss []string
		for i < len(ops) && ops[i].kind != opEqual {
			if ops[i].kind == opDelete {
				dels = append(dels, ops[i].text)
			} else {
				inss = append(inss, ops[i].text)
			}
			i++
		}
		for k := 0; k < len(dels) || k < len(inss); k++ {
			var r Row
			if k < len(dels) {
				r.Old, r.OldNo, r.OldDel = dels[k], oldNo, true
				oldNo++
			}
			if k < len(inss) {
				r.New, r.NewNo, r.NewIns = inss[k], newNo, true
				newNo++
			}
			rows = append(rows, r)
		}
	}
	if len(rows) > MaxRows {
		return nil, fmt.Errorf("the comparison lines up %d rows, over the %d limit (compare a smaller range, or use the git-diff preset for large changes)", len(rows), MaxRows)
	}
	return rows, nil
}

// op kinds recorded by editOps.
const (
	opEqual = iota
	opDelete
	opInsert
)

type op struct {
	kind int
	text string
}

// editOps appends the ops that turn a into b, in order. Common prefixes and
// suffixes are peeled off first (that is where most of a real diff sits),
// and the differing middle is split at the middle snake of its edit graph
// until the pieces are trivial.
func editOps(a, b []string, ops *[]op) {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	j := 0
	for j < len(a)-i && j < len(b)-i && a[len(a)-1-j] == b[len(b)-1-j] {
		j++
	}
	for _, l := range a[:i] {
		*ops = append(*ops, op{opEqual, l})
	}
	midA, midB := a[i:len(a)-j], b[i:len(b)-j]
	switch {
	case len(midA) == 0:
		for _, l := range midB {
			*ops = append(*ops, op{opInsert, l})
		}
	case len(midB) == 0:
		for _, l := range midA {
			*ops = append(*ops, op{opDelete, l})
		}
	case (len(midA)+len(midB)+1)/2 > MaxEdit:
		// Too different to search within budget: one rewrite block.
		for _, l := range midA {
			*ops = append(*ops, op{opDelete, l})
		}
		for _, l := range midB {
			*ops = append(*ops, op{opInsert, l})
		}
	default:
		x, y := splitPoint(midA, midB)
		editOps(midA[:x], midB[:y], ops)
		editOps(midA[x:], midB[y:], ops)
	}
	for _, l := range a[len(a)-j:] {
		*ops = append(*ops, op{opEqual, l})
	}
}

// splitPoint finds where a shortest edit path from a to b crosses the
// middle, so the two halves can be diffed independently: a forward search
// from (0,0) and a backward one from (len(a),len(b)), each taking half the
// steps (Myers' linear-space bisection). a and b must be non-empty.
func splitPoint(a, b []string) (x, y int) {
	n, m := len(a), len(b)
	delta := n - m
	off := n + m // V arrays are indexed by the diagonal k = x-y, plus off
	vf := make([]int, 2*(n+m)+1)
	vb := make([]int, 2*(n+m)+1)
	vb[off+delta] = n // the backward search starts at the far corner
	half := (n + m + 1) / 2
	// Diagonals reachable after d steps, clipped to [-m, n] and keeping the
	// step's parity: outside that range the V arrays have no meaning.
	span := func(d, ref int) (lo, hi int) {
		lo, hi = max(ref-d, -m), min(ref+d, n)
		if (lo-ref-d)%2 != 0 {
			lo++
		}
		if (hi-ref-d)%2 != 0 {
			hi--
		}
		return lo, hi
	}
	for d := 0; d <= half; d++ {
		// Forward: the farthest x reached on each diagonal after d edits.
		lo, hi := span(d, 0)
		for k := lo; k <= hi; k += 2 {
			var fx int
			switch {
			case k == -d:
				fx = vf[off+k+1] // down from diagonal k+1
			case k == d:
				fx = vf[off+k-1] + 1 // right from diagonal k-1
			default:
				fx = max(vf[off+k+1], vf[off+k-1]+1)
			}
			fy := fx - k
			for fx < n && fy < m && a[fx] == b[fy] {
				fx++
				fy++
			}
			vf[off+k] = fx
			// An odd total edit distance meets on a forward step, on a
			// diagonal the backward search reached one step ago.
			if delta%2 != 0 && k >= delta-d+1 && k <= delta+d-1 && fx >= vb[off+k] {
				return fx, fx - k
			}
		}
		// Backward: the nearest x reached on each diagonal after d edits.
		lo, hi = span(d, delta)
		for k := lo; k <= hi; k += 2 {
			var bx int
			switch {
			case d == 0:
				bx = n
			case k == delta-d:
				bx = vb[off+k+1] - 1 // left from diagonal k+1
			case k == delta+d:
				bx = vb[off+k-1] // up from diagonal k-1
			default:
				bx = min(vb[off+k+1]-1, vb[off+k-1])
			}
			by := bx - k
			for bx > 0 && by > 0 && a[bx-1] == b[by-1] {
				bx--
				by--
			}
			vb[off+k] = bx
			// An even total edit distance meets on a backward step.
			if delta%2 == 0 && k >= -d && k <= d && bx <= vf[off+k] {
				return bx, bx - k
			}
		}
	}
	// Unreachable while the searches are correct; diff the whole thing as a
	// rewrite rather than crash if they ever stop being so.
	return n, 0
}
