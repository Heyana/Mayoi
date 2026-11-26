package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// showTree spells the rows of a tree of files, a folder's name ending in
// a slash, each indented by its depth.
func showTree(rows []treeRow) string {
	var out []string
	for _, r := range rows {
		s := strings.Repeat(" ", r.depth)
		if r.dir != "" {
			s += r.name + "/"
		} else {
			s += filepath.Base(r.e.Path)
		}
		out = append(out, s)
	}
	return strings.Join(out, "|")
}

// The tab of a commit hides the panel of its files at first; shown, it
// lists them beside the diff, in a tree of their folders, and the diff
// shows the file the panel has chosen, alone: a click on one shows that
// file, a folder closes, and the panel hides again.
func TestCommitFilesPanel(t *testing.T) {
	hs := newHarness(t, true)
	hs.gitRepo()
	a, tt := hs.a, hs.tt
	a.settings.ScmTree = true
	a.window.SidebarClosed = true // the Explorer's folders are named as the panel's
	a.showCommit(a.git.history[0], true)
	hs.settleGit()
	hs.settle()
	v := a.activeTab().diff
	if v == nil || len(v.files) == 0 {
		t.Fatalf("no files in the commit's tab")
	}
	if a.window.CommitFilesOpen || tt.HasText("Hide Files") {
		t.Fatalf("the panel shows at first")
	}
	a.run("git.toggleCommitFiles")
	hs.settle()
	rows := v.tree.rows
	var trees []treeRow
	for _, r := range rows {
		trees = append(trees, r.treeRow)
	}
	if got, want := showTree(trees), "internal/| a/|  a.go| b/|  b.ts|web/| index.html| style.css|.gitignore|README.md|go.mod|main.go"; got != want {
		t.Fatalf("rows\n%s, want\n%s", got, want)
	}
	if !tt.HasText("Files") {
		t.Errorf("no panel: %q", tt.Texts())
	}
	// Each file of the panel has the lines it changed.
	if cm := v.commit; cm == nil || len(cm.stat) == 0 {
		t.Fatalf("the files have no lines counted")
	} else {
		for _, f := range v.files {
			if _, ok := cm.stat[f.path]; !ok {
				t.Fatalf("%s has no lines counted in %v", f.path, cm.stat)
			}
		}
	}
	// The diff beside the panel shows one file at a time, the first.
	if got := rowFiles(v); len(got) != 1 {
		t.Fatalf("rows of %v, want one file alone", got)
	}
	hs.shot("commit-files-panel")
	if len(v.files) < 2 {
		t.Fatalf("the commit changed %d files, want more", len(v.files))
	}
	// A file clicked is the one the diff shows, alone.
	if err := tt.Click("style.css"); err != nil {
		t.Fatal(err)
	}
	hs.settle()
	k := slices.IndexFunc(v.files, func(f *diffFile) bool { return filepath.Base(f.path) == "style.css" })
	if k < 0 {
		t.Fatalf("style.css is not one of the files")
	}
	if f := hs.settleShown(v); f != v.files[k] {
		t.Errorf("the diff shows %v, not style.css", f)
	}
	if got := rowFiles(v); len(got) != 1 || got[0] != v.files[k].path {
		t.Errorf("rows of %v, want style.css alone", got)
	}
	if v.tree.top != v.files[k] {
		t.Errorf("the file at the top is not style.css")
	}
	// A folder closed hides its files.
	if err := tt.Click("internal"); err != nil {
		t.Fatal(err)
	}
	hs.settle()
	if got, want := len(v.tree.rows), len(rows)-4; got != want {
		t.Errorf("%d rows with internal closed, want %d", got, want)
	}
	// Hidden, the diff takes the tab; shown, the panel is back.
	a.run("git.toggleCommitFiles")
	hs.settle()
	if a.window.CommitFilesOpen || tt.HasText("Hide Files") {
		t.Errorf("the panel did not hide")
	}
	a.run("git.toggleCommitFiles")
	hs.settle()
	if !a.window.CommitFilesOpen || !tt.HasText("Hide Files") {
		t.Errorf("the panel did not show again")
	}
	// As a list, the files go by path.
	a.settings.ScmTree = false
	hs.settle()
	if got := len(v.tree.rows); got != len(v.files) {
		t.Errorf("%d rows as a list, want %d", got, len(v.files))
	}
}

// A commit of source control opens to list the files it changed under it,
// and a click on one shows the commit's changes at that file.
func TestCommitFilesInSourceControl(t *testing.T) {
	hs := newHarness(t, true)
	git := hs.gitRepo()
	a, tt := hs.a, hs.tt
	a.settings.ScmTree = true
	os.WriteFile(hs.path("internal/a/a.go"), []byte("package a\n\nconst Answer = 43\n"), 0o644)
	os.WriteFile(hs.path("notes.txt"), []byte("notes\n"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "second")
	a.run("view.scm")
	hs.settleGit()
	ctx := a.repository()
	cm := a.git.history[0]
	ctx.toggleCommitOpen(cm)
	for range 100 {
		hs.settle()
		if r := ctx.scm.commitFiles[cm.Hash]; r != nil && !r.loading {
			break
		}
	}
	var got []string
	for _, r := range a.scmRows() {
		if r.under == nil {
			continue
		}
		s := strings.Repeat(" ", r.depth)
		switch {
		case r.note != "":
			s += "(" + r.note + ")"
		case r.dir != "":
			s += r.name + "/"
		default:
			s += filepath.Base(r.e.Path)
		}
		got = append(got, s)
	}
	if want := "internal/a/| a.go|notes.txt"; strings.Join(got, "|") != want {
		t.Fatalf("rows under the commit %q, want %q", got, want)
	}
	hs.shot("commit-files-scm")
	if err := tt.Click("notes.txt"); err != nil {
		t.Fatal(err)
	}
	hs.settle()
	hs.settle()
	v := a.columnTabDiff()
	if v == nil || v.commit == nil || v.commit.hash != cm.Hash {
		t.Fatalf("the commit's changes do not show")
	}
	k := slices.IndexFunc(v.files, func(f *diffFile) bool { return filepath.Base(f.path) == "notes.txt" })
	if k < 0 || v.cursor != v.heads[k] {
		t.Errorf("cursor %d, not the header %v of notes.txt", v.cursor, v.heads)
	}
	// Closed again, the commit's files go.
	ctx.toggleCommitOpen(cm)
	hs.settle()
	for _, r := range a.scmRows() {
		if r.under != nil {
			t.Fatalf("a row under a closed commit: %+v", r)
		}
	}
}

// columnTabDiff is the diff the column of the files shows, nil for none.
func (a *app) columnTabDiff() *diffView {
	if t := a.fileColumn().tab(); t != nil {
		return t.diff
	}
	return nil
}

// Right-clicking a committed file in the commit files panel or under a
// commit in Source Control offers Reveal in Explorer and Reveal in Finder.
func TestCommitFileContextMenuReveal(t *testing.T) {
	hs := newHarness(t, true)
	hs.gitRepo()
	a, tt := hs.a, hs.tt
	a.settings.ScmTree = false
	a.window.SidebarClosed = true
	a.showCommit(a.git.history[0], true)
	hs.settleGit()
	hs.settle()
	a.run("git.toggleCommitFiles")
	hs.settle()

	target := hs.path("README.md")
	if err := tt.RightClick("README.md"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(revealLabel()); err != nil {
		t.Fatalf("no %q in commit file menu %q: %v", revealLabel(), tt.Menu(), err)
	}
	hs.settle()
	if !slices.Equal(hs.h.revealed, []string{target}) {
		t.Fatalf("revealed in Finder %q, want [%q]", hs.h.revealed, target)
	}

	if err := tt.RightClick("README.md"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Reveal in Explorer"); err != nil {
		t.Fatalf("no Reveal in Explorer in commit file menu %q: %v", tt.Menu(), err)
	}
	hs.until("revealed in explorer", func() bool { return a.explorerChoice() == target })

	// Also verify under an expanded commit in Source Control.
	hs.h.revealed = nil
	a.run("view.scm")
	hs.settleGit()
	ctx := a.repository()
	cm := a.git.history[0]
	ctx.toggleCommitOpen(cm)
	hs.until("commit files loaded", func() bool {
		r := ctx.scm.commitFiles[cm.Hash]
		return r != nil && !r.loading
	})
	if err := tt.RightClick("README.md"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(revealLabel()); err != nil {
		t.Fatalf("no %q in scm commit file menu %q: %v", revealLabel(), tt.Menu(), err)
	}
	hs.settle()
	if !slices.Equal(hs.h.revealed, []string{target}) {
		t.Fatalf("scm commit file revealed in Finder %q, want [%q]", hs.h.revealed, target)
	}
}
