package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// What HistoryStat counts for each commit is what CommitStat counts: the
// lines a commit changed from its first parent, a root commit's whole, a
// rename's none and a merge's once, though git prints its changes against
// each parent.
func TestHistoryStat(t *testing.T) {
	r, git := repo(t) // a first commit of a.txt and b.txt
	// A commit that gains and loses lines, and one that adds a file.
	write(t, r.Root, "a.txt", "one\nTWO\nthree\nfour\n")
	write(t, r.Root, "c.txt", "see\n")
	git("add", ".")
	git("commit", "-q", "-m", "second")
	// A binary file changes no lines.
	if err := os.WriteFile(filepath.Join(r.Root, "bin.dat"), []byte{0, 1, 2, 0, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "bin.dat")
	git("commit", "-q", "-m", "binary")
	// A rename counts as one file of no lines.
	git("mv", "b.txt", "bb.txt")
	git("commit", "-q", "-m", "rename")
	// A merge changes a.txt against its first parent and c.txt against its
	// second: only the first parent's counts.
	git("switch", "-qc", "side")
	write(t, r.Root, "a.txt", "one\nTWO\nthree\nfour\nfive\n")
	git("commit", "-qam", "side")
	git("switch", "-q", "main")
	write(t, r.Root, "c.txt", "see\nmore\n")
	git("commit", "-qam", "third")
	git("merge", "-q", "--no-edit", "side")

	cs, err := r.Log(50, false)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := r.HistoryStat(50, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != len(cs) {
		t.Fatalf("%d commits counted, want %d: %v", len(stats), len(cs), stats)
	}
	merges := 0
	for _, c := range cs {
		want, err := r.CommitStat(c)
		if err != nil {
			t.Fatal(err)
		}
		if got := stats[c.Hash]; got != want {
			t.Fatalf("%s: got %+v, want %+v", c.Short(), got, want)
		}
		if len(c.Parents) > 1 {
			merges++
		}
	}
	if merges != 1 {
		t.Fatalf("%d merges, want 1", merges)
	}
	// The first commit is counted with all it added.
	if root := cs[len(cs)-1]; stats[root.Hash].Added == 0 || len(root.Parents) != 0 {
		t.Fatalf("the first commit: %+v, parents %v", stats[root.Hash], root.Parents)
	}

	// A commit of a branch HEAD lacks counts only when all is read.
	git("switch", "-qc", "away")
	write(t, r.Root, "away.txt", "away\naway\n")
	git("add", "away.txt")
	git("commit", "-q", "-m", "away")
	out, err := r.run(nil, "rev-parse", "away")
	if err != nil {
		t.Fatal(err)
	}
	away := strings.TrimSpace(string(out))
	git("switch", "-q", "main")
	if stats, _ := r.HistoryStat(50, false); stats[away].Added != 0 || stats[away].Files != 0 {
		t.Fatalf("a branch's commit counted without all: %+v", stats[away])
	}
	all, err := r.HistoryStat(50, true)
	if err != nil {
		t.Fatal(err)
	}
	if st := all[away]; st.Added != 2 || st.Files != 1 {
		t.Fatalf("the branch's commit: %+v, want two lines of one file", st)
	}
}

// A repository with no commit has no history to count.
func TestHistoryStatOfEmptyRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := r.HistoryStat(50, false)
	if err != nil || stats != nil {
		t.Fatalf("got %v, %v, want no commit and no error", stats, err)
	}
}
