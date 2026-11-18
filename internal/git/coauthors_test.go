package git

import (
	"slices"
	"testing"
)

func TestParseCoAuthors(t *testing.T) {
	body := "Detail.\n\nCo-authored-by: Claude <noreply@anthropic.com>\nco-authored-by: Jane Doe <jane@x.io>\n" +
		"Co-Authored-By: Claude <NOREPLY@anthropic.com>\nCo-authored-by: Me <me@x.io>\nCo-authored-by: Bob\nCo-authored-by:  "
	got := parseCoAuthors(body, "Me", "me@x.io")
	want := []Person{{"Claude", "noreply@anthropic.com"}, {"Jane Doe", "jane@x.io"}, {"Bob", ""}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := parseCoAuthors("no trailers", "a", "b"); got != nil {
		t.Fatalf("got %v, want none", got)
	}
}

func TestParseNumstat(t *testing.T) {
	got := parseNumstat("3\t1\ta.go\n-\t-\timg.png\n10\t0\tb/c.go\n")
	if want := (CommitStat{Files: 3, Added: 13, Deleted: 1}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// TestParseNumstatFilesByNewPath keeps a renamed file's numbers with the
// path it has now, which git prints after the one it had.
func TestParseNumstatFilesByNewPath(t *testing.T) {
	out := []byte("3\t1\ta.go\x0015\t6\t\x00old/b.go\x00new/b.go\x00-\t-\timg.png\x00")
	got := parseNumstatFiles(out)
	want := map[string]FileStat{
		"a.go":     {Added: 3, Deleted: 1},
		"new/b.go": {Added: 15, Deleted: 6},
		"img.png":  {},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for path, st := range want {
		if got[path] != st {
			t.Fatalf("%s: got %+v, want %+v", path, got[path], st)
		}
	}
	if _, ok := got["old/b.go"]; ok {
		t.Fatalf("the old path kept the numbers: %v", got)
	}
}

// The lines of a commit are keyed by the paths its files have in the work
// tree, as the editor looks them up.
func TestCommitNumstatKeysArePathsInTheWorkTree(t *testing.T) {
	r, git := repo(t)
	write(t, r.Root, "a.txt", "one\ntwo\nthree\nfour\nfive\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "more")
	cs, err := r.Log(1, false)
	if err != nil || len(cs) != 1 {
		t.Fatalf("log %v, %v", cs, err)
	}
	stat, err := r.CommitNumstat(cs[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := stat[r.Abs("a.txt")]; got.Added != 2 || got.Deleted != 0 {
		t.Fatalf("a.txt: %+v, want +2", got)
	}
	if _, ok := stat["a.txt"]; ok {
		t.Fatalf("a commit's file kept its path in the repository: %v", stat)
	}
}

// A work tree's lines count what a file gained and lost against HEAD, and
// a file Git does not track yet whole.
func TestWorkNumstat(t *testing.T) {
	r, git := repo(t)
	write(t, r.Root, "a.txt", "one\nTWO\nthree\nfour\n")
	write(t, r.Root, "c.txt", "new\nlines\nhere\n")
	stat, err := r.WorkNumstat()
	if err != nil {
		t.Fatal(err)
	}
	if got := stat[r.Abs("a.txt")]; got.Added != 2 || got.Deleted != 1 {
		t.Fatalf("a.txt: %+v, want +2 −1", got)
	}
	if got := stat[r.Abs("c.txt")]; got.Added != 3 || got.Deleted != 0 {
		t.Fatalf("c.txt: %+v, want +3", got)
	}
	if _, ok := stat[r.Abs("b.txt")]; ok {
		t.Fatalf("a file unchanged is not counted: %v", stat)
	}
	// A staged change counts as the work tree's too, against HEAD.
	git("add", "a.txt")
	stat, err = r.WorkNumstat()
	if err != nil {
		t.Fatal(err)
	}
	if got := stat[r.Abs("a.txt")]; got.Added != 2 || got.Deleted != 1 {
		t.Fatalf("a.txt staged: %+v, want +2 −1", got)
	}
}
