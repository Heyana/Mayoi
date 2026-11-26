package main

import (
	"fmt"
	"math"
	"path/filepath"

	"github.com/egoist/mygo/ui"

	"mayoi/locales"
)

type diffMediaKind uint8

const (
	diffMediaNone diffMediaKind = iota
	diffMediaImage
	diffMediaVideo
)

type diffMediaSide struct {
	ok   bool
	bm   *ui.Bitmap
	w, h int
	size int64
	path string // local worktree video path (streamed without loading into RAM)
	data []byte // git blob video bytes
	err  string
}

type diffMedia struct {
	kind     diffMediaKind
	old, new diffMediaSide
	players  [2]*videoPlayer // 0 = old, 1 = new
}

func (m *diffMedia) stop() {
	if m == nil {
		return
	}
	for i := range m.players {
		if m.players[i] != nil {
			m.players[i].stop()
			m.players[i] = nil
		}
	}
}

func (m *diffMedia) side(isNew bool) *diffMediaSide {
	if isNew {
		return &m.new
	}
	return &m.old
}

func buildDiffImageMedia(oldData []byte, oldOK bool, newData []byte, newOK bool) *diffMedia {
	if !oldOK && !newOK {
		return nil
	}
	decode := func(data []byte, ok bool) diffMediaSide {
		if !ok {
			return diffMediaSide{}
		}
		s := diffMediaSide{ok: true, size: int64(len(data))}
		bm, w, h, err := decodeImageData(data)
		if err != nil {
			s.err = err.Error()
			return s
		}
		if bw, bh := bm.Size(); bw == w && bh == h {
			bm.Pixelated()
		}
		s.bm, s.w, s.h = bm, w, h
		return s
	}
	return &diffMedia{
		kind: diffMediaImage,
		old:  decode(oldData, oldOK),
		new:  decode(newData, newOK),
	}
}

func buildDiffVideoMedia(oldData []byte, oldOK bool, newSide diffMediaSide) *diffMedia {
	if !oldOK && !newSide.ok {
		return nil
	}
	var oldSide diffMediaSide
	if oldOK {
		oldSide = diffMediaSide{ok: true, data: oldData, size: int64(len(oldData))}
	}
	return &diffMedia{
		kind: diffMediaVideo,
		old:  oldSide,
		new:  newSide,
	}
}

const (
	diffMediaPadX    = float32(24)
	diffMediaPadY    = float32(16)
	diffMediaGap     = float32(20)
	diffMediaHeaderH = float32(22)
	diffMediaMetaH   = float32(24)
	diffMediaMaxH    = float32(360)
	diffMediaMinH    = float32(120)
)

func diffMediaSideHeight(m *diffMedia, s *diffMediaSide, cardW float32) float32 {
	if !s.ok {
		return 0
	}
	if m.kind == diffMediaVideo {
		h := cardW * 9 / 16
		return clamp(h, diffMediaMinH, diffMediaMaxH)
	}
	if s.bm == nil || s.w <= 0 || s.h <= 0 {
		return diffMediaMinH
	}
	iw, ih := float32(s.w), float32(s.h)
	scale := min(1, cardW/iw, diffMediaMaxH/ih)
	return clamp(ih*scale, 64, diffMediaMaxH)
}

// mediaRowHeight returns the number of diff line heights a media preview
// row needs at viewW.
func mediaRowHeight(m *diffMedia, viewW, lineH float32) int {
	if m == nil || lineH <= 0 {
		return 1
	}
	if viewW <= 0 {
		viewW = 640
	}
	both := m.old.ok && m.new.ok
	var contentH float32
	if both {
		cardW := max((viewW-2*diffMediaPadX-diffMediaGap)/2, 160)
		contentH = max(diffMediaSideHeight(m, &m.old, cardW), diffMediaSideHeight(m, &m.new, cardW))
		contentH += diffMediaHeaderH
	} else {
		cardW := max(min(viewW-2*diffMediaPadX, 720), 160)
		s := &m.new
		if !s.ok {
			s = &m.old
		}
		contentH = diffMediaSideHeight(m, s, cardW)
	}
	totalH := 2*diffMediaPadY + contentH + diffMediaMetaH
	return max(int(math.Ceil(float64(totalH/lineH))), 6)
}

func (m *diffMedia) sideBoxes(full ui.Rect) (boxes [2]ui.Rect, hasHeader bool) {
	both := m.old.ok && m.new.ok
	if both {
		cardW := max((full.W-2*diffMediaPadX-diffMediaGap)/2, 120)
		h := max(full.H-2*diffMediaPadY, 64)
		boxes[0] = ui.Rect{X: full.X + diffMediaPadX, Y: full.Y + diffMediaPadY, W: cardW, H: h}
		boxes[1] = ui.Rect{X: boxes[0].X + cardW + diffMediaGap, Y: full.Y + diffMediaPadY, W: cardW, H: h}
		return boxes, true
	}
	cardW := max(min(full.W-2*diffMediaPadX, 720), 120)
	h := max(full.H-2*diffMediaPadY, 64)
	x := full.X + (full.W-cardW)/2
	box := ui.Rect{X: x, Y: full.Y + diffMediaPadY, W: cardW, H: h}
	if m.old.ok {
		boxes[0] = box
	} else if m.new.ok {
		boxes[1] = box
	}
	return boxes, false
}

// paintDiffMedia paints an image preview row directly on the diff canvas.
func (a *app) paintDiffMedia(pt *ui.Painter, f *diffFile, full ui.Rect) {
	m := f.media
	if m == nil || m.kind != diffMediaImage {
		return
	}
	p := a.pal
	boxes, hasHeader := m.sideBoxes(full)
	oldName, newName := f.base.sides()
	names := [2]string{oldName, newName}
	accents := [2]ui.Color{p.removed, p.added}

	for sideIdx := range 2 {
		s := m.side(sideIdx == 1)
		if !s.ok {
			continue
		}
		box := boxes[sideIdx]
		y := box.Y
		availH := box.H - diffMediaMetaH
		if hasHeader {
			// Side label with a small colored dot.
			pt.Fill(ui.Rect{X: box.X, Y: y + diffMediaHeaderH/2 - 3, W: 6, H: 6}, accents[sideIdx], 3)
			uiText(pt, box.X+12, y, diffMediaHeaderH, names[sideIdx], 11.5, 500, p.muted)
			y += diffMediaHeaderH
			availH -= diffMediaHeaderH
		}
		frame := ui.Rect{X: box.X, Y: y, W: box.W, H: max(availH, 32)}
		if s.bm != nil && s.w > 0 && s.h > 0 {
			iw, ih := float32(s.w), float32(s.h)
			scale := min(1, frame.W/iw, frame.H/ih)
			dw, dh := iw*scale, ih*scale
			at := ui.Rect{
				X: frame.X + (frame.W-dw)/2,
				Y: frame.Y + (frame.H-dh)/2,
				W: dw,
				H: dh,
			}
			pt.Clip(at, 4, func() {
				tile := checkers(p.dark)
				for ty := at.Y; ty < at.Y+at.H; ty += checkTile {
					for tx := at.X; tx < at.X+at.W; tx += checkTile {
						pt.Image(tile, ui.Rect{X: tx, Y: ty, W: checkTile, H: checkTile}, ui.FillBox)
					}
				}
				pt.Image(s.bm, at, ui.FillBox)
			})
			borderCol := p.border
			if hasHeader {
				borderCol = accents[sideIdx].Alpha(0.55)
			}
			pt.Stroke(at, borderCol, 4, 1)
		} else {
			pt.Fill(frame, p.surface, 6)
			pt.Stroke(frame, p.border, 6, 1)
			msg := s.err
			if msg == "" {
				msg = locales.CannotShow(filepath.Base(f.path))
			}
			uiText(pt, frame.X+12, frame.Y+frame.H/2-10, 20, msg, 12, 400, p.muted)
		}
		metaY := frame.Y + frame.H + 4
		meta := sizeText(s.size)
		if s.w > 0 && s.h > 0 {
			meta = fmt.Sprintf("%d × %d  ·  %s", s.w, s.h, sizeText(s.size))
		}
		uiText(pt, box.X, metaY, diffMediaMetaH-4, meta, 11.5, 400, p.faint)
	}
}

// diffMediaWidgets renders interactive video players for visible rowMedia
// rows inside the diff scroll container.
func (a *app) diffMediaWidgets(c *ui.Context, v *diffView, first, last int, view ui.Rect, lineH float32) {
	p := a.pal
	rows := v.rows
	for i := first; i <= last; i++ {
		if rows[i].kind != rowMedia {
			continue
		}
		f := v.files[rows[i].file]
		m := f.media
		if m == nil || m.kind != diffMediaVideo {
			continue
		}
		y := editorPadY + float32(v.tops[i])*lineH
		h := float32(rows[i].h) * lineH
		full := ui.Rect{X: v.scroll.X, Y: y, W: view.W, H: h}
		boxes, hasHeader := m.sideBoxes(full)
		oldName, newName := f.base.sides()
		names := [2]string{oldName, newName}
		accents := [2]ui.Color{p.removed, p.added}

		for sideIdx := range 2 {
			s := m.side(sideIdx == 1)
			if !s.ok {
				continue
			}
			vp := m.ensureDiffVideoPlayer(sideIdx, f.path, s)
			box := boxes[sideIdx]
			cardKey := fmt.Sprintf("diff-video:%p:%d", f, sideIdx)
			ui.Column(c.Key(cardKey)).Absolute().Left(box.X).Top(box.Y).Width(box.W).Height(box.H).Children(func() {
				if hasHeader {
					ui.Row(c).Height(diffMediaHeaderH).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(accents[sideIdx])
						ui.Text(c, names[sideIdx]).FontSize(11.5).FontWeight(500).TextColor(p.muted)
					})
				}
				playerH := max(box.H-diffMediaMetaH, 48)
				if hasHeader {
					playerH = max(playerH-diffMediaHeaderH, 48)
				}
				borderCol := p.border
				if hasHeader {
					borderCol = accents[sideIdx].Alpha(0.55)
				}
				ui.Box(c).Width(box.W).Height(playerH).Radius(6).Clip().Background(p.surface).Border(1, borderCol).Children(func() {
					errMsg := s.err
					if errMsg == "" && vp != nil {
						errMsg = vp.err
					}
					if errMsg != "" {
						ui.Column(c).Fill().Center().Padding(12).Gap(6).Children(func() {
							ui.Icon(c, iconVideo).FontSize(24).TextColor(p.faint)
							ui.Text(c, locales.CannotShowWhy(filepath.Base(f.path), errMsg)).FontSize(12).TextColor(p.muted).TextAlign(ui.Center)
						})
						return
					}
					if wv := vp.ensureWebView(a, hexColor(p.surface)); wv != nil {
						ui.WebView(c.Key(cardKey+":wv:"+vp.mediaID), wv).Fill()
						return
					}
					// Headless test environment fallback.
					ui.Column(c).Fill().Center().Padding(12).Gap(6).Children(func() {
						ui.Icon(c, iconVideo).FontSize(24).TextColor(p.faint)
						ui.Text(c, filepath.Base(f.path)).FontSize(12.5).FontWeight(500).TextColor(p.text)
					})
				})
				meta := sizeText(s.size)
				if vp != nil {
					var parts []string
					if vp.w > 0 && vp.h > 0 {
						parts = append(parts, fmt.Sprintf("%d × %d", vp.w, vp.h))
					}
					if d := formatDuration(vp.duration); d != "" {
						parts = append(parts, d)
					}
					if s.size > 0 {
						parts = append(parts, sizeText(s.size))
					}
					if len(parts) > 0 {
						meta = joinDot(parts)
					}
				}
				ui.Row(c).Height(diffMediaMetaH).AlignItems(ui.Center).Children(func() {
					ui.Text(c, meta).FontSize(11.5).TextColor(p.faint)
				})
			})
		}
	}
}

func (m *diffMedia) ensureDiffVideoPlayer(sideIdx int, filePath string, s *diffMediaSide) *videoPlayer {
	if vp := m.players[sideIdx]; vp != nil {
		return vp
	}
	vp := &videoPlayer{}
	m.players[sideIdx] = vp
	var id, mediaURL string
	var err error
	if s.path != "" {
		id, mediaURL, err = registerMediaFile(s.path)
	} else if len(s.data) > 0 {
		id, mediaURL, err = registerMediaBytes(filepath.Base(filePath), s.data)
	}
	if err != nil {
		vp.err = err.Error()
		return vp
	}
	vp.mediaID, vp.url = id, mediaURL
	return vp
}

func joinDot(parts []string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		out += "  ·  " + p
	}
	return out
}
