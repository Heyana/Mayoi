package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"mayoi/internal/git"
	"mayoi/locales"
)

// The changes of the work tree show the lines each file gained and lost,
// before its letter.
func TestScmShowsTheLinesChanged(t *testing.T) {
	hs := newHarness(t, false)
	scmFixture(hs)
	hs.a.run("view.scm")
	hs.settleGit()
	a := hs.a
	for _, name := range []string{"main.go", "notes.txt"} {
		st, ok := a.git.workStat[hs.path(name)]
		if !ok || st.Added == 0 {
			t.Fatalf("%s: %+v, set %v", name, st, ok)
		}
		if want := fmt.Sprintf("+%d", st.Added); !hs.tt.HasText(want) {
			t.Errorf("%s: %q is not shown in %q", name, want, hs.tt.Texts())
		}
	}
	// The header of a group adds its files' lines up, beside their count.
	g := a.repository()
	var changes git.FileStat
	for _, e := range g.groupEntries()[groupChanges] {
		st := a.git.workStat[e.Path]
		changes.Added += st.Added
		changes.Deleted += st.Deleted
	}
	if got := g.groupStat(groupChanges); got != changes || changes.Added == 0 {
		t.Fatalf("the changes add up to %+v, want %+v", got, changes)
	}
	if want := fmt.Sprintf("+%d", changes.Added); !hs.tt.HasText(want) {
		t.Errorf("%q is not beside the count of the changes", want)
	}
	hs.shot("scm-lines")
	// A file staged shows the lines it changed against HEAD too.
	if st := a.git.workStat[hs.path("internal/a/a.go")]; st.Added == 0 && st.Deleted == 0 {
		t.Errorf("a staged file: %+v", st)
	}
}

// The branch button counts the branches, and the worktree button the
// worktrees, once there is more than one of each.
func TestScmCountsBranchesAndWorktrees(t *testing.T) {
	hs := newHarness(t, false)
	git := hs.gitRepo()
	git("branch", "one")
	git("branch", "two")
	wt := filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "-q", wt, "one")
	hs.a.run("view.scm")
	hs.settleGit()
	a := hs.a
	if n := len(a.git.branches); n != 3 {
		t.Fatalf("%d branches, want 3", n)
	}
	if n := len(a.git.worktrees); n != 2 {
		t.Fatalf("%d worktrees, want 2", n)
	}
	if got, want := a.repository().scmCountsW(), float32(2*scmCountW); got != want {
		t.Fatalf("the counts take %v, want %v", got, want)
	}
	if !hs.tt.HasText("3") {
		t.Errorf("the branches are not counted in %q", hs.tt.Texts())
	}
	hs.shot("scm-counts")
}

// The worktrees the box lists say how many files each has uncommitted, so
// which of them is still being worked in shows.
func TestWorktreePickerCountsUncommittedFiles(t *testing.T) {
	hs := newHarness(t, false)
	git := hs.gitRepo()
	git("branch", "side")
	wt := filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "-q", wt, "side")
	if err := os.WriteFile(filepath.Join(wt, "one.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "two.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hs.settleGit()
	hs.a.run("git.switchWorktree")
	w := &hs.a.worktrees
	for range 200 {
		hs.settle()
		if !w.open {
			t.Fatal("the box closed")
		}
		if !w.loading && len(w.changes) == len(w.trees) {
			break
		}
	}
	// Git spells the folder as the system resolves it, past /private.
	resolved, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := w.changes[resolved]; !ok || n != 2 {
		t.Fatalf("the worktree has %d files uncommitted (%v), want 2", n, ok)
	}
	if want := locales.WorktreeFilesChanged("2"); !hs.tt.HasText(want) {
		t.Errorf("%q is not in the box: %q", want, hs.tt.Texts())
	}
	hs.shot("worktree-changes")
}
