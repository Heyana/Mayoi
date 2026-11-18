package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"mayoi/internal/git"
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
