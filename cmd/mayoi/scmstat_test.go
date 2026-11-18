package main

import (
	"fmt"
	"testing"
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
	hs.shot("scm-lines")
	// A file staged shows the lines it changed against HEAD too.
	if st := a.git.workStat[hs.path("internal/a/a.go")]; st.Added == 0 && st.Deleted == 0 {
		t.Errorf("a staged file: %+v", st)
	}
}
