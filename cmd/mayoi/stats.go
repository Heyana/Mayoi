package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/bg"
	"mayoi/internal/git"
	"mayoi/locales"
)

// statSpans are the periods the panel counts a project's commits over: the
// day, the week and the month before, each counting the shorter ones too.
const statSpans = 3

var statSpanLong = [statSpans]time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}

// statsEvery is how often the panel reads the commits again while it is
// open: a day's window slides, and a commit made while it shows belongs in
// it.
const statsEvery = 5 * time.Minute

// statsRow is one project's commits over the spans, with the lines they
// added and deleted, and when it last committed.
type statsRow struct {
	dir     string
	name    string
	commits [statSpans]int
	added   [statSpans]int
	removed [statSpans]int
	latest  time.Time
}

// statsState is the panel of recent commits: whether it is open, the rows it
// shows, when they were read, and while a read runs.
type statsState struct {
	f    *frame
	open bool
	at   time.Time
	busy bool
	gen  int
	rows []statsRow
}

// toggle opens or closes the panel, reading the commits as it opens.
func (st *statsState) toggle() {
	st.open = !st.open
	if st.open {
		st.read(time.Now())
	}
}

// read fills the rows from what each project committed since the longest
// span, once they are stale: the projects of the rail, in its order, or by
// how recently each committed.
func (st *statsState) read(now time.Time) {
	f := st.f
	if f == nil || st.busy || (!st.at.IsZero() && now.Sub(st.at) < statsEvery) {
		return
	}
	wins := f.m.s.InRail()
	var dirs, names []string
	for _, w := range wins {
		if w.Folder == "" || !hasRepository(w.Folder) {
			continue
		}
		dirs = append(dirs, w.Folder)
		names = append(names, railTitle(f.m.s, w.Folder))
	}
	if len(dirs) == 0 {
		st.rows, st.at, st.busy = nil, now, false
		return
	}
	manual := f.m.s.RailOrder == "manual"
	post, draw := f.active.a.post, f.active.a.invalidate
	st.busy, st.gen = true, st.gen+1
	gen := st.gen
	rows := make([]statsRow, len(dirs))
	bg.Go(func() {
		for i, dir := range dirs {
			row := statsRow{dir: dir, name: names[i]}
			if r, err := git.Open(dir); err == nil {
				if cs, err := r.CommitsSince(now.Add(-statSpanLong[statSpans-1])); err == nil {
					for _, c := range cs {
						if row.latest.Before(c.When) {
							row.latest = c.When
						}
						for s := 0; s < statSpans; s++ {
							if !c.When.After(now.Add(-statSpanLong[s])) {
								continue
							}
							row.commits[s]++
							row.added[s] += c.Added
							row.removed[s] += c.Deleted
						}
					}
				}
			}
			rows[i] = row
		}
		if !manual {
			slices.SortStableFunc(rows, func(a, b statsRow) int { return b.latest.Compare(a.latest) })
		}
		post(func() {
			if st.gen != gen {
				return
			}
			st.at, st.busy, st.rows = time.Now(), false, rows
		})
		draw()
	})
}

// statSpanName is the name of a span over its column.
func statSpanName(s int) string {
	switch s {
	case 0:
		return locales.Stats24h()
	case 1:
		return locales.Stats7d()
	}
	return locales.Stats30d()
}

// statsButton is the title bar's button for the panel of recent commits.
func (a *app) statsButton(c *ui.Context) {
	if a.stats == nil {
		return
	}
	if a.iconButton(c, iconStats, a.withKey(locales.StatsTitle(), "view.stats"), a.stats.open).Clicked() {
		a.stats.toggle()
	}
}

// statsView is the panel of recent commits: what each project of the rail
// committed over the last day, week and month, with the lines those commits
// added and deleted.
func (a *app) statsView(c *ui.Context) {
	st := a.stats
	if st == nil || !st.open {
		return
	}
	st.read(c.Now())
	p := a.pal
	ui.Overlay(c, func() {
		back := a.dialogBack(c)
		back.Children(func() {
			box := a.floating(c, ui.Column(c).Width(620).MaxWidthPercent(92).Margin(titleBarH+8, 0, 0, 0).Radius(10), true).Clip().
				Role(ui.RoleDialog).Label(locales.StatsTitle())
			box.Children(func() {
				ui.Row(c).Padding(11, 14).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
					ui.Icon(c, iconStats).FontSize(14).TextColor(p.muted)
					ui.Text(c, locales.StatsTitle()).FontSize(13.5).TextColor(p.text).Grow(1).Basis(0).MinWidth(0).SingleLine()
					if st.busy {
						ui.Text(c, locales.StatsReading()).FontSize(11.5).TextColor(p.faint).Shrink(0).SingleLine()
					}
					if a.smallButton(c, iconX, locales.QuickClose()) {
						st.open = false
					}
				})
				// Each span over its column of counts.
				ui.Row(c).Padding(8, 14, 4, 14).Gap(statColumnGap).Children(func() {
					ui.Text(c, "").Grow(1).Basis(0).MinWidth(0)
					for s := 0; s < statSpans; s++ {
						ui.Text(c, statSpanName(s)).Width(statColumnW).Shrink(0).FontSize(11.5).TextColor(p.faint).SingleLine()
					}
				})
				ui.Scroll(c.Key("stats-rows")).MaxHeight(420).Padding(2, 14, 8, 14).Children(func() {
					if len(st.rows) == 0 {
						ui.Text(c, locales.StatsEmpty()).FontSize(13).TextColor(p.muted).Padding(8, 4)
						return
					}
					for _, row := range st.rows {
						a.statsLine(c, row)
					}
				})
				if len(st.rows) > 0 {
					a.statsTotal(c, st.rows)
				}
			})
			if box.PressedOutside() || back.OverlayShortcut(0, ui.KeyEscape) {
				st.open = false
			}
		})
	})
}

// statColumnW is how wide a span's column is, and statColumnGap the room
// between them and the names.
const (
	statColumnW   float32 = 120
	statColumnGap float32 = 14
)

// statsLine is one project of the panel: its name, and under each span the
// commits it made and the lines they added and deleted.
func (a *app) statsLine(c *ui.Context, row statsRow) {
	p := a.pal
	ui.Row(c.Key("stats:"+row.dir)).FillWidth().Padding(6, 0).Gap(statColumnGap).AlignItems(ui.Center).Children(func() {
		a.statsCells(c, row.name, p.text, row.commits, row.added, row.removed)
	})
}

// statsTotal is the panel's last line, kept below the list: the commits of
// every project it lists, and the lines those commits added and deleted,
// over each span.
func (a *app) statsTotal(c *ui.Context, rows []statsRow) {
	p := a.pal
	commits, added, removed := statsSum(rows)
	ui.Row(c.Key("stats-total")).FillWidth().Padding(10, 0).Gap(statColumnGap).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
		a.statsCells(c, locales.StatsTotal(), p.muted, commits, added, removed)
	})
}

// statsSum adds the commits of every row, and the lines they added and
// deleted, over each span.
func statsSum(rows []statsRow) (commits, added, removed [statSpans]int) {
	for _, r := range rows {
		for s := 0; s < statSpans; s++ {
			commits[s] += r.commits[s]
			added[s] += r.added[s]
			removed[s] += r.removed[s]
		}
	}
	return commits, added, removed
}

// statsCells is a row of the panel: the name at the left, here in nameCol,
// and under each span the commits and the lines, in their colors.
func (a *app) statsCells(c *ui.Context, name string, nameCol ui.Color, commits, added, removed [statSpans]int) {
	p := a.pal
	ui.Text(c, name).FontSize(13).TextColor(nameCol).Grow(1).Basis(0).MinWidth(0).SingleLine()
	for s := 0; s < statSpans; s++ {
		ui.Column(c).Width(statColumnW).Shrink(0).Gap(3).Children(func() {
			col := p.text
			if commits[s] == 0 {
				col = p.faint
			}
			ui.Text(c, fmt.Sprintf(locales.StatsCommits(), commits[s])).FontSize(12.5).TextColor(col).SingleLine()
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				switch {
				case added[s] == 0 && removed[s] == 0:
					ui.Text(c, "\u2014").FontSize(10.5).TextColor(p.faint).SingleLine()
					return
				}
				if added[s] > 0 {
					ui.Text(c, "+"+railLinesCount(added[s])).FontSize(10.5).TextColor(p.added).SingleLine()
				}
				if removed[s] > 0 {
					ui.Text(c, "\u2212"+railLinesCount(removed[s])).FontSize(10.5).TextColor(p.removed).SingleLine()
				}
			})
		})
	}
}
