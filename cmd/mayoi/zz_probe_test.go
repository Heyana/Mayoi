package main

import (
	"testing"

	"mayoi/internal/git"
)

func TestProbeNumstatKeys(t *testing.T) {
	hs := newHarness(t, true)
	hs.gitRepo()
	a := hs.a
	c := a.git.history[0]
	st, err := a.git.repo.CommitNumstat(c)
	t.Logf("err=%v keys=%v", err, keys(st))
	fs, _ := a.git.repo.CommitFiles(c)
	for _, f := range fs {
		t.Logf("file %s -> stat=%v ok=%v", f.Path, st[f.Path], func() bool { _, ok := st[f.Path]; return ok }())
	}
}

func keys(m map[string]git.FileStat) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
