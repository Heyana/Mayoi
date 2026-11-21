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
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
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
	writeLines(t, filepath.Join(dir, "README.md"), 3)
	gitAt(t, dir, old, "add", "README.md")
	gitAt(t, dir, old, "commit", "-q", "-m", "old")
	writeLines(t, filepath.Join(dir, "README.md"), 6)
	gitAt(t, dir, week, "commit", "-q", "-a", "-m", "week")
	writeLines(t, filepath.Join(dir, "README.md"), 9)
	gitAt(t, dir, today, "commit", "-q", "-a", "-m", "today")

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
	if row.commits != [3]int{1, 1, 2} {
		t.Fatalf("the project committed %v over the day, week and month, want [1 1 2]", row.commits)
	}
	if row.added != [3]int{3, 3, 6} {
		t.Fatalf("its commits added %v lines, want [3 3 6]: a commit of forty days ago is out of every span", row.added)
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
