package main

import (
	"encoding/json"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/settings"
)

// gitAt runs git in dir as a commit at when, for the commits made before
// now.
func gitAt(t *testing.T, dir string, when time.Time, args ...string) {
	t.Helper()
	gitAs(t, dir, "t@t", when, args...)
}

// gitAs runs git as who, the author and the committer both, which tells
// whose commits the panel adds under its own name.
func gitAs(t *testing.T, dir, who string, when time.Time, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=" + who, "-c", "user.name=" + who}, args...)...)
	if !when.IsZero() {
		stamp := when.Format(time.RFC3339)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// The panel counts what each project committed over the day, the week and
// the month before, with the lines those commits added, and reads the same
// commit under every span that holds it.
func TestStatsCountsTheCommitsOfEachProject(t *testing.T) {
	hs := newHarness(t, true)
	hs.gitRepo()
	dir := filepath.Join(t.TempDir(), "statslab")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old, week, today := now.Add(-40*24*time.Hour), now.Add(-8*24*time.Hour), now.Add(-time.Hour)
	gitAt(t, dir, time.Time{}, "init", "-q", "-b", "main")
	// The project commits as t@t, which its settings say is its own.
	gitAt(t, dir, time.Time{}, "config", "user.email", "t@t")
	writeLines(t, filepath.Join(dir, "README.md"), 3)
	gitAt(t, dir, old, "add", "README.md")
	gitAt(t, dir, old, "commit", "-q", "-m", "old")
	writeLines(t, filepath.Join(dir, "README.md"), 6)
	gitAt(t, dir, week, "commit", "-q", "-a", "-m", "week")
	writeLines(t, filepath.Join(dir, "README.md"), 9)
	gitAt(t, dir, today, "commit", "-q", "-a", "-m", "today")
	// One of another's, the same day: the panel says whose it is.
	writeLines(t, filepath.Join(dir, "README.md"), 10)
	gitAs(t, dir, "o@o", today, "commit", "-q", "-a", "-m", "theirs")

	f, _, tt := railFixture(t, hs.root, dir)
	if got := f.stats.statsPath(); got != "" {
		t.Fatalf("statsPath with in-memory settings = %q, want empty", got)
	}
	f.stats.toggle()
	deadline := time.Now().Add(10 * time.Second)
	for f.stats.busy && time.Now().Before(deadline) {
		step(tt)
		time.Sleep(5 * time.Millisecond)
	}
	var row statsRow
	for _, r := range f.stats.rows {
		if r.dir == dir {
			row = r
		}
	}
	for s := 0; s < statSpans; s++ {
		if got := row.count[s].Commits; got != []int{1, 1, 2}[s] {
			t.Fatalf("the project counts %v of its own over the day, week and month, want [1 1 2]: another's commit is not counted",
				[]int{row.count[0].Commits, row.count[1].Commits, row.count[2].Commits})
		}
	}
	if got := row.count[2].Added; got != 6 {
		t.Fatalf("its commits added %d lines over the month, want 6: a commit of forty days ago, and another's, are not counted", got)
	}
	// The panel's last line adds every project it lists.
	sum := statsSum(f.stats.rows)
	if sum[2].Commits < row.count[2].Commits {
		t.Fatalf("the panel's total is %d commits over the month, less than the project's %d", sum[2].Commits, row.count[2].Commits)
	}
	if dir := os.Getenv("SHOTS"); dir != "" {
		out, err := os.Create(filepath.Join(dir, "stats.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		if err := png.Encode(out, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

// The panel reads a month of commits once: opening it again reads only what
// was committed since the read before, and its numbers are still there for
// the next run before Git answers.
func TestStatsReadsOnlyWhatWasCommittedSinceTheReadBefore(t *testing.T) {
	hs := newHarness(t, true)
	hs.gitRepo()
	dir := filepath.Join(t.TempDir(), "since")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	gitAt(t, dir, time.Time{}, "init", "-q", "-b", "main")
	gitAt(t, dir, time.Time{}, "config", "user.email", "t@t")
	writeLines(t, filepath.Join(dir, "README.md"), 3)
	gitAt(t, dir, now.Add(-2*time.Hour), "add", "README.md")
	gitAt(t, dir, now.Add(-2*time.Hour), "commit", "-q", "-m", "first")

	f, _, tt := railFixture(t, hs.root, dir)
	saved := settings.Load(filepath.Join(t.TempDir(), "settings.json"))
	saved.ProjectTabs = true
	saved.Windows = f.m.s.Windows
	f.m.s = saved
	f.stats.toggle()
	waitFor(t, tt, &f.stats.busy)
	if got := findRow(t, f.stats.rows, dir).count[0].Commits; got != 1 {
		t.Fatalf("the project counts %d commits over the day, want 1", got)
	}

	// One more, after the read: reading only what came since finds it.
	// Two commits in the same second with identical line counts must both count.
	same := now.Add(-time.Hour)
	writeLines(t, filepath.Join(dir, "README.md"), 5)
	gitAt(t, dir, same, "commit", "-q", "-a", "-m", "second")
	writeLines(t, filepath.Join(dir, "OTHER.md"), 2)
	gitAt(t, dir, same, "add", "OTHER.md")
	gitAt(t, dir, same, "commit", "-q", "-m", "third")
	f.stats.at = time.Time{}
	f.stats.read(time.Now())
	waitFor(t, tt, &f.stats.busy)
	row := findRow(t, f.stats.rows, dir)
	for s := 0; s < statSpans; s++ {
		if got := row.count[s].Commits; got != 3 {
			t.Fatalf("the project counts %d commits after a read of what came since, want 3", got)
		}
	}
	if got := row.count[2].Added; got != 7 {
		t.Fatalf("its commits added %d lines, want 7: the one read before is kept", got)
	}
	// What it read lives beside the settings: the next run shows it before
	// Git runs at all.
	f.stats.rows, f.stats.reads, f.stats.loaded = nil, nil, false
	f.stats.load()
	back := f.stats.rowsOf([]string{dir}, []string{"since"}, false, now)
	if got := findRow(t, back, dir).count[2].Commits; got != 3 {
		t.Fatalf("the panel shows %d commits of the project it read before, want 3: they are kept across runs", got)
	}
}

// waitFor steps the window until the panel is done reading.
func waitFor(t *testing.T, tt *ui.Tester, busy *bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for *busy && time.Now().Before(deadline) {
		step(tt)
		time.Sleep(5 * time.Millisecond)
	}
}

// findRow is the line of the panel for dir.
func findRow(t *testing.T, rows []statsRow, dir string) statsRow {
	t.Helper()
	for _, r := range rows {
		if r.dir == dir {
			return r
		}
	}
	t.Fatalf("the panel lists no line for %s", dir)
	return statsRow{}
}

// What the rail names a project, and the order it puts it in, shows in the
// panel at once, as the rows are made again from the numbers of the last
// read even when a read that ran a moment ago is not run again.
func TestStatsRowsFollowTheRailWithinTheReadInterval(t *testing.T) {
	hs := newHarness(t, true)
	hs.gitRepo()
	now := time.Now()
	dir := filepath.Join(t.TempDir(), "labelled")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitAt(t, dir, time.Time{}, "init", "-q", "-b", "main")
	gitAt(t, dir, time.Time{}, "config", "user.email", "t@t")
	writeLines(t, filepath.Join(dir, "README.md"), 3)
	gitAt(t, dir, now.Add(-time.Hour), "add", "README.md")
	gitAt(t, dir, now.Add(-time.Hour), "commit", "-q", "-m", "first")

	f, ws, tt := railFixture(t, hs.root, dir)
	f.stats.toggle()
	waitFor(t, tt, &f.stats.busy)
	if got, want := findRow(t, f.stats.rows, dir).name, filepath.Base(dir); got != want {
		t.Fatalf("the row is named %q, want %q", got, want)
	}

	// A label given to the project shows on the next read of the panel, which
	// reads nothing again within the interval it last read: what the numbers
	// stand for is made again at once.
	f.m.s.SetTabStyle(dir, settings.TabStyle{Label: "Renamed"})
	before := f.stats.at
	f.stats.read(time.Now())
	if f.stats.at != before || f.stats.busy {
		t.Fatal("the panel read the commits again within the interval it last read")
	}
	if got := findRow(t, f.stats.rows, dir).name; got != "Renamed" {
		t.Fatalf("the row is named %q after the rail renamed the project, want %q", got, "Renamed")
	}

	// So does the order the rail was dragged to, once it is put in manually.
	f.m.s.RailOrder = "manual"
	f.m.s.Rail = []int{ws[1].a.window.ID, ws[0].a.window.ID}
	f.stats.read(time.Now())
	if first := f.stats.rows[0].dir; first != dir {
		t.Fatalf("the panel starts with %q, want %q, the project the rail put first", first, dir)
	}
}

// A project out of the rail keeps its numbers only while the commits they
// count are still inside the month: one closed for longer goes, and
// stats-cache.json stays the panel's projects.
func TestStatsCacheDropsProjectsOutOfTheRail(t *testing.T) {
	hs := newHarness(t, true)
	hs.gitRepo()
	now := time.Now()
	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitAt(t, dir, time.Time{}, "init", "-q", "-b", "main")
	gitAt(t, dir, time.Time{}, "config", "user.email", "t@t")
	writeLines(t, filepath.Join(dir, "README.md"), 3)
	gitAt(t, dir, now.Add(-2*time.Hour), "add", "README.md")
	gitAt(t, dir, now.Add(-2*time.Hour), "commit", "-q", "-m", "first")

	f, _, tt := railFixture(t, hs.root, dir)
	saved := settings.Load(filepath.Join(t.TempDir(), "settings.json"))
	saved.ProjectTabs = true
	saved.Windows = f.m.s.Windows
	f.m.s = saved
	closed := filepath.Join(t.TempDir(), "closed")
	recent := filepath.Join(t.TempDir(), "recent")
	f.stats.reads = map[string]*statRead{
		recent: {Dir: recent, At: now.Add(-time.Hour),
			Commits: []statCommit{{Hash: "recent", When: now.Add(-time.Hour)}}},
		closed: {Dir: closed, At: now.Add(-40 * 24 * time.Hour),
			Commits: []statCommit{{Hash: "closed", When: now.Add(-40 * 24 * time.Hour)}}},
	}
	f.stats.toggle()
	waitFor(t, tt, &f.stats.busy)
	if _, ok := f.stats.reads[closed]; ok {
		t.Fatalf("a project out of the rail for over a month is still read: %v", f.stats.reads)
	}
	if got := findRow(t, f.stats.rows, dir).count[0].Commits; got != 1 {
		t.Fatalf("the project counts %d commits over the day, want 1", got)
	}

	// The file it saves keeps the same: the recent project stays, the one
	// closed for longer goes.
	var cache struct {
		Projects []*statRead `json:"projects"`
	}
	path := f.stats.statsPath()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &cache) == nil && len(cache.Projects) > 0 {
			break
		}
		step(tt)
		time.Sleep(5 * time.Millisecond)
	}
	var dirs []string
	for _, p := range cache.Projects {
		if p != nil {
			dirs = append(dirs, filepath.Base(p.Dir))
		}
	}
	if slices.Contains(dirs, filepath.Base(closed)) {
		t.Fatalf("stats-cache.json keeps a project out of the rail for over a month: %v", dirs)
	}
	if !slices.Contains(dirs, "recent") {
		t.Fatalf("stats-cache.json dropped a project whose commits still count: %v", dirs)
	}
}
