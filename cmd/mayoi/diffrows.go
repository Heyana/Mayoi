package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/width"

	"mayoi/internal/linediff"
)

// rowKind is what a row of a diff shows.
type rowKind uint8

const (
	rowLine    rowKind = iota // a line of each side, or of one
	rowGap                    // unchanged lines folded away
	rowHunk                   // the header of a run of changes and the lines around them
	rowSection                // the header of the changes staged, or those not
	rowFile                   // the header of a file
	rowNote                   // why the lines of a file do not show
	rowSpace                  // the room between two files
	rowCommit                 // the message of the commit a diff shows
	rowComment                // the card of a note, n of the review's
)

// diffRow is a row of a diff as it shows, of file, one of the diff's.
//
// A line row has a line of each side, -1 where a side has none, and the
// hunk it is part of, -1 for lines unchanged. In the unified layout it
// has one side when it changed, and peer is the line of the other side
// it was modified from, or into, -1 for none.
//
// gap is the key of the gap a gap row folds, or that a line row shows as
// it opened, -1 for none, and n the lines the gap folds, or for the card
// of a note, the note's number, as review.noteAt takes it. label is what a
// header or a note says, and h how many lines tall the row is, as its
// line wraps.
type diffRow struct {
	kind                      rowKind
	file                      int
	old, new, hunk, peer, gap int
	n, h                      int
	label                     string
}

// lineRow is the row of a line of each side.
func lineRow(old, new, hunk, peer int) diffRow {
	return diffRow{kind: rowLine, old: old, new: new, hunk: hunk, peer: peer, gap: -1, h: 1}
}

// between reports whether a row is one the keys pass over: the room
// between files, or a header of more than one.
func (r *diffRow) between() bool {
	return r.kind == rowSpace || r.kind == rowSection || r.kind == rowCommit
}

// changed reports whether a row is a line changed.
func (r *diffRow) changed() bool { return r.kind == rowLine && r.hunk >= 0 }

// lineRows lays out every line of two sides and the hunks between them:
// in one column, the lines of each hunk removed before those added, and
// side by side, the lines of a hunk across from each other.
func lineRows(old, new []string, hunks []linediff.Hunk) (unified, pairs []diffRow) {
	unified = make([]diffRow, 0, max(len(old), len(new)))
	pairs = make([]diffRow, 0, max(len(old), len(new)))
	i, j := 0, 0
	same := func(to int) {
		for ; i < to; i, j = i+1, j+1 {
			r := lineRow(i, j, -1, -1)
			unified, pairs = append(unified, r), append(pairs, r)
		}
	}
	for k, h := range hunks {
		same(h.A)
		m := min(h.ALen, h.BLen)
		for n := range h.ALen {
			peer := -1
			if n < m {
				peer = h.B + n
			}
			unified = append(unified, lineRow(h.A+n, -1, k, peer))
		}
		for n := range h.BLen {
			peer := -1
			if n < m {
				peer = h.A + n
			}
			unified = append(unified, lineRow(-1, h.B+n, k, peer))
		}
		for n := range max(h.ALen, h.BLen) {
			r := lineRow(-1, -1, k, -1)
			if n < h.ALen {
				r.old = h.A + n
			}
			if n < h.BLen {
				r.new = h.B + n
			}
			pairs = append(pairs, r)
		}
		i, j = h.A+h.ALen, h.B+h.BLen
	}
	same(len(old))
	return unified, pairs
}

// span is a run of rows, from up to to.
type span struct{ from, to int }

// foldRows adds the rows of file f, number file of the diff, to out, from
// all its line rows of the layout shown: the unchanged lines more than
// context away from a change folded into a gap, unless the gap is open,
// and a header before each run of changes and the lines around them.
// With full set, every line shows, and no header.
func foldRows(out []diffRow, f *diffFile, file int, all []diffRow, full bool, context int) []diffRow {
	if full {
		for _, r := range all {
			r.file = file
			out = append(out, r)
		}
		return out
	}
	oldAt, newAt, folds, opens := foldSpans(all, f.open, context)
	section := func(from, to int) {
		if from >= to {
			return
		}
		changes := false
		for _, r := range all[from:to] {
			changes = changes || r.hunk >= 0
		}
		if changes {
			out = append(out, diffRow{kind: rowHunk, file: file, old: -1, new: -1, hunk: -1, peer: -1, gap: -1, h: 1,
				label: hunkHeader(f, oldAt[from], oldAt[to]-oldAt[from], newAt[from], newAt[to]-newAt[from])})
		}
		for i := from; i < to; i++ {
			r := all[i]
			r.file = file
			for _, o := range opens {
				if i >= o.from && i < o.to {
					r.gap = newAt[o.from]
				}
			}
			out = append(out, r)
		}
	}
	at := 0
	for _, g := range folds {
		section(at, g.from)
		out = append(out, diffRow{kind: rowGap, file: file, old: all[g.from].old, new: all[g.from].new, hunk: -1, peer: -1,
			gap: newAt[g.from], n: g.to - g.from, h: 1})
		at = g.to
	}
	section(at, len(all))
	return out
}

// foldSpans returns how many lines of each side come before each of all
// the line rows of a file, and after the last, and the runs of unchanged
// lines more than context away from a change that fold, and those of
// them that open says are open.
func foldSpans(all []diffRow, open map[int]bool, context int) (oldAt, newAt []int, folds, opens []span) {
	oldAt, newAt = make([]int, len(all)+1), make([]int, len(all)+1)
	for i, r := range all {
		oldAt[i+1], newAt[i+1] = oldAt[i], newAt[i]
		if r.old >= 0 {
			oldAt[i+1]++
		}
		if r.new >= 0 {
			newAt[i+1]++
		}
	}
	for i := 0; i < len(all); {
		if all[i].hunk >= 0 {
			i++
			continue
		}
		j := i
		for j < len(all) && all[j].hunk < 0 {
			j++
		}
		from, to := i, j
		if i > 0 {
			from += context
		}
		if j < len(all) {
			to -= context
		}
		if to-from >= 2 {
			if open[newAt[from]] {
				opens = append(opens, span{from, to})
			} else {
				folds = append(folds, span{from, to})
			}
		}
		i = j
	}
	return oldAt, newAt, folds, opens
}

// hunkHeader is the header of a run of lines of each side, from line
// oldAt of the old side, n of them, and newAt of the new, m of them: as
// Git writes it, with the line opening the block the run starts in.
func hunkHeader(f *diffFile, oldAt, n, newAt, m int) string {
	num := func(at, n int) string {
		if n > 0 {
			at++
		}
		if n == 1 {
			return fmt.Sprint(at)
		}
		return fmt.Sprintf("%d,%d", at, n)
	}
	h := fmt.Sprintf("@@ -%s +%s @@", num(oldAt, n), num(newAt, m))
	s, line := &f.new, newAt
	if !f.new.ok || len(f.new.lines) == 0 {
		s, line = &f.old, oldAt
	}
	if line <= 0 || line >= len(s.lines) {
		return h
	}
	if scopes := scopesIn(len(s.lines), line, s.window()); len(scopes) > 0 {
		t := strings.TrimSpace(s.lines[scopes[len(scopes)-1]])
		if utf8.RuneCountInString(t) > 80 {
			t = string([]rune(t)[:79]) + "…"
		}
		h += " " + t
	}
	return h
}

// window returns the lines of a side as a window over all of them.
func (s *diffSide) window() *lineWindow {
	if s.win == nil {
		w := &lineWindow{}
		if s.src != nil {
			w.text = s.src.text
			w.starts = append(append(make([]int, 0, len(s.src.starts)+1), s.src.starts...), len(s.src.text)+1)
		}
		s.win = w
	}
	return s.win
}

// runeCols returns how many monospace columns r takes: two for wide East
// Asian glyphs and one otherwise.
func runeCols(r rune) int {
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	default:
		return 1
	}
}

// wrapAt returns where the rows of a line after the first start, as it
// wraps at cols columns, in bytes of shown, the line with its tabs
// expanded: after the last space that fits, when there is one past the
// middle of the row, or else at the edge.
func wrapAt(shown string, cols int) []int {
	if cols < 1 || len(shown) <= cols {
		return nil // as long in bytes as in columns, or longer
	}
	var out []int
	start, col := 0, 0
	space, spaceCol := -1, 0 // after the row's last space, and its column
	for i, r := range shown {
		w := runeCols(r)
		if col > 0 && col+w > cols {
			if space > start && spaceCol > cols/2 && col-spaceCol+w <= cols {
				out = append(out, space)
				start, col = space, col-spaceCol
			} else {
				out = append(out, i)
				start, col = i, 0
			}
			space = -1
		}
		col += w
		if r == ' ' {
			space, spaceCol = i+1, col
		}
	}
	return out
}

// diffWrapAt returns where the rows of shown after the first start, as it
// wraps within cols cells of f. ASCII lines count cells directly; lines with
// other characters wrap to f's measured width so wide glyphs never overflow.
func diffWrapAt(f codeFont, shown string, cols int) []int {
	if cols < 1 || f.charW <= 0 || isASCII(shown) {
		return wrapAt(shown, cols)
	}
	maxW := float32(cols) * f.charW
	shape := diffWrapShapeOf(f, shown)
	if len(shape.ends) == 0 || shape.cum[len(shape.cum)-1] <= maxW {
		return nil
	}
	// Each row is a bisection over the clusters' cumulative widths, which are
	// shaped once per line: a resize measures nothing.
	var out []int
	si := 0 // the cluster the row starts at
	for si < len(shape.ends) {
		lo, hi := si, len(shape.ends)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if shape.cum[mid]-shape.cum[si] <= maxW {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		ei := lo
		if ei <= si {
			// A cluster wider than the row goes in it alone.
			ei = si + 1
		}
		if ei >= len(shape.ends)-1 {
			break
		}
		start, end := shape.ends[si], shape.ends[ei]
		next := ei
		if sp := strings.LastIndexByte(shown[start:end], ' '); sp >= 0 {
			after := start + sp + 1
			if ai := shape.clusterEndingAt(after, si, ei); ai >= 0 && shape.cum[ai]-shape.cum[si] > maxW/2 {
				end = after
				// The row after starts past the space, at the cluster that
				// follows it.
				next = ai
			}
		}
		out = append(out, end)
		si = next
	}
	return out
}

// diffWrapShape is a line shaped for wrapping: where its grapheme clusters
// end in shown, ends[0] being where it starts, and how wide each prefix of
// them comes to, cum[k] being the width of the first k clusters.
type diffWrapShape struct {
	ends []int
	cum  []float32
}

// clusterEndingAt returns where the cluster that ends at b sits in ends,
// between i and j, -1 where no cluster ends there.
func (s diffWrapShape) clusterEndingAt(b, i, j int) int {
	for k := i; k < j; k++ {
		if s.ends[k] == b {
			return k
		}
	}
	return -1
}

const diffWrapShapesKept = 1024

// diffWrapShapes is the shaping of the lines wrapped: a line and a font
// shape once, and every resize and frame of it bisects over the widths,
// rather than shaping each prefix it measures again.
var diffWrapShapes struct {
	sync.Mutex
	m   map[diffWrapShapeKey]diffWrapShape
	gen int
}

type diffWrapShapeKey struct {
	font  string
	shown string
}

// diffWrapShapeOf returns the shaped clusters of shown in f, shaping them
// the first time the line and font are asked for.
func diffWrapShapeOf(f codeFont, shown string) diffWrapShape {
	key := diffWrapShapeKey{font: wrapFontKey(f), shown: shown}
	diffWrapShapes.Lock()
	defer diffWrapShapes.Unlock()
	if s, ok := diffWrapShapes.m[key]; ok {
		return s
	}
	ends := make([]int, 1, len(shown)+1)
	cum := make([]float32, 1, len(shown)+1)
	for i := 0; i < len(shown); {
		end := graphemeEnd(shown, i)
		cum = append(cum, cum[len(cum)-1]+f.width(shown[i:end]))
		ends = append(ends, end)
		i = end
	}
	s := diffWrapShape{ends: ends, cum: cum}
	if diffWrapShapes.m == nil {
		diffWrapShapes.m = map[diffWrapShapeKey]diffWrapShape{}
	}
	if len(diffWrapShapes.m) >= diffWrapShapesKept {
		diffWrapShapes.m = map[diffWrapShapeKey]diffWrapShape{}
	}
	diffWrapShapes.m[key] = s
	return s
}

// wrapFontKey names a font, for the shaping of lines in it.
func wrapFontKey(f codeFont) string {
	return f.family + "\x00" + strconv.FormatFloat(float64(f.size), 'g', -1, 32) + "\x00" +
		strconv.Itoa(f.weight) + "\x00" + f.features
}

// graphemeEnd returns where the grapheme cluster of s that starts at i
// ends: a base rune with the marks, emoji modifiers and joiners that go
// with it, a flag's two regional indicators, a Hangul syllable's jamo, and
// a CR of its LF.
func graphemeEnd(s string, i int) int {
	r, n := utf8.DecodeRuneInString(s[i:])
	end := i + n
	if r == '\r' && end < len(s) && s[end] == '\n' {
		return end + 1
	}
	regional := isRegional(r)
	joined := false // the join's rune, in an emoji joined to this one
	for end < len(s) {
		r2, n2 := utf8.DecodeRuneInString(s[end:])
		switch {
		case joined:
			joined = false
		case r2 == 0x200D:
			joined = true
		case isClusterExtend(r2):
			// A mark, a selector or a modifier of the rune before it.
		case regional && isRegional(r2):
			regional = false
		case isHangulExtend(r, r2):
		default:
			return end
		}
		end += n2
		r = r2
	}
	return end
}

// isRegional reports whether r is a regional indicator, of the pair a flag
// is written with.
func isRegional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// isClusterExtend reports whether r extends the cluster of the rune before
// it: a combining mark, a variation selector, a skin tone, a keycap, a tag.
func isClusterExtend(r rune) bool {
	switch {
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Mc, r), unicode.Is(unicode.Me, r):
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF: // variation selectors
		return true
	case r >= 0x1F3FB && r <= 0x1F3FF: // skin tones
		return true
	case r >= 0xE0020 && r <= 0xE007F: // tags
		return true
	}
	return false
}

// isHangulExtend reports whether r joins the cluster of prev as a Hangul
// syllable's vowel or final consonant.
func isHangulExtend(prev, r rune) bool {
	switch {
	case prev >= 0x1100 && prev <= 0x115F, prev >= 0xA960 && prev <= 0xA97F: // an initial, which takes a vowel or a final
		return r >= 0x1160 && r <= 0x11FF || r >= 0xD7B0 && r <= 0xD7FB
	case prev >= 0x1160 && prev <= 0x11A7, prev >= 0xD7B0 && prev <= 0xD7C6: // a vowel, which takes a final
		return r >= 0x11A8 && r <= 0x11FF || r >= 0xD7CB && r <= 0xD7FB
	case prev >= 0xAC00 && prev <= 0xD7A3: // a syllable, which takes a final
		return (prev-0xAC00)%28 == 0 && (r >= 0x11A8 && r <= 0x11FF || r >= 0xD7CB && r <= 0xD7FB)
	}
	return false
}

// wrapCount returns how many rows a line takes as it wraps at cols
// columns.
func wrapCount(text string, cols int) int {
	if cols < 1 || len(text) <= cols && strings.IndexByte(text, '\t') < 0 {
		return 1
	}
	shown, _ := expandTabs(clipLine(text))
	return len(wrapAt(shown, cols)) + 1
}

// diffWrapCount returns how many rows a diff line takes as it wraps within
// cols cells of f.
func diffWrapCount(f codeFont, text string, cols int) int {
	if cols < 1 || len(text) <= cols && strings.IndexByte(text, '\t') < 0 && isASCII(text) {
		return 1
	}
	shown, _ := expandTabs(clipLine(text))
	return len(diffWrapAt(f, shown, cols)) + 1
}

// clipLine returns as much of a line as a diff shows: the rest of a
// longer one, as of a minified file, is cut.
func clipLine(text string) string {
	if len(text) > maxDiffLine {
		return strings.ToValidUTF8(text[:maxDiffLine], "")
	}
	return text
}
