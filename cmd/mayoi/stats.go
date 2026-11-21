package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/bg"
	"mayoi/internal/git"
	"mayoi/internal/settings"
	"mayoi/locales"
)

// statSpans are the periods the panel counts a project's commits over: the
// day, the week and the month before, each counting the shorter ones too.
const statSpans = 3

var statSpanLong = [statSpans]time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}

// statsEvery is how often the panel reads the commits again while it is
// open: a day's window slides, and a commit made while it shows belongs in
// it. Reading again reads only what was committed since the last read, so
// it comes cheap.
const statsEvery = time.Minute

// statsAmount is a count of commits, and of the lines they added and
// deleted.
type statsAmount struct {
	Commits, Added, Removed int
	// Lines is whether the lines were read: with nothing read before, the
	// counts come first, as they read no diff, the lines after.
	Lines bool
}

// statCommit is one commit as the panel counts it: when it was made, and the
// lines it added and deleted.
type statCommit struct {
	When    time.Time `json:"when"`
	Added   int       `json:"added"`
	Removed int       `json:"removed"`
}

// statRead is the commits one project made within the longest span, as the
// panel read them last. Reading them again then reads only those made since,
// and a panel opened again has its numbers before Git runs at all.
type statRead struct {
	Dir     string       `json:"dir"`
	At      time.Time    `json:"at"`
	Head    string       `json:"head"`
	Commits []statCommit `json:"commits"`
}

// statsRow is one project's commits over the spans: those its own identity
// made, and when it last committed.
type statsRow struct {
	dir    string
	name   string
	count  [statSpans]statsAmount
	latest time.Time
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
	// reads is what each project's commits were read as, so that a read
	// only counts what was committed since, and its numbers outlive a
	// restart.
	reads  map[string]*statRead
	loaded bool
}

// toggle opens or closes the panel, reading the commits as it opens.
func (st *statsState) toggle() {
	st.open = !st.open
	if st.open {
		st.read(time.Now())
	}
}

// read fills the rows from what each project's own identity committed since
// the longest span: the projects of the rail, in its order, or by how
// recently each committed.
//
// What was read last shows at once, and only what was committed since is
// read again: a month of diffs is read once, not on every opening.
func (st *statsState) read(now time.Time) {
	f := st.f
	if f == nil || st.busy {
		return
	}
	st.load()
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
	// The numbers of the last read show before this one answers, and a
	// read that ran a moment ago is not run again.
	if len(st.reads) > 0 && len(st.rows) == 0 {
		st.rows = st.rowsOf(dirs, names, manual, now)
		draw()
	}
	if !st.at.IsZero() && now.Sub(st.at) < statsEvery {
		return
	}
	gen := st.gen + 1
	st.busy, st.gen = true, gen
	reads := make([]*statRead, len(dirs))
	// Each project reads on its own: a repository of thousands of commits
	// takes a moment, and the others must not wait for it.
	bg.Go(func() {
		var wg sync.WaitGroup
		if len(st.reads) == 0 {
			// Nothing was read before: the counts of the commits come
			// first, as they read no diff, and the lines follow.
			counts := countsOf(dirs, names, manual, now)
			post(func() {
				if st.gen == gen && len(st.rows) == 0 {
					st.rows = counts
				}
				draw()
			})
		}
		for i := range dirs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				reads[i] = st.readOne(dirs[i], now)
			}(i)
		}
		wg.Wait()
		next := make(map[string]*statRead, len(st.reads)+len(reads))
		for dir, r := range st.reads {
			next[dir] = r
		}
		for _, r := range reads {
			if r != nil {
				next[r.Dir] = r
			}
		}
		post(func() {
			if st.gen != gen {
				return
			}
			st.reads = next
			st.at, st.busy = now, false
			st.rows = st.rowsOf(dirs, names, manual, now)
		})
		draw()
		st.save(next)
	})
}

// countsOf is what the panel shows before it reads a diff: how many each
// project committed over each span.
func countsOf(dirs, names []string, manual bool, now time.Time) []statsRow {
	rows := make([]statsRow, len(dirs))
	var wg sync.WaitGroup
	for i := range dirs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rows[i].dir, rows[i].name = dirs[i], names[i]
			r, err := git.Open(dirs[i])
			if err != nil {
				return
			}
			rows[i].latest = r.HeadTime()
			me := r.Identity()
			for s := 0; s < statSpans; s++ {
				if n, err := r.CountSince(now.Add(-statSpanLong[s]), me); err == nil {
					rows[i].count[s].Commits = n
				}
			}
		}(i)
	}
	wg.Wait()
	if !manual {
		slices.SortStableFunc(rows, func(a, b statsRow) int { return b.latest.Compare(a.latest) })
	}
	return rows
}

// readOne reads what one project committed: only what it committed since it
// was last read, as long as the branch still holds the commits it had then,
// and a month of diffs otherwise, which a rewritten branch needs.
func (st *statsState) readOne(dir string, now time.Time) *statRead {
	old := st.reads[dir]
	r, err := git.Open(dir)
	if err != nil {
		return old
	}
	since := now.Add(-statSpanLong[statSpans-1])
	me := r.Identity()
	out := &statRead{Dir: dir, At: now, Head: r.Head()}
	if old != nil && !old.At.IsZero() && r.Holds(old.Head) {
		// By where the commits are, not by when they are dated: one
		// dated before the read before still counts, as an amend or a
		// rebased commit is.
		if cs, err := r.CommitsBetween(old.Head, "HEAD", me); err == nil {
			out.Commits = append(out.Commits, within(old.Commits, since)...)
			out.Commits = append(out.Commits, made(cs, since)...)
			return tidy(out)
		}
	}
	if cs, err := r.CommitsSince(since, me); err == nil {
		out.Commits = made(cs, since)
	}
	return tidy(out)
}

// rowsOf is the rows of the panel as the commits it read for these projects
// count over each span.
func (st *statsState) rowsOf(dirs, names []string, manual bool, now time.Time) []statsRow {
	rows := make([]statsRow, len(dirs))
	for i, dir := range dirs {
		rows[i].dir, rows[i].name = dir, names[i]
		read := st.reads[dir]
		if read == nil {
			continue
		}
		for _, c := range read.Commits {
			if rows[i].latest.Before(c.When) {
				rows[i].latest = c.When
			}
			for s := 0; s < statSpans; s++ {
				if c.When.After(now.Add(-statSpanLong[s])) {
					rows[i].count[s].Commits++
					rows[i].count[s].Added += c.Added
					rows[i].count[s].Removed += c.Removed
				}
			}
		}
		for s := 0; s < statSpans; s++ {
			rows[i].count[s].Lines = true
		}
	}
	if !manual {
		slices.SortStableFunc(rows, func(a, b statsRow) int { return b.latest.Compare(a.latest) })
	}
	return rows
}

// within are the commits of cs still inside the span ending now.
func within(cs []statCommit, since time.Time) []statCommit {
	kept := cs[:0]
	for _, c := range cs {
		if c.When.After(since) {
			kept = append(kept, c)
		}
	}
	return kept
}

// made are the commits of cs made from when on.
func made(cs []git.Committed, from time.Time) []statCommit {
	out := make([]statCommit, 0, len(cs))
	for _, c := range cs {
		if !c.When.Before(from) {
			out = append(out, statCommit{When: c.When, Added: c.Added, Removed: c.Deleted})
		}
	}
	return out
}

// tidy puts the commits of a read in order, drops the ones the same commit
// gave twice, and drops those that fell out of the month.
func tidy(r *statRead) *statRead {
	slices.SortFunc(r.Commits, func(a, b statCommit) int { return a.When.Compare(b.When) })
	r.Commits = slices.CompactFunc(r.Commits, func(a, b statCommit) bool {
		return a.When.Equal(b.When) && a.Added == b.Added && a.Removed == b.Removed
	})
	return r
}

// statsPath is the file the panel keeps the commits it read in, beside the
// settings of the editor.
func (st *statsState) statsPath() string {
	if st.f == nil || st.f.m == nil {
		return ""
	}
	return filepath.Join(filepath.Dir(st.f.m.s.Path()), "stats-cache.json")
}

// load reads what the panel counted last time it ran, once.
func (st *statsState) load() {
	if st.loaded {
		return
	}
	st.loaded = true
	b, err := os.ReadFile(st.statsPath())
	if err != nil {
		return
	}
	var in struct {
		Projects []*statRead `json:"projects"`
	}
	if json.Unmarshal(b, &in) != nil {
		return
	}
	st.reads = make(map[string]*statRead, len(in.Projects))
	for _, r := range in.Projects {
		if r != nil && r.Dir != "" {
			st.reads[r.Dir] = r
		}
	}
}

// save writes the commits a read found for the next run to show at once,
// beside the settings of the editor.
func (st *statsState) save(reads map[string]*statRead) {
	if st.f == nil || len(reads) == 0 {
		return
	}
	projects := make([]*statRead, 0, len(reads))
	for _, r := range reads {
		projects = append(projects, r)
	}
	slices.SortFunc(projects, func(a, b *statRead) int {
		if a.Dir < b.Dir {
			return -1
		}
		if a.Dir > b.Dir {
			return 1
		}
		return 0
	})
	b, err := json.Marshal(struct {
		Projects []*statRead `json:"projects"`
	}{projects})
	if err != nil {
		return
	}
	_ = settings.WriteFile(st.statsPath(), b)
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
			box := a.floating(c, ui.Column(c).Width(560).MaxWidthPercent(92).Margin(titleBarH+8, 0, 0, 0).Radius(10), true).Clip().
				Role(ui.RoleDialog).Label(locales.StatsTitle())
			box.Children(func() {
				ui.Row(c).Padding(11, 14).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
					ui.Icon(c, iconStats).FontSize(14).TextColor(p.muted)
					ui.Text(c, locales.StatsTitle()).FontSize(13.5).TextColor(p.text).Grow(1).Basis(0).MinWidth(0).SingleLine()
					if st.busy && len(st.rows) == 0 {
						ui.Text(c, locales.StatsReading()).FontSize(11.5).TextColor(p.faint).Shrink(0).SingleLine()
					}
					if a.smallButton(c, iconX, locales.QuickClose()) {
						st.open = false
					}
				})
				// Each span over its column of counts.
				ui.Row(c).MinHeight(statHeadH).Padding(0, statEdge).Gap(statColumnGap).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
					ui.Text(c, "").Grow(1).Basis(0).MinWidth(0)
					for s := 0; s < statSpans; s++ {
						ui.Text(c, statSpanName(s)).Width(statColumnW).Shrink(0).FontSize(11.5).TextColor(p.faint).SingleLine()
					}
				})
				ui.Scroll(c.Key("stats-rows")).MaxHeight(420).Padding(2, 14, 4, 14).Children(func() {
					if len(st.rows) == 0 {
						// While a read runs, the rows are not there yet.
						if !st.busy {
							ui.Text(c, locales.StatsEmpty()).FontSize(13).TextColor(p.muted).Padding(8, 4)
						}
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
// between them and the names. statRowH is the height of a project's line and
// of the total's, and statHeadH the height of the row that names the spans:
// one rhythm, so the numbers line up down the panel.
const (
	statColumnW   float32 = 124
	statColumnGap float32 = 14
	statRowH      float32 = 54
	statHeadH     float32 = 34
	// statEdge is how far the list's own padding and a line's put a line's
	// names from the panel's edge, which the spans and the total are set
	// to, so that every column starts on the same line.
	statEdge = 14 + 6
)

// statsLine is one project of the panel: its name, and under each span the
// commits it made and the lines they added and deleted.
func (a *app) statsLine(c *ui.Context, row statsRow) {
	p := a.pal
	b := ui.ButtonBase(c.Key("stats:"+row.dir)).FillWidth().MinHeight(statRowH).Padding(0, 6).Gap(statColumnGap).
		Radius(6).AlignItems(ui.Center).Label(row.name)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Transition(hoverFade)
	b.Children(func() {
		a.statsCells(c, row.name, p.text, row.count)
	})
}

// statsTotal is the panel's last line, kept below the list: the commits of
// every project it lists, and the lines those commits added and deleted,
// over each span.
func (a *app) statsTotal(c *ui.Context, rows []statsRow) {
	p := a.pal
	ui.Row(c.Key("stats-total")).FillWidth().MinHeight(statRowH).Padding(0, statEdge).Gap(statColumnGap).
		AlignItems(ui.Center).Margin(0, 0, 8, 0).BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
		a.statsCells(c, locales.StatsTotal(), p.muted, statsSum(rows))
	})
}

// statsSum adds the commits of every row, and the lines they added and
// deleted, over each span.
func statsSum(rows []statsRow) (sum [statSpans]statsAmount) {
	for _, r := range rows {
		for s := 0; s < statSpans; s++ {
			sum[s].Commits += r.count[s].Commits
			sum[s].Added += r.count[s].Added
			sum[s].Removed += r.count[s].Removed
			sum[s].Lines = sum[s].Lines || r.count[s].Lines
		}
	}
	return sum
}

// statsCells is a row of the panel: the name at the left, here in nameCol,
// and under each span the commits and the lines.
func (a *app) statsCells(c *ui.Context, name string, nameCol ui.Color, count [statSpans]statsAmount) {
	p := a.pal
	ui.Text(c, name).FontSize(13).TextColor(nameCol).Grow(1).Basis(0).MinWidth(0).SingleLine()
	for s := 0; s < statSpans; s++ {
		ui.Column(c).Width(statColumnW).Shrink(0).Gap(3).Children(func() {
			col := p.text
			if count[s].Commits == 0 {
				col = p.faint
			}
			ui.Text(c, fmt.Sprintf(locales.StatsCommits(), count[s].Commits)).FontSize(12.5).TextColor(col).SingleLine()
			a.statsLines(c, count[s])
		})
	}
}

// statsLines are the lines a count of commits added and deleted, in their
// colors, a dash for none, and a mark while they are still being read.
func (a *app) statsLines(c *ui.Context, n statsAmount) {
	p := a.pal
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		switch {
		case !n.Lines:
			ui.Text(c, "\u2026").FontSize(10.5).TextColor(p.faint).SingleLine()
			return
		case n.Added == 0 && n.Removed == 0:
			ui.Text(c, "\u2014").FontSize(10.5).TextColor(p.faint).SingleLine()
			return
		}
		if n.Added > 0 {
			ui.Text(c, "+"+railLinesCount(n.Added)).FontSize(10.5).TextColor(p.added).SingleLine()
		}
		if n.Removed > 0 {
			ui.Text(c, "\u2212"+railLinesCount(n.Removed)).FontSize(10.5).TextColor(p.removed).SingleLine()
		}
	})
}
