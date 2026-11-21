package main

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
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
